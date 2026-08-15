package labview

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const SemanticRegionGraphSchemaVersion = "pysolate.lab-semantic-regions.v0"

type SemanticRegionDataEdge struct {
	Name             string `json:"name"`
	ProducerRegionID string `json:"producer_region_id"`
}

type SemanticRegionView struct {
	ID                    string                   `json:"id"`
	Kind                  string                   `json:"kind"`
	Span                  semantic.SourceSpan      `json:"span"`
	ControlPredecessors   []string                 `json:"control_predecessors"`
	DataDependencies      []SemanticRegionDataEdge `json:"data_dependencies"`
	LiveIns               []string                 `json:"live_ins"`
	LiveOuts              []string                 `json:"live_outs"`
	LiveInsCanonical      bool                     `json:"live_ins_canonical"`
	LiveOutsCanonical     bool                     `json:"live_outs_canonical"`
	Effects               semantic.EffectSummary   `json:"effects"`
	CapabilityOccurrences []string                 `json:"capability_occurrences"`
	Barriers              []semantic.BarrierCode   `json:"barriers"`
	RejectionReasons      []string                 `json:"rejection_reasons"`
}

type SemanticRegionGraph struct {
	SchemaVersion   string               `json:"schema_version"`
	AnalysisSHA256  string               `json:"analysis_sha256"`
	SourceSHA256    string               `json:"source_sha256"`
	AnalyzerSHA256  string               `json:"analyzer_sha256"`
	SourcePrivacy   Privacy              `json:"source_privacy"`
	SourceAvailable bool                 `json:"source_available"`
	Source          string               `json:"source,omitempty"`
	Regions         []SemanticRegionView `json:"regions"`
}

// ProjectSemanticRegionGraph accepts only the opaque Host-minted analysis handle.
// The projection is read-only and carries no execution or scheduling authority.
func ProjectSemanticRegionGraph(verified semantic.VerifiedAnalysis, source string, includePrivateSource bool) (SemanticRegionGraph, error) {
	analysis, err := verified.Analysis()
	if err != nil {
		return SemanticRegionGraph{}, ErrInvalid
	}
	analysisSHA, _, err := analysis.Identity()
	if err != nil {
		return SemanticRegionGraph{}, ErrInvalid
	}
	if source != "" {
		digest := sha256.Sum256([]byte(source))
		if fmt.Sprintf("sha256:%x", digest[:]) != analysis.SourceSHA256 {
			return SemanticRegionGraph{}, ErrInvalid
		}
	}
	if includePrivateSource && source == "" {
		return SemanticRegionGraph{}, ErrInvalid
	}
	view := SemanticRegionGraph{
		SchemaVersion:   SemanticRegionGraphSchemaVersion,
		AnalysisSHA256:  analysisSHA,
		SourceSHA256:    analysis.SourceSHA256,
		AnalyzerSHA256:  analysis.AnalyzerSHA256,
		SourcePrivacy:   PrivacyPortable,
		SourceAvailable: includePrivateSource,
		Regions:         make([]SemanticRegionView, len(analysis.CandidateRegions)),
	}
	if includePrivateSource {
		view.SourcePrivacy = PrivacyPrivate
		view.Source = source
	}
	for index, region := range analysis.CandidateRegions {
		dependencies := make([]SemanticRegionDataEdge, len(region.DataDependencies))
		for edgeIndex, edge := range region.DataDependencies {
			dependencies[edgeIndex] = SemanticRegionDataEdge{Name: edge.Name, ProducerRegionID: edge.ProducerRegionID}
		}
		rejections := make([]string, len(region.RejectionReasons))
		for rejectionIndex, rejection := range region.RejectionReasons {
			rejections[rejectionIndex] = string(rejection)
		}
		view.Regions[index] = SemanticRegionView{
			ID: region.ID, Kind: string(region.Kind), Span: region.Span,
			ControlPredecessors: cloneSlice(region.ControlPredecessors),
			DataDependencies:    dependencies,
			LiveIns:             cloneSlice(region.LiveIns), LiveOuts: cloneSlice(region.LiveOuts),
			LiveInsCanonical: region.LiveInsCanonical, LiveOutsCanonical: region.LiveOutsCanonical,
			Effects:               region.Effects,
			CapabilityOccurrences: cloneSlice(region.CapabilityOccurrences),
			Barriers:              cloneSlice(region.Barriers), RejectionReasons: rejections,
		}
	}
	if err := ValidateSemanticRegionGraph(view); err != nil {
		return SemanticRegionGraph{}, err
	}
	return view, nil
}

