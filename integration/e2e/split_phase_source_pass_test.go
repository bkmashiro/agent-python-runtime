package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

func TestRealGuestCapabilityFuturesOverlapWithoutAnalyzer(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	source := "first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "future-overlap-e2e", Code: source, Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	handler := capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		current := active.Add(1)
		for observed := maxActive.Load(); current > observed && !maxActive.CompareAndSwap(observed, current); observed = maxActive.Load() {
		}
		select {
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		active.Add(-1)
		return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
	})
	plan := splitPhaseE2EPlan(t, 2, handler)
	baselinePrelude := plan.PythonPrelude()
	futurePass := futurePassSelection(t, plan)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: futurePass.registry, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "future-overlap-e2e", Plan: plan})
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	baselineStarted := time.Now()
	baseline, err := runner.Run(context.Background(), request, baselinePrelude)
	baselineDuration := time.Since(baselineStarted)
	if err != nil {
		t.Fatal(err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil || string(baselineResult) != `["alpha-body","beta-body"]` || maxActive.Load() != 1 {
		t.Fatalf("baseline result=%s max_active=%d err=%v payload=%s", baselineResult, maxActive.Load(), err, baseline)
	}

	maxActive.Store(0)
	treatmentStarted := time.Now()
	treatment, err := runner.Run(context.Background(), request, futurePass.prelude)
	treatmentDuration := time.Since(treatmentStarted)
	if err != nil {
		t.Fatal(err)
	}
	treatmentResult, err := decodeSuccessfulGuestResult(treatment)
	if err != nil || string(treatmentResult) != string(baselineResult) || maxActive.Load() != 2 {
		t.Fatalf("treatment result=%s max_active=%d err=%v payload=%s", treatmentResult, maxActive.Load(), err, treatment)
	}
	if evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence(); evidence.Submitted != 2 || evidence.Consumed != 2 || evidence.Discarded != 0 {
		t.Fatalf("future evidence=%+v", evidence)
	}
	if treatmentDuration >= baselineDuration {
		t.Fatalf("Future path did not reduce latency: baseline=%s treatment=%s", baselineDuration, treatmentDuration)
	}
	t.Logf("direct Future evidence: baseline=%s treatment=%s saved=%s ratio=%.4f analyzers=0", baselineDuration, treatmentDuration, baselineDuration-treatmentDuration, float64(treatmentDuration)/float64(baselineDuration))
}

func TestRealGuestCapabilityFutureDrainsUnobservedWrite(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"future-write-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Uint32
	spec := capability.Spec{
		Name: "fixture.write", Version: "fixture.write.future-e2e.v1", Description: "Write one fixture.",
		EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-write-future-e2e.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"written":{"type":"boolean"}},"required":["written"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "fixture", Method: "write", Arguments: []string{"value"}, ResultField: "written"},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		writes.Add(1)
		return json.RawMessage(`{"written":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	futurePass := futurePassSelection(t, plan)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: futurePass.registry, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "future-write-e2e", Plan: plan})
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "future-write-e2e", Code: "fixture.write(\"value\")\nresult = \"done\"\n", Inputs: json.RawMessage(`{}`),
	})
	payload, err := runner.Run(context.Background(), request, futurePass.prelude)
	result, decodeErr := decodeSuccessfulGuestResult(payload)
	if err != nil || decodeErr != nil || string(result) != `"done"` || writes.Load() != 1 {
		t.Fatalf("result=%s writes=%d decode_err=%v err=%v payload=%s", result, writes.Load(), decodeErr, err, payload)
	}
	if evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence(); evidence.Submitted != 1 || evidence.Consumed != 1 || evidence.Discarded != 0 {
		t.Fatalf("write Future evidence=%+v", evidence)
	}
}

func TestRealGuestCapabilityFutureDrainsAfterEarlierError(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"future-drain-error-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Uint32
	spec := capability.Spec{
		Name: "fixture.write", Version: "fixture.write.future-error-e2e.v1", Description: "Write one fixture.",
		EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-write-future-error-e2e.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"written":{"type":"boolean"}},"required":["written"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "fixture", Method: "write", Arguments: []string{"value"}, ResultField: "written"},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		writes.Add(1)
		var arguments struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(raw, &arguments)
		if arguments.Value == "error" {
			return nil, errors.New("first write failed")
		}
		return json.RawMessage(`{"written":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	futurePass := futurePassSelection(t, plan)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: futurePass.registry, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "future-drain-error-e2e", Plan: plan})
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID:  "future-drain-error-e2e",
		Code:   "fixture.write(\"error\")\nfixture.write(\"ok\")\nresult = \"done\"\n",
		Inputs: json.RawMessage(`{}`),
	})
	payload, runErr := runner.Run(context.Background(), request, futurePass.prelude)
	var response struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeErr := json.Unmarshal(payload, &response)
	if runErr != nil || decodeErr != nil || response.Status != "error" || response.Error == nil || response.Error.Code != "python_exception" || writes.Load() != 2 {
		t.Fatalf("response=%+v writes=%d decode_err=%v run_err=%v payload=%s", response, writes.Load(), decodeErr, runErr, payload)
	}
	if evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence(); evidence.Submitted != 2 || evidence.Consumed != 2 || evidence.Discarded != 0 {
		t.Fatalf("error drain evidence=%+v", evidence)
	}
}

