package placementcensus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const SchemaVersion = "pysolate.semantic-placement-census.v0"

const (
	PlacementWASM    = "pysolate_wasm"
	PlacementNative  = "native_sandbox"
	PlacementUnknown = "unknown"
	maxReportBytes   = 1 << 20
)

var (
	ErrInvalid    = errors.New("invalid semantic placement census")
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type Target struct {
	ArtifactSourceCommit   string `json:"artifact_source_commit"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	AnalyzerSHA256         string `json:"analyzer_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ImportClosureSHA256    string `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	CorpusSHA256           string `json:"corpus_sha256"`
}

type Counts struct {
	WASM    uint32 `json:"pysolate_wasm"`
	Native  uint32 `json:"native_sandbox"`
	Unknown uint32 `json:"unknown"`
}

type ProgramResult struct {
	ProgramID          string                      `json:"program_id"`
	BaselinePlacement  string                      `json:"baseline_placement"`
	SemanticBackend    semantic.BackendRequirement `json:"semantic_backend"`
	SemanticRejections []semantic.RejectionReason  `json:"semantic_rejections"`
}

type Decision struct {
	Status                string   `json:"status"`
	ReasonCodes           []string `json:"reason_codes"`
	CurrentRouterRetained bool     `json:"current_router_retained"`
	ConsumerAdmitted      bool     `json:"consumer_admitted"`
}

type Report struct {
	SchemaVersion          string          `json:"schema_version"`
	Target                 Target          `json:"target"`
	ProgramsAnalyzed       uint32          `json:"programs_analyzed"`
	BaselineCounts         Counts          `json:"baseline_counts"`
	SemanticCounts         Counts          `json:"semantic_counts"`
	SafePrecisionGains     uint32          `json:"safe_precision_gains"`
	Agreements             uint32          `json:"agreements"`
	Disagreements          uint32          `json:"disagreements"`
	ReplacementRegressions uint32          `json:"replacement_regressions"`
	Decision               Decision        `json:"integration_decision"`
	Programs               []ProgramResult `json:"programs"`
	SealSHA256             string          `json:"seal_sha256"`
}

type VerifiedObservation struct {
	ProgramID         string
	BaselinePlacement string
	Verified          semantic.VerifiedAnalysis
}

type observation struct {
	ProgramID  string
	Baseline   string
	Semantic   semantic.BackendRequirement
	Rejections []semantic.RejectionReason
}

func BuildVerified(target Target, values []VerifiedObservation) (Report, error) {
	observations := make([]observation, len(values))
	for index, value := range values {
		analysis, err := value.Verified.Analysis()
		if err != nil || analysis.ArtifactSHA256 != target.ArtifactSHA256 || analysis.AnalyzerSHA256 != target.AnalyzerSHA256 ||
			analysis.ExecutionProfileSHA256 != target.ExecutionProfileSHA256 || analysis.ImportClosureSHA256 != target.ImportClosureSHA256 ||
			analysis.CapabilityPlanSHA256 != target.CapabilityPlanSHA256 {
			return Report{}, ErrInvalid
		}
		decision := semantic.RequiredBackend(value.Verified)
		rejections := decision.Decision.Rejections()
		if rejections == nil {
			rejections = []semantic.RejectionReason{}
		}
		observations[index] = observation{ProgramID: value.ProgramID, Baseline: value.BaselinePlacement, Semantic: decision.Backend, Rejections: rejections}
	}
	return build(target, observations)
}

func build(target Target, observations []observation) (Report, error) {
	if !validTarget(target) || len(observations) == 0 || len(observations) > 256 {
		return Report{}, ErrInvalid
	}
	report := Report{SchemaVersion: SchemaVersion, Target: target, Programs: make([]ProgramResult, 0, len(observations))}
	lastID := ""
	for _, value := range observations {
		if !idPattern.MatchString(value.ProgramID) || value.ProgramID <= lastID || !validPlacement(value.Baseline) || !validSemantic(value.Semantic) ||
			!sortedUniqueRejections(value.Rejections) || (value.Semantic == semantic.BackendUnknown) != (len(value.Rejections) > 0) {
			return Report{}, ErrInvalid
		}
		lastID = value.ProgramID
		row := ProgramResult{ProgramID: value.ProgramID, BaselinePlacement: value.Baseline, SemanticBackend: value.Semantic, SemanticRejections: append([]semantic.RejectionReason{}, value.Rejections...)}
		report.Programs = append(report.Programs, row)
	}
	derive(&report)
	report.SealSHA256 = reportSeal(report)
	if report.Validate() != nil {
		return Report{}, ErrInvalid
	}
	return report, nil
}

func derive(report *Report) {
	report.ProgramsAnalyzed = uint32(len(report.Programs))
	for _, row := range report.Programs {
		increment(&report.BaselineCounts, row.BaselinePlacement)
		semanticPlacement := placementForBackend(row.SemanticBackend)
		increment(&report.SemanticCounts, semanticPlacement)
		baselineDecisive := row.BaselinePlacement != PlacementUnknown
		semanticDecisive := semanticPlacement != PlacementUnknown
		switch {
		case !baselineDecisive && semanticDecisive:
			report.SafePrecisionGains++
		case baselineDecisive && !semanticDecisive:
			report.ReplacementRegressions++
		case baselineDecisive && semanticDecisive && row.BaselinePlacement == semanticPlacement:
			report.Agreements++
		case baselineDecisive && semanticDecisive:
			report.Disagreements++
		}
	}
	reasons := []string{}
	if report.SafePrecisionGains == 0 {
		reasons = append(reasons, "no_safe_precision_gain")
	}
	if report.Disagreements > 0 {
		reasons = append(reasons, "semantic_baseline_disagreement")
	}
	if report.ReplacementRegressions > 0 {
		reasons = append(reasons, "semantic_would_regress_decisive_baseline")
	}
	sort.Strings(reasons)
	if report.SafePrecisionGains > 0 && report.Disagreements == 0 && report.ReplacementRegressions == 0 {
		report.Decision = Decision{Status: "go_for_minimal_integration", ReasonCodes: []string{}, CurrentRouterRetained: false, ConsumerAdmitted: false}
	} else {
		report.Decision = Decision{Status: "no_go", ReasonCodes: reasons, CurrentRouterRetained: true, ConsumerAdmitted: false}
	}
}

func (report Report) Validate() error {
	if report.SchemaVersion != SchemaVersion || !validTarget(report.Target) || report.SealSHA256 != reportSeal(report) ||
		report.Programs == nil || len(report.Programs) == 0 || len(report.Programs) > 256 || report.Decision.ConsumerAdmitted {
		return ErrInvalid
	}
	observations := make([]observation, len(report.Programs))
	for index, row := range report.Programs {
		observations[index] = observation{ProgramID: row.ProgramID, Baseline: row.BaselinePlacement, Semantic: row.SemanticBackend, Rejections: row.SemanticRejections}
	}
	expected, err := buildUnsealed(report.Target, observations)
	if err != nil {
		return ErrInvalid
	}
	expected.SealSHA256 = report.SealSHA256
	left, _ := json.Marshal(report)
	right, _ := json.Marshal(expected)
	if !bytes.Equal(left, right) {
		return ErrInvalid
	}
	return nil
}

func buildUnsealed(target Target, observations []observation) (Report, error) {
	report := Report{SchemaVersion: SchemaVersion, Target: target, Programs: make([]ProgramResult, 0, len(observations))}
	lastID := ""
	for _, value := range observations {
		if !idPattern.MatchString(value.ProgramID) || value.ProgramID <= lastID || !validPlacement(value.Baseline) || !validSemantic(value.Semantic) ||
			!sortedUniqueRejections(value.Rejections) || (value.Semantic == semantic.BackendUnknown) != (len(value.Rejections) > 0) {
			return Report{}, ErrInvalid
		}
		lastID = value.ProgramID
		report.Programs = append(report.Programs, ProgramResult{ProgramID: value.ProgramID, BaselinePlacement: value.Baseline, SemanticBackend: value.Semantic, SemanticRejections: append([]semantic.RejectionReason{}, value.Rejections...)})
	}
	derive(&report)
	return report, nil
}

func Encode(report Report) ([]byte, error) {
	if report.Validate() != nil {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maxReportBytes {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func Decode(raw []byte) (Report, error) {
	if len(raw) == 0 || len(raw) > maxReportBytes || rejectDuplicateJSON(raw) != nil {
		return Report{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report Report
	if decoder.Decode(&report) != nil {
		return Report{}, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || report.Validate() != nil {
		return Report{}, ErrInvalid
	}
	canonical, _ := json.Marshal(report)
	if !bytes.Equal(canonical, raw) {
		return Report{}, ErrInvalid
	}
	return report, nil
}

func reportSeal(report Report) string {
	report.SealSHA256 = ""
	encoded, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("pysolate.semantic-placement-census.v0\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validTarget(target Target) bool {
	return commitPattern.MatchString(target.ArtifactSourceCommit) && digestPattern.MatchString(target.ArtifactSHA256) &&
		digestPattern.MatchString(target.AnalyzerSHA256) && digestPattern.MatchString(target.ExecutionProfileSHA256) &&
		digestPattern.MatchString(target.ImportClosureSHA256) && digestPattern.MatchString(target.CapabilityPlanSHA256) &&
		digestPattern.MatchString(target.CorpusSHA256)
}

func validPlacement(value string) bool {
	return value == PlacementWASM || value == PlacementNative || value == PlacementUnknown
}
func validSemantic(value semantic.BackendRequirement) bool {
	return value == semantic.BackendPysolate || value == semantic.BackendNative || value == semantic.BackendUnknown
}
func placementForBackend(value semantic.BackendRequirement) string {
	switch value {
	case semantic.BackendPysolate:
		return PlacementWASM
	case semantic.BackendNative:
		return PlacementNative
	default:
		return PlacementUnknown
	}
}
func increment(counts *Counts, value string) {
	switch value {
	case PlacementWASM:
		counts.WASM++
	case PlacementNative:
		counts.Native++
	default:
		counts.Unknown++
	}
}
func sortedUniqueRejections(values []semantic.RejectionReason) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalid
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return ErrInvalid
	}
	return nil
}
