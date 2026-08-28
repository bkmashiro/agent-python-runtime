package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestSplitPhaseTableIsAtomicallyOwnedByOneRunBroker(t *testing.T) {
	plan := splitPhasePlan(t, 2, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"ready"}`), nil
	})
	owner, err := capability.NewBroker(capability.Config{RunIdentity: "owner-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(owner, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	if table.RunIdentity() != "owner-run" || table.PlanIdentity() != plan.Identity() || owner.AttachedSplitPhaseTable() != table {
		t.Fatalf("owner run=%q plan=%q attached=%p table=%p", table.RunIdentity(), table.PlanIdentity(), owner.AttachedSplitPhaseTable(), table)
	}

	foreign, err := capability.NewBroker(capability.Config{RunIdentity: "foreign-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.AttachStagedClaimer(table); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("foreign attach err=%v", err)
	}
	if foreign.AttachedSplitPhaseTable() != nil {
		t.Fatal("foreign Broker retained another Run's table")
	}
}

func TestSplitPhaseTableRejectsNilDuplicateAndLateOwners(t *testing.T) {
	plan := splitPhasePlan(t, 2, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"ready"}`), nil
	})
	if _, err := capability.NewSplitPhaseTable(nil, ownerLimits()); !errors.Is(err, capability.ErrSplitPhaseUnavailable) {
		t.Fatalf("nil owner err=%v", err)
	}

	owner, err := capability.NewBroker(capability.Config{RunIdentity: "duplicate-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capability.NewSplitPhaseTable(owner, ownerLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := capability.NewSplitPhaseTable(owner, ownerLimits()); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("duplicate owner err=%v", err)
	}

	late, err := capability.NewBroker(capability.Config{RunIdentity: "late-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := late.Call(context.Background(), []byte(`{"call_id":"ordinary-1","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`))
	if err != nil || !containsResult(response, "ready") {
		t.Fatalf("ordinary response=%s err=%v", response, err)
	}
	if _, err := capability.NewSplitPhaseTable(late, ownerLimits()); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("late owner err=%v", err)
	}
}

func TestSplitPhaseReuseEventsAreBoundedSeparatelyFromAttempts(t *testing.T) {
	var physical atomic.Uint32
	plan := splitPhasePlan(t, 1, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"text":"ready"}`), nil
	})
	owner, err := capability.NewBroker(capability.Config{RunIdentity: "event-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(owner, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"event-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	for index := 0; index < 10_000; index++ {
		if err := table.IssueOrReuse(context.Background(), "event-slot", request); err != nil {
			t.Fatalf("reuse %d: %v", index, err)
		}
	}
	response, err := table.Materialize(context.Background(), "event-slot")
	if err != nil || !containsResult(response, "ready") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if err := owner.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.Reused != 9_999 || physical.Load() != 1 {
		t.Fatalf("reused=%d physical=%d", snapshot.Reused, physical.Load())
	}
	if len(snapshot.Events) > 16 || snapshot.EventsDropped == 0 {
		t.Fatalf("events=%d dropped=%d", len(snapshot.Events), snapshot.EventsDropped)
	}
}

func ownerLimits() capability.SplitPhaseLimits {
	return capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20}
}
