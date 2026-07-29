package scheduler

import (
	"errors"
	"testing"
	"time"
)

func submitRunning(t *testing.T, scheduler *Scheduler, spec TaskSpec) AttemptSnapshot {
	t.Helper()
	if _, err := scheduler.Submit(spec); err != nil {
		t.Fatal(err)
	}
	attempt, err := scheduler.Admit()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = scheduler.Start(attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestObserveMemoryUsesHysteresisToGateAdmission(t *testing.T) {
	now := time.Unix(10, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Submit(TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 10, MaxEvictions: 1}); err != nil {
		t.Fatal(err)
	}
	if level := scheduler.ObserveMemory(91); level != PressureHigh {
		t.Fatalf("level = %q", level)
	}
	if _, err := scheduler.Admit(); !errors.Is(err, ErrNoAdmissibleTask) {
		t.Fatalf("Admit() under pressure error = %v", err)
	}
	if level := scheduler.ObserveMemory(85); level != PressureHigh || !scheduler.Snapshot().Pressured {
		t.Fatalf("hysteresis level = %q snapshot = %#v", level, scheduler.Snapshot())
	}
	if level := scheduler.ObserveMemory(80); level != PressureNormal || scheduler.Snapshot().Pressured {
		t.Fatalf("recovered level = %q snapshot = %#v", level, scheduler.Snapshot())
	}
	if _, err := scheduler.Admit(); err != nil {
		t.Fatal(err)
	}
}

func TestEvictionProtectsPinnedAttemptAndWaitsForVerifiedReclaim(t *testing.T) {
	now := time.Unix(20, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	older := submitRunning(t, scheduler, TaskSpec{TaskID: "older", ProfileKey: "small", Lane: LaneSpeculative, Priority: 5, ReservationBytes: 20, MaxEvictions: 2})
	now = now.Add(time.Second)
	newer := submitRunning(t, scheduler, TaskSpec{TaskID: "newer", ProfileKey: "large", Lane: LaneSpeculative, Priority: 5, ReservationBytes: 40, MaxEvictions: 1})
	now = now.Add(time.Second)
	pinned := submitRunning(t, scheduler, TaskSpec{TaskID: "pinned", ProfileKey: "effect", Lane: LaneSpeculative, Priority: 1, ReservationBytes: 25, MaxEvictions: 2})
	if _, err := scheduler.UpdateReclaimable(older.AttemptID, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.UpdateReclaimable(newer.AttemptID, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.UpdateReclaimable(pinned.AttemptID, 60); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Pin(pinned.AttemptID); err != nil {
		t.Fatal(err)
	}
	victims, err := scheduler.RequestEvictions(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 1 || victims[0].AttemptID != newer.AttemptID || victims[0].State != AttemptEvictionRequested {
		t.Fatalf("victims = %#v", victims)
	}
	if scheduler.Snapshot().ReservedBytes != 85 {
		t.Fatalf("reservation released before reclaim: %#v", scheduler.Snapshot())
	}
	if _, err := scheduler.ConfirmReclaimed(newer.AttemptID, ReclaimReport{ExecutorTerminated: false, ObservedFootprintBytes: 50, ReclaimedBytes: 50}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unverified reclaim error = %v", err)
	}
	requeued, err := scheduler.ConfirmReclaimed(newer.AttemptID, ReclaimReport{ExecutorTerminated: true, ObservedFootprintBytes: 50, ReclaimedBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	if requeued.State != TaskWaitingRetry || requeued.ReservationFloor != 55 || requeued.Evictions != 1 || requeued.Lane != LaneGuaranteed {
		t.Fatalf("requeued = %#v", requeued)
	}
	if scheduler.Snapshot().ReservedBytes != 45 {
		t.Fatalf("reservation after reclaim = %#v", scheduler.Snapshot())
	}
	if _, err := scheduler.Admit(); !errors.Is(err, ErrNoAdmissibleTask) {
		t.Fatalf("retry admitted while pressure/timeout active: %v", err)
	}
	if _, err := scheduler.Complete(older.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Complete(pinned.AttemptID); err != nil {
		t.Fatal(err)
	}
	scheduler.ObserveMemory(80)
	now = now.Add(time.Second)
	retry, err := scheduler.Admit()
	if err != nil {
		t.Fatal(err)
	}
	if retry.TaskID != "newer" || retry.Ordinal != 2 || retry.ReservedBytes != 55 || retry.Lane != LaneGuaranteed {
		t.Fatalf("retry = %#v", retry)
	}
}

func TestEvictionUsesYoungestTieBreakAndFailsAtomicallyWhenInsufficient(t *testing.T) {
	now := time.Unix(30, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	first := submitRunning(t, scheduler, TaskSpec{TaskID: "first", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 20, MaxEvictions: 2})
	now = now.Add(time.Second)
	second := submitRunning(t, scheduler, TaskSpec{TaskID: "second", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 20, MaxEvictions: 2})
	for _, attempt := range []AttemptSnapshot{first, second} {
		if _, err := scheduler.UpdateReclaimable(attempt.AttemptID, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scheduler.RequestEvictions(101); !errors.Is(err, ErrInsufficientReclaim) {
		t.Fatalf("insufficient reclaim error = %v", err)
	}
	for _, attempt := range scheduler.Snapshot().Attempts {
		if attempt.State != AttemptRunning {
			t.Fatalf("failed selection mutated attempt: %#v", attempt)
		}
	}
	victims, err := scheduler.RequestEvictions(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 2 || victims[0].AttemptID != second.AttemptID || victims[1].AttemptID != first.AttemptID {
		t.Fatalf("victim order = %#v", victims)
	}
}
