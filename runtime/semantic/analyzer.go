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
	Name        string `json:"name"`
	EffectClass string `json:"effect_class"`
	Playback    string `json:"playback"`
	Module      string `json:"module"`
	Method      string `json:"method"`
	GlobalAlias string `json:"global_alias"`
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
	return analysis.SourceSHA256 == fmt.Sprintf("sha256:%x", sourceDigest[:]) &&
		analysis.ArtifactSHA256 == request.Bindings.ArtifactSHA256 &&
		analysis.ExecutionProfileSHA256 == request.Bindings.ExecutionProfileSHA256 &&
		analysis.ImportClosureSHA256 == request.Bindings.ImportClosureSHA256 &&
		analysis.CapabilityPlanSHA256 == request.Bindings.CapabilityPlanSHA256
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
