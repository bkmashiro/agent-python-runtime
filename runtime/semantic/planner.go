package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const (
	SourceBoundPlanSchemaVersion   = "pysolate.source-bound-plan.v0"
	SemanticPreDispatchPassVersion = "pysolate.semantic-pre-dispatch-pass.v0"
	maxPlannerPasses               = 32
)

var (
	ErrInvalidPlannerConfig = errors.New("invalid semantic planner configuration")
	ErrInvalidPlannerInput  = errors.New("invalid semantic planner input")
)

type PassName string
type PassDisposition string

const (
	PassSemanticPreDispatch PassName        = "semantic_pre_dispatch"
	PassAdmitted            PassDisposition = "admitted"
	PassRejected            PassDisposition = "rejected"
)

type PassConfig struct {
	Name    PassName
	Version string
	Enabled bool
}

type PlannerConfig struct {
	Passes          []PassConfig
	PreissueContext PreissueContext
}

type SourceDocument struct {
	ID       string     `json:"id"`
	Language string     `json:"language"`
	SHA256   string     `json:"sha256"`
	Span     SourceSpan `json:"span"`
}

type SourceOccurrence struct {
	ID                string     `json:"id"`
	DocumentID        string     `json:"document_id"`
	Span              SourceSpan `json:"span"`
	Capability        string     `json:"capability"`
	DynamicOccurrence uint32     `json:"dynamic_occurrence"`
}

type PassSelection struct {
	Name    PassName `json:"name"`
	Version string   `json:"version"`
}

type PassDecision struct {
	PassName         PassName          `json:"pass_name"`
	PassVersion      string            `json:"pass_version"`
	OccurrenceID     string            `json:"occurrence_id"`
	Disposition      PassDisposition   `json:"disposition"`
	RejectionReasons []RejectionReason `json:"rejection_reasons"`
}

type SourceBoundPlanProjection struct {
	SchemaVersion        string             `json:"schema_version"`
	AnalysisSHA256       string             `json:"analysis_sha256"`
	CapabilityPlanSHA256 string             `json:"capability_plan_sha256"`
	Passes               []PassSelection    `json:"passes"`
	Documents            []SourceDocument   `json:"documents"`
	Occurrences          []SourceOccurrence `json:"occurrences"`
	Decisions            []PassDecision     `json:"decisions"`
}

type SourceBoundPlan struct {
	projection SourceBoundPlanProjection
	identity   string
	qualified  map[string]QualifiedCall
}

