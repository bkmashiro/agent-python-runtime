package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

type exactEagerComparatorResult struct {
	Outcome                string          `json:"outcome"`
	ErrorClass             string          `json:"error_class"`
	Result                 json.RawMessage `json:"result"`
	ResultPresent          bool            `json:"result_present"`
	PrefixPythonExecutions int             `json:"prefix_python_executions"`
	PythonExecutions       int             `json:"python_executions"`
	Sealed                 bool            `json:"sealed"`
}

func TestExactGuestEagerStyleComparatorUsesLookaheadAndPersistentNamespace(t *testing.T) {
	result := runExactGuestEagerComparator(t, "eager-comparator-safe", semanticspeculation.EagerComparatorPrepareConfig{
		Inputs: json.RawMessage(`{"value":2}`),
		Chunks: []string{
			"base = inputs['value'] + 1\n",
			"derived = base * 4\n",
			"result = derived\n",
		},
	}, nil)
	if result.Outcome != "success" || string(result.Result) != "12" || !result.ResultPresent ||
		result.PrefixPythonExecutions != 2 || result.PythonExecutions != 3 || result.Sealed {
		t.Fatalf("result=%+v body=%s", result, result.Result)
	}
}

func TestExactGuestEagerStyleComparatorDeniedNameSealsUntilFinalSource(t *testing.T) {
	var physical atomic.Int32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	result := runExactGuestEagerComparator(t, "eager-comparator-denied", semanticspeculation.EagerComparatorPrepareConfig{
		Inputs: json.RawMessage(`{}`), Plan: plan,
		Chunks: []string{"value = time.read('weather')\n", "result = value\n"},
	}, func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "eager-comparator-denied", Plan: plan})
	})
	if result.Outcome != "success" || string(result.Result) != `"weather"` || !result.Sealed ||
		result.PrefixPythonExecutions != 0 || result.PythonExecutions != 1 || physical.Load() != 1 {
		t.Fatalf("result=%+v body=%s physical=%d", result, result.Result, physical.Load())
	}
}

func TestExactGuestEagerStyleComparatorReportsInvalidSuffixAfterPrefix(t *testing.T) {
	result := runExactGuestEagerComparator(t, "eager-comparator-syntax", semanticspeculation.EagerComparatorPrepareConfig{
		Inputs: json.RawMessage(`{}`), Chunks: []string{"value = 1\n", "result = )\n"},
	}, nil)
	if result.Outcome != "syntax_error" || result.ErrorClass != "SyntaxError" || result.PrefixPythonExecutions != 1 ||
		result.PythonExecutions != 1 || result.ResultPresent || len(result.Result) != 0 {
		t.Fatalf("result=%+v body=%s", result, result.Result)
	}
}

func TestExactGuestEagerStyleComparatorFreezesRuntimeFailureWithoutMessageBody(t *testing.T) {
	result := runExactGuestEagerComparator(t, "eager-comparator-runtime", semanticspeculation.EagerComparatorPrepareConfig{
		Inputs: json.RawMessage(`{}`), Chunks: []string{"raise ValueError('private-body')\n", "result = 9\n"},
	}, nil)
	if result.Outcome != "runtime_error" || result.ErrorClass != "ValueError" || result.PrefixPythonExecutions != 1 ||
		result.PythonExecutions != 1 || result.ResultPresent || len(result.Result) != 0 {
		t.Fatalf("result=%+v body=%s", result, result.Result)
	}
}

func runExactGuestEagerComparator(
	t *testing.T,
	label string,
	config semanticspeculation.EagerComparatorPrepareConfig,
	brokerFactory func(context.Context) (*capability.Broker, error),
) exactEagerComparatorResult {
	t.Helper()
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	binding := newBranchWorkspace(t, label)
	attempt, err := binding.manager.ForkAttempt(binding.ref)
	if err != nil {
		t.Fatal(err)
	}
	fragments, err := semanticspeculation.BuildEagerComparatorPrepareChunks(config)
	if err != nil {
		t.Fatal(err)
	}
	factory := wazeroengine.Factory{
		WorkspaceManager: binding.manager,
		WorkspaceRef:     attempt.Ref(),
		WorkspaceOwner:   label,
		BrokerFactory:    brokerFactory,
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
	runner, err := factory.New(context.Background(), artifact, runConfig)
	if err != nil {
		t.Fatal(err)
	}
	streamRunner, ok := runner.(streaming.StreamRunner)
	if !ok {
		t.Fatal(errors.New("wazero runner lacks live stream support"))
	}
	prepares := make(chan string, len(fragments))
	for _, fragment := range fragments {
		prepares <- fragment
	}
	close(prepares)
	request := []byte(fmt.Sprintf(`{"run_id":%q,"code":"result = comparator_final","inputs":{}}`, label))
	outcome, err := streaming.ExecuteStream(context.Background(), streamRunner, attempt, request, prepares)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Status string                     `json:"status"`
		Result exactEagerComparatorResult `json:"result"`
	}
	if err := json.Unmarshal(outcome.Response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "ok" {
		t.Fatalf("response=%s", outcome.Response)
	}
	return envelope.Result
}

func eagerComparatorCapabilityPlan(t *testing.T, handler capability.Handler) *capability.Plan {
	t.Helper()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"eager-comparator"}`))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	spec := capability.Spec{
		Name: "fixture.eager_time", Version: "fixture.eager-time.v1", Description: "Comparator external read.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		ReadOnly: true, Idempotent: true, HandlerIdentity: "fixture-eager-time-handler-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "time", Method: "read", Arguments: []string{"value"}, ResultField: "value"},
		PreDispatch: &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "fixture", Argument: "value"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
			Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
			MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
