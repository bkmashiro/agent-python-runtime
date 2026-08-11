package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestCapabilityGrantCanonicalPolicyBindsPlanIdentity(t *testing.T) {
	firstGrant, err := capability.NewGrant(json.RawMessage(`{"endpoint":"https://source.invalid/catalog","max_bytes":4096}`))
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := capability.NewGrant(json.RawMessage("{\n  \"max_bytes\": 4096, \"endpoint\": \"https://source.invalid/catalog\"\n}"))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := capability.NewGrant(json.RawMessage(`{"endpoint":"https://source.invalid/other","max_bytes":4096}`))
	if err != nil {
		t.Fatal(err)
	}
	if firstGrant.Identity() == "" || firstGrant.Identity() != equivalent.Identity() || firstGrant.Identity() == changed.Identity() {
		t.Fatalf("grant identities first=%q equivalent=%q changed=%q", firstGrant.Identity(), equivalent.Identity(), changed.Identity())
	}

	first := capability.NewRegistry()
	if err := first.Register(testSpec(), firstGrant, noopHandler); err != nil {
		t.Fatal(err)
	}
	firstPlan, err := first.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	second := capability.NewRegistry()
	if err := second.Register(testSpec(), changed, noopHandler); err != nil {
		t.Fatal(err)
	}
	secondPlan, err := second.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Identity() == secondPlan.Identity() {
		t.Fatal("different per-Run policy grants produced the same plan identity")
	}
}

func TestCapabilityGrantRejectsAmbiguousOrInvalidPolicy(t *testing.T) {
	for name, policy := range map[string]json.RawMessage{
		"empty":       nil,
		"duplicate":   json.RawMessage(`{"endpoint":"one","endpoint":"two"}`),
		"trailing":    json.RawMessage(`{} {}`),
		"invalid UTF": append(json.RawMessage(`{"endpoint":"`), append([]byte{0xff}, []byte(`"}`)...)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := capability.NewGrant(policy); err != capability.ErrInvalidGrant {
				t.Fatalf("error=%v", err)
			}
		})
	}
	registry := capability.NewRegistry()
	if err := registry.Register(testSpec(), capability.Grant{}, noopHandler); err != capability.ErrInvalidGrant {
		t.Fatalf("missing grant error=%v", err)
	}
}

func TestCapabilityGrantIsHostOnlyAndReturnedDefensively(t *testing.T) {
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"host-local"}`))
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(testSpec(), grant, noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	grants := plan.Grants()
	if len(grants) != 1 || grants[0].Capability != testSpec().Name || grants[0].PolicySHA256 != grant.Identity() {
		t.Fatalf("grants=%#v", grants)
	}
	grants[0].Capability = "tampered"
	grants[0].PolicySHA256 = "tampered"
	fresh := plan.Grants()
	if fresh[0].Capability != testSpec().Name || fresh[0].PolicySHA256 != grant.Identity() {
		t.Fatalf("plan grant mutated through caller slice: %#v", fresh)
	}

	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"path":"note.txt"},"grant_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !containsCode(response, "invalid_arguments") || broker.Calls() != 0 {
		t.Fatalf("Guest-authored grant was accepted: response=%s calls=%d", response, broker.Calls())
	}
}
