package trajectory_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestAppendOnlyLogReconstructsExactlyWhatModelSaw(t *testing.T) {
	root := t.TempDir()
	store, err := labstore.Open(filepath.Join(root, "objects"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log, err := trajectory.Create(filepath.Join(root, "trajectory.jsonl"), store, trajectory.SessionHeader{
		SessionID: "session-0000000000000001", SourceCommit: "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	system := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventContext, Source: trajectory.SourceSystem, TurnID: "turn-0000000000000001", ModelVisible: true, Body: []byte("You are a careful coding agent."), BodyKind: labstore.KindPrompt})
	user := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventUserMessage, Source: trajectory.SourceUser, TurnID: "turn-0000000000000001", ModelVisible: true, Body: []byte("Inspect the workspace."), BodyKind: labstore.KindPrompt})
	request := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, TurnID: "turn-0000000000000001", StepID: "step-0000000000000001", ContextEventIDs: []string{system.EventID, user.EventID}, Provider: "fixture", Model: "scripted-model"})
	reasoning := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventAssistantReasoning, Source: trajectory.SourceModel, TurnID: request.TurnID, StepID: request.StepID, ParentEventID: request.EventID, Body: []byte("I should list the workspace first."), BodyKind: labstore.KindProviderBody})
	call := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventToolCall, Source: trajectory.SourceModel, TurnID: request.TurnID, StepID: request.StepID, ParentEventID: reasoning.EventID, ToolCallID: "call-0000000000000001", ToolName: "workspace.list", Body: []byte(`{"path":"."}`), BodyKind: labstore.KindToolPayload})
	appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventRuntime, Source: trajectory.SourceRuntime, TurnID: request.TurnID, StepID: request.StepID, ParentEventID: call.EventID, ToolCallID: call.ToolCallID, RunID: "run-0000000000000001", LogicalRequestID: "logical-0000000000000001", PhysicalExecutionID: "physical-0000000000000001", SpanID: "span-0000000000000001", Body: []byte(`{"status":"closed"}`), BodyKind: labstore.KindMetadataEvent})
	result := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventToolResult, Source: trajectory.SourceTool, TurnID: request.TurnID, StepID: request.StepID, ParentEventID: call.EventID, ToolCallID: call.ToolCallID, ToolName: call.ToolName, ModelVisible: true, Body: []byte(`{"files":["README.md"]}`), BodyKind: labstore.KindToolPayload})
	request2 := appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, TurnID: request.TurnID, StepID: "step-0000000000000002", ContextEventIDs: []string{system.EventID, user.EventID, call.EventID, result.EventID}, Provider: "fixture", Model: "scripted-model"})
	appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventAssistantOutput, Source: trajectory.SourceModel, TurnID: request.TurnID, StepID: request2.StepID, ParentEventID: request2.EventID, Body: []byte("README.md is present."), BodyKind: labstore.KindProviderBody})

	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := trajectory.Open(filepath.Join(root, "trajectory.jsonl"), store)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.ModelContext(request2.EventID)
	if err != nil {
		t.Fatal(err)
	}
	want := []trajectory.ContextItem{{EventID: system.EventID, Source: trajectory.SourceSystem, Body: "You are a careful coding agent."}, {EventID: user.EventID, Source: trajectory.SourceUser, Body: "Inspect the workspace."}, {EventID: call.EventID, Source: trajectory.SourceModel, Body: `{"path":"."}`}, {EventID: result.EventID, Source: trajectory.SourceTool, Body: `{"files":["README.md"]}`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("context\n got=%#v\nwant=%#v", got, want)
	}
	export, err := reopened.ExportPrivate()
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Events) != 9 || export.Events[3].BodyText != "I should list the workspace first." || export.Events[5].PhysicalExecutionID == "" {
		t.Fatalf("incomplete export: %+v", export.Events)
	}
	if export.SealSHA256 == "" {
		t.Fatal("materialized export was not sealed")
	}
	tampered := export
	tampered.Events = append([]trajectory.ExportEvent(nil), export.Events...)
	tampered.Events[3].BodyText = "tampered"
	if err := trajectory.ValidateExport(tampered); err == nil {
		t.Fatal("tampered materialized body accepted")
	}
}

func TestOpenRejectsTamperedHashChain(t *testing.T) {
	root := t.TempDir()
	store, err := labstore.Open(filepath.Join(root, "objects"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(root, "trajectory.jsonl")
	log, err := trajectory.Create(path, store, trajectory.SessionHeader{SessionID: "session-0000000000000001", SourceCommit: "0123456789abcdef0123456789abcdef01234567"})
	if err != nil {
		t.Fatal(err)
	}
	appendEvent(t, log, trajectory.EventInput{Type: trajectory.EventUserMessage, Source: trajectory.SourceUser, TurnID: "turn-0000000000000001", ModelVisible: true, Body: []byte("original"), BodyKind: labstore.KindPrompt})
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("sha256:"), []byte("sha256:f"), 1)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := trajectory.Open(path, store); err == nil {
		t.Fatal("tampered log accepted")
	}
}

func TestRuntimeAndToolResultsRequireKnownToolCall(t *testing.T) {
	root := t.TempDir()
	store, err := labstore.Open(filepath.Join(root, "objects"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	log, err := trajectory.Create(filepath.Join(root, "trajectory.jsonl"), store, trajectory.SessionHeader{SessionID: "session-0000000000000001", SourceCommit: "0123456789abcdef0123456789abcdef01234567"})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := log.Append(trajectory.EventInput{Type: trajectory.EventToolResult, Source: trajectory.SourceTool, TurnID: "turn-0000000000000001", StepID: "step-0000000000000001", ToolCallID: "call-0000000000000001", ToolName: "workspace.list", Body: []byte("result"), BodyKind: labstore.KindToolPayload}); err == nil {
		t.Fatal("orphan tool result accepted")
	}
}

func appendEvent(t *testing.T, log *trajectory.Log, input trajectory.EventInput) trajectory.Event {
	t.Helper()
	event, err := log.Append(input)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
