package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
)

func journalOnlyRunner(store *Store, tools ...Tool) *Runner {
	declared := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		declared[tool.Name] = tool
	}
	return &Runner{
		store:              store,
		artifactSHA256:     "artifact",
		environmentVersion: "env-1",
		tools:              declared,
		active:             make(map[string]context.CancelFunc),
	}
}

func testArtifact(t *testing.T) []byte {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = filepath.Join("..", "dist", "pysolate.wasm")
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("real pysolate artifact unavailable at %s: %v", path, err)
	}
	if len(artifact) == 0 {
		t.Skipf("real pysolate artifact is empty: %s", path)
	}
	return artifact
}

func realRunner(t *testing.T, store *Store, tools []Tool) *Runner {
	t.Helper()
	runner, err := NewRunner(context.Background(), store, testArtifact(t), "env-1", tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	return runner
}

func TestRunnerCompilesOnceAndFreezesDeclarations(t *testing.T) {
	store, _ := openTestStore(t)
	tools := []Tool{
		{Name: "zeta", Version: "v2", Recovery: Manual, Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }},
		{Name: "alpha", Version: "v1", Recovery: RetrySafe, Call: func(context.Context, json.RawMessage) (any, error) { return true, nil }},
	}
	runner := realRunner(t, store, tools)
	definition := Definition{
		ID: "declarations", Code: "result = 1", Seed: "seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	run, err := runner.Create(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	var declarations []toolDeclaration
	if err := json.Unmarshal(run.Definition.Tools, &declarations); err != nil {
		t.Fatal(err)
	}
	if len(declarations) != 2 || declarations[0].Name != "alpha" || declarations[1].Name != "zeta" {
		t.Fatalf("declarations=%+v", declarations)
	}
	tools[0].Version = "v3"
	if !bytes.Contains(run.Definition.Tools, []byte(`"version":"v2"`)) {
		t.Fatalf("declaration snapshot changed after caller mutation: %s", run.Definition.Tools)
	}
	if runner.core == nil || runner.ArtifactID() == "" {
		t.Fatal("runner did not retain one compiled core and artifact identity")
	}
}

func TestNewRunnerAllowsWaitWithoutCallAndJournalInterceptsIt(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, []Tool{{
		Name: "approval", Version: "v1", Recovery: WaitMode,
		Wait: func(context.Context, json.RawMessage) (WaitSpec, error) {
			return WaitSpec{Kind: "approval"}, nil
		},
	}})
	if runner.tools["approval"].Call == nil {
		t.Fatal("WaitMode nil Call was not replaced for the root manifest")
	}
	definition := Definition{
		ID: "wait-nil-call", Code: "result = approval({})", Seed: "seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		called = true
		return []byte(`{"value":true}`)
	})
	if !errors.Is(err, ErrParked) || called {
		t.Fatalf("wait err=%v core call=%v", err, called)
	}
}