func TestRealGuestCapabilityFutureMaterializesAnyJSONResultShape(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"future-json-shapes-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "fixture.value", Version: "fixture.value.future-e2e.v1", Description: "Return one JSON shape.",
		EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-value-future-e2e.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"}},"required":["kind"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{}`),
		Python:       &capability.PythonProjection{Module: "fixture", Method: "value", Arguments: []string{"kind"}},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(raw, &arguments)
		switch arguments.Kind {
		case "scalar":
			return json.RawMessage(`7`), nil
		case "array":
			return json.RawMessage(`[1,2]`), nil
		case "null":
			return json.RawMessage(`null`), nil
		default:
			return nil, errors.New("unknown shape")
		}
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	futurePass := futurePassSelection(t, plan)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: futurePass.registry, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "future-json-shapes-e2e", Plan: plan})
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID:  "future-json-shapes-e2e",
		Code:   "scalar = fixture.value(\"scalar\")\narray = fixture.value(\"array\")\nnone = fixture.value(\"null\")\nresult = [scalar, array, none]\n",
		Inputs: json.RawMessage(`{}`),
	})
	payload, runErr := runner.Run(context.Background(), request, futurePass.prelude)
	result, decodeErr := decodeSuccessfulGuestResult(payload)
	if runErr != nil || decodeErr != nil || string(result) != `[7,[1,2],null]` {
		t.Fatalf("result=%s decode_err=%v run_err=%v payload=%s", result, decodeErr, runErr, payload)
	}
}

func TestRealGuestCapabilityFuturesUseWholePlanCallBudget(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"future-plan-budget-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	spec := capability.Spec{
		Name: "fixture.read", Version: "fixture.read.future-budget-e2e.v1", Description: "Read one fixture.",
		EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-read-future-budget-e2e.v1",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"integer"}`),
		Python:       &capability.PythonProjection{Module: "fixture", Method: "read"},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(calls.Add(1))
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 5})
	if err != nil {
		t.Fatal(err)
	}
	futurePass := futurePassSelection(t, plan)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	runner, err := (wazeroengine.Factory{Passes: futurePass.registry, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "future-plan-budget-e2e", Plan: plan})
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID:  "future-plan-budget-e2e",
		Code:   "a = fixture.read()\nb = fixture.read()\nc = fixture.read()\nd = fixture.read()\ne = fixture.read()\nresult = [a, b, c, d, e]\n",
		Inputs: json.RawMessage(`{}`),
	})
	payload, runErr := runner.Run(context.Background(), request, futurePass.prelude)
	_, decodeErr := decodeSuccessfulGuestResult(payload)
	if runErr != nil || decodeErr != nil || calls.Load() != 5 {
		t.Fatalf("calls=%d decode_err=%v run_err=%v payload=%s", calls.Load(), decodeErr, runErr, payload)
	}
	if evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence(); evidence.Submitted != 5 || evidence.Consumed != 5 || evidence.Discarded != 0 {
		t.Fatalf("plan budget evidence=%+v", evidence)
	}
}

