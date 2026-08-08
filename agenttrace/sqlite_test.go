package agenttrace_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

func TestSQLiteStoreAppendsQueriesAndReopensAnOrderedTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.db")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin := agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: store}
	recorder, err := plugin.Begin("agent-run-1", func() time.Time { return time.Unix(100, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	first, err := recorder.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{"spec_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := recorder.Record(context.Background(), agenttrace.EventCheckpointCreated, first.EventID, json.RawMessage(`{"checkpoint_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`), "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trace store mode=%v", info.Mode().Perm())
	}
	reopened, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events, err := reopened.Events(context.Background(), "agent-run-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventID != first.EventID || events[1].EventID != second.EventID || events[1].Sequence != 2 {
		t.Fatalf("events=%+v", events)
	}
	foreignSink := &agenttrace.MemorySink{}
	foreignRecorder, err := agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: foreignSink}.Begin("agent-run-2", func() time.Time { return time.Unix(3, 0) })
	if err != nil {
		t.Fatal(err)
	}
	foreignEvent, err := foreignRecorder.Record(context.Background(), agenttrace.EventRunStarted, first.EventID, json.RawMessage(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Append(context.Background(), foreignEvent); !errors.Is(err, agenttrace.ErrIntegrity) {
		t.Fatalf("cross-run parent err=%v", err)
	}
	playback, err := reopened.LoadPlayback(context.Background(), "agent-run-1")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := playback.IntegrityDigest()
	if err != nil {
		t.Fatal(err)
	}
	mutated := agenttrace.Playback{AgentRunID: playback.AgentRunID, Events: append([]agenttrace.Event(nil), playback.Events...)}
	mutated.Events[1].ObservedAt = mutated.Events[1].ObservedAt.Add(time.Nanosecond)
	if err := mutated.Events[1].Validate(); err != nil {
		t.Fatalf("timestamp-only mutation should preserve event validation: %v", err)
	}
	mutatedDigest, err := mutated.IntegrityDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest == mutatedDigest {
		t.Fatal("event envelope mutation did not change integrity digest")
	}
	fork, err := playback.ForkAt(2, "agent-run-fork")
	if err != nil {
		t.Fatal(err)
	}
	if fork.SourceAgentRunID != "agent-run-1" || fork.AgentRunID != "agent-run-fork" || fork.ParentEventID != second.EventID || fork.StateFingerprint != events[1].StateFingerprint || fork.PrefixDigest == "" {
		t.Fatalf("fork=%+v", fork)
	}
}

func TestPlaybackAggregateBoundsFailClosed(t *testing.T) {
	sink := agenttrace.NewMemorySink()
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}).Begin("bounded-run", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4097; index++ {
		if _, err := recorder.Record(context.Background(), agenttrace.EventLLMOutputObserved, "", json.RawMessage(`{"status":"ok"}`), ""); err != nil {
			t.Fatalf("record %d: %v", index, err)
		}
	}
	playback := agenttrace.Playback{AgentRunID: "bounded-run", Events: sink.Events()}
	if err := playback.ValidateBounds(); !errors.Is(err, agenttrace.ErrIntegrity) {
		t.Fatalf("bounds err=%v", err)
	}
	if _, err := playback.IntegrityDigest(); !errors.Is(err, agenttrace.ErrIntegrity) {
		t.Fatalf("integrity err=%v", err)
	}
}

func TestSQLiteStoreRejectsSequenceCollisionAndTamperedEvent(t *testing.T) {
	store, err := agenttrace.OpenSQLiteStore(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := agenttrace.Event{
		Version: agenttrace.EventVersion, EventID: "evt-1", AgentRunID: "agent-run-1", Sequence: 1,
		EventType: agenttrace.EventRunStarted, ObservedAt: time.Unix(1, 0).UTC(), Payload: json.RawMessage(`{}`),
		PayloadDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := store.Append(context.Background(), event); !errors.Is(err, agenttrace.ErrIntegrity) {
		t.Fatalf("err=%v", err)
	}
}

func TestSQLiteReadOnlyQueriesRunsWithoutMutatingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"run-b", "run-a"} {
		recorder, beginErr := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: store}).Begin(runID, func() time.Time { return time.Unix(10, 0) })
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, recordErr := recorder.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{"spec_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), ""); recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	readOnly, err := agenttrace.OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	runs, err := readOnly.Runs(context.Background(), 10)
	if err != nil || len(runs) != 2 || runs[0].AgentRunID != "run-a" || runs[0].EventCount != 1 || runs[1].AgentRunID != "run-b" {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if err := readOnly.Append(context.Background(), agenttrace.Event{}); !errors.Is(err, agenttrace.ErrAppend) {
		t.Fatalf("read-only append err=%v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
}