func TestResumeUsesSharedCoreAndPersistsRootOutput(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, nil)
	definition := Definition{
		ID: "resume-output", Code: "result = 1", Seed: "seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	output, err := runner.Resume(context.Background(), definition.ID)
	if err != nil || string(output.Value) != "1" {
		t.Fatalf("output=%+v err=%v", output, err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status != StatusCompleted || !bytes.Contains(got.Outcome, []byte(`"Value":1`)) {
		t.Fatalf("persisted run=%#v err=%v", got, err)
	}
}

func TestCancelDuringResumeReturnsCancelledAfterCoreStops(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, []Tool{{
		Name: "approval", Version: "v1", Recovery: WaitMode,
		Wait: func(ctx context.Context, _ json.RawMessage) (WaitSpec, error) {
			<-ctx.Done()
			return WaitSpec{}, ctx.Err()
		},
	}})
	definition := Definition{
		ID: "cancel-resume", Code: "result = approval({})", Seed: "seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runner.Resume(context.Background(), definition.ID)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runner.mu.Lock()
		active := len(runner.active) != 0
		runner.mu.Unlock()
		if active {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("resume ended before cancellation: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("resume did not become active")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := runner.Cancel(context.Background(), definition.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("resume err=%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled resume did not stop")
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("cancelled run=%#v err=%v", got, err)
	}
}

func TestJournalLookupErrorOnlyAndCompletedReplay(t *testing.T) {
	store, _ := openTestStore(t)
	var observations []ToolObservation
	runner := journalOnlyRunner(store, Tool{
		Name: "lookup", Version: "v1", Recovery: Lookup,
		Observer: ToolObserverFunc(func(observation ToolObservation) {
			observations = append(observations, observation)
		}),
		Call: func(context.Context, json.RawMessage) (any, error) { return "live", nil },
		Lookup: func(context.Context, json.RawMessage) (LookupResult, error) {
			return LookupResult{State: LookupDone, Error: "already failed"}, nil
		},
	})
	definition := storeTestDefinition("lookup-error")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	call := json.RawMessage(`{}`)
	if _, created, err := store.BeginCall(context.Background(), definition.ID, LoggedCall{
		Sequence: 0, CallID: "call-0", Capability: "lookup", Arguments: call,
	}); err != nil || !created {
		t.Fatalf("begin lookup created=%v err=%v", created, err)
	}
	response, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "lookup", call, func(context.Context) []byte {
		return []byte(`{"value":"live"}`)
	})
	if err != nil || string(response) != `{"value":null,"error":"already failed"}` {
		t.Fatalf("response=%s err=%v", response, err)
	}
	replayed, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "lookup", call, func(context.Context) []byte {
		t.Fatal("completed lookup was dispatched")
		return nil
	})
	if err != nil || !bytes.Equal(replayed, response) {
		t.Fatalf("replayed=%s err=%v", replayed, err)
	}
	if len(observations) != 1 || observations[0].Operation != ToolLookup || observations[0].Outcome != ToolSucceeded {
		t.Fatalf("lookup observations=%+v", observations)
	}
}

func TestDiagnosticsJournalPhasesAndCancelledPersistence(t *testing.T) {
	store, _ := openTestStore(t)
	collector := perfdiag.NewCollector()
	ctx := perfdiag.WithCollector(context.Background(), collector)
	runner := journalOnlyRunner(store, Tool{
		Name: "echo", Version: "v1", Recovery: RetrySafe,
		Call: func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	})
	success := storeTestDefinition("diagnostic-success")
	if err := store.Create(ctx, success); err != nil {
		t.Fatal(err)
	}
	response, err := (&journal{runner: runner, runID: success.ID}).Call(ctx, "echo", json.RawMessage(`{"x":1}`), func(context.Context) []byte {
		return []byte(`{"value":"ok"}`)
	})
	if err != nil || string(response) != `{"value":"ok"}` {
		t.Fatalf("success response=%s err=%v", response, err)
	}

	waitRunner := journalOnlyRunner(store, Tool{
		Name: "approval", Version: "v1", Recovery: WaitMode,
		Wait: func(context.Context, json.RawMessage) (WaitSpec, error) { return WaitSpec{Kind: "approval"}, nil },
	})
	waiting := storeTestDefinition("diagnostic-wait")
	if err := store.Create(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	j := &journal{runner: waitRunner, runID: waiting.ID}
	if _, err := j.Call(ctx, "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("wait operation dispatched")
		return nil
	}); !errors.Is(err, ErrParked) {
		t.Fatalf("wait error=%v", err)
	}
	if err := store.ResolveWait(ctx, waiting.ID+"/wait/0", Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	j = &journal{runner: waitRunner, runID: waiting.ID}
	response, err = j.Call(ctx, "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("recovered wait operation dispatched")
		return nil
	})
	if err != nil || string(response) != `{"value":true}` {
		t.Fatalf("recovered response=%s err=%v", response, err)
	}

	cancelled := storeTestDefinition("diagnostic-cancelled")
	if err := store.Create(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	logged := LoggedCall{Sequence: 0, CallID: "call-0", Capability: "echo", Arguments: json.RawMessage(`{}`)}
	if _, created, err := store.BeginCall(context.Background(), cancelled.ID, logged); err != nil || !created {
		t.Fatalf("pending call created=%v err=%v", created, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if response, err := (&journal{runner: runner, runID: cancelled.ID}).complete(cancelCtx, logged, []byte(`{"value":"persisted"}`)); err != nil || string(response) != `{"value":"persisted"}` {
		t.Fatalf("cancelled completion=%s err=%v", response, err)
	}
	completed, created, err := store.BeginCall(context.Background(), cancelled.ID, logged)
	if err != nil || created || completed.State != CallCompleted || string(completed.Outcome) != `{"value":"persisted"}` {
		t.Fatalf("persisted call=%#v created=%v err=%v", completed, created, err)
	}

	counts := make(map[string]uint64)
	for _, entry := range collector.Snapshot() {
		counts[entry.Phase] = entry.Stats.Count
	}
	for phase, want := range map[string]uint64{
		"durable.journal.call":        3,
		"durable.journal.wait":        2,
		"durable.journal.complete":    3,
		"durable.store.complete_call": 3,
	} {
		if counts[phase] != want {
			t.Fatalf("phase %q count=%d, want %d", phase, counts[phase], want)
		}
	}
}
func TestDiagnosticsExpiredWaitPreservesCollector(t *testing.T) {
	store, _ := openTestStore(t)
	collector := perfdiag.NewCollector()
	ctx := perfdiag.WithCollector(context.Background(), collector)
	deadline := time.Now().Add(-time.Second)
	runner := journalOnlyRunner(store, Tool{
		Name: "approval", Version: "v1", Recovery: WaitMode,
		Wait: func(context.Context, json.RawMessage) (WaitSpec, error) {
			return WaitSpec{Kind: "approval", Deadline: &deadline}, nil
		},
	})
	definition := storeTestDefinition("diagnostic-expired")
	if err := store.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	response, err := (&journal{runner: runner, runID: definition.ID}).Call(ctx, "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("expired wait dispatched")
		return nil
	})
	if err != nil || string(response) != `{"value":null,"error":"wait expired"}` {
		t.Fatalf("response=%s err=%v", response, err)
	}
	counts := make(map[string]uint64)
	for _, entry := range collector.Snapshot() {
		counts[entry.Phase] = entry.Count
	}
	if counts["durable.store.resolve_wait"] != 1 || counts["durable.store.get_wait"] != 2 {
		t.Fatalf("missing expired-wait diagnostics: %v", counts)
	}
}

func TestJournalUnknownToolPersistsCoreDenial(t *testing.T) {
	store, _ := openTestStore(t)
	runner := journalOnlyRunner(store)
	definition := storeTestDefinition("unknown-tool")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	denial := []byte(`{"value":null,"error":"unknown tool"}`)
	response, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "missing", json.RawMessage(`{}`), func(context.Context) []byte {
		return denial
	})
	if err != nil || !bytes.Equal(response, denial) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	replayed, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "missing", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("completed unknown tool was dispatched")
		return nil
	})
	if err != nil || !bytes.Equal(replayed, denial) {
		t.Fatalf("replayed=%s err=%v", replayed, err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status == StatusBlocked {
		t.Fatalf("unknown tool blocked run=%#v err=%v", got, err)
	}
}

func TestWaitParkCanResumeAfterControlError(t *testing.T) {
	store, _ := openTestStore(t)
	runner := journalOnlyRunner(store, Tool{
		Name: "approval", Version: "v1", Recovery: WaitMode,
		Wait: func(context.Context, json.RawMessage) (WaitSpec, error) { return WaitSpec{Kind: "approval"}, nil },
	})
	definition := storeTestDefinition("wait-recover")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	_, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("wait dispatched to core")
		return nil
	})
	if !errors.Is(err, ErrParked) {
		t.Fatalf("first wait err=%v", err)
	}
	wait, err := store.GetWait(context.Background(), definition.ID+"/wait/0")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveWait(context.Background(), wait.ID, Decision{Result: json.RawMessage(`{"approved":true}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&journal{runner: runner, runID: definition.ID}).Call(context.Background(), "approval", json.RawMessage(`{}`), func(context.Context) []byte {
		t.Fatal("resolved wait dispatched to core")
		return nil
	}); err != nil {
		t.Fatalf("resolved wait err=%v", err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status == StatusBlocked {
		t.Fatalf("wait recovery blocked run=%#v err=%v", got, err)
	}
}

func TestCancelPersistsBeforeLateResult(t *testing.T) {
	store, _ := openTestStore(t)
	runner := journalOnlyRunner(store)
	definition := storeTestDefinition("cancel")
	if err := store.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BeginCall(context.Background(), definition.ID, LoggedCall{Sequence: 0, CallID: "call-0", Capability: "late", Arguments: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Cancel(context.Background(), definition.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteCall(context.Background(), definition.ID, 0, json.RawMessage(`{"late":true}`)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), definition.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("late result changed cancellation: run=%#v err=%v", got, err)
	}
}

func TestCloseBusyThenClosesCoreWithoutClosingStore(t *testing.T) {
	store, _ := openTestStore(t)
	runner := journalOnlyRunner(store)
	runner.active["run"] = func() {}
	if err := runner.Close(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy close err=%v", err)
	}
	delete(runner.active, "run")
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), storeTestDefinition("store-owned")); err != nil {
		t.Fatalf("runner closed caller-owned store: %v", err)
	}
}
