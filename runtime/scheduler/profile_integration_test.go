package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestSubmitAndAdmitProfiledUseHostEstimateAndTrackAttempt(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	profileConfig := profileConfig()
	profileConfig.UnknownReservationBytes = 40
	store, err := NewProfileStore(profileConfig)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	task, estimate, err := scheduler.SubmitProfiled(store, ProfiledTaskSpec{
		TaskID: "task", Profile: profile, Lane: LaneSpeculative, Priority: 3, MaxEvictions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateUnknown || estimate.ReservationBytes != 40 || task.ReservationBytes != 40 || task.ProfileKey != estimate.ProfileKey {
		t.Fatalf("task = %#v estimate = %#v", task, estimate)
	}
	attempt, err := scheduler.AdmitProfiled(store)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.TaskID != "task" || attempt.ReservedBytes != 40 || !store.ShouldSample(attempt.AttemptID) {
		t.Fatalf("attempt = %#v profile snapshot = %#v", attempt, store.Snapshot())
	}
}

func TestAdmitProfiledReestimatesFutureSpeculativeAttempt(t *testing.T) {
	now := time.Unix(205, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	config := profileConfig()
	config.HardBytes = 100
	config.UnknownReservationBytes = 80
	config.PerAttemptMarginBytes = 5
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("b", "future_estimate", RequestSizeSmall)
	task, initial, err := scheduler.SubmitProfiled(store, ProfiledTaskSpec{TaskID: "future", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if task.ReservationBytes != 80 || initial.Source != EstimateUnknown {
		t.Fatalf("initial task=%#v estimate=%#v", task, initial)
	}
	addProfileSamples(t, store, profile, 20, 40)
	if err := store.compareAndSwapReservationQuantile(10000, 5000); err != nil {
		t.Fatal(err)
	}
	attempt, err := scheduler.AdmitProfiled(store)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ReservedBytes != 25 {
		t.Fatalf("future attempt reservation=%d, want latest p50+margin 25", attempt.ReservedBytes)
	}
	if queued := scheduler.tasks["future"].snapshot; queued.ReservationBytes != 80 || queued.ReservationFloor != 80 {
		t.Fatalf("submission audit/floor mutated: %#v", queued)
	}
}

func TestAdmitProfiledPreservesRetryAndGuaranteedFloor(t *testing.T) {
	now := time.Unix(207, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	config := profileConfig()
	config.HardBytes = 100
	config.UnknownReservationBytes = 80
	config.PerAttemptMarginBytes = 5
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("c", "retry_floor", RequestSizeSmall)
	addProfileSamples(t, store, profile, 20, 40)
	if _, _, err := scheduler.SubmitProfiled(store, ProfiledTaskSpec{TaskID: "retry", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.AdmitProfiled(store)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReservedBytes != 45 {
		t.Fatalf("first reservation=%d, want p100+margin 45", first.ReservedBytes)
	}
	if _, err := scheduler.Start(first.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.UpdateReclaimable(first.AttemptID, 80); err != nil {
		t.Fatal(err)
	}
	victims, err := scheduler.RequestEvictions(91)
	if err != nil || len(victims) != 1 || victims[0].AttemptID != first.AttemptID {
		t.Fatalf("victims=%#v err=%v", victims, err)
	}
	retry, err := scheduler.ConfirmReclaimed(first.AttemptID, ReclaimReport{ObservedFootprintBytes: 80, ReclaimedBytes: 80, ExecutorTerminated: true})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Lane != LaneGuaranteed || retry.ReservationFloor != 85 {
		t.Fatalf("retry task=%#v", retry)
	}
	if err := store.compareAndSwapReservationQuantile(10000, 5000); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	scheduler.ObserveMemory(0)
	second, err := scheduler.AdmitProfiled(store)
	if err != nil {
		t.Fatal(err)
	}
	if second.ReservedBytes != 85 || second.Lane != LaneGuaranteed {
		t.Fatalf("guaranteed retry attempt=%#v", second)
	}
}

func TestAdmitProfiledRollsBackCreditWhenAttemptTrackingIsFull(t *testing.T) {
	now := time.Unix(210, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	config := profileConfig()
	config.MaxTrackedAttempts = 1
	config.UnknownReservationBytes = 40
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	if err := store.RegisterAttempt("occupied:attempt", profile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := scheduler.SubmitProfiled(store, ProfiledTaskSpec{TaskID: "task", Profile: profile, Lane: LaneSpeculative, MaxEvictions: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.AdmitProfiled(store); !errors.Is(err, ErrCapacity) {
		t.Fatalf("AdmitProfiled() error = %v", err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.ReservedBytes != 0 || len(snapshot.Queued) != 1 || snapshot.Queued[0].TaskID != "task" {
		t.Fatalf("admission was not rolled back: %#v", snapshot)
	}
	if len(snapshot.Attempts) != 1 || snapshot.Attempts[0].State != AttemptAdmissionReleased {
		t.Fatalf("released attempt is not auditable: %#v", snapshot.Attempts)
	}
}