func cloneSlice[T any](values []T) []T {
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func ValidateSemanticRegionGraph(view SemanticRegionGraph) error {
	if view.SchemaVersion != SemanticRegionGraphSchemaVersion || !digestRE.MatchString(view.AnalysisSHA256) ||
		!digestRE.MatchString(view.SourceSHA256) || !digestRE.MatchString(view.AnalyzerSHA256) || view.Regions == nil || len(view.Regions) > 256 {
		return ErrInvalid
	}
	if view.SourceAvailable != (view.Source != "") || view.SourceAvailable != (view.SourcePrivacy == PrivacyPrivate) {
		return ErrInvalid
	}
	if !view.SourceAvailable && view.SourcePrivacy != PrivacyPortable {
		return ErrInvalid
	}
	if view.SourceAvailable {
		digest := sha256.Sum256([]byte(view.Source))
		if fmt.Sprintf("sha256:%x", digest[:]) != view.SourceSHA256 {
			return ErrInvalid
		}
	}
	seen := make(map[string]struct{}, len(view.Regions))
	for index, region := range view.Regions {
		if !digestRE.MatchString(region.ID) || !validSemanticRegionView(region, seen) {
			return ErrInvalid
		}
		if index == 0 {
			if len(region.ControlPredecessors) != 0 {
				return ErrInvalid
			}
		} else if len(region.ControlPredecessors) != 1 || region.ControlPredecessors[0] != view.Regions[index-1].ID {
			return ErrInvalid
		}
		seen[region.ID] = struct{}{}
	}
	return nil
}

func validSemanticRegionView(region SemanticRegionView, seen map[string]struct{}) bool {
	if region.ControlPredecessors == nil || region.DataDependencies == nil || region.LiveIns == nil || region.LiveOuts == nil ||
		region.CapabilityOccurrences == nil || region.Barriers == nil || region.RejectionReasons == nil ||
		len(region.LiveIns) > 256 || len(region.LiveOuts) > 256 || len(region.DataDependencies) > 256 ||
		len(region.CapabilityOccurrences) > 256 || len(region.Barriers) > 256 || len(region.RejectionReasons) > 256 ||
		!strictStrings(region.LiveIns) || !strictStrings(region.LiveOuts) ||
		!strictStrings(region.CapabilityOccurrences) || !strictStrings(region.RejectionReasons) || !strictBarriers(region.Barriers) ||
		!strictDataEdges(region.DataDependencies) {
		return false
	}
	if region.Kind != string(semantic.CandidateRegionStraightLine) && region.Kind != string(semantic.CandidateRegionOpaqueControl) && region.Kind != string(semantic.CandidateRegionDeclaration) {
		return false
	}
	for _, edge := range region.DataDependencies {
		if _, ok := seen[edge.ProducerRegionID]; !ok || !containsString(region.LiveIns, edge.Name) {
			return false
		}
	}
	return true
}

func strictStrings(values []string) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func strictBarriers(values []semantic.BarrierCode) bool {
	for index, value := range values {
		if value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func strictDataEdges(values []SemanticRegionDataEdge) bool {
	for index, value := range values {
		if value.Name == "" || !digestRE.MatchString(value.ProducerRegionID) {
			return false
		}
		if index > 0 {
			previous := values[index-1]
			if previous.Name > value.Name || previous.Name == value.Name && previous.ProducerRegionID >= value.ProducerRegionID {
				return false
			}
		}
	}
	return true
}

func containsString(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}
