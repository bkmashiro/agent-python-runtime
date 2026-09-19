package capability_test

import (
	"context"
	"encoding/json"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"testing"
)

type writeClaimer struct{ called bool }

func (c *writeClaimer) Claim(context.Context, string, json.RawMessage) (capability.StagedCapabilityOutcome, error) {
	c.called = true
	return capability.StagedCapabilityOutcome{}, capability.ErrStagedObservationNotTargeted
}
func (*writeClaimer) Finalize(bool) error { return nil }

func TestExternalWriteCannotUseLateStagedClaimer(t *testing.T) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"write-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	spec := capability.Spec{Name: "external.write", Version: "v1", Description: "external write", EffectClass: capability.EffectExternalWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "external.write.v1", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	err = registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "write-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	claimer := &writeClaimer{}
	if err := broker.AttachStagedClaimer(claimer); err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"write","capability":"external.write","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "denied" || claimer.called || calls != 0 {
		t.Fatalf("response=%s staged=%v calls=%d", response, claimer.called, calls)
	}
}
