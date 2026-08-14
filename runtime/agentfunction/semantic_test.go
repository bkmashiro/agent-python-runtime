package agentfunction_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestQualifiedGuestInvocationIsOpaquePlanBoundAndRequestBound(t *testing.T) {
	invocation := cacheableInvocation()
	request := qualifiedSemanticRequest(t, []string{})
	analysis := semanticAnalysisFor(invocation)
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
	qualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, analysis, plan, request)
	if err != nil {
		t.Fatal(err)
	}
	baseKey, _, err := qualified.Identity()
	if err != nil {
		t.Fatal(err)
	}
	changedRequest := qualifiedSemanticRequest(t, []string{"sys"})
	changedQualified, err := agentfunction.NewQualifiedGuestInvocation(invocation, analysis, plan, changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	changedKey, _, _ := changedQualified.Identity()
	if changedKey == baseKey {
		t.Fatal("compatibility contract did not change identity")
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
			if _, err := agentfunction.NewQualifiedGuestInvocation(candidate, analysis, plan, request); !errors.Is(err, agentfunction.ErrGuestQualification) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	unsafe := plan
	unsafe.Regions = append([]semantic.Region{}, plan.Regions...)
	unsafe.Regions[0].Effects.MayObserveLive = true
	if _, err := agentfunction.NewQualifiedGuestInvocation(invocation, analysis, unsafe, request); !errors.Is(err, agentfunction.ErrGuestQualification) {
		t.Fatalf("unsafe error=%v", err)
	}
}

func semanticAnalysisFor(invocation agentfunction.Invocation) semantic.Analysis {
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  invocation.FunctionSourceSHA256, ASTSHA256: digest('a'), AnalyzerSHA256: digest('b'),
		ArtifactSHA256: invocation.ArtifactSHA256, ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256: invocation.ImportClosureSHA256, CapabilityPlanSHA256: digest('c'),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: 1},
		Functions:  []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
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
