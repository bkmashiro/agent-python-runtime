package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

	var brokersMu sync.Mutex
	brokers := make([]*capability.Broker, 0, 2)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true, SplitPhaseCalls: true}
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
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

	baseline, err := runner.Run(context.Background(), request, prelude)
	if err != nil {
		t.Fatal(err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil || string(baselineResult) != `["alpha-body","beta-body"]` || maxActive.Load() != 1 {
		t.Fatalf("baseline result=%s max_active=%d err=%v payload=%s", baselineResult, maxActive.Load(), err, baseline)
	}
	maxActive.Store(0)

	pass, err := sourcepatch.NewSplitPhaseSourcesRead(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := passplugin.New(pass)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseSourcesReadName)
	if err != nil {
		t.Fatal(err)
	}
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 4, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	execution, err := plugins.ExecuteHostScheduled(
		context.Background(), sourcepatch.SplitPhaseSourcesReadName, session, engine, request, prelude,
	)
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
		if broker.CallCount() != 2 || len(receipts) != 2 || receipts[0].CallID != splitPhaseCallID(source, 1) || receipts[1].CallID != splitPhaseCallID(source, 2) {
			t.Fatalf("broker[%d] calls=%d receipts=%#v", index, broker.CallCount(), receipts)
		}
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
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true, SplitPhaseCalls: true}
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "split-phase-failure", Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	pass, _ := sourcepatch.NewSplitPhaseSourcesRead(passregistration.SemanticAnalyzerSHA256)
	plugins, _ := passplugin.New(pass)
	plugins, _ = plugins.Enable(sourcepatch.SplitPhaseSourcesReadName)
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 2, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
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
	if broker.CallCount() != 1 || len(receipts) != 1 || receipts[0].CallID != splitPhaseCallID(source, 1) || receipts[0].Outcome != "error" {
		t.Fatalf("calls=%d receipts=%#v", broker.CallCount(), receipts)
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
