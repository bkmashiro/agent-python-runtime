package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"

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
	sourceDigest := sha256.Sum256([]byte(source))
	sourceSealIdentity := fmt.Sprintf("sha256:%x", sourceDigest[:])
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-source-time", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	preissue := []byte(`{"call_id":"plm-s1c8-e1c29-1","capability":"sources.read","arguments":{"path":"alpha"}}`)
	if err := table.PrepareRuntimePLM(context.Background(), "slot-s1c8-e1c29-1", preissue, sourceSealIdentity); err != nil {
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
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.PLMCapabilityCallsName, engine, request,
		plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan),
	)
	if err != nil || !execution.Applied || strings.Contains(execution.Patch.DerivedSource, "_pysolate_call_") ||
		!strings.Contains(execution.Patch.DerivedSource, "_pysolate_plm_prepare") || !strings.Contains(execution.Patch.DerivedSource, "_pysolate_plm_linearize") {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(result) != `["alpha-body",12]` {
		t.Fatalf("result=%s payload=%s err=%v", result, execution.Payload, err)
	}
	receipts := broker.SnapshotReceipts()
	evidence := engine.SplitPhaseEvidence()
	lifecycle := engine.PLMRunLifecycleEvidence()
	if physical.Load() != 1 || broker.CallCount() != 1 || len(receipts) != 1 || receipts[0].CallID != "plm-s1c8-e1c29-1" ||
		evidence.Submitted != 1 || evidence.Reused != 1 || evidence.CandidatesAdopted != 1 || evidence.JobsLinearized != 1 || evidence.JobsMaterialized != 1 ||
		evidence.ProviderNanos == 0 || evidence.ValidationNanos == 0 || evidence.LinearizationNanos == 0 || evidence.MaterializationNanos == 0 ||
		lifecycle.ModuleInstantiations != 1 || lifecycle.LoweringCalls != 1 || lifecycle.SelectionCalls != 1 || lifecycle.ExecuteCalls != 1 || lifecycle.TotalNanos == 0 {
		t.Fatalf("physical=%d calls=%d receipts=%#v evidence=%+v lifecycle=%+v", physical.Load(), broker.CallCount(), receipts, evidence, lifecycle)
	}
}

func TestRealGuestPLMPassDisabledExecutesUnchangedSource(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"body":"baseline"}`), nil
	})}
	plan := plmE2EPlan(t, 1, adapter)
	plugins := unifiedPassCatalog(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 60 * time.Second
	var broker *capability.Broker
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		broker, err = capability.NewBroker(capability.Config{RunIdentity: "plm-disabled", Plan: plan})
		return broker, err
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	source := "value = sources.read(\"alpha\")\nresult = [value, 9]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "plm-disabled", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.PLMCapabilityCallsName, engine, request,
		plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan),
	)
	if err != nil || execution.Applied || execution.Patch.DerivedSource != "" {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(result) != `["baseline",9]` || physical.Load() != 1 || broker.CallCount() != 1 {
		t.Fatalf("result=%s physical=%d calls=%d err=%v", result, physical.Load(), broker.CallCount(), err)
	}
	if lifecycle := engine.PLMRunLifecycleEvidence(); lifecycle.LoweringCalls != 0 || lifecycle.TotalNanos != 0 {
		t.Fatalf("lifecycle=%+v", lifecycle)
	}
}

func TestRealGuestPLMRuntimeDerivedCallsPreserveCodeAndReceipts(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	var getCalls atomic.Uint32
	var priceCalls atomic.Uint32
	getAdapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		getCalls.Add(1)
		return json.RawMessage(`{"value":5}`), nil
	})}
	priceAdapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		priceCalls.Add(1)
		var arguments struct {
			Value int `json:"value"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		return json.Marshal(map[string]int{"quote": arguments.Value * 2})
	})}
	plan := plmTwoE2EPlan(t, getAdapter, priceAdapter)
	source := "a = tools.get(\"alpha\")\nx = a + 1\nindependent = 3 * 4\nb = tools.price(x)\nresult = [a, b, independent]\n"
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "plm-runtime-derived", Code: source, Inputs: json.RawMessage(`{}`)})
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "plm-runtime-derived", Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.PLMCapabilityCallsName, trustedSemanticRunner(t, runner), request,
		plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan),
	)
	if err != nil || !execution.Applied || strings.Contains(execution.Patch.DerivedSource, "_pysolate_call_") {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(result) != `[5,12,12]` {
		t.Fatalf("result=%s payload=%s err=%v", result, execution.Payload, err)
	}
	evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence()
	if getCalls.Load() != 1 || priceCalls.Load() != 1 || broker.CallCount() != 2 ||
		evidence.CandidatesAdopted != 2 || evidence.JobsMaterialized != 2 {
		t.Fatalf("get=%d price=%d calls=%d evidence=%+v", getCalls.Load(), priceCalls.Load(), broker.CallCount(), evidence)
	}
}

