package semantic

import (
	"context"
	"errors"
	"reflect"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/internal/semantictrusted"
)

var ErrUnverifiedAnalysis = errors.New("semantic analysis provenance is unverified")

// VerifiedAnalysis is an opaque Host-qualified result minted only after the exact
// analyzer Runner and its bound artifact/profile have passed Analyze. "Verified"
// is not a signature or trust anchor against the Host application itself: the Host
// selects the artifact and remains in the TCB.
type VerifiedAnalysis struct {
	analysisJSON []byte
	properties   enginecontract.Properties
}

// AnalyzeVerified accepts only the concrete target-Guest Wazero engine. Arbitrary
// engine.Runner implementations may be used with Analyze for untrusted reports, but
// cannot mint VerifiedAnalysis. This is an implementation/provenance boundary inside
// the Host TCB, not sandboxing for hostile in-process Host plugins; analysis never
// enlarges the sealed capability Plan.
func AnalyzeVerified(ctx context.Context, runner *wazeroengine.Engine, request Request) (VerifiedAnalysis, error) {
	return analyzeVerified(ctx, runner, request)
}

// AnalyzeVerifiedSession preserves the same concrete target-Guest provenance
// boundary while allowing multiple exact requests in one private bounded session.
func AnalyzeVerifiedSession(ctx context.Context, session *wazeroengine.SemanticAnalysisSession, request Request) (VerifiedAnalysis, error) {
	return analyzeVerified(ctx, session, request)
}

// AnalyzeVerifiedTrusted is restricted by Go's internal-package boundary to runtime
// TCB code. It exists for Host-internal composition and tests; external plugins cannot
// name or construct semantictrusted.Authority.
func AnalyzeVerifiedTrusted(ctx context.Context, authority semantictrusted.Authority, request Request) (VerifiedAnalysis, error) {
	return analyzeVerified(ctx, authority.Runner(), request)
}

func analyzeVerified(ctx context.Context, runner enginecontract.Runner, request Request) (VerifiedAnalysis, error) {
	if runner == nil {
		return VerifiedAnalysis{}, ErrAnalyzerUnavailable
	}
	properties := cloneProperties(runner.Properties())
	if properties.Validate() != nil || properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return VerifiedAnalysis{}, ErrAnalyzerEngineBinding
	}
	analysis, err := Analyze(ctx, runner, request)
	if err != nil {
		return VerifiedAnalysis{}, err
	}
	if after := cloneProperties(runner.Properties()); !reflect.DeepEqual(properties, after) {
		return VerifiedAnalysis{}, ErrAnalyzerEngineBinding
	}
	_, encoded, err := analysis.Identity()
	if err != nil {
		return VerifiedAnalysis{}, err
	}
	return VerifiedAnalysis{analysisJSON: encoded, properties: properties}, nil
}

func (verified VerifiedAnalysis) Analysis() (Analysis, error) {
	if len(verified.analysisJSON) == 0 {
		return Analysis{}, ErrUnverifiedAnalysis
	}
	analysis, err := DecodeAnalysis(verified.analysisJSON)
	if err != nil {
		return Analysis{}, ErrUnverifiedAnalysis
	}
	return analysis, nil
}

// VerifiedWholeRunPlan binds one validated Plan to the exact verified analysis
// that produced its embedded report. Its fields are intentionally inaccessible.
type VerifiedWholeRunPlan struct {
	analysisJSON []byte
	planJSON     []byte
	properties   enginecontract.Properties
}

func BindVerifiedWholeRunPlan(verified VerifiedAnalysis, plan Plan) (VerifiedWholeRunPlan, error) {
	analysis, err := verified.Analysis()
	if err != nil || plan.Validate() != nil {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	analysisIdentity, _, err := analysis.Identity()
	if err != nil {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	planAnalysisIdentity, _, err := plan.Analysis.Identity()
	if err != nil || planAnalysisIdentity != analysisIdentity {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	_, planJSON, err := plan.Identity()
	if err != nil {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	return VerifiedWholeRunPlan{
		analysisJSON: append([]byte(nil), verified.analysisJSON...),
		planJSON:     planJSON,
		properties:   cloneProperties(verified.properties),
	}, nil
}

func (verified VerifiedWholeRunPlan) Bound() (Analysis, Plan, enginecontract.Properties, error) {
	if len(verified.analysisJSON) == 0 || len(verified.planJSON) == 0 {
		return Analysis{}, Plan{}, enginecontract.Properties{}, ErrUnverifiedAnalysis
	}
	analysis, err := DecodeAnalysis(verified.analysisJSON)
	if err != nil {
		return Analysis{}, Plan{}, enginecontract.Properties{}, ErrUnverifiedAnalysis
	}
	plan, err := DecodePlan(verified.planJSON)
	if err != nil {
		return Analysis{}, Plan{}, enginecontract.Properties{}, ErrUnverifiedAnalysis
	}
	return analysis, plan, cloneProperties(verified.properties), nil
}

func cloneProperties(properties enginecontract.Properties) enginecontract.Properties {
	properties.AllowedImports = append([]string(nil), properties.AllowedImports...)
	properties.AvailableImports = append([]string(nil), properties.AvailableImports...)
	properties.QualifiedImports = append([]string(nil), properties.QualifiedImports...)
	return properties
}
