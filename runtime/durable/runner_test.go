package durable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "durable.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = os.Remove(path) })
	return store
}

func runnerTestDefinition(artifact string) Definition {
	return Definition{ID: "run-test", Code: "result = {}", Seed: "seed", ArtifactSHA256: artifact, EnvironmentVersion: "env-v1", Inputs: json.RawMessage(`{}`)}
}

func testTool(mode RecoveryMode, handler capability.Handler) Tool {
	grant, _ := capability.NewGrant(json.RawMessage(`{"scope":"test"}`))
	return Tool{
		Spec: capability.Spec{
			Name: "tools.echo", Version: "v1", EffectClass: capability.EffectPure,
			Playback: capability.PlaybackLiveOnly, HandlerIdentity: "test-handler",
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		}, Grant: grant, Handler: handler, Recovery: mode,
	}
}

func testJournalRunner(t *testing.T, tool Tool) (*Runner, string) {
	t.Helper()
	artifact := []byte("fake-wasm-artifact")
	digest := sha256.Sum256(artifact)
	identity := runtimeconfig.VerifiedArtifactIdentity{ArtifactSHA256: "sha256:" + hexDigest(digest[:])}
	runner := &Runner{store: testStore(t), artifact: artifact, artifactSHA256: identity.ArtifactSHA256, artifactIdentity: identity, environmentVersion: "env-v1", tools: map[string]Tool{tool.Spec.Name: tool}}
	definition := runnerTestDefinition(identity.ArtifactSHA256)
	if err := runner.store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	return runner, definition.ID
}

func hexDigest(value []byte) string {
	const hex = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, byteValue := range value {
		result[index*2] = hex[byteValue>>4]
		result[index*2+1] = hex[byteValue&15]
	}
	return string(result)
}

func testIdentity(artifact []byte) runtimeconfig.VerifiedArtifactIdentity {
	digest := sha256.Sum256(artifact)
	return runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: "sha256:" + hexDigest(digest[:]),
		ManifestSHA256: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		ImportRoots:    []string{"json"}, QualifiedImportRoots: []string{"json"},
	}
}

