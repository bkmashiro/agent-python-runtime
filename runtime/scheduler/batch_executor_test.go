package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type gatedBatchRunner struct {
	mu      sync.Mutex
	active  int
	maximum int
	started chan string
	release chan struct{}
}

func (runner *gatedBatchRunner) Run(ctx context.Context, request []byte, _ string) ([]byte, error) {
	attemptID, _ := engine.AttemptIdentityFromContext(ctx)
	runner.mu.Lock()
	runner.active++
	if runner.active > runner.maximum {
		runner.maximum = runner.active
	}
	runner.mu.Unlock()
	runner.started <- attemptID
	select {
	case <-runner.release:
	case <-ctx.Done():
		runner.mu.Lock()
		runner.active--
		runner.mu.Unlock()
		return nil, ctx.Err()
	}
	runner.mu.Lock()
	runner.active--
	runner.mu.Unlock()
	return append([]byte("done:"), request...), nil
}

func (*gatedBatchRunner) Close(context.Context) error { return nil }
func (*gatedBatchRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "batch-fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance}
}

type retryBatchRunner struct {
	started chan string
	release chan struct{}
}

func (runner *retryBatchRunner) Run(ctx context.Context, request []byte, _ string) ([]byte, error) {
	attemptID, _ := engine.AttemptIdentityFromContext(ctx)
	runner.started <- attemptID
	if attemptID == "task:retry:attempt:1" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	select {
	case <-runner.release:
		return append([]byte("retried:"), request...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*retryBatchRunner) Close(context.Context) error { return nil }
func (*retryBatchRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "retry-fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance}
}

func TestProfiledBatchExecutorRetriesEvictedAttemptThroughControlLoop(t *testing.T) {
	scheduler, err := New(Config{
		TargetBytes: 60, HighBytes: 80, CriticalBytes: 90, HardBytes: 100,
		MaxTasks: 4, MaxAttempts: 8, RetryMarginBytes: 5, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	profileOptions := profileConfig()
	profileOptions.HardBytes = 100
	profileOptions.UnknownReservationBytes = 40
	profileOptions.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	greedOptions := greedConfig()
	greedOptions.MinimumAttempts = 1
	controller, err := NewGreedController(greedOptions)
	if err != nil {
		t.Fatal(err)
	}
	runner := &retryBatchRunner{started: make(chan string, 2), release: make(chan struct{})}
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	readerValues := make(chan LiveMemorySnapshot, 4)
	readerValues <- controlSnapshot(1, 91)
	readerValues <- controlSnapshot(2, 50)
	readerValues <- controlSnapshot(3, 50)
	readerValues <- controlSnapshot(4, 50)
	reader := liveMemoryReaderFunc(func() (LiveMemorySnapshot, error) { return <-readerValues, nil })
	dispatcher, err := NewCoordinatedVictimDispatcher(CoordinatedVictimDispatcherConfig{
		Scheduler: scheduler, Canceler: worker,
		Observer: reclaimObserverFunc(func(context.Context, Termination) (ReclaimReport, error) {
			return ReclaimReport{ExecutorTerminated: true, ObservedFootprintBytes: 45, ReclaimedBytes: 45}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewLiveMemoryControlLoop(LiveMemoryControlLoopConfig{
		Scheduler: scheduler, Profiles: store, Controller: controller, Reader: reader, Dispatcher: dispatcher,
		Interval: time.Millisecond, MaxSamples: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProfiledBatchExecutor(ProfiledBatchExecutorConfig{
		Scheduler: scheduler, Profiles: store, Worker: worker, ControlLoop: control,
		PollInterval: time.Millisecond, MaxPayloadBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	type outcome struct {
		results []ProfiledExecutionResult
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		results, runErr := executor.Run(context.Background(), []ProfiledExecution{{
			Spec: ProfiledTaskSpec{TaskID: "task:retry", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}, Request: []byte("payload"),
		}})
		done <- outcome{results: results, err: runErr}
	}()
	if first := <-runner.started; first != "task:retry:attempt:1" {
		t.Fatalf("first=%s", first)
	}
	select {
	case second := <-runner.started:
		if second != "task:retry:attempt:2" {
			t.Fatalf("second=%s", second)
		}
	case early := <-done:
		t.Fatalf("executor returned before retry: err=%v results=%#v snapshot=%#v", early.err, early.results, scheduler.Snapshot())
	}
	close(runner.release)
	result := <-done
	if result.err != nil || len(result.results) != 1 || result.results[0].AttemptID != "task:retry:attempt:2" || string(result.results[0].Response) != "retried:payload" {
		t.Fatalf("result=%#v", result)
	}
	if snapshot := scheduler.Snapshot(); snapshot.ReservedBytes != 0 || len(snapshot.Queued) != 0 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestProfiledBatchExecutorRunsBoundedConcurrentTasks(t *testing.T) {
	scheduler, err := New(Config{
		TargetBytes: 40, HighBytes: 80, CriticalBytes: 90, HardBytes: 100,
		MaxTasks: 8, MaxAttempts: 16, RetryMarginBytes: 5, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	profileOptions := profileConfig()
	profileOptions.HardBytes = 100
	profileOptions.UnknownReservationBytes = 30
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	runner := &gatedBatchRunner{started: make(chan string, 3), release: make(chan struct{}, 3)}
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 2, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewProfiledBatchExecutor(ProfiledBatchExecutorConfig{
		Scheduler: scheduler, Profiles: store, Worker: worker, PollInterval: time.Millisecond, MaxPayloadBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	batch := []ProfiledExecution{
		{Spec: ProfiledTaskSpec{TaskID: "task:1", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}, Request: []byte("one")},
		{Spec: ProfiledTaskSpec{TaskID: "task:2", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}, Request: []byte("two")},
		{Spec: ProfiledTaskSpec{TaskID: "task:3", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}, Request: []byte("three")},
	}
	type outcome struct {
		results []ProfiledExecutionResult
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		results, runErr := executor.Run(context.Background(), batch)
		done <- outcome{results: results, err: runErr}
	}()
	<-runner.started
	<-runner.started
	select {
	case third := <-runner.started:
		t.Fatalf("third task started above worker capacity: %s", third)
	default:
	}
	runner.release <- struct{}{}
	<-runner.started
	runner.release <- struct{}{}
	runner.release <- struct{}{}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if runner.maximum != 2 || len(result.results) != 3 {
		t.Fatalf("maximum=%d results=%#v", runner.maximum, result.results)
	}
	for index, want := range []string{"done:one", "done:two", "done:three"} {
		if result.results[index].TaskID != batch[index].Spec.TaskID || string(result.results[index].Response) != want || result.results[index].Err != nil {
			t.Fatalf("result[%d]=%#v", index, result.results[index])
		}
	}
	if snapshot := scheduler.Snapshot(); snapshot.ReservedBytes != 0 || len(snapshot.Queued) != 0 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