func BuildSourceBoundPlan(verified VerifiedAnalysis, capabilityPlan *capability.Plan, config PlannerConfig) (SourceBoundPlan, error) {
	passes, err := validatePassConfig(config.Passes)
	if err != nil {
		return SourceBoundPlan{}, err
	}
	analysis, err := verified.Analysis()
	if err != nil || capabilityPlan == nil || analysis.CapabilityPlanSHA256 != capabilityPlan.Identity() {
		return SourceBoundPlan{}, ErrInvalidPlannerInput
	}
	analysisIdentity, _, err := analysis.Identity()
	if err != nil {
		return SourceBoundPlan{}, ErrInvalidPlannerInput
	}
	document := SourceDocument{
		ID: sourceDocumentIdentity(analysis.SourceSHA256), Language: "python",
		SHA256: analysis.SourceSHA256, Span: analysis.ModuleSpan,
	}
	occurrences := make([]SourceOccurrence, 0, len(analysis.CallSites))
	for _, site := range analysis.CallSites {
		occurrences = append(occurrences, SourceOccurrence{
			ID: site.ID, DocumentID: document.ID, Span: site.Span,
			Capability: site.Capability, DynamicOccurrence: site.DynamicOccurrence,
		})
	}
	sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].ID < occurrences[j].ID })

	projection := SourceBoundPlanProjection{
		SchemaVersion: SourceBoundPlanSchemaVersion, AnalysisSHA256: analysisIdentity,
		CapabilityPlanSHA256: capabilityPlan.Identity(), Passes: passes,
		Documents: []SourceDocument{document}, Occurrences: occurrences, Decisions: []PassDecision{},
	}
	qualified := make(map[string]QualifiedCall)
	for _, pass := range passes {
		switch pass.Name {
		case PassSemanticPreDispatch:
			for _, occurrence := range occurrences {
				decision := CanPreissue(verified, capabilityPlan, occurrence.ID, config.PreissueContext)
				row := PassDecision{
					PassName: pass.Name, PassVersion: pass.Version, OccurrenceID: occurrence.ID,
					Disposition: PassRejected, RejectionReasons: decision.Rejections(),
				}
				if call, ok := decision.QualifiedCall(); ok {
					row.Disposition = PassAdmitted
					qualified[qualifiedKey(pass.Name, occurrence.ID)] = call
				}
				sort.Slice(row.RejectionReasons, func(i, j int) bool { return row.RejectionReasons[i] < row.RejectionReasons[j] })
				projection.Decisions = append(projection.Decisions, row)
			}
		default:
			return SourceBoundPlan{}, ErrInvalidPlannerConfig
		}
	}
	sort.Slice(projection.Decisions, func(i, j int) bool {
		if projection.Decisions[i].PassName != projection.Decisions[j].PassName {
			return projection.Decisions[i].PassName < projection.Decisions[j].PassName
		}
		return projection.Decisions[i].OccurrenceID < projection.Decisions[j].OccurrenceID
	})
	identityValue, _, err := identity(projection)
	if err != nil {
		return SourceBoundPlan{}, ErrInvalidPlannerInput
	}
	return SourceBoundPlan{projection: projection, identity: identityValue, qualified: qualified}, nil
}

func (plan SourceBoundPlan) Identity() string { return plan.identity }

func (plan SourceBoundPlan) Projection() SourceBoundPlanProjection {
	projection := plan.projection
	projection.Passes = append([]PassSelection(nil), plan.projection.Passes...)
	projection.Documents = append([]SourceDocument(nil), plan.projection.Documents...)
	projection.Occurrences = append([]SourceOccurrence(nil), plan.projection.Occurrences...)
	projection.Decisions = make([]PassDecision, len(plan.projection.Decisions))
	for index, decision := range plan.projection.Decisions {
		projection.Decisions[index] = decision
		projection.Decisions[index].RejectionReasons = append([]RejectionReason(nil), decision.RejectionReasons...)
	}
	return projection
}

func (plan SourceBoundPlan) QualifiedCall(pass PassName, occurrenceID string) (QualifiedCall, bool) {
	call, ok := plan.qualified[qualifiedKey(pass, occurrenceID)]
	return call, ok
}

func validatePassConfig(config []PassConfig) ([]PassSelection, error) {
	if len(config) > maxPlannerPasses {
		return nil, ErrInvalidPlannerConfig
	}
	seen := make(map[PassName]struct{}, len(config))
	passes := make([]PassSelection, 0, len(config))
	for _, candidate := range config {
		if _, duplicate := seen[candidate.Name]; duplicate {
			return nil, ErrInvalidPlannerConfig
		}
		seen[candidate.Name] = struct{}{}
		if candidate.Name != PassSemanticPreDispatch || candidate.Version != SemanticPreDispatchPassVersion {
			return nil, ErrInvalidPlannerConfig
		}
		if candidate.Enabled {
			passes = append(passes, PassSelection{Name: candidate.Name, Version: candidate.Version})
		}
	}
	sort.Slice(passes, func(i, j int) bool { return passes[i].Name < passes[j].Name })
	return passes, nil
}

func sourceDocumentIdentity(sourceSHA256 string) string {
	digest := sha256.Sum256([]byte("pysolate.source-document.v0\x00python\x00" + sourceSHA256))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func qualifiedKey(pass PassName, occurrenceID string) string {
	return string(pass) + "\x00" + occurrenceID
}
