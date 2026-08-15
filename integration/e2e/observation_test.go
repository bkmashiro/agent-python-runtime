package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestObservationCoversNoBrokerLifecycle(t *testing.T) {
	runner := newEngine(t)
	recorder := &observationRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "observation-exec-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "agent-run-1", InvocationID: "invocation-1", InvocationAttempt: 1, ExecutionID: "observation-exec-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = enginecontract.WithObservationSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"observation-no-broker","code":"result = {'value': 42}","inputs":{}}`)
	payload, err := runner.Run(ctx, request, "")
	if err != nil {
		t.Fatal(err)
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
	if err != nil {
		t.Fatal(err)
	}
	if response.ExecutionRef == nil || response.ExecutionRef.ExecutionID != "observation-exec-1" {
		t.Fatalf("missing Host execution reference: %+v", response.ExecutionRef)
	}
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].Type != "execution.started" || events[0].Sequence != 1 || events[0].ParentSequence != nil {
		t.Fatalf("start=%+v", events[0])
	}
	if events[1].Type != "execution.completed" || events[1].Sequence != 2 || events[1].ParentSequence == nil || *events[1].ParentSequence != 1 {
		t.Fatalf("completed=%+v", events[1])
	}
	var started map[string]json.RawMessage
	if err := json.Unmarshal(events[0].Payload, &started); err != nil {
		t.Fatal(err)
	}
	if _, ok := started["executed_code_sha256"]; !ok {
		t.Fatalf("started payload=%s", events[0].Payload)
	}
}

func TestRealGuestObservationCorrelatesCapabilityCall(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	spec, grant, err := capability.DemoCatalogDefinition(policy)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"items":[{"id":"a","score":1,"title":"Alpha"}]}`)
	entry := capability.TranscriptEntry{
		OperationIndex: 0, Capability: "sources.demo_catalog", Arguments: json.RawMessage(`{}`), ArgumentsSHA256: playback.SHA256([]byte(`{}`)),
		Result: result, ResultSHA256: playback.SHA256(result),
		Evidence: capability.TransportEvidence{Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: playback.SHA256(result)},
	}
	executionID := "observation-capability-exec"
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{
			RunIdentity: executionID, Plan: plan, Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{entry}},
		})
	}}
	runner, err := factory.New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	recorder := &observationRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, executionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "agent-capability", InvocationID: "invocation-capability", InvocationAttempt: 1, ExecutionID: executionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = enginecontract.WithObservationSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"observation-capability","code":"items=sources.demo_catalog()\nresult=items[0]['title']","inputs":{}}`)
	if _, err := runner.Run(ctx, request, plan.PythonPrelude()); err != nil {
		t.Fatal(err)
	}
	events := recorder.snapshot()
	if len(events) != 5 || events[1].Type != observe.EventCapabilityPlan || events[2].Type != observe.EventCapabilityIntent || events[3].Type != observe.EventCapabilityCall ||
		events[1].ParentSequence == nil || *events[1].ParentSequence != 1 || events[2].ParentSequence == nil || *events[2].ParentSequence != 2 ||
		events[3].ParentSequence == nil || *events[3].ParentSequence != 3 || events[4].ParentSequence == nil || *events[4].ParentSequence != 4 {
		t.Fatalf("events=%+v", events)
	}
	var bound observe.CapabilityPlanBoundPayload
	if err := json.Unmarshal(events[1].Payload, &bound); err != nil || bound.CapabilityPlanSHA256 != plan.Identity() {
		t.Fatalf("bound=%+v err=%v payload=%s", bound, err, events[1].Payload)
	}
	var call observe.CapabilityCallPayload
	if err := json.Unmarshal(events[3].Payload, &call); err != nil || call.CapabilityPlanSHA256 != plan.Identity() || call.ResultSHA256 != entry.ResultSHA256 {
		t.Fatalf("call=%+v err=%v payload=%s", call, err, events[3].Payload)
	}
}

func TestRealGuestObservationBindsBrokerPlanWithZeroCalls(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	spec, grant, err := capability.DemoCatalogDefinition(policy)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	executionID := "observation-zero-call-plan"
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: executionID, Plan: plan})
	}}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	recorder := &observationRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, executionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "agent-zero-call", InvocationID: "invocation-zero-call", InvocationAttempt: 1, ExecutionID: executionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = enginecontract.WithObservationSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, []byte(`{"run_id":"observation-zero-call","code":"result=42","inputs":{}}`), plan.PythonPrelude()); err != nil {
		t.Fatal(err)
	}
	events := recorder.snapshot()
	if len(events) != 3 || events[0].Type != observe.EventExecutionStarted || events[1].Type != observe.EventCapabilityPlan || events[2].Type != observe.EventExecutionCompleted {
		t.Fatalf("events=%+v", events)
	}
	var bound observe.CapabilityPlanBoundPayload
	if err := json.Unmarshal(events[1].Payload, &bound); err != nil || bound.CapabilityPlanSHA256 != plan.Identity() {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
}

func TestRealGuestObservationReportsInitialFinalWorkspaceFileDelta(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	executionID := "observation-workspace-exec"
	runner, err := (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: executionID}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = runner.Close(context.Background())
		_ = manager.Close()
	}()
	recorder := &observationRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, executionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "agent-workspace", InvocationID: "invocation-workspace", InvocationAttempt: 1, ExecutionID: executionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = enginecontract.WithObservationSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"observation-workspace","code":"with open('/workspace/report.txt','w',encoding='utf-8') as handle:\n    handle.write('bounded report')\nresult=True","inputs":{}}`)
	if _, err := runner.Run(ctx, request, ""); err != nil {
		t.Fatal(err)
	}
	events := recorder.snapshot()
	if len(events) != 3 || events[1].Type != observe.EventWorkspaceFinalized || events[2].Type != observe.EventExecutionCompleted {
		t.Fatalf("events=%+v", events)
	}
	var finalized observe.WorkspaceFinalizedPayload
	if err := json.Unmarshal(events[1].Payload, &finalized); err != nil || finalized.SyscallOrderAvailable || finalized.ChangesTruncated || len(finalized.Changes) != 1 ||
		finalized.Changes[0].Kind != "added" || finalized.Changes[0].Path != "report.txt" || finalized.Changes[0].AfterSHA256 == "" {
		t.Fatalf("finalized=%+v err=%v payload=%s", finalized, err, events[1].Payload)
	}
}