func TestRealGuestSplitPhaseSourcesReadOverlapsPhysicalWorkAndKeepsLogicalReceipts(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	source := "first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "split-phase-overlap-e2e", Code: source, Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	var physical atomic.Uint32
	handler := capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		physical.Add(1)
		current := active.Add(1)
		for observed := maxActive.Load(); current > observed && !maxActive.CompareAndSwap(observed, current); observed = maxActive.Load() {
		}
		select {
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		active.Add(-1)
		return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
	})
	plan := splitPhaseE2EPlan(t, 2, handler)
	prelude := splitPhaseDirectPrelude(source)
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseSourcesReadName)
	if err != nil {
		t.Fatal(err)
	}

	var brokersMu sync.Mutex
	brokers := make([]*capability.Broker, 0, 2)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		broker, createErr := capability.NewBroker(capability.Config{RunIdentity: "split-phase-e2e", Plan: plan})
		if createErr == nil {
			brokersMu.Lock()
			brokers = append(brokers, broker)
			brokersMu.Unlock()
		}
		return broker, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)

	baselineStarted := time.Now()
	baseline, err := runner.Run(context.Background(), request, prelude)
	baselineDuration := time.Since(baselineStarted)
	if err != nil {
		t.Fatal(err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil || string(baselineResult) != `["alpha-body","beta-body"]` || maxActive.Load() != 1 {
		t.Fatalf("baseline result=%s max_active=%d err=%v payload=%s", baselineResult, maxActive.Load(), err, baseline)
	}
	maxActive.Store(0)

	treatmentStarted := time.Now()
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 4)
	defer closeAnalysis()
	execution, err := plugins.ExecuteHostScheduled(
		context.Background(), sourcepatch.SplitPhaseSourcesReadName, session, engine, request, prelude,
	)
	treatmentDuration := time.Since(treatmentStarted)
	if err != nil || !execution.Applied || execution.Patch.ReplacementCount != 2 {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	treatmentResult, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(treatmentResult) != string(baselineResult) || maxActive.Load() != 2 || physical.Load() != 4 {
		t.Fatalf("treatment result=%s max_active=%d physical=%d err=%v payload=%s", treatmentResult, maxActive.Load(), physical.Load(), err, execution.Payload)
	}

	brokersMu.Lock()
	captured := append([]*capability.Broker(nil), brokers...)
	brokersMu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("brokers=%d", len(captured))
	}
	for index, broker := range captured {
		receipts := broker.SnapshotReceipts()
		firstID, secondID := splitPhaseCallID(source, 1), splitPhaseCallID(source, 2)
		if index == 1 {
			firstID, secondID = firstID+"-1", secondID+"-1"
		}
		if broker.CallCount() != 2 || len(receipts) != 2 || receipts[0].CallID != firstID || receipts[1].CallID != secondID {
			t.Fatalf("broker[%d] calls=%d receipts=%#v", index, broker.CallCount(), receipts)
		}
	}
	physicalEvidence := engine.SplitPhaseEvidence()
	if physicalEvidence.Submitted != 2 || physicalEvidence.PhysicalStarts != 2 || physicalEvidence.PhysicalFinishes != 2 || physicalEvidence.LogicalClaims != 2 || physicalEvidence.Consumed != 2 || physicalEvidence.MaximumConcurrent != 2 {
		t.Fatalf("physical evidence=%+v", physicalEvidence)
	}
	t.Logf("split-phase matched durations: baseline_ns=%d treatment_ns=%d", baselineDuration.Nanoseconds(), treatmentDuration.Nanoseconds())
}

