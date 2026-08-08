package claimmanifest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
)

func TestMetadataPlaybackRejectsNullScalars(t *testing.T) {
	ref := executionRef()
	base := map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256,
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq,
		"segment_seq": ref.SegmentSeq, "status": "ok",
	}
	for _, key := range []string{"turn_seq", "output_item_seq", "segment_seq", "run_error", "error_code", "capability_calls"} {
		payload := make(map[string]any, len(base)+1)
		for field, value := range base {
			payload[field] = value
		}
		payload[key] = nil
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = claimmanifest.FromMetadataPlayback(ref, playbackWithPayload(t, ref.AgentRunID, encoded)); !errors.Is(err, claimmanifest.ErrExecutionNotObserved) {
			t.Fatalf("key=%s err=%v", key, err)
		}
	}
}

func TestMetadataPlaybackRejectsDuplicateMatchingCompletions(t *testing.T) {
	ref, playback := metadataPlayback(t)
	sink := agenttrace.NewMemorySink()
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}).Begin(ref.AgentRunID, func() time.Time { return time.Unix(123, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	payload := playback.Events[0].Payload
	for range 2 {
		if _, err = recorder.Record(context.Background(), agenttrace.EventRuntimeCompleted, "", payload, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = claimmanifest.FromMetadataPlayback(ref, agenttrace.Playback{AgentRunID: ref.AgentRunID, Events: sink.Events()}); !errors.Is(err, claimmanifest.ErrAmbiguousExecutionObservation) {
		t.Fatalf("err=%v", err)
	}
}

func TestMetadataPlaybackRejectsContradictoryMatchingCompletion(t *testing.T) {
	ref, playback := metadataPlayback(t)
	sink := agenttrace.NewMemorySink()
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}).Begin(ref.AgentRunID, func() time.Time { return time.Unix(123, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recorder.Record(context.Background(), agenttrace.EventRuntimeCompleted, "", playback.Events[0].Payload, ""); err != nil {
		t.Fatal(err)
	}
	contradictory, err := json.Marshal(map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256,
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq,
		"segment_seq": ref.SegmentSeq, "status": "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recorder.Record(context.Background(), agenttrace.EventRuntimeCompleted, "", contradictory, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = claimmanifest.FromMetadataPlayback(ref, agenttrace.Playback{AgentRunID: ref.AgentRunID, Events: sink.Events()}); !errors.Is(err, claimmanifest.ErrExecutionNotObserved) {
		t.Fatalf("err=%v", err)
	}
}
