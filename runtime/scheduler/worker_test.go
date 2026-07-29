package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type blockingRunner struct {
	mu         sync.Mutex
	started    chan string
	requests   map[string][]byte
	attemptIDs map[string]string
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{started: make(chan string, 8), requests: make(map[string][]byte), attemptIDs: make(map[string]string)}
}

func (runner *blockingRunner) Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	runner.mu.Lock()
	runner.requests[trustedPrepare] = append([]byte(nil), request...)
	if attemptID, ok := engine.AttemptIdentityFromContext(ctx); ok {
		runner.attemptIDs[trustedPrepare] = attemptID
	}
	runner.mu.Unlock()
	runner.started <- trustedPrepare
	<-ctx.Done()
	return nil, ctx.Err()
}
func (runner *blockingRunner) Close(context.Context) error { return nil }
func (runner *blockingRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance}
}

type immediateRunner struct{}

func (immediateRunner) Run(_ context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	return append(append([]byte(nil), request...), trustedPrepare...), nil
}
func (immediateRunner) Close(context.Context) error { return nil }
func (immediateRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance}
}

type successfulOnCancelRunner struct{ started chan struct{} }

func (runner *successfulOnCancelRunner) Run(ctx context.Context, _ []byte, _ string) ([]byte, error) {
	close(runner.started)
	<-ctx.Done()
	return []byte("finished"), nil
}
func (*successfulOnCancelRunner) Close(context.Context) error { return nil }
func (*successfulOnCancelRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance}
}

func TestInProcessWorkerTreatsSuccessfulReturnAfterCancelAsCompletionWon(t *testing.T) {
	runner := &successfulOnCancelRunner{started: make(chan struct{})}
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:success-race", Request: []byte("request")}); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	termination, err := worker.Cancel(context.Background(), "attempt:success-race")
	if err != nil {
		t.Fatal(err)
	}
	if !termination.CompletionWon || string(termination.Result.Response) != "finished" || termination.Result.Err != nil {
		t.Fatalf("termination=%#v", termination)
	}
}

func TestInProcessWorkerCancellationReportsCompletedRace(t *testing.T) {
	runner := immediateRunner{}
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:done", Request: []byte("request")})
	if err != nil {
		t.Fatal(err)
	}
	<-handle.Done()
	termination, err := worker.Cancel(context.Background(), "attempt:done")
	if err != nil {
		t.Fatal(err)
	}
	if !termination.ExecutorTerminated || !termination.CompletionWon || termination.AttemptID != "attempt:done" {
		t.Fatalf("termination=%#v", termination)
	}
}

func TestInProcessWorkerCancelsOneAttemptWithoutConsumingItsResult(t *testing.T) {
	runner := newBlockingRunner()
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 2, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	request := []byte("request")
	handle, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:1", Request: request, TrustedPrepare: "prepare"})
	if err != nil {
		t.Fatal(err)
	}
	request[0] = 'X'
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	termination, err := worker.Cancel(context.Background(), "attempt:1")
	if err != nil {
		t.Fatal(err)
	}
	if !termination.ExecutorTerminated || !errors.Is(termination.Result.Err, context.Canceled) {
		t.Fatalf("termination = %#v", termination)
	}
	<-handle.Done()
	result, ok := handle.Result()
	if !ok || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
	runner.mu.Lock()
	got := string(runner.requests["prepare"])
	gotAttemptID := runner.attemptIDs["prepare"]
	runner.mu.Unlock()
	if got != "request" || gotAttemptID != "attempt:1" {
		t.Fatalf("runner request = %q attempt_id = %q", got, gotAttemptID)
	}
	if snapshot := worker.Snapshot(); snapshot.Active != 0 || snapshot.Closed {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInProcessWorkerBoundsActiveAttemptsAndRejectsChangedDuplicate(t *testing.T) {
	runner := newBlockingRunner()
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{AttemptID: "attempt:1", Request: []byte("one"), TrustedPrepare: "prepare"}
	first, err := worker.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	replayed, err := worker.Start(context.Background(), request)
	if err != nil || replayed != first {
		t.Fatalf("replay handle = %p first = %p err = %v", replayed, first, err)
	}
	changed := request
	changed.Request = []byte("two")
	if _, err := worker.Start(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed duplicate error = %v", err)
	}
	if _, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:2", Request: []byte("two")}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("active bound error = %v", err)
	}
	if _, err := worker.Cancel(context.Background(), request.AttemptID); err != nil {
		t.Fatal(err)
	}
}

func TestInProcessWorkerReturnsCopiedResultAndClosesFailClosed(t *testing.T) {
	worker, err := NewInProcessWorker(immediateRunner{}, WorkerConfig{MaxActive: 1, MaxRequestBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:1", Request: []byte("ok"), TrustedPrepare: "!"})
	if err != nil {
		t.Fatal(err)
	}
	<-handle.Done()
	first, ok := handle.Result()
	if !ok || string(first.Response) != "ok!" || first.Err != nil {
		t.Fatalf("first result = %#v ok=%v", first, ok)
	}
	first.Response[0] = 'X'
	second, ok := handle.Result()
	if !ok || string(second.Response) != "ok!" {
		t.Fatalf("result was not copy-isolated: %#v", second)
	}
	if err := worker.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !worker.Snapshot().Closed {
		t.Fatal("worker did not close")
	}
	if _, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: "attempt:2", Request: []byte("no")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("start after close error = %v", err)
	}
}

var _ engine.Runner = (*blockingRunner)(nil)
var _ engine.Runner = immediateRunner{}
