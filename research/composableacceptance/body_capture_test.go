package composableacceptance

import (
	"testing"
)

func TestBodyCaptureRoundTripAndRejectsDrift(t *testing.T) {
	events := []TraceEvent{{Sequence: 1, SpanID: "run", AgentID: "runtime", AgentRole: "host", Type: TraceEventTypeRunStart, Action: "run.start", Outcome: TraceEventOutcomeStarted, Count: 1}}
	traceSHA, err := TraceIdentity(events)
	if err != nil {
		t.Fatal(err)
	}
	capture := BodyCapture{
		SchemaVersion: BodyCaptureSchemaVersion, ScenarioID: "release-fixture", ScenarioSHA256: digest([]byte("scenario")), TraceSHA256: traceSHA,
		ProviderIO: ProviderIONotApplicable, WorkflowOutput: "release ready", WorkflowEventSequence: 35,
		SelectedChildID: "reviewer", SelectedRootSHA256: digest([]byte("selected-root")),
		AgentOutputs: []CapturedAgentOutput{{AgentID: "researcher", Path: "dependency-review.md", Disposition: "discarded_branch", Body: "dependency review", EventSequence: 14}, {AgentID: "reviewer", Path: "release-checklist.md", Disposition: "selected_branch", Body: "release checklist", EventSequence: 15}},
	}
	encoded, identity, err := EncodeBodyCapture(capture)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := DecodeBodyCapture(encoded)
	if err != nil || decodedIdentity != identity || decoded.WorkflowOutput != capture.WorkflowOutput {
		t.Fatalf("decoded=%+v identity=%s err=%v", decoded, decodedIdentity, err)
	}
	capture.AgentOutputs[0].Disposition = "unknown"
	if _, _, err := EncodeBodyCapture(capture); err == nil {
		t.Fatal("invalid disposition accepted")
	}
}
