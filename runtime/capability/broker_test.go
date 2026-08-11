package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestBrokerUsesHostRegistryAndBoundedCalls(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register("workspace.read_text", capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"hello"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", MaxCalls: 1, Registry: registry})
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
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", MaxCalls: 1, Registry: capability.NewRegistry()})
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

func containsCode(response []byte, code string) bool {
	var decoded struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(response, &decoded) == nil && decoded.Error != nil && decoded.Error.Code == code
}