func TestRealGuestSplitPhasePreservesDynamicActivation(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, source, inputs string
		submitted, minimum   uint32
		applied              bool
	}{
		{"branch-not-taken", "if inputs[\"take\"]:\n    first = sources.read(\"alpha\")\n    second = sources.read(\"beta\")\n    result = [first, second]\nelse:\n    result = []\n", `{"take":false}`, 0, 0, true},
		{"branch-taken", "if inputs[\"take\"]:\n    first = sources.read(\"alpha\")\n    second = sources.read(\"beta\")\n    result = [first, second]\nelse:\n    result = []\n", `{"take":true}`, 2, 1, true},
		{"zero-loop", "result = []\nfor item in inputs[\"items\"]:\n    first = sources.read(\"alpha\")\n    result.append(first)\n", `{"items":[]}`, 0, 0, true},
		{"two-loop-iterations", "result = []\nfor item in inputs[\"items\"]:\n    first = sources.read(\"alpha\")\n    result.append(first)\n", `{"items":[1,2]}`, 2, 1, true},
		{"one-line-branch-fallback", "if inputs[\"take\"]: first = sources.read(\"alpha\"); result = first\nelse: result = []\n", `{"take":false}`, 0, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := capability.HandlerFunc(func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
				var request struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(arguments, &request) != nil {
					return nil, capability.ErrInvalidTool
				}
				return json.Marshal(map[string]string{"body": request.Path + "-body"})
			})
			plan := splitPhaseE2EPlan(t, 2, handler)
			plugins := unifiedPassCatalog(t)
			plugins, enableErr := plugins.Enable(sourcepatch.SplitPhaseSourcesReadName)
			if enableErr != nil {
				t.Fatal(enableErr)
			}
			var broker *capability.Broker
			factory := func(context.Context) (*capability.Broker, error) {
				created, createErr := capability.NewBroker(capability.Config{RunIdentity: "dynamic-" + testCase.name, Plan: plan})
				broker = created
				return created, createErr
			}
			config := runtimeconfig.DefaultRunConfig()
			config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
			runner, createErr := (wazeroengine.Factory{Passes: plugins, BrokerFactory: factory}).New(context.Background(), artifact, config)
			if createErr != nil {
				t.Fatal(createErr)
			}
			defer runner.Close(context.Background())
			engine := trustedSemanticRunner(t, runner)
			session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 2)
			defer closeAnalysis()
			request := runtimeconfig.RunRequest{RunID: "dynamic-" + strings.ReplaceAll(testCase.name, " ", "-"), Code: testCase.source, Inputs: json.RawMessage(testCase.inputs)}
			raw, _ := runtimeconfig.EncodeRunRequest(request)
			execution, runErr := plugins.ExecuteHostScheduled(context.Background(), sourcepatch.SplitPhaseSourcesReadName, session, engine, raw, "")
			if runErr != nil {
				t.Fatal(runErr)
			}
			response, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, execution.Payload)
			if decodeErr != nil || response.Status != runtimeconfig.RunResponseOK {
				t.Fatalf("response=%s decoded=%+v err=%v", execution.Payload, response, decodeErr)
			}
			if execution.Applied != testCase.applied {
				t.Fatalf("applied=%t want=%t patch=%+v", execution.Applied, testCase.applied, execution.Patch)
			}
			evidence := engine.SplitPhaseEvidence()
			if evidence.Submitted != testCase.submitted || evidence.LogicalClaims != testCase.submitted || evidence.MaximumConcurrent < testCase.minimum || evidence.MaximumConcurrent > testCase.submitted || broker.CallCount() != testCase.submitted {
				t.Fatalf("evidence=%+v calls=%d", evidence, broker.CallCount())
			}
		})
	}
}

