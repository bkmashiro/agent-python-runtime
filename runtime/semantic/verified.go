package semantic

import (
	"context"
	"errors"
	"reflect"
	"slices"

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
	analysis   *Analysis
	properties enginecontract.Properties
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
	frozen := cloneAnalysis(analysis)
	return VerifiedAnalysis{analysis: &frozen, properties: properties}, nil
}

func (verified VerifiedAnalysis) Analysis() (Analysis, error) {
	if verified.analysis == nil {
		return Analysis{}, ErrUnverifiedAnalysis
	}
	return cloneAnalysis(*verified.analysis), nil
}

// VerifiedWholeRunPlan binds one validated Plan to the exact verified analysis
// that produced its embedded report. Its fields are intentionally inaccessible.
type VerifiedWholeRunPlan struct {
	plan       *Plan
	properties enginecontract.Properties
}

func BindVerifiedWholeRunPlan(verified VerifiedAnalysis, plan Plan) (VerifiedWholeRunPlan, error) {
	if verified.analysis == nil {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	frozenPlan := clonePlan(plan)
	if frozenPlan.Validate() != nil || !reflect.DeepEqual(*verified.analysis, frozenPlan.Analysis) {
		return VerifiedWholeRunPlan{}, ErrUnverifiedAnalysis
	}
	return VerifiedWholeRunPlan{
		plan:       &frozenPlan,
		properties: cloneProperties(verified.properties),
	}, nil
}

func (verified VerifiedWholeRunPlan) Bound() (Analysis, Plan, enginecontract.Properties, error) {
	if verified.plan == nil {
		return Analysis{}, Plan{}, enginecontract.Properties{}, ErrUnverifiedAnalysis
	}
	return cloneAnalysis(verified.plan.Analysis), clonePlan(*verified.plan), cloneProperties(verified.properties), nil
}

func cloneAnalysis(value Analysis) Analysis {
	// Analysis has no map fields; copy every mutable slice, including the raw
	// canonical-argument bytes, so returned reports cannot alias the handle.
	cloned := value
	cloned.Functions = slices.Clone(value.Functions)
	for index := range cloned.Functions {
		cloned.Functions[index].Calls = slices.Clone(value.Functions[index].Calls)
		cloned.Functions[index].DirectCapabilities = slices.Clone(value.Functions[index].DirectCapabilities)
	}
	cloned.Barriers = slices.Clone(value.Barriers)
	cloned.CallSites = slices.Clone(value.CallSites)
	for index := range cloned.CallSites {
		cloned.CallSites[index].CanonicalArguments = slices.Clone(value.CallSites[index].CanonicalArguments)
	}
	cloned.CandidateRegions = slices.Clone(value.CandidateRegions)
	for index := range cloned.CandidateRegions {
		cloned.CandidateRegions[index].ControlPredecessors = slices.Clone(value.CandidateRegions[index].ControlPredecessors)
		cloned.CandidateRegions[index].DataDependencies = slices.Clone(value.CandidateRegions[index].DataDependencies)
		cloned.CandidateRegions[index].LiveIns = slices.Clone(value.CandidateRegions[index].LiveIns)
		cloned.CandidateRegions[index].LiveOuts = slices.Clone(value.CandidateRegions[index].LiveOuts)
		cloned.CandidateRegions[index].CapabilityOccurrences = slices.Clone(value.CandidateRegions[index].CapabilityOccurrences)
		cloned.CandidateRegions[index].Barriers = slices.Clone(value.CandidateRegions[index].Barriers)
		cloned.CandidateRegions[index].RejectionReasons = slices.Clone(value.CandidateRegions[index].RejectionReasons)
	}
	return cloned
}

func clonePlan(value Plan) Plan {
	cloned := value
	cloned.Analysis = cloneAnalysis(value.Analysis)
	cloned.Regions = slices.Clone(value.Regions)
	for index := range cloned.Regions {
		cloned.Regions[index].Dependencies = slices.Clone(value.Regions[index].Dependencies)
		cloned.Regions[index].RejectionReasons = slices.Clone(value.Regions[index].RejectionReasons)
	}
	return cloned
}

func cloneProperties(properties enginecontract.Properties) enginecontract.Properties {
	properties.AllowedImports = slices.Clone(properties.AllowedImports)
	properties.AvailableImports = slices.Clone(properties.AvailableImports)
	properties.QualifiedImports = slices.Clone(properties.QualifiedImports)
	return properties
}
