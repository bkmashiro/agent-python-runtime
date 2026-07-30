package hermesbridge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func traceRef(executionID, invocationID string) runtimeconfig.InvocationRef {
	return runtimeconfig.InvocationRef{
		AgentRunID: "agent-run-trace", TurnSeq: 1, OutputItemSeq: 1, SegmentSeq: 1,
		InvocationID: invocationID, InvocationAttempt: 1, ExecutionID: executionID,
	}
}

func TestTraceManagerPersistsMetadataOnlyInvocationEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewTraceManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ref := traceRef("execution-1", "invocation-1")
	started, err := manager.RuntimeStarted(context.Background(), ref, digestString("request containing secret code"))
	if err != nil {
		t.Fatal(err)
	}
	executionRef := runtimeconfig.ExecutionRef{InvocationRef: ref, ExecutedCodeSHA256: digestString("result = 42")}
	if err := manager.RuntimeCompleted(context.Background(), started, executionRef, "ok", digestString("42")); err != nil {
		t.Fatal(err)
	}
	playback, err := store.LoadPlayback(context.Background(), ref.AgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(playback.Events) != 2 || playback.Events[0].EventType != agenttrace.EventRuntimeStarted ||
		playback.Events[1].EventType != agenttrace.EventRuntimeCompleted || playback.Events[1].ParentEventID != started {
		t.Fatalf("unexpected playback: %#v", playback)
	}
	for _, event := range playback.Events {
		if string(event.Payload) == "" || containsRawTraceBody(string(event.Payload)) {
			t.Fatalf("unsafe payload: %s", event.Payload)
		}
	}
}

func TestTraceManagerResumesExistingRunAfterBridgeRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewTraceManager(store)
	if err != nil {
		t.Fatal(err)
	}
	first := traceRef("execution-1", "invocation-1")
	parent, err := manager.RuntimeStarted(context.Background(), first, digestString("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RuntimeCompleted(context.Background(), parent, runtimeconfig.ExecutionRef{InvocationRef: first, ExecutedCodeSHA256: digestString("code-1")}, "ok", digestString("result-1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restarted, err := NewTraceManager(store)
	if err != nil {
		t.Fatal(err)
	}
	second := traceRef("execution-2", "invocation-2")
	parent, err = restarted.RuntimeStarted(context.Background(), second, digestString("request-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RuntimeCompleted(context.Background(), parent, runtimeconfig.ExecutionRef{InvocationRef: second, ExecutedCodeSHA256: digestString("code-2")}, "ok", digestString("result-2")); err != nil {
		t.Fatal(err)
	}
	playback, err := store.LoadPlayback(context.Background(), second.AgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(playback.Events) != 4 || playback.Events[2].Sequence != 3 || playback.Events[3].Sequence != 4 {
		t.Fatalf("trace was not resumed: %#v", playback.Events)
	}
}

func containsRawTraceBody(payload string) bool {
	for _, forbidden := range []string{"secret code", "result = 42", "request-1", "result-1"} {
		if len(forbidden) > 0 && contains(payload, forbidden) {
			return true
		}
	}
	return false
}

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
