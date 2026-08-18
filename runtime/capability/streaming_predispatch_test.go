package capability_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestBrokerFallsBackToOneLiveCallWhenDynamicStageDidNotTargetOccurrence(t *testing.T) {
	var liveCalls atomic.Uint32
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"principal":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "workspace.read_text", Version: "workspace.read_text.v1", Description: "Read text.",
		EffectClass: capability.EffectWorkspaceRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "workspace-read-text-v1", ReadOnly: true, Idempotent: true,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "workspace", Method: "read_text", Arguments: []string{"path"}},
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "workspace", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition,
		},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		liveCalls.Add(1)
		return json.RawMessage(`{"text":"live"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimer := &notTargetedClaimer{}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan, StagedClaimer: claimer, SemanticPreDispatch: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"other.txt"}}`))
	if err != nil || liveCalls.Load() != 1 || !strings.Contains(string(response), `"text":"live"`) {
		t.Fatalf("response=%s live=%d err=%v", response, liveCalls.Load(), err)
	}
}

type notTargetedClaimer struct{}

func (*notTargetedClaimer) Claim(context.Context, string, json.RawMessage) (capability.StagedCapabilityOutcome, error) {
	return capability.StagedCapabilityOutcome{}, capability.ErrStagedObservationNotTargeted
}
func (*notTargetedClaimer) Finalize(bool) error { return nil }

var _ capability.StagedObservationClaimer = (*notTargetedClaimer)(nil)
