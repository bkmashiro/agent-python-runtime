package scheduler

import (
	"errors"
	"testing"
	"time"
)

func testConfig(clock func() time.Time) Config {
	return Config{
		TargetBytes:       80,
		HighBytes:         90,
		CriticalBytes:     95,
		HardBytes:         100,
		MaxTasks:          8,
		MaxAttempts:       16,
		RetryMarginBytes:  5,
		DefaultRetryDelay: time.Second,
		Clock:             clock,
	}
}

func TestNewRejectsNonMonotonicWatermarksAndMissingBounds(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	valid := testConfig(func() time.Time { return now })
	for name, mutate := range map[string]func(*Config){
		"target zero":       func(value *Config) { value.TargetBytes = 0 },
		"high below target": func(value *Config) { value.HighBytes = value.TargetBytes },
		"critical below high": func(value *Config) {
			value.CriticalBytes = value.HighBytes
		},
		"hard below critical": func(value *Config) { value.HardBytes = value.CriticalBytes },
		"tasks unbounded":     func(value *Config) { value.MaxTasks = 0 },
		"attempts unbounded":  func(value *Config) { value.MaxAttempts = 0 },
		"missing clock":       func(value *Config) { value.Clock = nil },
	} {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestAdmitSkipsOversizedHeadAndChargesExactReservation(t *testing.T) {
	now := time.Unix(2, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Submit(TaskSpec{TaskID: "held", ProfileKey: "profile_held", Lane: LaneGuaranteed, Priority: 20, ReservationBytes: 50}); err != nil {
		t.Fatal(err)
	}
	held, err := scheduler.Admit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Start(held.AttemptID); err != nil {
		t.Fatal(err)
	}
	for _, task := range []TaskSpec{
		{TaskID: "large", ProfileKey: "profile_large", Lane: LaneGuaranteed, Priority: 10, ReservationBytes: 60, MaxEvictions: 0},
		{TaskID: "small", ProfileKey: "profile_small", Lane: LaneSpeculative, Priority: 1, ReservationBytes: 30, MaxEvictions: 2},
	} {
		if _, err := scheduler.Submit(task); err != nil {
			t.Fatal(err)
		}
	}
	attempt, err := scheduler.Admit()
	if err != nil {
		t.Fatal(err)
	}
	if attempt.TaskID != "small" || attempt.ReservedBytes != 30 || attempt.State != AttemptAdmitted {
		t.Fatalf("attempt = %#v", attempt)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.ReservedBytes != 80 || len(snapshot.Queued) != 1 || snapshot.Queued[0].TaskID != "large" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if _, err := scheduler.Admit(); !errors.Is(err, ErrNoAdmissibleTask) {
		t.Fatalf("second Admit() error = %v", err)
	}
}

func TestCompleteReleasesReservationExactlyOnce(t *testing.T) {
	now := time.Unix(3, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Submit(TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2}); err != nil {
		t.Fatal(err)
	}
	attempt, err := scheduler.Admit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Start(attempt.AttemptID); err != nil {
		t.Fatal(err)
	}
	completed, err := scheduler.Complete(attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != AttemptCompleted || scheduler.Snapshot().ReservedBytes != 0 {
		t.Fatalf("completed = %#v snapshot = %#v", completed, scheduler.Snapshot())
	}
	replayed, err := scheduler.Complete(attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != completed || scheduler.Snapshot().ReservedBytes != 0 {
		t.Fatalf("replay = %#v snapshot = %#v", replayed, scheduler.Snapshot())
	}
}

func TestSubmitIsBoundedAndRejectsChangedDuplicate(t *testing.T) {
	now := time.Unix(4, 0).UTC()
	config := testConfig(func() time.Time { return now })
	config.MaxTasks = 1
	scheduler, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	spec := TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 20, MaxEvictions: 1}
	first, err := scheduler.Submit(spec)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := scheduler.Submit(spec)
	if err != nil || replayed != first {
		t.Fatalf("replayed = %#v err = %v", replayed, err)
	}
	changed := spec
	changed.ReservationBytes++
	if _, err := scheduler.Submit(changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed duplicate error = %v", err)
	}
	if _, err := scheduler.Submit(TaskSpec{TaskID: "other", ProfileKey: "profile", Lane: LaneGuaranteed, ReservationBytes: 10}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("bounded submit error = %v", err)
	}
}
