package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

func TestRealGuestPLMSourceTimeCandidateReusesAndLinearizesAtOriginalCall(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"body":"alpha-body"}`), nil
	})}
	plan := plmE2EPlan(t, 1, adapter)
	source := "value = sources.read(\"alpha\")\nindependent = 3 * 4\nresult = [value, independent]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "plm-source-time", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 1)
	defer closeAnalysis()
	plmPass, err := sourcepatch.NewPLMCapabilityCalls(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := plmPass.Transform(context.Background(), session, source, passplugin.PLMCapabilityProjections(plan))
	if err != nil || !patch.Applied() || strings.Contains(patch.DerivedSource, "_pysolate_call_") ||
		!strings.Contains(patch.DerivedSource, "_pysolate_plm_prepare") || !strings.Contains(patch.DerivedSource, "_pysolate_plm_linearize") {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-source-time", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	preissue := []byte(`{"call_id":"plm-s1c8-e1c29-1","capability":"sources.read","arguments":{"path":"alpha"}}`)
	if err := table.PrepareRuntimePLM(context.Background(), "slot-s1c8-e1c29-1", preissue, patch.OriginalSourceSHA256); err != nil {
		t.Fatal(err)
	}

	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return broker, nil
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	payload, err := engine.RunHostScheduledSourcePatchDerived(context.Background(), request, patch, plmPass.Registration())
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeSuccessfulGuestResult(payload)
	if err != nil || string(result) != `["alpha-body",12]` {
		t.Fatalf("result=%s payload=%s err=%v", result, payload, err)
	}
	receipts := broker.SnapshotReceipts()
	evidence := engine.SplitPhaseEvidence()
	if physical.Load() != 1 || broker.CallCount() != 1 || len(receipts) != 1 || receipts[0].CallID != "plm-s1c8-e1c29-1" ||
		evidence.Submitted != 1 || evidence.Reused != 1 || evidence.CandidatesAdopted != 1 || evidence.JobsLinearized != 1 || evidence.JobsMaterialized != 1 {
		t.Fatalf("physical=%d calls=%d receipts=%#v evidence=%+v", physical.Load(), broker.CallCount(), receipts, evidence)
	}
}

type e2ePLMAdapter struct {
	handler capability.Handler
}

func (adapter *e2ePLMAdapter) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return adapter.handler.Call(ctx, raw)
}

func (adapter *e2ePLMAdapter) PLMValidatorIdentities() capability.PLMValidatorIdentities {
	return capability.PLMValidatorIdentities{
		Temporal: "pysolate.e2e.immutable-validator.v1", ProviderNonInterference: "pysolate.e2e.provider-validator.v1",
	}
}

func (adapter *e2ePLMAdapter) PLMProviderSessionIdentity(context.Context) string {
	return "pysolate-e2e-provider-session-v1"
}

func (adapter *e2ePLMAdapter) ValidatePLM(_ context.Context, request capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
	return capability.PLMValidationResult{
		Temporal: request.Certificate.Temporal, TemporalValid: true, ProviderNonInterferenceValid: true,
		ValidationCostUnits: 1,
	}, nil
}

func plmE2EPlan(t *testing.T, maxCalls uint32, adapter *e2ePLMAdapter) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"plm-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.sources.read.plm-e2e.v1", Description: "Read one immutable PLM fixture.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "plm-e2e-handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":256}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string","maxLength":4096}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}, ResultField: "body"},
		ReadOnly:     true, Idempotent: true,
		PLM: &capability.PLMContract{
			Version: capability.PLMContractVersionV1, Temporal: capability.TemporalImmutable, PrepareEffect: capability.PrepareSilentRead,
			Speculation: capability.SpeculationBudgeted, Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
			Resource:          capability.ResourceReference{Namespace: "sources", Argument: "path"},
			TemporalValidator: "pysolate.e2e.immutable-validator.v1", ProviderNonInterferenceValidator: "pysolate.e2e.provider-validator.v1",
			MaxResultBytes: 4096, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, adapter); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func osReadGuestArtifact(t *testing.T) ([]byte, error) {
	t.Helper()
	return os.ReadFile(guestArtifact(t))
}