func newTestRunner(t *testing.T, store *Store, tools ...Tool) *Runner {
	t.Helper()
	for index := range tools {
		if tools[index].Spec.Description == "" {
			tools[index].Spec.Description = "durable test tool"
		}
	}
	artifact := []byte("runner-test-artifact")
	runner, err := NewRunner(store, artifact, testIdentity(artifact), "env-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestRunnerToolSnapshotIsStableAndCreateBindsIt(t *testing.T) {
	store := testStore(t)
	first := testTool(Manual, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	first.Spec.Name = "tools.zed"
	second := testTool(RetrySafe, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	second.Spec.Name = "tools.alpha"
	runner := newTestRunner(t, store, first, second)

	run, err := runner.Create(context.Background(), runnerTestDefinition(runner.artifactSHA256))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(run.Definition.Tools, runner.toolSnapshot) {
		t.Fatalf("Create did not persist the runner tool snapshot: got %s want %s", run.Definition.Tools, runner.toolSnapshot)
	}
	var declarations []toolDeclaration
	if err := json.Unmarshal(run.Definition.Tools, &declarations); err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 2 || declarations[0].Spec.Name != "tools.alpha" || declarations[1].Spec.Name != "tools.zed" {
		t.Fatalf("declarations are not stably sorted: %+v", declarations)
	}
	if declarations[0].Recovery != RetrySafe || declarations[1].Recovery != Manual || declarations[0].Grant == "" || declarations[1].Grant == "" {
		t.Fatalf("declarations lost recovery or grant identity: %+v", declarations)
	}
	if _, err := runner.Create(context.Background(), Definition{
		ID: "different-tools", Code: "result = {}", ArtifactSHA256: runner.artifactSHA256,
		EnvironmentVersion: "env-v1", Inputs: json.RawMessage(`{}`), Tools: json.RawMessage(`[]`),
	}); !errors.Is(err, ErrInvalidRunner) {
		t.Fatalf("Create accepted a different tool snapshot: %v", err)
	}
}

func TestResumeBlocksDeclarationDriftBeforeDispatch(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(Tool) Tool
		terminal   bool
		baseMode   RecoveryMode
		baseLookup bool
	}{
		{name: "spec-pending", mutate: func(tool Tool) Tool { tool.Spec.Version = "v2"; return tool }},
		{name: "grant-pending", mutate: func(tool Tool) Tool {
			tool.Grant, _ = capability.NewGrant(json.RawMessage(`{"scope":"changed"}`))
			return tool
		}},
		{name: "spec-completed", mutate: func(tool Tool) Tool { tool.Spec.Version = "v2"; return tool }, terminal: true},
		{name: "grant-completed", mutate: func(tool Tool) Tool {
			tool.Grant, _ = capability.NewGrant(json.RawMessage(`{"scope":"changed"}`))
			return tool
		}, terminal: true},
		{name: "manual-to-retry-safe", mutate: func(tool Tool) Tool { tool.Recovery = RetrySafe; return tool }, baseMode: Manual},
		{name: "lookup-to-retry-safe", mutate: func(tool Tool) Tool { tool.Recovery = RetrySafe; tool.Resolve = nil; return tool }, baseMode: Lookup, baseLookup: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t)
			mode := test.baseMode
			if mode == "" {
				mode = RetrySafe
			}
			tool := testTool(mode, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{}`), nil
			}))
			tool.Spec.Name = "tools.drift"
			if test.baseLookup {
				tool.Resolve = func(context.Context, capability.LoggedCall) (LookupResult, error) {
					return LookupResult{State: LookupPending}, nil
				}
			}
			original := newTestRunner(t, store, tool)
			definition := runnerTestDefinition(original.artifactSHA256)
			if _, err := original.Create(context.Background(), definition); err != nil {
				t.Fatal(err)
			}
			if test.terminal {
				if err := store.SetState(context.Background(), definition.ID, StatusCompleted, json.RawMessage(`{"ok":true}`), ""); err != nil {
					t.Fatal(err)
				}
			}

			changed := test.mutate(tool)
			resumed := newTestRunner(t, store, changed)
			if _, err := resumed.Resume(context.Background(), definition.ID); !errors.Is(err, ErrBlocked) {
				t.Fatalf("Resume accepted declaration drift: %v", err)
			}
			got, err := store.Get(context.Background(), definition.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := StatusBlocked
			if test.terminal {
				wantStatus = StatusCompleted
			}
			if got.Status != wantStatus {
				t.Fatalf("drift changed history status to %q, want %q", got.Status, wantStatus)
			}
		})
	}
}

func TestResumeBlocksMissingToolSnapshot(t *testing.T) {
	store := testStore(t)
	tool := testTool(RetrySafe, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	tool.Spec.Name = "tools.missing_snapshot"
	runner := newTestRunner(t, store, tool)
	definition := runnerTestDefinition(runner.artifactSHA256)
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(context.Background(), definition.ID); !errors.Is(err, ErrBlocked) {
		t.Fatalf("Resume accepted a missing tool snapshot: %v", err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status != StatusBlocked {
		t.Fatalf("missing snapshot status=%q err=%v", got.Status, err)
	}
}

func TestJournalCommitsManualCallOnFirstDispatch(t *testing.T) {
	tool := testTool(Manual, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}))
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "manual-1", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	var dispatches atomic.Int32
	response, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		dispatches.Add(1)
		return json.RawMessage(`{"call_id":"manual-1","status":"error","error":{"code":"denied"}}`), nil
	})
	if err != nil || dispatches.Load() != 1 {
		t.Fatalf("manual response=%s err=%v dispatches=%d", response, err, dispatches.Load())
	}
	persisted, created, err := runner.store.BeginCall(context.Background(), runID, call)
	if err != nil || created || persisted.State != CallCompleted || string(persisted.Outcome) != string(response) {
		t.Fatalf("manual call=%+v err=%v", persisted, err)
	}
}

func TestJournalPersistsUnknownToolBrokerResponse(t *testing.T) {
	runner, runID := testJournalRunner(t, testTool(RetrySafe, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})))
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "unknown-1", Capability: "tools.unknown", Arguments: json.RawMessage(`{}`)}
	response, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		return json.RawMessage(`{"call_id":"unknown-1","status":"error","error":{"code":"unknown_tool"}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, created, err := runner.store.BeginCall(context.Background(), runID, call)
	if err != nil || created || persisted.State != CallCompleted || string(persisted.Outcome) != string(response) {
		t.Fatalf("unknown call=%+v err=%v", persisted, err)
	}
}

func TestJournalCommitsPendingBeforeDispatchAndReplaysExactResponse(t *testing.T) {
	var dispatches atomic.Int32
	var observedKey string
	tool := testTool(RetrySafe, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		observedKey, _ = OperationKey(ctx)
		return json.RawMessage(`{"value":1}`), nil
	}))
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "call-1", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	exact := json.RawMessage(`{"call_id":"call-1","status":"error","error":{"code":"handler_error","message":"saved"}}`)
	got, err := journal.Call(context.Background(), call, func(ctx context.Context) ([]byte, error) {
		dispatches.Add(1)
		if key := mustOperationKey(ctx); key != runID+"/0" {
			t.Fatalf("operation key=%q", key)
		}
		return exact, nil
	})
	if err != nil || string(got) != string(exact) {
		t.Fatalf("first response=%s err=%v", got, err)
	}
	got, err = journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		dispatches.Add(1)
		return nil, errors.New("replay dispatched")
	})
	if err != nil || string(got) != string(exact) || dispatches.Load() != 1 {
		t.Fatalf("replay response=%s err=%v dispatches=%d", got, err, dispatches.Load())
	}
	if observedKey != "" {
		t.Fatalf("direct journal test unexpectedly invoked handler with key %q", observedKey)
	}
}

func TestJournalWaitParksThenReplaysDecision(t *testing.T) {
	var declarations atomic.Int32
	tool := testTool(WaitMode, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"approved":true}`), nil
	}))
	tool.Wait = func(context.Context, capability.LoggedCall) (WaitSpec, error) {
		declarations.Add(1)
		return WaitSpec{Kind: "approval", Request: json.RawMessage(`{"prompt":"continue"}`)}, nil
	}
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "approve-1", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	_, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		return json.RawMessage(`{"unexpected":true}`), nil
	})
	var park *ParkError
	if !errors.As(err, &park) || park.WaitID == "" {
		t.Fatalf("wait error=%v", err)
	}
	wait, err := runner.store.GetWait(context.Background(), park.WaitID)
	if err != nil || wait.Decision != nil {
		t.Fatalf("wait=%+v err=%v", wait, err)
	}
	_, err = journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		t.Fatal("undecided wait dispatched")
		return nil, nil
	})
	if !errors.As(err, &park) || declarations.Load() != 1 {
		t.Fatalf("redeclared wait err=%v declarations=%d", err, declarations.Load())
	}
	if err := runner.Decide(context.Background(), park.WaitID, Decision{Result: json.RawMessage(`{"approved":true}`)}); err != nil {
		t.Fatal(err)
	}
	response, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		return json.RawMessage(`{"call_id":"approve-1","status":"ok","result":{"approved":true}}`), nil
	})
	if err != nil || string(response) != `{"call_id":"approve-1","status":"ok","result":{"approved":true}}` {
		t.Fatalf("decided response=%s err=%v", response, err)
	}
}

func TestJournalMismatchNeverDispatches(t *testing.T) {
	tool := testTool(RetrySafe, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "first", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	if _, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) { return []byte(`{}`), nil }); err != nil {
		t.Fatal(err)
	}
	call.CallID = "different"
	_, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		t.Fatal("mismatched call dispatched")
		return nil, nil
	})
	if !errors.Is(err, ErrHistoryMismatch) {
		t.Fatalf("mismatch error=%v", err)
	}
}

func TestLookupDoneReplaysFailureWithoutResult(t *testing.T) {
	tool := testTool(Lookup, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"unexpected":true}`), nil
	}))
	tool.Resolve = func(context.Context, capability.LoggedCall) (LookupResult, error) {
		return LookupResult{State: LookupDone, Error: "saved failure"}, nil
	}
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "lookup-1", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	if _, _, err := runner.store.BeginCall(context.Background(), runID, call); err != nil {
		t.Fatal(err)
	}
	response, err := journal.Call(context.Background(), call, func(ctx context.Context) ([]byte, error) {
		if replay, ok := ctx.Value(replayResultContextKey{}).(replayResult); !ok || replay.result != nil || replay.err == nil || replay.err.Error() != "saved failure" {
			t.Fatalf("replay=%#v ok=%v", replay, ok)
		}
		return json.RawMessage(`{"call_id":"lookup-1","status":"error","error":{"code":"handler_error","message":"saved failure"}}`), nil
	})
	if err != nil || len(response) == 0 {
		t.Fatalf("lookup response=%s err=%v", response, err)
	}
}

