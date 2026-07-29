package scheduler

import (
	"context"
	"testing"
	"time"
)

type attemptCancelerFunc func(context.Context, string) (Termination, error)

func (function attemptCancelerFunc) Cancel(ctx context.Context, attemptID string) (Termination, error) {
	return function(ctx, attemptID)
}

func TestCoordinatedVictimDispatcherRequiresTerminationAndReclaimEvidence(t *testing.T) {
	now := time.Unix(600, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	attempt := submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	victims, err := scheduler.RequestEvictions(100)
	if err != nil {
		t.Fatal(err)
	}
	var canceled string
	dispatcher, err := NewCoordinatedVictimDispatcher(CoordinatedVictimDispatcherConfig{
		Scheduler: scheduler,
		Canceler: attemptCancelerFunc(func(_ context.Context, attemptID string) (Termination, error) {
			canceled = attemptID
			return Termination{AttemptID: attemptID, ExecutorTerminated: true}, nil
		}),
		Observer: reclaimObserverFunc(func(_ context.Context, termination Termination) (ReclaimReport, error) {
			return ReclaimReport{ExecutorTerminated: termination.ExecutorTerminated, ObservedFootprintBytes: 50, ReclaimedBytes: 50}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(context.Background(), victims); err != nil {
		t.Fatal(err)
	}
	snapshot := scheduler.Snapshot()
	if canceled != attempt.AttemptID || snapshot.ReservedBytes != 0 || snapshot.Attempts[0].State != AttemptReclaimed || len(snapshot.Queued) != 1 || snapshot.Queued[0].ReservationFloor != 55 {
		t.Fatalf("canceled=%q snapshot=%#v", canceled, snapshot)
	}
}
