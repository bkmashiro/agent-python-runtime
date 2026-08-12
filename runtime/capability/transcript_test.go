package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type evidenceHandler struct{}

func (evidenceHandler) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"value":1}`), nil
}

func (evidenceHandler) CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, capability.TransportEvidence, error) {
	return json.RawMessage(`{ "value" : 1 }`), capability.TransportEvidence{
		Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: 11,
		BodySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, nil
}

func TestBrokerCapturesValidatedCanonicalTranscript(t *testing.T) {
	registry := capability.NewRegistry()
	spec := basicSpec("sources.test", "test.sources.v1")
	spec.Playback = capability.PlaybackCaptured
	spec.OutputSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)
	if err := registry.Register(spec, basicGrant(t), evidenceHandler{}); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.test","arguments":{ }}`))
	if err != nil || !containsCodeOrStatus(response, "ok") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	entries := broker.SnapshotTranscript()
	if len(entries) != 1 || entries[0].OperationIndex != 0 || entries[0].Capability != "sources.test" || string(entries[0].Arguments) != `{}` || string(entries[0].Result) != `{"value":1}` {
		t.Fatalf("entries=%#v", entries)
	}
	if entries[0].ArgumentsSHA256 == "" || entries[0].ResultSHA256 == "" || entries[0].Evidence.Status != 200 {
		t.Fatalf("entry evidence=%#v", entries[0])
	}
	entries[0].Arguments[0] = 'x'
	entries[0].Result[0] = 'x'
	fresh := broker.SnapshotTranscript()[0]
	if string(fresh.Arguments) != `{}` || string(fresh.Result) != `{"value":1}` {
		t.Fatalf("transcript leaked mutable bytes: %#v", fresh)
	}
}

func TestCapturedSpecRequiresPerCallEvidence(t *testing.T) {
	registry := capability.NewRegistry()
	spec := basicSpec("sources.test", "test.sources.v1")
	spec.Playback = capability.PlaybackCaptured
	if err := registry.Register(spec, basicGrant(t), noopHandler); err != capability.ErrInvalidTool {
		t.Fatalf("error=%v", err)
	}
}

func TestLiveOnlyCapabilityIsNotCaptured(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("workspace.test", "test.workspace.v1"), basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.test","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	if entries := broker.SnapshotTranscript(); len(entries) != 0 {
		t.Fatalf("live-only transcript=%#v", entries)
	}
}

func containsCodeOrStatus(response []byte, status string) bool {
	var decoded struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(response, &decoded) == nil && decoded.Status == status
}