func TestPythonBusinessErrorIsTerminalPayload(t *testing.T) {
	payload := []byte(`{"status":"error","error":{"code":"python_exception","message":"failed"}}`)
	reason, ok := pythonBusinessError(payload)
	if !ok || reason != "python_exception: failed" {
		t.Fatalf("reason=%q ok=%v", reason, ok)
	}
	if _, ok := pythonBusinessError([]byte(`{"status":"ok"}`)); ok {
		t.Fatal("success payload classified as business error")
	}
}

func TestNewRunnerAllowsNilWaitHandler(t *testing.T) {
	artifact := []byte("fake-wasm-artifact")
	identity := testIdentity(artifact)
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour)
	toolSpec := testTool(WaitMode, nil).Spec
	toolSpec.Description = "wait test"
	toolSpec.EffectClass = capability.EffectExternalRead
	runner, err := NewRunner(testStore(t), artifact, identity, "env-v1", []Tool{{
		Spec: toolSpec, Grant: grant, Recovery: WaitMode,
		Wait: func(context.Context, capability.LoggedCall) (WaitSpec, error) {
			return WaitSpec{Kind: "approval", Deadline: &deadline}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestResumeLeavesGoInfrastructureErrorRetryable(t *testing.T) {
	artifact := []byte("not-wasm")
	runner, err := NewRunner(testStore(t), artifact, testIdentity(artifact), "env-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	definition := runnerTestDefinition(runner.artifactSHA256)
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(context.Background(), definition.ID); err == nil {
		t.Fatal("invalid Guest unexpectedly ran")
	}
	run, err := runner.store.Get(context.Background(), definition.ID)
	if err != nil || run.Status == StatusFailed {
		t.Fatalf("infrastructure error became terminal: run=%+v err=%v", run, err)
	}
}

func TestCancelPersistsBeforeCancellingAttempt(t *testing.T) {
	runner, runID := testJournalRunner(t, testTool(RetrySafe, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})))
	var observed string
	runner.active = map[string]context.CancelFunc{runID: func() {
		run, err := runner.store.Get(context.Background(), runID)
		if err != nil {
			t.Fatalf("observe cancelled run: %v", err)
		}
		observed = run.Status
	}}
	if err := runner.Cancel(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if observed != StatusCancelled {
		t.Fatalf("cancel callback observed %q", observed)
	}
}

func TestWaitDeadlineBecomesImmutableDecision(t *testing.T) {
	tool := testTool(WaitMode, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	tool.Wait = func(context.Context, capability.LoggedCall) (WaitSpec, error) {
		deadline := time.Now().Add(-time.Second)
		return WaitSpec{Kind: "deadline", Deadline: &deadline}, nil
	}
	runner, runID := testJournalRunner(t, tool)
	journal := &journal{runner: runner, runID: runID}
	call := capability.LoggedCall{Sequence: 0, CallID: "deadline-1", Capability: tool.Spec.Name, Arguments: json.RawMessage(`{}`)}
	response, err := journal.Call(context.Background(), call, func(context.Context) ([]byte, error) {
		return []byte(`{"call_id":"deadline-1","status":"error"}`), nil
	})
	if err != nil || len(response) == 0 {
		t.Fatalf("deadline response=%s err=%v", response, err)
	}
	wait, err := runner.store.GetWait(context.Background(), runID+"/wait/0")
	if err != nil || wait.Decision == nil || wait.Decision.Error != "wait expired" {
		t.Fatalf("expired wait=%+v err=%v", wait, err)
	}
}

func mustOperationKey(ctx context.Context) string {
	key, ok := OperationKey(ctx)
	if !ok {
		return ""
	}
	return key
}
