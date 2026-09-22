package durable

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReplayHistoryIsOrderedBoundedAndPrivateByDefault(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics")
	definition.Tools = json.RawMessage(`[{"name":"alpha","version":"v7"},{"name":"beta","version":"v2"}]`)
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	for sequence, call := range []LoggedCall{
		{Sequence: 0, CallID: "call-0", Capability: "alpha", Arguments: json.RawMessage(`{"secret":"first"}`)},
		{Sequence: 1, CallID: "call-1", Capability: "beta", Arguments: json.RawMessage(`{"secret":"second"}`)},
		{Sequence: 2, CallID: "call-2", Capability: "alpha", Arguments: json.RawMessage(`{"secret":"third"}`)},
	} {
		if _, created, err := store.BeginCall(ctx, definition.ID, call); err != nil || !created {
			t.Fatalf("begin %d created=%v err=%v", sequence, created, err)
		}
		outcome := json.RawMessage(`{"value":true}`)
		if sequence == 1 {
			outcome = json.RawMessage(`{"value":null,"error":"sensitive failure"}`)
		}
		if _, err := store.CompleteCall(ctx, definition.ID, uint32(sequence), outcome); err != nil {
			t.Fatal(err)
		}
	}

	first, err := store.ReplayHistory(ctx, definition.ID, ReplayHistoryOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Calls) != 2 || first.Calls[0].Sequence != 0 || first.Calls[1].Sequence != 1 {
		t.Fatalf("first page=%+v", first)
	}
	if !first.HasMore || first.NextSequence != 2 {
		t.Fatalf("first pagination=%+v", first)
	}
	if first.Calls[0].Version != "v7" || first.Calls[1].Version != "v2" {
		t.Fatalf("versions=%+v", first.Calls)
	}
	if first.Calls[0].OutcomeClass != OutcomeSuccess || first.Calls[1].OutcomeClass != OutcomeError {
		t.Fatalf("outcome classes=%+v", first.Calls)
	}
	for _, call := range first.Calls {
		if len(call.Arguments) != 0 || len(call.Outcome) != 0 {
			t.Fatalf("default diagnostics exposed payload: %+v", call)
		}
	}

	second, err := store.ReplayHistory(ctx, definition.ID, ReplayHistoryOptions{
		FromSequence:    first.NextSequence,
		Limit:           2,
		IncludePayloads: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Calls) != 1 || second.Calls[0].Sequence != 2 {
		t.Fatalf("second page=%+v", second)
	}
	if string(second.Calls[0].Arguments) != `{"secret":"third"}` || string(second.Calls[0].Outcome) != `{"value":true}` {
		t.Fatalf("opt-in payloads=%+v", second.Calls[0])
	}
}

func TestReplayHistoryLimitIsClamped(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-limit")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	for sequence := uint32(0); sequence < MaxReplayHistoryPageSize+1; sequence++ {
		call := LoggedCall{Sequence: sequence, CallID: "call-" + string(rune('a'+sequence%26)), Capability: "tool", Arguments: json.RawMessage(`{}`)}
		if _, _, err := store.BeginCall(ctx, definition.ID, call); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompleteCall(ctx, definition.ID, sequence, json.RawMessage(`{"value":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ReplayHistory(ctx, definition.ID, ReplayHistoryOptions{Limit: MaxReplayHistoryPageSize + 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != int(MaxReplayHistoryPageSize) || !page.HasMore {
		t.Fatalf("unbounded page=%+v", page)
	}
}

func TestReplayHistoryFailsClosedOnMalformedToolDeclarations(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-malformed-tools")
	definition.Tools = json.RawMessage(`[]`)
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE runs SET tools=? WHERE run_id=?", []byte(`{"name":`), definition.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplayHistory(ctx, definition.ID, ReplayHistoryOptions{}); err == nil || !strings.Contains(err.Error(), "decode durable tool declarations") {
		t.Fatalf("malformed declarations were silently ignored: %v", err)
	}
}

func TestReplayHistoryClassifiesMalformedOutcomeWithoutLoadingIt(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-malformed-outcome")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	call := LoggedCall{Sequence: 0, CallID: "call-0", Capability: "tool", Arguments: json.RawMessage(`{"private":"argument"}`)}
	if _, _, err := store.BeginCall(ctx, definition.ID, call); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteCall(ctx, definition.ID, 0, json.RawMessage(`not-json`)); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReplayHistory(ctx, definition.ID, ReplayHistoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Calls) != 1 || page.Calls[0].OutcomeClass != OutcomeUnknown || len(page.Calls[0].Outcome) != 0 {
		t.Fatalf("malformed default classification=%+v", page.Calls)
	}
}

func TestHistoryMismatchDetailsIdentifyFieldWithoutPayload(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-mismatch")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	original := LoggedCall{Sequence: 0, CallID: "call-0", Capability: "tool", Arguments: json.RawMessage(`{"token":"do-not-leak"}`)}
	if _, _, err := store.BeginCall(ctx, definition.ID, original); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		call  LoggedCall
		field HistoryMismatchField
	}{
		{"call id", LoggedCall{Sequence: 0, CallID: "other", Capability: "tool", Arguments: original.Arguments}, HistoryMismatchCallID},
		{"capability", LoggedCall{Sequence: 0, CallID: "call-0", Capability: "other", Arguments: original.Arguments}, HistoryMismatchCapability},
		{"arguments", LoggedCall{Sequence: 0, CallID: "call-0", Capability: "tool", Arguments: json.RawMessage(`{"token":"different"}`)}, HistoryMismatchArguments},
		{"sequence", LoggedCall{Sequence: 2, CallID: "call-2", Capability: "tool", Arguments: json.RawMessage(`{}`)}, HistoryMismatchSequence},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := store.BeginCall(ctx, definition.ID, test.call)
			var mismatch *HistoryMismatchError
			if !errors.As(err, &mismatch) || !errors.Is(err, ErrHistoryMismatch) {
				t.Fatalf("err=%v", err)
			}
			if mismatch.Sequence != test.call.Sequence || mismatch.Field != test.field {
				t.Fatalf("details=%+v want sequence=%d field=%s", mismatch, test.call.Sequence, test.field)
			}
			if strings.Contains(err.Error(), "do-not-leak") || strings.Contains(err.Error(), "different") {
				t.Fatalf("mismatch leaked payload: %v", err)
			}
		})
	}
}

func TestHistoryMismatchDetailsCoverJournalReplayAndOperationKey(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-journal-mismatch")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	call := LoggedCall{Sequence: 0, CallID: "call-0", Capability: "tool", Arguments: json.RawMessage(`{"token":"secret"}`)}
	if _, _, err := store.BeginCall(ctx, definition.ID, call); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteCall(ctx, definition.ID, 0, json.RawMessage(`{"value":true}`)); err != nil {
		t.Fatal(err)
	}

	runner := journalOnlyRunner(store)
	_, err := (&journal{runner: runner, runID: definition.ID}).Call(ctx, "tool", json.RawMessage(`{"token":"changed"}`), func(context.Context) []byte {
		t.Fatal("mismatched replay dispatched")
		return nil
	})
	var mismatch *HistoryMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, ErrHistoryMismatch) || mismatch.Field != HistoryMismatchArguments {
		t.Fatalf("journal mismatch=%v details=%+v", err, mismatch)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "changed") {
		t.Fatalf("journal mismatch leaked payload: %v", err)
	}

	if _, err := store.db.ExecContext(ctx, "UPDATE calls SET operation_key=? WHERE run_id=? AND sequence=?", "corrupt", definition.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.BeginCall(ctx, definition.ID, call)
	if !errors.As(err, &mismatch) || mismatch.Field != HistoryMismatchOperationKey {
		t.Fatalf("operation-key mismatch=%v details=%+v", err, mismatch)
	}
}

func TestJournalReplayChecksSequenceBeforeOtherFields(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	definition := storeTestDefinition("diagnostics-journal-sequence")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	runner := journalOnlyRunner(store)
	args := json.RawMessage(`{"same":"arguments"}`)
	j := &journal{
		runner:  runner,
		runID:   definition.ID,
		history: []Call{{Sequence: 9, CallID: "call-0", Tool: "tool", Arguments: args}},
	}
	_, err := j.Call(ctx, "tool", args, func(context.Context) []byte {
		t.Fatal("sequence-mismatched replay dispatched")
		return nil
	})
	var mismatch *HistoryMismatchError
	if !errors.As(err, &mismatch) || mismatch.Field != HistoryMismatchSequence {
		t.Fatalf("sequence mismatch=%v details=%+v", err, mismatch)
	}
}
