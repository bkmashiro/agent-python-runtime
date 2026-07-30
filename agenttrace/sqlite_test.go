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
	fork, err := playback.ForkAt(2, "agent-run-fork")
	if err != nil {
		t.Fatal(err)
	}
	if fork.SourceAgentRunID != "agent-run-1" || fork.AgentRunID != "agent-run-fork" || fork.ParentEventID != second.EventID || fork.StateFingerprint != events[1].StateFingerprint || fork.PrefixDigest == "" {
		t.Fatalf("fork=%+v", fork)
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
