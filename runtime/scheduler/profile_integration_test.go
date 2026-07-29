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
