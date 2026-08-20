package agentfunction_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/internal/semantictrusted"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestQualifiedGuestInvocationRequiresVerifiedProvenanceAndCompatibility(t *testing.T) {
	invocation := cacheableInvocation()
	invocation.ImportClosureSHA256 = agentfunction.ImportClosureIdentity([]string{"sys"}, []string{"sys"})
	request := qualifiedSemanticRequest(t, []string{})
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256([]byte(decoded.Code))
	invocation.FunctionSourceSHA256 = fmt.Sprintf("sha256:%x", sourceDigest[:])
	analysis := semanticAnalysisFor(invocation)
	verified := verifiedSemanticPlanFor(t, invocation, analysis)
	qualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, verified, request)
	if err != nil {
		t.Fatal(err)
	}
	baseKey, _, err := qualified.Identity()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := agentfunction.NewQualifiedGuestInvocation(invocation, semantic.VerifiedWholeRunPlan{}, request); !errors.Is(err, agentfunction.ErrGuestQualification) {
		t.Fatalf("forged provenance error=%v", err)
	}

	mismatchedImports := qualifiedSemanticRequest(t, []string{"sys"})
	if _, err := agentfunction.NewQualifiedGuestInvocation(invocation, verified, mismatchedImports); !errors.Is(err, agentfunction.ErrGuestQualification) {
		t.Fatalf("compatibility error=%v", err)
	}

	var unsupported map[string]any
	if err := json.Unmarshal(request, &unsupported); err != nil {
		t.Fatal(err)
	}
	unsupported["requirements"] = []string{"browser_runtime"}
	unsupportedRequest, err := json.Marshal(unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentfunction.NewQualifiedGuestInvocation(invocation, verified, unsupportedRequest); !errors.Is(err, agentfunction.ErrGuestQualification) {
		t.Fatalf("requirements error=%v", err)
	}

	for name, mutate := range map[string]func(*semantic.Analysis){
		"capability plan": func(value *semantic.Analysis) { value.CapabilityPlanSHA256 = digest('e') },
	} {
		t.Run(name, func(t *testing.T) {
			changed := analysis
			mutate(&changed)
			changedVerified := verifiedSemanticPlanFor(t, invocation, changed)
			changedQualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, changedVerified, request)
			if err != nil {
				t.Fatal(err)
			}
			changedKey, _, _ := changedQualified.Identity()
			if changedKey == baseKey {
				t.Fatal("semantic qualification change did not change identity")
			}
		})
	}

	for name, mutate := range map[string]func(*agentfunction.Invocation){
		"source":   func(value *agentfunction.Invocation) { value.FunctionSourceSHA256 = digest('a') },
		"artifact": func(value *agentfunction.Invocation) { value.ArtifactSHA256 = digest('b') },
		"profile":  func(value *agentfunction.Invocation) { value.ExecutionProfileSHA256 = digest('c') },
		"imports":  func(value *agentfunction.Invocation) { value.ImportClosureSHA256 = digest('d') },
		"inputs":   func(value *agentfunction.Invocation) { value.CanonicalInputs = json.RawMessage(`{"value":2}`) },
		"roots":    func(value *agentfunction.Invocation) { value.ImmutableRootSHA256 = []string{digest('e')} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := invocation
			mutate(&candidate)
			if _, err := agentfunction.NewQualifiedGuestInvocation(candidate, verified, request); !errors.Is(err, agentfunction.ErrGuestQualification) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	unsafeAnalysis := analysis
	unsafeAnalysis.ModuleEffects.MayObserveLive = true
	unsafeVerified := verifiedSemanticPlanFor(t, invocation, unsafeAnalysis)
	if _, err := agentfunction.NewQualifiedGuestInvocation(invocation, unsafeVerified, request); !errors.Is(err, agentfunction.ErrGuestQualification) {
		t.Fatalf("unsafe error=%v", err)
	}
}

func verifiedSemanticPlanFor(t *testing.T, invocation agentfunction.Invocation, template semantic.Analysis) semantic.VerifiedWholeRunPlan {
	t.Helper()
	bindings := semantic.Bindings{
		ArtifactSHA256: invocation.ArtifactSHA256, ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256: invocation.ImportClosureSHA256, CapabilityPlanSHA256: template.CapabilityPlanSHA256,
	}
	request, err := semantic.NewRequest("result = 1", bindings, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &verifiedFixtureRunner{template: template, bindings: bindings}
	verifiedAnalysis, err := semantic.AnalyzeVerifiedTrusted(context.Background(), semantictrusted.New(runner), request)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verifiedAnalysis.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := agentfunction.SemanticWholeRunDependencies(invocation)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		Dependencies: dependencies, InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedPlan, err := semantic.BindVerifiedWholeRunPlan(verifiedAnalysis, plan)
	if err != nil {
		t.Fatal(err)
	}
	return verifiedPlan
}

type verifiedFixtureRunner struct {
	template semantic.Analysis
	bindings semantic.Bindings
}

func (runner *verifiedFixtureRunner) AnalyzeSemantic(_ context.Context, payload []byte) ([]byte, error) {
	var request semantic.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	sourceDigest := sha256.Sum256([]byte(request.Source))
	analysis := runner.template
	analysis.SourceSHA256 = fmt.Sprintf("sha256:%x", sourceDigest[:])
	analysis.ArtifactSHA256 = request.Bindings.ArtifactSHA256
	analysis.ExecutionProfileSHA256 = request.Bindings.ExecutionProfileSHA256
	analysis.ImportClosureSHA256 = request.Bindings.ImportClosureSHA256
	analysis.CapabilityPlanSHA256 = request.Bindings.CapabilityPlanSHA256
	if request.Source != "" {
		span := semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: uint32(len([]byte(strings.TrimSuffix(request.Source, "\n"))))}
		regionDigest := sha256.Sum256([]byte(fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x000\x00%d:%d:%d:%d", analysis.SourceSHA256, span.StartLine, span.StartColumn, span.EndLine, span.EndColumn)))
		controlDigest := sha256.Sum256([]byte("pysolate.semantic-control-region.v0\x00" + analysis.SourceSHA256 + "\x00module-entry"))
		analysis.ModuleSpan = span
		analysis.CandidateRegionCount = 1
		analysis.CandidateRegions = []semantic.CandidateRegion{{
			ID: fmt.Sprintf("sha256:%x", regionDigest[:]), Kind: semantic.CandidateRegionStraightLine, Span: span,
			ControlRegionID: fmt.Sprintf("sha256:%x", controlDigest[:]), ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{},
			LiveIns: []string{}, LiveOuts: []string{}, LiveInsCanonical: true, LiveOutsCanonical: true, Effects: analysis.ModuleEffects,
			CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
		}}
	}
	return json.Marshal(analysis)
}
func (runner *verifiedFixtureRunner) Run(context.Context, []byte, string) ([]byte, error) {
	return nil, nil
}
func (runner *verifiedFixtureRunner) Close(context.Context) error { return nil }
func (runner *verifiedFixtureRunner) Properties() enginecontract.Properties {
	return enginecontract.Properties{
		Backend: "verified-fixture", ExecutionProfileID: "base", AllowedImports: []string{"sys"},
		AvailableImports: []string{"sys"}, QualifiedImports: []string{"sys"},
		ArtifactSHA256: runner.bindings.ArtifactSHA256, ManifestSHA256: digest('f'),
		ExecutionProfileBindingSHA256: runner.bindings.ExecutionProfileSHA256,
	}
}

func semanticAnalyzerIdentity() string {
	digest := sha256.Sum256([]byte("pysolate.semantic-analyzer.v8"))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func semanticAnalysisFor(invocation agentfunction.Invocation) semantic.Analysis {
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  invocation.FunctionSourceSHA256, ASTSHA256: digest('a'), AnalyzerSHA256: semanticAnalyzerIdentity(),
		ArtifactSHA256: invocation.ArtifactSHA256, ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256: invocation.ImportClosureSHA256, CapabilityPlanSHA256: digest('c'),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: 1},
		Functions:  []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
		CallSiteCoverage: "positive_only", CandidateRegionCoverage: "module_top_level_complete", CallSites: []semantic.CallSite{}, CandidateRegions: []semantic.CandidateRegion{},
	}
}

func qualifiedSemanticRequest(t *testing.T, imports []string) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"run_id": "semantic", "code": "result = 1", "inputs": map[string]any{"value": 1},
		"compatibility": map[string]any{"profile": "base", "imports": imports},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
