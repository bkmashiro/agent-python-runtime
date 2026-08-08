package claimmanifest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestMetadataPlaybackProducesOnlyStructuralQualification(t *testing.T) {
	ref, playback := metadataPlayback(t)

	manifest, err := claimmanifest.FromMetadataPlayback(ref, playback)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest validation failed: %v", err)
	}
	if manifest.Qualification != claimmanifest.QualificationStructuralOnly {
		t.Fatalf("qualification=%q", manifest.Qualification)
	}
	if err := manifest.RequireReplay(claimmanifest.ReplayR0); err != nil {
		t.Fatalf("R0 rejected: %v", err)
	}
	for _, level := range []claimmanifest.ReplayLevel{claimmanifest.ReplayR1, claimmanifest.ReplayR2} {
		if err := manifest.RequireReplay(level); !errors.Is(err, claimmanifest.ErrInsufficientEvidence) {
			t.Fatalf("level=%q err=%v", level, err)
		}
	}

	wantStatuses := map[claimmanifest.ClaimKind]claimmanifest.Status{
		claimmanifest.ClaimArtifact:  claimmanifest.StatusVerified,
		claimmanifest.ClaimBase:      claimmanifest.StatusInsufficient,
		claimmanifest.ClaimAuthority: claimmanifest.StatusInsufficient,
		claimmanifest.ClaimExecution: claimmanifest.StatusVerified,
		claimmanifest.ClaimEffect:    claimmanifest.StatusInsufficient,
		claimmanifest.ClaimOutcome:   claimmanifest.StatusInsufficient,
	}
	for kind, want := range wantStatuses {
		claim, ok := manifest.Claim(kind)
		if !ok || claim.Status != want {
			t.Fatalf("kind=%q claim=%+v present=%v", kind, claim, ok)
		}
	}
}

func TestMetadataPlaybackRejectsForgedR1AndR2Qualifications(t *testing.T) {
	ref, playback := metadataPlayback(t)
	manifest, err := claimmanifest.FromMetadataPlayback(ref, playback)
	if err != nil {
		t.Fatal(err)
	}

	for _, forged := range []claimmanifest.Qualification{
		claimmanifest.QualificationInputInjection,
		claimmanifest.QualificationStateEquivalent,
	} {
		candidate := manifest
		candidate.Claims = append([]claimmanifest.Claim(nil), manifest.Claims...)
		for index := range candidate.Claims {
			candidate.Claims[index].Status = claimmanifest.StatusVerified
		}
		candidate.Qualification = forged
		if err := candidate.Validate(); !errors.Is(err, claimmanifest.ErrOverclaimedReplay) {
			t.Fatalf("qualification=%q err=%v", forged, err)
		}
	}
}

func TestMetadataPlaybackRequiresMatchingCompletedExecution(t *testing.T) {
	ref, playback := metadataPlayback(t)
	ref.ExecutionID = "different-execution"

	if _, err := claimmanifest.FromMetadataPlayback(ref, playback); !errors.Is(err, claimmanifest.ErrExecutionNotObserved) {
		t.Fatalf("err=%v", err)
	}
}

func TestMetadataPlaybackRejectsFoldedExecutionIdentityAlias(t *testing.T) {
	ref := executionRef()
	payload, err := json.Marshal(map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"Execution_ID": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256,
		"status": "ok", "turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq, "segment_seq": ref.SegmentSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	playback := playbackWithPayload(t, ref.AgentRunID, payload)
	if _, err := claimmanifest.FromMetadataPlayback(ref, playback); !errors.Is(err, claimmanifest.ErrExecutionNotObserved) {
		t.Fatalf("err=%v", err)
	}
}

func metadataPlayback(t *testing.T) (runtimeconfig.ExecutionRef, agenttrace.Playback) {
	t.Helper()
	ref := executionRef()
	payload, err := json.Marshal(map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256,
		"status": "ok", "result_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq, "segment_seq": ref.SegmentSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ref, playbackWithPayload(t, ref.AgentRunID, payload)
}

func executionRef() runtimeconfig.ExecutionRef {
	return runtimeconfig.ExecutionRef{
		InvocationRef: runtimeconfig.InvocationRef{
			AgentRunID: "agent-run-1", TurnSeq: 2, OutputItemSeq: 3, SegmentSeq: 4,
			InvocationID: "invocation-1", InvocationAttempt: 1, ExecutionID: "execution-1",
		},
		ExecutedCodeSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func playbackWithPayload(t *testing.T, agentRunID string, payload []byte) agenttrace.Playback {
	t.Helper()
	sink := agenttrace.NewMemorySink()
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}).Begin(
		agentRunID,
		func() time.Time { return time.Unix(123, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Record(context.Background(), agenttrace.EventRuntimeCompleted, "", payload, ""); err != nil {
		t.Fatal(err)
	}
	return agenttrace.Playback{AgentRunID: agentRunID, Events: sink.Events()}
}