func TestRealGuestObservationTakesWorkspaceInitialSnapshotPerRun(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "observation-multi-owner"}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = runner.Close(context.Background())
		_ = manager.Close()
	}()
	runObserved := func(executionID, runID, code string) observe.WorkspaceFinalizedPayload {
		recorder := &observationRecorder{}
		session, sessionErr := observe.NewSession(observe.Required, recorder, executionID)
		if sessionErr != nil {
			t.Fatal(sessionErr)
		}
		ctx, contextErr := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
			AgentRunID: "agent-workspace-multi", InvocationID: "invocation-" + executionID,
			InvocationAttempt: 1, ExecutionID: executionID,
		})
		if contextErr != nil {
			t.Fatal(contextErr)
		}
		ctx, contextErr = enginecontract.WithObservationSession(ctx, session)
		if contextErr != nil {
			t.Fatal(contextErr)
		}
		request, marshalErr := json.Marshal(map[string]any{"run_id": runID, "code": code, "inputs": map[string]any{}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, runErr := runner.Run(ctx, request, ""); runErr != nil {
			t.Fatal(runErr)
		}
		events := recorder.snapshot()
		if len(events) != 3 || events[1].Type != observe.EventWorkspaceFinalized {
			t.Fatalf("events=%+v", events)
		}
		var payload observe.WorkspaceFinalizedPayload
		if decodeErr := json.Unmarshal(events[1].Payload, &payload); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return payload
	}
	first := runObserved("observation-multi-1", "workspace-first", "with open('/workspace/state.txt','w',encoding='utf-8') as handle:\n    handle.write('one')\nresult=True")
	second := runObserved("observation-multi-2", "workspace-second", "with open('/workspace/state.txt','w',encoding='utf-8') as handle:\n    handle.write('two')\nwith open('/workspace/second.txt','w',encoding='utf-8') as handle:\n    handle.write('new')\nresult=True")
	if second.InitialWorkspaceSHA256 != first.FinalWorkspaceSHA256 || second.FinalWorkspaceSHA256 == first.FinalWorkspaceSHA256 || len(second.Changes) != 2 ||
		second.Changes[0].Path != "second.txt" || second.Changes[0].Kind != "added" || second.Changes[1].Path != "state.txt" || second.Changes[1].Kind != "modified" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestRealGuestObservationRecorderModesAndExecutionIdentity(t *testing.T) {
	request := []byte(`{"run_id":"observation-mode","code":"result = 42","inputs":{}}`)
	ref := runtimeconfig.InvocationRef{
		AgentRunID: "agent-run-modes", InvocationID: "invocation-modes", InvocationAttempt: 1, ExecutionID: "observation-exec-modes",
	}
	for _, testCase := range []struct {
		name         string
		mode         observe.Mode
		sessionID    string
		wantError    bool
		wantComplete bool
	}{
		{name: "required", mode: observe.Required, sessionID: ref.ExecutionID, wantError: true},
		{name: "best-effort", mode: observe.BestEffort, sessionID: ref.ExecutionID, wantComplete: true},
		{name: "identity-mismatch", mode: observe.Required, sessionID: "different-execution", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := newEngine(t)
			recorder := &observationRecorder{failNext: testCase.name != "identity-mismatch"}
			session, err := observe.NewSession(testCase.mode, recorder, testCase.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := enginecontract.WithInvocationRef(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err = enginecontract.WithObservationSession(ctx, session)
			if err != nil {
				t.Fatal(err)
			}
			payload, runErr := runner.Run(ctx, request, "")
			if testCase.wantError {
				if runErr == nil || len(payload) != 0 {
					t.Fatalf("payload=%s err=%v", payload, runErr)
				}
				return
			}
			if runErr != nil || !testCase.wantComplete || len(payload) == 0 || !session.Incomplete() {
				t.Fatalf("payload=%s err=%v incomplete=%v", payload, runErr, session.Incomplete())
			}
		})
	}
}

type observationRecorder struct {
	mu       sync.Mutex
	events   []observe.Event
	failNext bool
}

func (recorder *observationRecorder) Append(_ context.Context, event observe.Event) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.failNext {
		recorder.failNext = false
		return errors.New("observation append failed")
	}
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *observationRecorder) snapshot() []observe.Event {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]observe.Event(nil), recorder.events...)
}
