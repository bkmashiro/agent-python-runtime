package placement

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func plainPolicy(t *testing.T) Policy {
	t.Helper()
	shard, err := runtimeconfig.NewShardProfile(runtimeconfig.ShardProfileConfig{
		ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"agent_runtime", "json", "math", "sys"},
		ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IdlePolicy:     runtimeconfig.ShardIdleRetireWhenIdle,
	})
	if err != nil {
		t.Fatal(err)
	}
	return Policy{AnalyzerVersion: "static-v1", PysolateAvailable: true, NativeAvailable: true, PlainShard: shard}
}

func TestAnalyzerRoutingMatrix(t *testing.T) {
	policy := plainPolicy(t)
	cases := []struct {
		name      string
		request   runtimeconfig.RunRequest
		state     runtimeconfig.NativeStateClass
		modelRisk bool
		backend   runtimeconfig.ExecutionBackend
		reason    Reason
	}{
		{"pure", runtimeconfig.RunRequest{RunID: "r", Code: "result = inputs['x'] * 2", Inputs: json.RawMessage(`{"x":2}`)}, runtimeconfig.StatePortableValue, false, runtimeconfig.BackendPysolateWASM, ReasonQualifiedPlainShard},
		{"stdlib", runtimeconfig.RunRequest{RunID: "r", Code: "import json\nresult = json.dumps(inputs)", Inputs: json.RawMessage(`{}`)}, runtimeconfig.StatePortableValue, false, runtimeconfig.BackendPysolateWASM, ReasonQualifiedPlainShard},
		{"shell", runtimeconfig.RunRequest{RunID: "r", Code: "result = 1", Inputs: json.RawMessage(`{}`), Requirements: []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeatureShell}}, runtimeconfig.StatePortableValue, false, runtimeconfig.BackendNativeSandbox, ReasonRequiredNativeFeature},
		{"unknown-import", runtimeconfig.RunRequest{RunID: "r", Code: "import numpy\nresult = 1", Inputs: json.RawMessage(`{}`)}, runtimeconfig.StatePortableValue, false, runtimeconfig.BackendNativeSandbox, ReasonNoQualifiedShard},
		{"dynamic", runtimeconfig.RunRequest{RunID: "r", Code: "result = __import__('json')", Inputs: json.RawMessage(`{}`)}, runtimeconfig.StatePortableValue, false, runtimeconfig.BackendNativeSandbox, ReasonSourceIndeterminate},
		{"workspace", runtimeconfig.RunRequest{RunID: "r", Code: "result = 1", Inputs: json.RawMessage(`{}`)}, runtimeconfig.StateWorkspaceRef, false, runtimeconfig.BackendNativeSandbox, ReasonNativeStateDependency},
		{"model-risk", runtimeconfig.RunRequest{RunID: "r", Code: "result = 1", Inputs: json.RawMessage(`{}`)}, runtimeconfig.StatePortableValue, true, runtimeconfig.BackendNativeSandbox, ReasonModelRiskSignal},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decision, err := Analyze(test.request, test.state, test.modelRisk, policy)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Backend != test.backend || decision.Reason != test.reason || decision.Identity == "" {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestAnalyzerReturnsTypedUnavailableWithoutWeakerExecution(t *testing.T) {
	policy := plainPolicy(t)
	policy.NativeAvailable = false
	decision, err := Analyze(runtimeconfig.RunRequest{RunID: "r", Code: "result = 1", Inputs: json.RawMessage(`{}`), Requirements: []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeatureShell}}, runtimeconfig.StatePortableValue, false, policy)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusUnavailable || decision.Backend != "" || decision.Reason != ReasonNativeUnavailable {
		t.Fatalf("decision=%+v", decision)
	}
}

type fakeBackend struct {
	calls  int
	result []byte
	err    error
	last   Plan
}

func (backend *fakeBackend) Execute(_ context.Context, plan Plan, _ []byte) ([]byte, error) {
	backend.calls++
	backend.last = plan
	return backend.result, backend.err
}

func TestOrchestratorRunsPlacementBeforeBackendAndSupportsBoundedL2(t *testing.T) {
	wasm := &fakeBackend{err: &runtimeconfig.UnsupportedRunError{Code: runtimeconfig.OutcomeRuntimeUnsupported, RequiredFeatures: []runtimeconfig.RequiredFeature{runtimeconfig.RequiredFeaturePOSIX}}}
	native := &fakeBackend{result: []byte(`{"status":"ok"}`)}
	orchestrator := Orchestrator{Policy: plainPolicy(t), Pysolate: wasm, Native: native}
	raw := []byte(`{"run_id":"r","code":"result = 1","inputs":{}}`)
	result, err := orchestrator.Execute(context.Background(), raw, runtimeconfig.StatePortableValue, false)
	if err != nil {
		t.Fatal(err)
	}
	if wasm.calls != 1 || native.calls != 1 || result.Decision.Backend != runtimeconfig.BackendNativeSandbox || result.Promotion == nil {
		t.Fatalf("wasm=%d native=%d result=%+v", wasm.calls, native.calls, result)
	}
	if result.Promotion.WorkspaceDisposition != runtimeconfig.WorkspaceNotStarted || result.Promotion.EffectDisposition != runtimeconfig.EffectsNotStarted || native.last.ParentDecisionID == "" {
		t.Fatalf("promotion=%+v plan=%+v", result.Promotion, native.last)
	}
}

func TestOrchestratorNeverPromotesOrdinaryFailure(t *testing.T) {
	wasm := &fakeBackend{err: errors.New("ImportError: pretend native")}
	native := &fakeBackend{}
	orchestrator := Orchestrator{Policy: plainPolicy(t), Pysolate: wasm, Native: native}
	_, err := orchestrator.Execute(context.Background(), []byte(`{"run_id":"r","code":"result = 1","inputs":{}}`), runtimeconfig.StatePortableValue, false)
	if err == nil || native.calls != 0 {
		t.Fatalf("err=%v native=%d", err, native.calls)
	}
}

func TestOrchestratorRejectsBeforeAnyBackend(t *testing.T) {
	wasm := &fakeBackend{}
	native := &fakeBackend{}
	orchestrator := Orchestrator{Policy: plainPolicy(t), Pysolate: wasm, Native: native}
	if _, err := orchestrator.Execute(context.Background(), []byte(`{"run_id":"r","code":"","inputs":{}}`), runtimeconfig.StatePortableValue, false); err == nil {
		t.Fatal("invalid request accepted")
	}
	if wasm.calls != 0 || native.calls != 0 {
		t.Fatalf("wasm=%d native=%d", wasm.calls, native.calls)
	}
}