func TestRealGuestPLMPreservesBranchLoopInvalidationAndFallback(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("branch and loop", func(t *testing.T) {
		var physical atomic.Uint32
		adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			physical.Add(1)
			var arguments struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(raw, &arguments)
			return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
		})}
		plan := plmE2EPlan(t, 3, adapter)
		source := "values = []\nif inputs[\"take\"]:\n    branch = sources.read(\"branch\")\n    values.append(branch)\nfor item in inputs[\"items\"]:\n    value = sources.read(item)\n    values.append(value)\nresult = values\n"
		result, broker, evidence, patch := runPLMExact(t, artifact, plan, adapter, "plm-control", source, json.RawMessage(`{"take":true,"items":["a","b"]}`))
		if string(result) != `["branch-body","a-body","b-body"]` || physical.Load() != 3 || broker.CallCount() != 3 ||
			evidence.CandidatesAdopted != 3 || evidence.JobsMaterialized != 3 || patch.ReplacementCount != 2 {
			t.Fatalf("result=%s physical=%d calls=%d evidence=%+v patch=%+v", result, physical.Load(), broker.CallCount(), evidence, patch)
		}
	})
	t.Run("validator invalidation", func(t *testing.T) {
		var physical atomic.Uint32
		adapter := &e2ePLMAdapter{invalidate: true, handler: capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			physical.Add(1)
			var arguments struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(raw, &arguments)
			return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
		})}
		plan := plmE2EPlan(t, 1, adapter)
		result, broker, evidence, _ := runPLMExact(t, artifact, plan, adapter, "plm-invalidate", "result = sources.read(\"alpha\")\n", json.RawMessage(`{}`))
		if string(result) != `"alpha-body"` || physical.Load() == 0 || physical.Load() > 2 || broker.CallCount() != 1 ||
			evidence.CandidatesRejected != 1 || evidence.CanonicalStarts != 1 || evidence.CandidatesAdopted != 0 {
			t.Fatalf("result=%s physical=%d calls=%d evidence=%+v", result, physical.Load(), broker.CallCount(), evidence)
		}
	})
	t.Run("unsupported fallback", func(t *testing.T) {
		var physical atomic.Uint32
		adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			physical.Add(1)
			var arguments struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(raw, &arguments)
			return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
		})}
		plan := plmE2EPlan(t, 1, adapter)
		result, broker, evidence, patch := runPLMExact(t, artifact, plan, adapter, "plm-fallback", "result = sources.read(inputs[\"key\"])\n", json.RawMessage(`{"key":"alpha"}`))
		if string(result) != `"alpha-body"` || physical.Load() != 1 || broker.CallCount() != 1 || patch.Applied() ||
			evidence.CandidatesPrepared != 0 {
			t.Fatalf("result=%s physical=%d calls=%d evidence=%+v patch=%+v", result, physical.Load(), broker.CallCount(), evidence, patch)
		}
	})
}

func runPLMExact(t *testing.T, artifact []byte, plan *capability.Plan, _ *e2ePLMAdapter, runID, source string, inputs json.RawMessage) (json.RawMessage, *capability.Broker, capability.SplitPhaseSnapshot, sourcepatch.Patch) {
	t.Helper()
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: source, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.PLMCapabilityCallsName, trustedSemanticRunner(t, runner), request,
		plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, decodeErr := decodeSuccessfulGuestResult(execution.Payload)
	if decodeErr != nil {
		t.Fatalf("payload=%s err=%v", execution.Payload, decodeErr)
	}
	return result, broker, trustedSemanticRunner(t, runner).SplitPhaseEvidence(), execution.Patch
}

