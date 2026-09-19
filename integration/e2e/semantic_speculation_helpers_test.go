package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func testDigest(value string) string { return testDigestBytes([]byte(value)) }

// testDigestBytes is shared by prepared semantic-analysis and PLM fixtures.
func testDigestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

// eagerComparatorCapabilityPlan remains as a capability-plan fixture for the
// retained serial/prepared tests; the obsolete eager comparator tests are gone.
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