func TestRealGuestSplitPhaseFailureDiscardsLaterPhysicalReadWithoutLogicalReceipt(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	source := "first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "split-phase-failure-e2e", Code: source, Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var started atomic.Uint32
	release := make(chan struct{})
	var releaseOnce sync.Once
	handler := capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		if started.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		if arguments.Path == "alpha" {
			return nil, errors.New("alpha failed")
		}
		return json.RawMessage(`{"body":"beta-body"}`), nil
	})
	plan := splitPhaseE2EPlan(t, 2, handler)
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseSourcesReadName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "split-phase-failure", Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 2)
	defer closeAnalysis()
	execution, err := plugins.ExecuteHostScheduled(
		context.Background(), sourcepatch.SplitPhaseSourcesReadName, session, engine, request, splitPhaseDirectPrelude(source),
	)
	if err != nil || !execution.Applied || started.Load() != 2 {
		t.Fatalf("execution=%+v started=%d err=%v", execution, started.Load(), err)
	}
	var response struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(execution.Payload, &response) != nil || response.Status != "error" || response.Error == nil || response.Error.Code != "python_exception" {
		t.Fatalf("response=%+v payload=%s", response, execution.Payload)
	}
	receipts := broker.SnapshotReceipts()
	if broker.CallCount() != 1 || len(receipts) != 1 || receipts[0].CallID != splitPhaseCallID(source, 1)+"-1" || receipts[0].Outcome != "error" {
		t.Fatalf("calls=%d receipts=%#v", broker.CallCount(), receipts)
	}
	physicalEvidence := engine.SplitPhaseEvidence()
	if physicalEvidence.Submitted != 2 || physicalEvidence.LogicalClaims != 1 || physicalEvidence.Consumed != 1 || physicalEvidence.Discarded != 1 {
		t.Fatalf("physical evidence=%+v", physicalEvidence)
	}
}

func splitPhaseAnalysisSession(t *testing.T, artifact []byte, maxRequests uint32) (sourcepatch.Transformer, func()) {
	t.Helper()
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: maxRequests, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		_ = runner.Close(context.Background())
		t.Fatal(err)
	}
	return session, func() {
		if closeErr := session.Close(context.Background()); closeErr != nil {
			t.Fatalf("close analysis session: %v", closeErr)
		}
		if closeErr := runner.Close(context.Background()); closeErr != nil {
			t.Fatalf("close analysis runner: %v", closeErr)
		}
	}
}

func splitPhaseE2EPlan(t *testing.T, maxCalls uint32, handler capability.Handler) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"split-phase-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.sources.read.split-phase-e2e.v1", Description: "Read one immutable source fixture.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "split-phase-e2e-handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":256}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string","maxLength":4096}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}, ResultField: "body"},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "sources", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
			Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 4096, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type futurePassExecution struct {
	registry *passplugin.Registry
	prelude  string
}

func unifiedPassCatalog(t *testing.T) *passplugin.Registry {
	t.Helper()
	registry, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func futurePassSelection(t *testing.T, plan *capability.Plan) futurePassExecution {
	t.Helper()
	registry := unifiedPassCatalog(t)
	var err error
	registry, err = registry.Enable(passregistration.CapabilityFutureProjection)
	if err != nil {
		t.Fatal(err)
	}
	prelude, err := registry.ProjectPlan(passregistration.CapabilityFutureProjection, plan)
	if err != nil {
		t.Fatal(err)
	}
	return futurePassExecution{registry: registry, prelude: prelude}
}

func splitPhaseCallID(source string, index int) string {
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("split-%x-%d", digest[:8], index)
}

func splitPhaseDirectPrelude(source string) string {
	return fmt.Sprintf(`
import json as _pysolate_split_json
import _agent_runtime_host as _pysolate_split_host
class _PysolateSplitSources:
    @staticmethod
    def read(path):
        call_ids = {"alpha": %q, "beta": %q}
        request = _pysolate_split_json.dumps(
            {"call_id": call_ids[path], "capability": "sources.read", "arguments": {"path": path}},
            ensure_ascii=False, separators=(",", ":"), allow_nan=False,
        )
        response = _pysolate_split_json.loads(_pysolate_split_host.call(request))
        if response["status"] != "ok":
            raise RuntimeError(response["error"]["message"])
        return response["result"]["body"]
sources = _PysolateSplitSources()
`, splitPhaseCallID(source, 1), splitPhaseCallID(source, 2))
}
