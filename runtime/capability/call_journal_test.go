package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type journalRecord struct {
	call     capability.LoggedCall
	response []byte
}

type memoryCallJournal struct {
	mu       sync.Mutex
	replay   bool
	records  map[uint32]journalRecord
	dispatch atomic.Uint32
	fail     error
}

func (journal *memoryCallJournal) Call(ctx context.Context, call capability.LoggedCall, dispatch func(context.Context) ([]byte, error)) ([]byte, error) {
	journal.mu.Lock()
	if journal.fail != nil {
		err := journal.fail
		journal.mu.Unlock()
		return nil, err
	}
	if journal.replay {
		record, ok := journal.records[call.Sequence]
		if !ok || record.call.CallID != call.CallID || record.call.Capability != call.Capability || string(record.call.Arguments) != string(call.Arguments) {
			journal.mu.Unlock()
			return nil, errors.New("journal mismatch")
		}
		response := append([]byte(nil), record.response...)
		journal.mu.Unlock()
		return response, nil
	}
	journal.mu.Unlock()

	journal.dispatch.Add(1)
	response, err := dispatch(ctx)
	if err != nil {
		return nil, err
	}
	journal.mu.Lock()
	if journal.records == nil {
		journal.records = make(map[uint32]journalRecord)
	}
	journal.records[call.Sequence] = journalRecord{
		call: capability.LoggedCall{
			Sequence: call.Sequence, CallID: call.CallID, Capability: call.Capability,
			Arguments: append(json.RawMessage(nil), call.Arguments...),
		},
		response: append([]byte(nil), response...),
	}
	journal.mu.Unlock()
	return response, nil
}

