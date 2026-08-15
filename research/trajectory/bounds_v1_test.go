package trajectory_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestConfiguredBoundsRequireExplicitTruncationEvidence(t *testing.T) {
	builder, err := trajectory.NewBoundedBuilder(trajectory.TraceHeader{
		TraceID: "trace-bounds-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-bounds-0001",
	}, nil, trajectory.EvidenceLimits{MaxEvents: 2, MaxParents: 1, MaxPayloadBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventExecutionAttempt, ActorID: "actor-host-0001", Payload: trajectory.ExecutionAttemptPayload{RunID: "run-bounds-0001", AttemptID: "attempt-bounds-0001", Status: "started"}})
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventExecutionAttempt, ActorID: "actor-host-0001", Payload: trajectory.ExecutionAttemptPayload{RunID: "run-bounds-0001", AttemptID: "attempt-bounds-0002", Status: "started"}}); err == nil {
		t.Fatal("event limit was not enforced")
	}
	if _, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate); err == nil {
		t.Fatal("silent bounded omission was exportable")
	}
	if _, err := builder.MarkTruncated("actor-host-0001", trajectory.TruncationPayload{Scope: "event_stream", Reason: "event_limit", DroppedEvents: 1}); err != nil {
		t.Fatal(err)
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: false}})
	full, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || len(full.Events) != 4 || full.Events[2].Type != trajectory.EventEvidenceTruncated {
		t.Fatalf("full=%+v err=%v", full, err)
	}
	production, err := builder.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil || len(production.Events) != 4 {
		t.Fatalf("production=%+v err=%v", production, err)
	}
}

func TestParentAndPayloadBoundsFailClosed(t *testing.T) {
	builder, err := trajectory.NewBoundedBuilder(trajectory.TraceHeader{
		TraceID: "trace-bounds-0002", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-bounds-0002",
	}, nil, trajectory.EvidenceLimits{MaxEvents: 8, MaxParents: 1, MaxPayloadBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	first := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	second := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0002", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventExecutionAttempt, ActorID: "actor-host-0001", ParentEventIDs: []string{first.EventID, second.EventID},
		Payload: trajectory.ExecutionAttemptPayload{RunID: "run-bounds-0002", AttemptID: "attempt-bounds-0002", Status: "started"},
	}); err == nil {
		t.Fatal("parent bound not enforced")
	}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-host-0001",
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('a'), BriefSHA256: testDigest('b'), Availability: trajectory.Available},
	}); err == nil {
		t.Fatal("payload bound not enforced")
	}
}
