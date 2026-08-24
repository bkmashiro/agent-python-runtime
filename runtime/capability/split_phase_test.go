package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestSplitPhaseTableFuturesAnyLiveCapability(t *testing.T) {
	registry := capability.NewRegistry()
	spec := basicSpec("workspace.write_text", "future-write-v1")
	spec.EffectClass = capability.EffectWorkspaceWrite
	spec.InputSchema = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
	spec.OutputSchema = json.RawMessage(`{"type":"object","properties":{"written":{"type":"boolean"}},"required":["written"],"additionalProperties":false}`)
	spec.Python = &capability.PythonProjection{Module: "workspace", Method: "write_text", Arguments: []string{"path", "content"}, ResultField: "written"}
	var physical atomic.Uint32
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"written":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "future-write", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.AttachStagedClaimer(table); err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"future-write","capability":"workspace.write_text","arguments":{"path":"a.txt","content":"hello"}}`)
	if err := table.Submit(context.Background(), "future-1", request); err != nil {
		t.Fatalf("submit write Future: %v", err)
	}
	response, err := table.Materialize(context.Background(), "future-1", broker)
	var decoded struct {
		Status string `json:"status"`
		Result struct {
			Written bool `json:"written"`
		} `json:"result"`
	}
	decodeErr := json.Unmarshal(response, &decoded)
	if err != nil || decodeErr != nil || decoded.Status != "ok" || !decoded.Result.Written || physical.Load() != 1 || broker.Calls() != 1 {
		t.Fatalf("response=%s physical=%d logical=%d decode_err=%v err=%v", response, physical.Load(), broker.Calls(), decodeErr, err)
	}
}

func TestSplitPhaseTableKeepsPhysicalWorkSeparateUntilBrokerMaterialize(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var physical atomic.Uint32
	plan := splitPhasePlan(t, 1, func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return json.RawMessage(`{"text":"ready"}`), nil
		}
	})
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "split-one", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.AttachStagedClaimer(table); err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"split-one","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	if err := table.Submit(context.Background(), "slot-one", request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("physical call did not start")
	}
	if broker.Calls() != 0 || len(broker.SnapshotReceipts()) != 0 {
		t.Fatalf("submit created logical evidence: calls=%d receipts=%#v", broker.Calls(), broker.SnapshotReceipts())
	}
	close(release)
	response, err := table.Materialize(context.Background(), "slot-one", broker)
	if err != nil || !containsResult(response, "ready") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if physical.Load() != 1 || broker.Calls() != 1 || len(broker.SnapshotReceipts()) != 1 {
		t.Fatalf("physical=%d logical=%d receipts=%#v", physical.Load(), broker.Calls(), broker.SnapshotReceipts())
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.Submitted != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 || snapshot.LogicalClaims != 1 || snapshot.Consumed != 1 || snapshot.Discarded != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSplitPhaseTableOverlapsTwoReadsAndMaterializesInSourceOrder(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	plan := splitPhasePlan(t, 2, func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		var decoded struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(arguments, &decoded) != nil {
			return nil, errors.New("invalid arguments")
		}
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- decoded.Path
		select {
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		case <-release:
			active.Add(-1)
			return json.Marshal(map[string]string{"text": decoded.Path})
		}
	})
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "split-two", Plan: plan})
	table, err := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 2, MaxCostUnits: 2, MaxResultBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.AttachStagedClaimer(table); err != nil {
		t.Fatal(err)
	}
	first := []byte(`{"call_id":"split-first","capability":"workspace.read_text","arguments":{"path":"first"}}`)
	second := []byte(`{"call_id":"split-second","capability":"workspace.read_text","arguments":{"path":"second"}}`)
	if err := table.Submit(context.Background(), "slot-first", first); err != nil {
		t.Fatal(err)
	}
	if err := table.Submit(context.Background(), "slot-second", second); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for len(seen) != 2 {
		select {
		case value := <-started:
			seen[value] = true
		case <-time.After(time.Second):
			t.Fatalf("only started %#v", seen)
		}
	}
	if maximum.Load() != 2 || broker.Calls() != 0 {
		t.Fatalf("max=%d logical=%d", maximum.Load(), broker.Calls())
	}
	close(release)
	firstResponse, err := table.Materialize(context.Background(), "slot-first", broker)
	if err != nil || !containsResult(firstResponse, "first") {
		t.Fatalf("first=%s err=%v", firstResponse, err)
	}
	secondResponse, err := table.Materialize(context.Background(), "slot-second", broker)
	if err != nil || !containsResult(secondResponse, "second") {
		t.Fatalf("second=%s err=%v", secondResponse, err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 2 || receipts[0].CallID != "split-first" || receipts[1].CallID != "split-second" {
		t.Fatalf("receipts=%#v", receipts)
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.MaximumConcurrent != 2 || snapshot.Consumed != 2 || snapshot.Discarded != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSplitPhaseTableDiscardsLaterPhysicalWorkWithoutLogicalReceipt(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	plan := splitPhasePlan(t, 2, func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		var decoded struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(arguments, &decoded)
		started <- decoded.Path
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		if decoded.Path == "fail" {
			return nil, errors.New("provider failed")
		}
		return json.Marshal(map[string]string{"text": decoded.Path})
	})
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "split-fail", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 2, MaxCostUnits: 2, MaxResultBytes: 2 << 20})
	if err := broker.AttachStagedClaimer(table); err != nil {
		t.Fatal(err)
	}
	first := []byte(`{"call_id":"split-fail-first","capability":"workspace.read_text","arguments":{"path":"fail"}}`)
	second := []byte(`{"call_id":"split-fail-second","capability":"workspace.read_text","arguments":{"path":"later"}}`)
	if err := table.Submit(context.Background(), "slot-fail", first); err != nil {
		t.Fatal(err)
	}
	if err := table.Submit(context.Background(), "slot-later", second); err != nil {
		t.Fatal(err)
	}
	<-started
	<-started
	close(release)
	response, err := table.Materialize(context.Background(), "slot-fail", broker)
	if err != nil || !containsCode(response, "handler_error") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if broker.Calls() != 1 || len(broker.SnapshotReceipts()) != 1 {
		t.Fatalf("calls=%d receipts=%#v", broker.Calls(), broker.SnapshotReceipts())
	}
	if err := broker.Finalize(false); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.LogicalClaims != 1 || snapshot.Consumed != 1 || snapshot.Discarded != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSplitPhaseTableTargetMismatchNeverFallsBackToSecondPhysicalCall(t *testing.T) {
	var physical atomic.Uint32
	started := make(chan struct{}, 1)
	plan := splitPhasePlan(t, 2, func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		started <- struct{}{}
		return json.RawMessage(`{"text":"ready"}`), nil
	})
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "split-mismatch", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err := broker.AttachStagedClaimer(table); err != nil {
		t.Fatal(err)
	}
	target := []byte(`{"call_id":"split-target","capability":"workspace.read_text","arguments":{"path":"expected"}}`)
	if err := table.Submit(context.Background(), "slot-target", target); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("target physical call did not start")
	}
	mismatch := []byte(`{"call_id":"split-target","capability":"workspace.read_text","arguments":{"path":"different"}}`)
	response, err := broker.Call(context.Background(), mismatch)
	if err != nil || !containsCode(response, "staged_observation_mismatch") || physical.Load() != 1 {
		t.Fatalf("response=%s physical=%d err=%v", response, physical.Load(), err)
	}
	if err := broker.Finalize(false); err != nil {
		t.Fatal(err)
	}
}

func TestSplitPhaseTableRejectsDuplicateSlots(t *testing.T) {
	readPlan := splitPhasePlan(t, 2, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"text":"ready"}`), nil
	})
	readTable, _ := capability.NewSplitPhaseTable(readPlan, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	request := []byte(`{"call_id":"split-read","capability":"workspace.read_text","arguments":{"path":"a"}}`)
	if err := readTable.Submit(context.Background(), "slot-read", request); err != nil {
		t.Fatal(err)
	}
	if err := readTable.Submit(context.Background(), "slot-read", request); !errors.Is(err, capability.ErrSplitPhaseDuplicate) {
		t.Fatalf("duplicate submit error=%v", err)
	}
	if err := readTable.Finalize(false); err != nil {
		t.Fatal(err)
	}
}

func TestSplitPhaseTableFinalizeCancelsAndJoinsUnclaimedWork(t *testing.T) {
	started := make(chan struct{})
	plan := splitPhasePlan(t, 1, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	table, err := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{
		MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"split-cancel","capability":"workspace.read_text","arguments":{"path":"a"}}`)
	if err := table.Submit(context.Background(), "slot-cancel", request); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := table.Finalize(false); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.Submitted != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 ||
		snapshot.Cancelled != 1 || snapshot.Discarded != 1 || snapshot.LogicalClaims != 0 || snapshot.Consumed != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func splitPhasePlan(t *testing.T, maxCalls uint32, handler capability.HandlerFunc) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	if err := registry.Register(stagedTestSpec(), basicGrant(t), handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
