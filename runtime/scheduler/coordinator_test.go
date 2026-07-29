package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reclaimObserverFunc func(context.Context, Termination) (ReclaimReport, error)

func (function reclaimObserverFunc) Observe(ctx context.Context, termination Termination) (ReclaimReport, error) {
	return function(ctx, termination)
}

func TestEvictAttemptDoesNotReleaseCreditWhenReclaimEvidenceFails(t *testing.T) {
	now := time.Unix(40, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	attempt := submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	if _, err := scheduler.RequestEvictions(100); err != nil {
		t.Fatal(err)
	}
	runner := newBlockingRunner()
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: attempt.AttemptID, Request: []byte("request"), TrustedPrepare: "prepare"}); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	observerErr := errors.New("memory sample unavailable")
	_, err = scheduler.EvictAttempt(context.Background(), worker, reclaimObserverFunc(func(context.Context, Termination) (ReclaimReport, error) {
		return ReclaimReport{}, observerErr
	}), attempt.AttemptID)
	if !errors.Is(err, observerErr) {
		t.Fatalf("EvictAttempt() error = %v", err)
	}
	if scheduler.Snapshot().ReservedBytes != 40 {
		t.Fatalf("credit released without evidence: %#v", scheduler.Snapshot())
	}
}

func TestEvictAttemptRequeuesOnlyAfterTerminationAndReclaimEvidence(t *testing.T) {
	now := time.Unix(50, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	attempt := submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	if _, err := scheduler.RequestEvictions(100); err != nil {
		t.Fatal(err)
	}
	runner := newBlockingRunner()
	worker, err := NewInProcessWorker(runner, WorkerConfig{MaxActive: 1, MaxRequestBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(context.Background(), ExecutionRequest{AttemptID: attempt.AttemptID, Request: []byte("request"), TrustedPrepare: "prepare"}); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	var observedTermination Termination
	task, err := scheduler.EvictAttempt(context.Background(), worker, reclaimObserverFunc(func(_ context.Context, termination Termination) (ReclaimReport, error) {
		observedTermination = termination
		return ReclaimReport{ExecutorTerminated: termination.ExecutorTerminated, ObservedFootprintBytes: 50, ReclaimedBytes: 50}, nil
	}), attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !observedTermination.ExecutorTerminated || task.State != TaskWaitingRetry || task.ReservationFloor != 55 || scheduler.Snapshot().ReservedBytes != 0 {
		t.Fatalf("termination = %#v task = %#v snapshot = %#v", observedTermination, task, scheduler.Snapshot())
	}
}
