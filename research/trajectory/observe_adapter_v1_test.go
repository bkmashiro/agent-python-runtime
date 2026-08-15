package trajectory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func TestObservationRecorderProducesPrivateRawAndMinimalProductionEvidence(t *testing.T) {
	root := t.TempDir()
	store, err := labstore.Open(filepath.Join(root, "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log, err := trajectory.CreateEvidenceLog(filepath.Join(root, "trace.jsonl"), store, trajectory.TraceHeader{
		TraceID: "trace-observe-adapter-0001", RootExecutionID: "execution-observe-adapter-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567",
	}, trajectory.EvidenceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	recorder, err := trajectory.NewObservationRecorder(log, trajectory.ObservationRecorderConfig{
		Profile: trajectory.ProfileExperimentFull, ActorID: "actor-host-runtime-0001", RunID: "run-observe-adapter-0001", AttemptID: "attempt-observe-adapter-0001",
		PolicySHA256: evidenceDigest('1'), FreshnessSHA256: evidenceDigest('2'), GrantsSHA256: evidenceDigest('3'),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := observe.NewSession(observe.Required, recorder, "execution-observe-adapter-0001")
	if err != nil {
		t.Fatal(err)
	}
	appendObservationEvent(t, session, observe.EventExecutionStarted, nil, observe.ExecutionStartedPayload{
		ArtifactSHA256: evidenceDigest('4'), ExecutedCodeSHA256: evidenceDigest('6'), ExecutionProfileSHA256: evidenceDigest('7'),
	})
	parent := uint32(1)
	appendObservationEvent(t, session, observe.EventCapabilityPlan, &parent, observe.CapabilityPlanBoundPayload{
		CapabilityPlanSHA256: evidenceDigest('5'),
	})
	parent = 2
	lifecycle := observe.CapabilityCallLifecyclePayload{
		ArgumentsSHA256: evidenceDigest('8'), CallID: "call-observe-adapter-0001", Capability: "workspace.read_text",
		CapabilityPlanSHA256: evidenceDigest('5'), OperationIndex: 0, Phase: "intent",
	}
	appendObservationEvent(t, session, observe.EventCapabilityIntent, &parent, lifecycle)
	parent = 3
	lifecycle.Phase = "started"
	appendObservationEvent(t, session, observe.EventCapabilityStarted, &parent, lifecycle)
	parent = 4
	appendObservationEvent(t, session, observe.EventCapabilityCall, &parent, observe.CapabilityCallPayload{
		ArgumentsSHA256: evidenceDigest('8'), CallID: "call-observe-adapter-0001", Capability: "workspace.read_text", CapabilityPlanSHA256: evidenceDigest('5'),
		OperationIndex: 0, Outcome: "ok", ParentCallID: "parent-call-observe-0001", ReceiptID: "rcpt_observe_adapter_0001", ResultSHA256: evidenceDigest('9'),
		Source: &observe.SourceBindingPayload{
			SchemaVersion: "pysolate.source-binding.v0", ClaimLevel: "source_bound",
			DocumentID: evidenceDigest('e'), SourceSHA256: evidenceDigest('f'), OccurrenceID: evidenceDigest('0'),
			Capability: "workspace.read_text", DynamicOccurrence: 1, StartLine: 1, StartColumn: 2, EndLine: 1, EndColumn: 20,
		},
	})
	parent = 5
	appendObservationEvent(t, session, observe.EventWorkspaceFinalized, &parent, observe.WorkspaceFinalizedPayload{
		Changes: []observe.WorkspaceChange{}, EntryCount: 1, FinalTreeSHA256: evidenceDigest('a'), FinalWorkspaceSHA256: evidenceDigest('b'),
		InitialWorkspaceSHA256: evidenceDigest('c'), TotalBytes: 1,
	})
	parent = 6
	appendObservationEvent(t, session, observe.EventExecutionCompleted, &parent, observe.ExecutionCompletedPayload{
		EvidenceComplete: true, ResultSHA256: evidenceDigest('d'), Status: "ok",
	})

	full, err := log.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	production, err := log.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil {
		t.Fatal(err)
	}
	if countEvidenceType(full.Events, trajectory.EventRuntimeObservation) != 7 ||
		countEvidenceType(production.Events, trajectory.EventAuthoritySnapshot) != 1 ||
		countEvidenceType(full.Events, trajectory.EventSourceDecision) != 1 ||
		countEvidenceType(full.Events, trajectory.EventToolDecision) != 1 ||
		countEvidenceType(full.Events, trajectory.EventExecutedLine) != 1 ||
		countEvidenceType(full.Events, trajectory.EventResourceSample) != 1 {
		t.Fatalf("full runtime=%d source=%d line=%d resource=%d",
			countEvidenceType(full.Events, trajectory.EventRuntimeObservation), countEvidenceType(full.Events, trajectory.EventSourceDecision),
			countEvidenceType(full.Events, trajectory.EventExecutedLine), countEvidenceType(full.Events, trajectory.EventResourceSample))
	}
	for _, event := range full.Events {
		if event.Type == trajectory.EventRuntimeObservation && event.Body == nil {
			t.Fatal("private runtime observation has no raw body ref")
		}
	}
	if countEvidenceType(production.Events, trajectory.EventRuntimeObservation) != 0 || countEvidenceType(production.Events, trajectory.EventEffectTransition) != 3 ||
		countEvidenceType(production.Events, trajectory.EventWorkspaceTerminal) != 1 || countEvidenceType(production.Events, trajectory.EventTraceEnded) != 1 {
		t.Fatalf("production events=%+v", production.Events)
	}
	for _, event := range production.Events {
		if event.Body != nil || !productionKindForTest(event.Type) {
			t.Fatalf("production leaked event=%+v", event)
		}
	}
}

func productionKindForTest(kind trajectory.EvidenceType) bool {
	switch kind {
	case trajectory.EventTraceStarted, trajectory.EventTraceEnded, trajectory.EventAuthoritySnapshot,
		trajectory.EventEffectTransition, trajectory.EventWorkspaceTerminal, trajectory.EventExecutionAttempt,
		trajectory.EventEvidenceTruncated:
		return true
	default:
		return false
	}
}

func TestProductionRecorderDefaultsToPhysicalMinimalCaptureAndPreservesPerCallChains(t *testing.T) {
	recorder, log, session := newObservationTestRecorder(t, "")
	parent := uint32(1)
	appendObservationEvent(t, session, observe.EventExecutionStarted, nil, observe.ExecutionStartedPayload{ArtifactSHA256: evidenceDigest('a'), ExecutedCodeSHA256: evidenceDigest('b'), ExecutionProfileSHA256: evidenceDigest('c')})
	appendObservationEvent(t, session, observe.EventCapabilityPlan, &parent, observe.CapabilityPlanBoundPayload{CapabilityPlanSHA256: evidenceDigest('d')})
	seq := uint32(2)
	for _, item := range []struct{ kind, call, phase string }{{observe.EventCapabilityIntent, "call-production-a", "intent"}, {observe.EventCapabilityIntent, "call-production-b", "intent"}, {observe.EventCapabilityStarted, "call-production-a", "started"}, {observe.EventCapabilityStarted, "call-production-b", "started"}} {
		appendObservationEvent(t, session, item.kind, &seq, observe.CapabilityCallLifecyclePayload{ArgumentsSHA256: evidenceDigest('e'), CallID: item.call, Capability: "workspace.read_text", CapabilityPlanSHA256: evidenceDigest('d'), OperationIndex: 0, Phase: item.phase})
		seq++
	}
	for _, call := range []string{"call-production-a", "call-production-b"} {
		appendObservationEvent(t, session, observe.EventCapabilityCall, &seq, observe.CapabilityCallPayload{ArgumentsSHA256: evidenceDigest('e'), CallID: call, Capability: "workspace.read_text", CapabilityPlanSHA256: evidenceDigest('d'), OperationIndex: 0, Outcome: "ok", ReceiptID: "rcpt_" + call, ResultSHA256: evidenceDigest('f')})
		seq++
	}
	appendObservationEvent(t, session, observe.EventExecutionCompleted, &seq, observe.ExecutionCompletedPayload{EvidenceComplete: true, ResultSHA256: evidenceDigest('9'), Status: "ok"})
	_ = recorder
	captured, err := log.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range captured.Events {
		if event.Type == trajectory.EventRuntimeObservation || event.Type == trajectory.EventToolDecision || event.Type == trajectory.EventResourceSample {
			t.Fatalf("production recorder physically retained experiment telemetry: %s", event.Type)
		}
	}
	if countEvidenceType(captured.Events, trajectory.EventEffectTransition) != 6 {
		t.Fatalf("events=%+v", captured.Events)
	}
}

func TestFailedExecutionReconcilesStartedEffectsAndCannotClaimComplete(t *testing.T) {
	_, log, session := newObservationTestRecorder(t, trajectory.ProfileProductionRollback)
	appendObservationEvent(t, session, observe.EventExecutionStarted, nil, observe.ExecutionStartedPayload{ArtifactSHA256: evidenceDigest('a'), ExecutedCodeSHA256: evidenceDigest('b'), ExecutionProfileSHA256: evidenceDigest('c')})
	parent := uint32(1)
	appendObservationEvent(t, session, observe.EventCapabilityPlan, &parent, observe.CapabilityPlanBoundPayload{CapabilityPlanSHA256: evidenceDigest('d')})
	parent = 2
	appendObservationEvent(t, session, observe.EventCapabilityIntent, &parent, observe.CapabilityCallLifecyclePayload{ArgumentsSHA256: evidenceDigest('e'), CallID: "call-failed-run", Capability: "workspace.read_text", CapabilityPlanSHA256: evidenceDigest('d'), OperationIndex: 0, Phase: "intent"})
	parent = 3
	appendObservationEvent(t, session, observe.EventCapabilityStarted, &parent, observe.CapabilityCallLifecyclePayload{ArgumentsSHA256: evidenceDigest('e'), CallID: "call-failed-run", Capability: "workspace.read_text", CapabilityPlanSHA256: evidenceDigest('d'), OperationIndex: 0, Phase: "started"})
	parent = 4
	appendObservationEvent(t, session, observe.EventExecutionFailed, &parent, observe.ExecutionFailedPayload{ErrorClass: "guest_trap", EvidenceComplete: false, Status: "error"})
	captured, err := log.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil {
		t.Fatal(err)
	}
	var states []trajectory.EffectState
	for _, event := range captured.Events {
		if event.Type == trajectory.EventEffectTransition {
			var payload trajectory.EffectTransitionPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				t.Fatal("decode effect")
			}
			states = append(states, payload.State)
		}
	}
	if len(states) != 3 || states[0] != trajectory.EffectIntent || states[1] != trajectory.EffectStarted || states[2] != trajectory.EffectReconciliationRequired {
		t.Fatalf("states=%v", states)
	}
	var ended trajectory.TraceEndedPayload
	if json.Unmarshal(captured.Events[len(captured.Events)-1].Payload, &ended) != nil || ended.EvidenceComplete {
		t.Fatalf("terminal=%+v", ended)
	}
}

func newObservationTestRecorder(t *testing.T, profile trajectory.Profile) (*trajectory.ObservationRecorder, *trajectory.EvidenceLog, *observe.Session) {
	t.Helper()
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	log, err := trajectory.CreateEvidenceLog(filepath.Join(t.TempDir(), "trace.jsonl"), store, trajectory.TraceHeader{TraceID: "trace-production-recorder-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-production-recorder-0001"}, trajectory.EvidenceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close(); _ = store.Close() })
	recorder, err := trajectory.NewObservationRecorder(log, trajectory.ObservationRecorderConfig{Profile: profile, ActorID: "actor-production-recorder-0001", RunID: "run-production-recorder-0001", AttemptID: "attempt-production-recorder-0001", PolicySHA256: evidenceDigest('1'), FreshnessSHA256: evidenceDigest('2'), GrantsSHA256: evidenceDigest('3')})
	if err != nil {
		t.Fatal(err)
	}
	session, err := observe.NewSession(observe.Required, recorder, "execution-production-recorder-0001")
	if err != nil {
		t.Fatal(err)
	}
	return recorder, log, session
}

func appendObservationEvent(t *testing.T, session *observe.Session, kind string, parent *uint32, payload any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(context.Background(), kind, parent, encoded); err != nil {
		t.Fatal(err)
	}
}

func countEvidenceType(events []trajectory.EvidenceEvent, kind trajectory.EvidenceType) int {
	count := 0
	for _, event := range events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func evidenceDigest(char byte) string {
	return "sha256:" + string(make([]byte, 0)) + repeatEvidenceByte(char, 64)
}

func repeatEvidenceByte(char byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = char
	}
	return string(result)
}
