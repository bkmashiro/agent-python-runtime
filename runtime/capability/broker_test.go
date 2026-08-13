package capability_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestBrokerUsesHostRegistryAndBoundedCalls(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("workspace.read_text", "test.workspace.read-text.v1"), basicGrant(t), capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"hello"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`))
	if err != nil || !json.Valid(response) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if broker.Calls() != 1 || len(broker.SnapshotReceipts()) != 1 || broker.SnapshotReceipts()[0].Outcome != "ok" {
		t.Fatalf("unexpected broker evidence: calls=%d receipts=%#v", broker.Calls(), broker.SnapshotReceipts())
	}
	response, err = broker.Call(context.Background(), []byte(`{"call_id":"two","capability":"workspace.read_text","arguments":{}}`))
	if err != nil || !containsCode(response, "call_budget_exceeded") {
		t.Fatalf("budget response=%s err=%v", response, err)
	}
}

func TestBrokerDeniesUnregisteredTool(t *testing.T) {
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"network.fetch","arguments":{}}`))
	if err != nil || !containsCode(response, "capability_denied") {
		t.Fatalf("denial response=%s err=%v", response, err)
	}
	if receipts := broker.SnapshotReceipts(); len(receipts) != 1 || receipts[0].Outcome != "denied" {
		t.Fatalf("denial receipt=%#v", receipts)
	}
}

func TestStreamingBrokerDeniesWriteEvenThroughRawBridge(t *testing.T) {
	var calls atomic.Uint32
	registry := capability.NewRegistry()
	spec := basicSpec("workspace.write_text", "test.workspace.write-text.v1")
	spec.EffectClass = capability.EffectWorkspaceWrite
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"stream-write","capability":"workspace.write_text","arguments":{}}`)
	response, err := broker.CallStreaming(context.Background(), request)
	if err != nil || !containsCode(response, "streaming_write_denied") || calls.Load() != 0 {
		t.Fatalf("response=%s calls=%d err=%v", response, calls.Load(), err)
	}
	response, err = broker.Call(context.Background(), []byte(`{"call_id":"sealed-write","capability":"workspace.write_text","arguments":{}}`))
	if err != nil || containsCode(response, "streaming_write_denied") || calls.Load() != 1 {
		t.Fatalf("sealed response=%s calls=%d err=%v", response, calls.Load(), err)
	}
}

func containsCode(response []byte, code string) bool {
	var decoded struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(response, &decoded) == nil && decoded.Error != nil && decoded.Error.Code == code
}