func plmTwoE2EPlan(t *testing.T, getAdapter, priceAdapter *e2ePLMAdapter) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"plm-two-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct {
		name, method, argument, result, namespace, handlerIdentity string
		input, output                                              json.RawMessage
		adapter                                                    *e2ePLMAdapter
	}{
		{"tools.get", "get", "key", "value", "tools-get", "plm-tools-get-e2e.v1", json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`), json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), getAdapter},
		{"tools.price", "price", "value", "quote", "tools-price", "plm-tools-price-e2e.v1", json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), json.RawMessage(`{"type":"object","properties":{"quote":{"type":"integer"}},"required":["quote"],"additionalProperties":false}`), priceAdapter},
	}
	for _, item := range specs {
		spec := capability.Spec{
			Name: item.name, Version: "pysolate." + strings.ReplaceAll(item.name, ".", "-") + ".plm-e2e.v1", Description: "PLM two-call fixture.",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: item.handlerIdentity,
			InputSchema: item.input, OutputSchema: item.output,
			Python:   &capability.PythonProjection{Module: "tools", Method: item.method, Arguments: []string{item.argument}, ResultField: item.result},
			ReadOnly: true, Idempotent: true,
			PLM: &capability.PLMContract{
				Version: capability.PLMContractVersionV1, Temporal: capability.TemporalImmutable, PrepareEffect: capability.PrepareSilentRead,
				Speculation: capability.SpeculationBudgeted, Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
				Resource:          capability.ResourceReference{Namespace: item.namespace, Argument: item.argument},
				TemporalValidator: "pysolate.e2e.immutable-validator.v1", ProviderNonInterferenceValidator: "pysolate.e2e.provider-validator.v1",
				MaxResultBytes: 4096, CostUnits: 1,
			},
		}
		if err := registry.Register(spec, grant, item.adapter); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRealGuestPLMEarlierExceptionDiscardsUnclaimedCandidate(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"body":"unused"}`), nil
	})}
	execution, broker, evidence := runPLMRaw(t, artifact, plmE2EPlan(t, 1, adapter), "plm-earlier-exception",
		"boom = 1 // 0\nvalue = sources.read(\"alpha\")\nresult = value\n")
	var payload map[string]any
	if json.Unmarshal(execution.Payload, &payload) != nil || payload["status"] != "error" || !execution.Applied {
		t.Fatalf("execution=%+v payload=%s", execution, execution.Payload)
	}
	if physical.Load() != 0 || broker.CallCount() != 0 || evidence.Submitted != 0 || evidence.CandidatesAdopted != 0 {
		t.Fatalf("physical=%d calls=%d evidence=%+v", physical.Load(), broker.CallCount(), evidence)
	}
}

func TestRealGuestPLMPrepareFailureRetriesOnlyAtLinearization(t *testing.T) {
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, errors.New("invalid fixture arguments")
		}
		if arguments.Path == "alpha" {
			return nil, errors.New("fixture provider failure")
		}
		return json.RawMessage(`{"body":"unused"}`), nil
	})}
	execution, broker, evidence := runPLMRaw(t, artifact, plmE2EPlan(t, 2, adapter), "plm-prepare-failure",
		"first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n")
	var payload map[string]any
	if json.Unmarshal(execution.Payload, &payload) != nil || payload["status"] != "error" || !execution.Applied {
		t.Fatalf("execution=%+v payload=%s", execution, execution.Payload)
	}
	if physical.Load() != 3 || broker.CallCount() != 1 || len(broker.SnapshotReceipts()) != 1 || evidence.CanonicalStarts != 1 || evidence.Discarded != 2 {
		t.Fatalf("physical=%d calls=%d receipts=%+v evidence=%+v", physical.Load(), broker.CallCount(), broker.SnapshotReceipts(), evidence)
	}
}

func runPLMRaw(t *testing.T, artifact []byte, plan *capability.Plan, runID, source string) (passplugin.Execution, *capability.Broker, capability.SplitPhaseSnapshot) {
	t.Helper()
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	execution, err := plugins.ExecuteCapabilityHostScheduled(context.Background(), sourcepatch.PLMCapabilityCallsName, engine, request,
		plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan))
	if err != nil {
		t.Fatal(err)
	}
	return execution, broker, engine.SplitPhaseEvidence()
}

type e2ePLMAdapter struct {
	handler    capability.Handler
	invalidate bool
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
	temporal := request.Certificate.Temporal
	if adapter.invalidate {
		temporal.ResourceIdentity += ":changed"
	}
	return capability.PLMValidationResult{
		Temporal: temporal, TemporalValid: true, ProviderNonInterferenceValid: true,
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

func unifiedPassCatalog(t *testing.T) *passplugin.Registry {
	t.Helper()
	registry, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func osReadGuestArtifact(t *testing.T) ([]byte, error) {
	t.Helper()
	return os.ReadFile(guestArtifact(t))
}
