package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	ErrAnalyzerUnavailable   = errors.New("semantic analyzer is unavailable")
	ErrAnalysisBinding       = errors.New("semantic analysis binding mismatch")
	ErrAnalyzerEngineBinding = errors.New("semantic analyzer engine binding mismatch")
	ErrInvalidRequest        = errors.New("invalid semantic analysis request")
)

type Bindings struct {
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ImportClosureSHA256    string `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
}

type CapabilityProjection struct {
	Name        string   `json:"name"`
	EffectClass string   `json:"effect_class"`
	Playback    string   `json:"playback"`
	Module      string   `json:"module"`
	Method      string   `json:"method"`
	GlobalAlias string   `json:"global_alias"`
	Arguments   []string `json:"arguments"`
}

type Request struct {
	Source       string                 `json:"source"`
	Bindings     Bindings               `json:"bindings"`
	Capabilities []CapabilityProjection `json:"capabilities"`
}

func NewRequest(source string, bindings Bindings, plan *capability.Plan) (Request, error) {
	projections := []CapabilityProjection{}
	if plan != nil {
		for _, spec := range plan.Specs() {
			if spec.Python == nil {
				continue
			}
			projections = append(projections, CapabilityProjection{
				Name: spec.Name, EffectClass: spec.EffectClass, Playback: spec.Playback,
				Module: spec.Python.Module, Method: spec.Python.Method,
				GlobalAlias: spec.Python.GlobalAlias,
				Arguments:   append([]string{}, spec.Python.Arguments...),
			})
		}
	}
	sort.Slice(projections, func(i, j int) bool { return projections[i].Name < projections[j].Name })
	request := Request{Source: source, Bindings: bindings, Capabilities: projections}
	return request, request.Validate()
}

func (request Request) Validate() error {
	if request.Source == "" || len([]byte(request.Source)) > MaxDocumentBytes || request.Capabilities == nil {
		return ErrInvalidRequest
	}
	for _, digest := range []string{
		request.Bindings.ArtifactSHA256, request.Bindings.ExecutionProfileSHA256,
		request.Bindings.ImportClosureSHA256, request.Bindings.CapabilityPlanSHA256,
	} {
		if !digestPattern.MatchString(digest) {
			return ErrInvalidRequest
		}
	}
	if len(request.Capabilities) > maxFunctions {
		return ErrInvalidRequest
	}
	seenCalls := map[string]struct{}{}
	lastName := ""
	for index, projection := range request.Capabilities {
		if !capabilityPattern.MatchString(projection.Name) || !validIdentifier(projection.Module) ||
			!validIdentifier(projection.Method) || projection.GlobalAlias != "" && !validIdentifier(projection.GlobalAlias) ||
			!validProjectionEffect(projection.EffectClass) || !validProjectionPlayback(projection.Playback) ||
			projection.Arguments == nil || len(projection.Arguments) > maxReferences || !validProjectionArguments(projection.Arguments) ||
			index > 0 && projection.Name <= lastName {
			return ErrInvalidRequest
		}
		for _, call := range []string{projection.Module + "." + projection.Method, projection.GlobalAlias} {
			if call == "" {
				continue
			}
			if _, exists := seenCalls[call]; exists {
				return ErrInvalidRequest
			}
			seenCalls[call] = struct{}{}
		}
		lastName = projection.Name
	}
	return nil
}

func (request Request) Marshal() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > MaxDocumentBytes {
		return nil, ErrInvalidRequest
	}
	return encoded, nil
}

func Analyze(ctx context.Context, runner enginecontract.Runner, request Request) (Analysis, error) {
	if runner == nil {
		return Analysis{}, ErrAnalyzerUnavailable
	}
	analyzer, ok := runner.(enginecontract.SemanticAnalyzer)
	if !ok {
		return Analysis{}, ErrAnalyzerUnavailable
	}
	properties := runner.Properties()
	if properties.ArtifactSHA256 == "" || properties.ExecutionProfileBindingSHA256 == "" ||
		properties.ArtifactSHA256 != request.Bindings.ArtifactSHA256 ||
		properties.ExecutionProfileBindingSHA256 != request.Bindings.ExecutionProfileSHA256 {
		return Analysis{}, ErrAnalyzerEngineBinding
	}
	encoded, err := request.Marshal()
	if err != nil {
		return Analysis{}, err
	}
	payload, err := analyzer.AnalyzeSemantic(ctx, encoded)
	if err != nil {
		return Analysis{}, err
	}
	analysis, err := DecodeAnalysis(payload)
	if err != nil {
		return Analysis{}, err
	}
	if !analysisMatchesRequest(analysis, request) {
		return Analysis{}, ErrAnalysisBinding
	}
	return analysis, nil
}

func analysisMatchesRequest(analysis Analysis, request Request) bool {
	sourceDigest := sha256.Sum256([]byte(request.Source))
	sourceSHA := fmt.Sprintf("sha256:%x", sourceDigest[:])
	if analysis.AnalyzerSHA256 != semanticDigest("pysolate.semantic-analyzer.v8") ||
		analysis.SourceSHA256 != sourceSHA ||
		analysis.ArtifactSHA256 != request.Bindings.ArtifactSHA256 ||
		analysis.ExecutionProfileSHA256 != request.Bindings.ExecutionProfileSHA256 ||
		analysis.ImportClosureSHA256 != request.Bindings.ImportClosureSHA256 ||
		analysis.CapabilityPlanSHA256 != request.Bindings.CapabilityPlanSHA256 {
		return false
	}
	projections := make(map[string]CapabilityProjection, len(request.Capabilities))
	for _, projection := range request.Capabilities {
		projections[projection.Name] = projection
	}
	if strings.TrimSpace(request.Source) != "" && analysis.CandidateRegionCount == 0 {
		return false
	}
	controlRegion := semanticDigest("pysolate.semantic-control-region.v0\x00" + sourceSHA + "\x00module-entry")
	callSites := make(map[string]CallSite, len(analysis.CallSites))
	for _, site := range analysis.CallSites {
		projection, ok := projections[site.Capability]
		if !ok || !sourceContainsSpan(request.Source, site.Span) || site.ControlRegionID != controlRegion || site.ID != semanticCallSiteID(sourceSHA, site) ||
			!argumentsMatchProjection(site.CanonicalArguments, projection.Arguments) {
			return false
		}
		switch projection.EffectClass {
		case capability.EffectWorkspaceRead, capability.EffectExternalRead:
			if !analysis.ModuleEffects.MayObserveLive || !analysis.ModuleEffects.MaySuspend {
				return false
			}
		case capability.EffectWorkspaceWrite:
			if !analysis.ModuleEffects.MayPublish || !analysis.ModuleEffects.MaySuspend {
				return false
			}
		}
		callSites[site.ID] = site
	}
	for index, region := range analysis.CandidateRegions {
		if !sourceContainsSpan(request.Source, region.Span) || region.ID != semanticCandidateRegionID(sourceSHA, index, region.Span) || region.ControlRegionID != controlRegion {
			return false
		}
		for _, occurrence := range region.CapabilityOccurrences {
			site, ok := callSites[occurrence]
			if !ok {
				return false
			}
			projection := projections[site.Capability]
			switch projection.EffectClass {
			case capability.EffectWorkspaceRead, capability.EffectExternalRead:
				if !region.Effects.MayObserveLive || !region.Effects.MaySuspend {
					return false
				}
			case capability.EffectWorkspaceWrite:
				if !region.Effects.MayPublish || !region.Effects.MaySuspend {
					return false
				}
			}
		}
	}
	return true
}

func validProjectionArguments(arguments []string) bool {
	seen := make(map[string]struct{}, len(arguments))
	for _, argument := range arguments {
		if !validIdentifier(argument) {
			return false
		}
		if _, exists := seen[argument]; exists {
			return false
		}
		seen[argument] = struct{}{}
	}
	return true
}

func semanticDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func semanticCallSiteID(sourceSHA string, site CallSite) string {
	return semanticDigest(fmt.Sprintf("pysolate.semantic-call-site.v0\x00%s\x00%s\x00%d:%d:%d:%d",
		sourceSHA, site.Capability, site.Span.StartLine, site.Span.StartColumn, site.Span.EndLine, site.Span.EndColumn))
}

func sourceContainsSpan(source string, span SourceSpan) bool {
	lines := strings.Split(source, "\n")
	if span.StartLine < 1 || span.EndLine < span.StartLine || int(span.StartLine) > len(lines) || int(span.EndLine) > len(lines) {
		return false
	}
	startWidth := len([]byte(lines[span.StartLine-1]))
	endWidth := len([]byte(lines[span.EndLine-1]))
	return int(span.StartColumn) <= startWidth && int(span.EndColumn) <= endWidth &&
		(span.EndLine > span.StartLine || span.EndColumn >= span.StartColumn)
}

func semanticCandidateRegionID(sourceSHA string, index int, span SourceSpan) string {
	return semanticDigest(fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x00%d\x00%d:%d:%d:%d",
		sourceSHA, index, span.StartLine, span.StartColumn, span.EndLine, span.EndColumn))
}

func argumentsMatchProjection(raw json.RawMessage, expected []string) bool {
	var arguments map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&arguments) != nil || len(arguments) != len(expected) {
		return false
	}
	for _, name := range expected {
		if _, ok := arguments[name]; !ok {
			return false
		}
	}
	return true
}

func validProjectionEffect(value string) bool {
	return value == capability.EffectPure || value == capability.EffectWorkspaceRead ||
		value == capability.EffectWorkspaceWrite || value == capability.EffectExternalRead
}

func validProjectionPlayback(value string) bool {
	return value == capability.PlaybackLiveOnly || value == capability.PlaybackCaptured
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	return true
}