func TestCallJournalRecordsAndReplaysBusinessOutcomesWithoutLiveDispatch(t *testing.T) {
	var handlerCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("tools.echo", "test.tools.echo.v1"), basicGrant(t), capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		var input struct {
			Error bool `json:"error"`
		}
		if err := json.Unmarshal(arguments, &input); err != nil {
			return nil, err
		}
		if input.Error {
			return nil, errors.New("handler failed")
		}
		return json.RawMessage(`{"value":"live"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	liveJournal := &memoryCallJournal{}
	live, err := capability.NewBroker(capability.Config{RunIdentity: "journal-live", Plan: plan, CallJournal: liveJournal})
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{
		`{"call_id":"success","capability":"tools.echo","arguments":{}}`,
		`{"call_id":"business-error","capability":"tools.echo","arguments":{"error":true}}`,
		`{"call_id":"denial","capability":"tools.missing","arguments":{}}`,
	}
	want := make([][]byte, len(calls))
	for index, request := range calls {
		want[index], err = live.Call(context.Background(), []byte(request))
		if err != nil {
			t.Fatalf("live call %d: %v", index, err)
		}
	}
	if got := handlerCalls.Load(); got != 2 {
		t.Fatalf("live handler calls=%d, want 2", got)
	}
	if live.Calls() != 3 || len(live.SnapshotReceipts()) != 3 {
		t.Fatalf("live calls=%d receipts=%d", live.Calls(), len(live.SnapshotReceipts()))
	}

	replayJournal := &memoryCallJournal{replay: true, records: liveJournal.records}
	replay, err := capability.NewBroker(capability.Config{RunIdentity: "journal-replay", Plan: plan, CallJournal: replayJournal})
	if err != nil {
		t.Fatal(err)
	}
	for index, request := range calls {
		got, callErr := replay.Call(context.Background(), []byte(request))
		if callErr != nil || string(got) != string(want[index]) {
			t.Fatalf("replay call %d response=%s err=%v, want=%s", index, got, callErr, want[index])
		}
	}
	if got := handlerCalls.Load(); got != 2 {
		t.Fatalf("replay dispatched live handler: calls=%d", got)
	}
	if len(replay.SnapshotReceipts()) != 3 {
		t.Fatalf("replay receipts=%d, want 3", len(replay.SnapshotReceipts()))
	}
}

func TestCallJournalLiveDispatchesHistoryTail(t *testing.T) {
	var handlerCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("tools.echo", "test.tools.echo-tail.v1"), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"value":"tail"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	journal := &memoryCallJournal{replay: true, records: map[uint32]journalRecord{
		0: {call: capability.LoggedCall{Sequence: 0, CallID: "first", Capability: "tools.echo", Arguments: json.RawMessage(`{}`)}, response: []byte(`{"call_id":"first","status":"ok","result":{"value":"cached"}}`)},
	}}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "journal-tail", Plan: plan, CallJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"first","capability":"tools.echo","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	journal.replay = false
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"second","capability":"tools.echo","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	if handlerCalls.Load() != 1 || len(broker.SnapshotReceipts()) != 2 {
		t.Fatalf("tail handler calls=%d receipts=%d", handlerCalls.Load(), len(broker.SnapshotReceipts()))
	}
}

func TestCallJournalRecordsSchemaDenialWithoutAnOperationGap(t *testing.T) {
	var handlerCalls atomic.Uint32
	registry := capability.NewRegistry()
	spec := basicSpec("tools.required", "test.tools.required.v1")
	spec.InputSchema = json.RawMessage(`{"type":"object","required":["value"]}`)
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"ok":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	journal := &memoryCallJournal{}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "journal-schema", Plan: plan, CallJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := broker.Call(context.Background(), []byte(`{"call_id":"invalid","capability":"tools.required","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var denial struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(denied, &denial) != nil || denial.Status != "denied" {
		t.Fatalf("schema denial=%s", denied)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"valid","capability":"tools.required","arguments":{"value":1}}`)); err != nil {
		t.Fatal(err)
	}
	if handlerCalls.Load() != 1 || broker.Calls() != 2 || len(journal.records) != 2 {
		t.Fatalf("handler calls=%d broker calls=%d journal records=%d", handlerCalls.Load(), broker.Calls(), len(journal.records))
	}
	for sequence := uint32(0); sequence < 2; sequence++ {
		if _, ok := journal.records[sequence]; !ok {
			t.Fatalf("missing journal sequence %d: %#v", sequence, journal.records)
		}
	}
}

func TestCallJournalControlFailureStopsFurtherDispatch(t *testing.T) {
	var handlerCalls atomic.Uint32
	registry := capability.NewRegistry()
	if err := registry.Register(basicSpec("tools.echo", "test.tools.echo-control.v1"), basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"value":"unexpected"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	controlErr := errors.New("journal storage unavailable")
	journal := &memoryCallJournal{fail: controlErr}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "journal-control", Plan: plan, CallJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"first","capability":"tools.echo","arguments":{}}`)); !errors.Is(err, controlErr) {
		t.Fatalf("first error=%v, want %v", err, controlErr)
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"second","capability":"tools.echo","arguments":{}}`)); !errors.Is(err, controlErr) {
		t.Fatalf("second error=%v, want original %v", err, controlErr)
	}
	if !errors.Is(broker.ControlError(), controlErr) || handlerCalls.Load() != 0 || broker.Calls() != 1 {
		t.Fatalf("control=%v handler calls=%d broker calls=%d", broker.ControlError(), handlerCalls.Load(), broker.Calls())
	}
}

func TestCallJournalRejectsUnsupportedCombinations(t *testing.T) {
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	journal := &memoryCallJournal{}
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "journal-playback", Plan: plan, CallJournal: journal, Playback: &capability.PlaybackConfig{}}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("journal/playback err=%v", err)
	}
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "journal-staged", Plan: plan, CallJournal: journal, StagedClaimer: &stagedClaimer{}}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("journal/staged err=%v", err)
	}
	owner, err := capability.NewBroker(capability.Config{RunIdentity: "journal-plm", Plan: plan, CallJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capability.NewSplitPhaseTable(owner, ownerLimits()); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("journal/PLM err=%v", err)
	}
}
