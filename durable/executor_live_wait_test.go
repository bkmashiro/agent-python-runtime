package durable

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutorExternalIOYieldRunsAnotherResident(t *testing.T) {
	firstWaiting := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	controller := fakeAttemptController{
		advance: func(ctx context.Context, id string) (AdvanceResult, error) {
			if id == "second" {
				close(secondStarted)
				return AdvanceResult{State: AttemptCompleted}, nil
			}
			if err := yieldExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			close(firstWaiting)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return AdvanceResult{}, ctx.Err()
			}
			if err := reacquireExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{State: AttemptCompleted}, nil
		},
		cancel: func(context.Context, string) error { return nil },
	}
	executor, err := NewExecutor(controller, Limits{MaxRunning: 1, MaxResident: 2, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, err := executor.Admit(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstWaiting:
	case <-time.After(2 * time.Second):
		t.Fatal("first attempt did not yield")
	}
	second, err := executor.Admit(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second attempt did not reuse running capacity")
	}
	if _, err = second.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	if _, err = first.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorResidentLimitKeepsNewAttemptQueued(t *testing.T) {
	waiting := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	controller := fakeAttemptController{
		advance: func(ctx context.Context, id string) (AdvanceResult, error) {
			if id == "second" {
				close(secondStarted)
				return AdvanceResult{State: AttemptCompleted}, nil
			}
			if err := yieldExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			close(waiting)
			<-release
			if err := reacquireExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{State: AttemptCompleted}, nil
		},
		cancel: func(context.Context, string) error { return nil },
	}
	executor, err := NewExecutor(controller, Limits{MaxRunning: 1, MaxResident: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := executor.Admit(context.Background(), "first")
	<-waiting
	second, err := executor.Admit(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
		t.Fatal("resident limit admitted a second Guest")
	case <-time.After(30 * time.Millisecond):
	}
	stats := executor.Stats()
	if stats.Resident != 1 || stats.WaitingLive != 1 || stats.Queued != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	close(release)
	if _, err = first.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = second.Result(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorContinuationPrecedesNewAdmission(t *testing.T) {
	aWaiting := make(chan struct{})
	releaseA := make(chan struct{})
	bRunning := make(chan struct{})
	releaseB := make(chan struct{})
	var mu sync.Mutex
	var order []string
	controller := fakeAttemptController{
		advance: func(ctx context.Context, id string) (AdvanceResult, error) {
			switch id {
			case "a":
				if err := yieldExecution(ctx); err != nil {
					return AdvanceResult{}, err
				}
				close(aWaiting)
				<-releaseA
				if err := reacquireExecution(ctx); err != nil {
					return AdvanceResult{}, err
				}
			case "b":
				close(bRunning)
				<-releaseB
			}
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return AdvanceResult{State: AttemptCompleted}, nil
		},
		cancel: func(context.Context, string) error { return nil },
	}
	executor, err := NewExecutor(controller, Limits{MaxRunning: 1, MaxResident: 2, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := executor.Admit(context.Background(), "a")
	<-aWaiting
	b, _ := executor.Admit(context.Background(), "b")
	<-bRunning
	close(releaseA)
	deadline := time.Now().Add(2 * time.Second)
	for executor.Stats().Ready != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if executor.Stats().Ready != 1 {
		t.Fatal("continuation did not become ready")
	}
	c, err := executor.Admit(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	close(releaseB)
	for _, attempt := range []*Attempt{a, b, c} {
		if _, err = attempt.Result(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "b" || order[1] != "a" || order[2] != "c" {
		t.Fatalf("completion order = %v", order)
	}
}

func TestExecutorExternalIOLimitBoundsHostCallbacks(t *testing.T) {
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	controller := fakeAttemptController{
		advance: func(ctx context.Context, id string) (AdvanceResult, error) {
			if err := yieldExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			switch id {
			case "first":
				close(firstEntered)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return AdvanceResult{}, ctx.Err()
				}
			case "second":
				close(secondEntered)
			}
			if err := reacquireExecution(ctx); err != nil {
				return AdvanceResult{}, err
			}
			return AdvanceResult{State: AttemptCompleted}, nil
		},
		cancel: func(context.Context, string) error { return nil },
	}
	executor, err := NewExecutor(controller, Limits{
		MaxRunning:       2,
		MaxResident:      2,
		MaxInflightTools: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := executor.Admit(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	<-firstEntered
	second, err := executor.Admit(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
		t.Fatal("second Host callback exceeded MaxInflightTools")
	case <-time.After(30 * time.Millisecond):
	}
	stats := executor.Stats()
	if stats.InflightTools != 1 || stats.WaitingLive != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second Host callback did not receive released capacity")
	}
	for _, attempt := range []*Attempt{first, second} {
		if _, err = attempt.Result(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRealGuestExternalIOReusesRunningSlot(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	var calls atomic.Int32
	runner, store := realExecutorRunner(t, []Tool{{
		Name: "remote_read", Version: "v1", Recovery: RetrySafe, Scheduling: ExternalIO,
		Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			calls.Add(1)
			close(entered)
			select {
			case <-release:
				return 7, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}})
	defer store.Close()
	defer runner.Close(context.Background())
	createExecutorRun(t, runner, "waiting", "result = remote_read()")
	createExecutorRun(t, runner, "short", "result = 2")
	executor, err := NewExecutor(runner, Limits{MaxRunning: 1, MaxResident: 2, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := executor.Admit(context.Background(), "waiting")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	short, err := executor.Admit(context.Background(), "short")
	if err != nil {
		t.Fatal(err)
	}
	completed, err := short.Result(context.Background())
	if err != nil || string(completed.Output.Value) != "2" {
		t.Fatalf("short result=%s err=%v", completed.Output.Value, err)
	}
	close(release)
	completed, err = waiting.Result(context.Background())
	if err != nil || string(completed.Output.Value) != "7" || calls.Load() != 1 {
		t.Fatalf("waiting result=%s calls=%d err=%v", completed.Output.Value, calls.Load(), err)
	}
	if err = executor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
