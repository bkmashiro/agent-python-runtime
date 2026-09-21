package durable

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type fakeAttemptController struct {
	advance func(context.Context, string) (AdvanceResult, error)
	cancel  func(context.Context, string) error
}

func (controller fakeAttemptController) Advance(ctx context.Context, runID string) (AdvanceResult, error) {
	return controller.advance(ctx, runID)
}

func (controller fakeAttemptController) Cancel(ctx context.Context, runID string) error {
	return controller.cancel(ctx, runID)
}

func fakeExecutor(t *testing.T, limits Limits, resume func(context.Context, string) (pysolate.Output, error)) *Executor {
	t.Helper()
	controller := fakeAttemptController{
		advance: func(ctx context.Context, runID string) (AdvanceResult, error) {
			out, err := resume(ctx, runID)
			return AdvanceResult{State: AttemptCompleted, Output: out}, err
		},
		cancel: func(context.Context, string) error { return nil },
	}
	e, err := NewExecutor(controller, limits)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExecutorAdmitExposesParkWithoutControlFlowError(t *testing.T) {
	controller := fakeAttemptController{
		advance: func(context.Context, string) (AdvanceResult, error) {
			return AdvanceResult{
				State: AttemptParked,
				Park:  &Park{Kind: ParkWait, RunID: "run", WaitID: "run/wait/0", Sequence: 0, Reason: "waiting for decision"},
			}, nil
		},
		cancel: func(context.Context, string) error { return nil },
	}
	executor, err := NewExecutor(controller, Limits{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := executor.Admit(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	result, err := attempt.Result(context.Background())
	if err != nil || result.State != AttemptParked || result.Park == nil || result.Park.WaitID != "run/wait/0" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err = attempt.Wait(context.Background()); !errors.Is(err, ErrParked) {
		t.Fatalf("legacy wait error=%v", err)
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func waitStarted(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("started %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func TestExecutorBoundsFIFOAndBusy(t *testing.T) {
	started := make(chan string, 3)
	release := map[string]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{}), "c": make(chan struct{})}
	e := fakeExecutor(t, Limits{MaxActive: 1, MaxQueued: 2}, func(ctx context.Context, id string) (pysolate.Output, error) {
		started <- id
		select {
		case <-release[id]:
			return pysolate.Output{Value: json.RawMessage(id)}, nil
		case <-ctx.Done():
			return pysolate.Output{}, ctx.Err()
		}
	})
	first, err := e.Submit(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started, "a")
	if _, err = e.Submit(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Submit(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Submit(context.Background(), "a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("active duplicate error = %v", err)
	}
	if _, err = e.Submit(context.Background(), "b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("queued duplicate error = %v", err)
	}
	if _, err = e.Submit(context.Background(), "d"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue error = %v", err)
	}
	close(release["a"])
	got, err := first.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := string(got.Value)
	got.Value[0] = 'x'
	replay, err := first.Wait(context.Background())
	if err != nil || string(replay.Value) != want {
		t.Fatalf("repeated wait = %s, %v", replay.Value, err)
	}
	waitStarted(t, started, "b")
	close(release["b"])
	waitStarted(t, started, "c")
	close(release["c"])
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorQueuedCancellationReleasesCapacity(t *testing.T) {
	started := make(chan string, 2)
	release := map[string]chan struct{}{"active": make(chan struct{}), "next": make(chan struct{})}
	e := fakeExecutor(t, Limits{MaxActive: 1, MaxQueued: 1}, func(ctx context.Context, id string) (pysolate.Output, error) {
		started <- id
		select {
		case <-release[id]:
			return pysolate.Output{}, nil
		case <-ctx.Done():
			return pysolate.Output{}, ctx.Err()
		}
	})
	active, err := e.Submit(context.Background(), "active")
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started, "active")
	queuedCtx, cancel := context.WithCancel(context.Background())
	queued, err := e.Submit(queuedCtx, "queued")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = queued.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v", err)
	}
	next, err := e.Submit(context.Background(), "next")
	if err != nil {
		t.Fatal(err)
	}
	close(release["active"])
	if _, err = active.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, started, "next")
	close(release["next"])
	if _, err = next.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorCancelPersistsBeforeActiveLocalCancel(t *testing.T) {
	persisted := make(chan string, 1)
	started := make(chan struct{})
	e := fakeExecutor(t, Limits{MaxActive: 1}, func(ctx context.Context, _ string) (pysolate.Output, error) {
		close(started)
		<-ctx.Done()
		return pysolate.Output{}, ctx.Err()
	})
	controller := e.controller.(fakeAttemptController)
	controller.cancel = func(context.Context, string) error {
		persisted <- "persisted"
		return nil
	}
	e.controller = controller
	a, err := e.Submit(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = e.Cancel(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-persisted:
		if got != "persisted" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not persist")
	}
	if _, err = a.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("active cancellation = %v", err)
	}
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorCloseTimeoutThenDrain(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	e := fakeExecutor(t, Limits{MaxActive: 1}, func(_ context.Context, _ string) (pysolate.Output, error) {
		close(started)
		<-release
		return pysolate.Output{}, nil
	})
	a, err := e.Submit(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = e.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first close = %v", err)
	}
	if _, err = e.Submit(context.Background(), "new"); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close = %v", err)
	}
	close(release)
	if _, err = a.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func realExecutorRunner(t *testing.T, tools []Tool) (*Runner, *Store) {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = "../dist/pysolate.wasm"
	}
	artifact, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(context.Background(), store, artifact, "executor-test-v1", tools)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return runner, store
}

func createExecutorRun(t *testing.T, runner *Runner, id, code string) {
	t.Helper()
	_, err := runner.Create(context.Background(), Definition{
		ID: id, Code: code, Seed: "seed", Inputs: json.RawMessage(`{}`),
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "executor-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRealGuestHostBlockingKeepsSlotAndFIFO(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	runner, store := realExecutorRunner(t, []Tool{{Name: "block", Version: "v1", Recovery: RetrySafe, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return 7, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}})
	defer store.Close()
	defer runner.Close(context.Background())
	createExecutorRun(t, runner, "one", "result = block()")
	createExecutorRun(t, runner, "two", "result = block()")
	createExecutorRun(t, runner, "three", "result = block()")
	e, err := NewExecutor(runner, Limits{MaxActive: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	one, err := e.Submit(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	two, err := e.Submit(context.Background(), "two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Submit(context.Background(), "three"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third submit = %v", err)
	}
	select {
	case <-entered:
		t.Fatal("queued Guest entered while Host call was blocked")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if _, err = one.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = two.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("host calls = %d, want 2", calls.Load())
	}
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRealGuestParkDecideResumesHistory(t *testing.T) {
	var reads atomic.Int32
	runner, store := realExecutorRunner(t, []Tool{
		{Name: "read", Version: "v1", Recovery: RetrySafe, Call: func(context.Context, json.RawMessage) (any, error) {
			reads.Add(1)
			return 7, nil
		}},
		{Name: "approve", Version: "v1", Recovery: WaitMode, Wait: func(_ context.Context, args json.RawMessage) (WaitSpec, error) {
			return WaitSpec{Kind: "approval", Request: args}, nil
		}},
	})
	defer store.Close()
	defer runner.Close(context.Background())
	createExecutorRun(t, runner, "approval", "x = read()\napprove(value=x)\nresult = x")
	e, err := NewExecutor(runner, Limits{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.Admit(context.Background(), "approval")
	if err != nil {
		t.Fatal(err)
	}
	parked, err := first.Result(context.Background())
	if err != nil || parked.State != AttemptParked || parked.Park == nil || parked.Park.Kind != ParkWait {
		t.Fatalf("first result=%+v err=%v", parked, err)
	}
	if reads.Load() != 1 {
		t.Fatalf("reads before decision = %d", reads.Load())
	}
	if err = runner.Decide(context.Background(), parked.Park.WaitID, Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	second, err := e.Admit(context.Background(), parked.Park.RunID)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := second.Result(context.Background())
	if err != nil || completed.State != AttemptCompleted || string(completed.Output.Value) != "7" {
		t.Fatalf("resumed result=%+v err=%v", completed, err)
	}
	if reads.Load() != 1 {
		t.Fatalf("history replay called read %d times", reads.Load())
	}
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRealGuestActiveAndQueuedCancel(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	runner, store := realExecutorRunner(t, []Tool{{Name: "block", Version: "v1", Recovery: RetrySafe, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return 1, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}})
	defer store.Close()
	defer runner.Close(context.Background())
	createExecutorRun(t, runner, "active-cancel", "result = block()")
	createExecutorRun(t, runner, "queued-cancel", "result = 2")
	e, err := NewExecutor(runner, Limits{MaxActive: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.Submit(context.Background(), "active-cancel")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	queued, err := e.Submit(context.Background(), "queued-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Cancel(context.Background(), "queued-cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = queued.Wait(context.Background()); !errors.Is(err, ErrCancelled) {
		t.Fatalf("queued cancel = %v", err)
	}
	if err = e.Cancel(context.Background(), "active-cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err = active.Wait(context.Background()); !errors.Is(err, ErrCancelled) {
		t.Fatalf("active cancel = %v", err)
	}
	close(release)
	if err = e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
