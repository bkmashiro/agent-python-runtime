package semanticspeculation

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type phase5FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newPhase5FakeClock() *phase5FakeClock { return &phase5FakeClock{now: time.Unix(0, 0)} }
func (clock *phase5FakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}
func (clock *phase5FakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}
func (clock *phase5FakeClock) Rewind(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(-duration)
	clock.mu.Unlock()
}

func TestPhase5StageRecorderAccountsOverlapsWithoutDoubleCounting(t *testing.T) {
	clock := newPhase5FakeClock()
	recorder, err := NewPhase5StageRecorder(clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.MarkCriticalPathStart(); err != nil {
		t.Fatal(err)
	}
	gap, err := recorder.Start("finalization_gap", Phase5StageMeasured)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(10 * time.Nanosecond)
	scratch, err := recorder.Start("scratch_execution", Phase5StageMeasured)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(40 * time.Nanosecond)
	if err := recorder.End(scratch); err != nil {
		t.Fatal(err)
	}
	clock.Advance(50 * time.Nanosecond)
	if err := recorder.End(gap); err != nil {
		t.Fatal(err)
	}
	final, _ := recorder.Start("final_execution", Phase5StageMeasured)
	clock.Advance(20 * time.Nanosecond)
	if err := recorder.End(final); err != nil {
		t.Fatal(err)
	}
	teardown, _ := recorder.Start("teardown", Phase5StageMeasured)
	clock.Advance(10 * time.Nanosecond)
	if err := recorder.End(teardown); err != nil {
		t.Fatal(err)
	}
	timeline, err := recorder.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if timeline.TotalCriticalPathNanos != 130 || timeline.UnattributedCriticalPathNanos != 0 {
		t.Fatalf("timeline=%+v", timeline)
	}
	var scratchObservation Phase5StageObservation
	for _, stage := range timeline.Stages {
		if stage.Name == "scratch_execution" {
			scratchObservation = stage
		}
	}
	if scratchObservation.StartedOffsetNanos != 10 || scratchObservation.EndedOffsetNanos != 50 || !scratchObservation.OnCriticalPath {
		t.Fatalf("scratch=%+v", scratchObservation)
	}
}

func TestPhase5StageRecorderSeparatesPreclockCapacityAndUnattributedTime(t *testing.T) {
	clock := newPhase5FakeClock()
	recorder, _ := NewPhase5StageRecorder(clock.Now)
	for _, name := range []string{"analyzer_provision", "scratch_guest_provision", "final_guest_provision"} {
		token, err := recorder.Start(name, Phase5StagePreclock)
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(10 * time.Nanosecond)
		if err := recorder.End(token); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.MarkCriticalPathStart(); err != nil {
		t.Fatal(err)
	}
	gap, _ := recorder.Start("finalization_gap", Phase5StageMeasured)
	clock.Advance(20 * time.Nanosecond)
	if err := recorder.End(gap); err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Nanosecond)
	final, _ := recorder.Start("final_execution", Phase5StageMeasured)
	clock.Advance(10 * time.Nanosecond)
	if err := recorder.End(final); err != nil {
		t.Fatal(err)
	}
	teardown, _ := recorder.Start("teardown", Phase5StageMeasured)
	clock.Advance(5 * time.Nanosecond)
	if err := recorder.End(teardown); err != nil {
		t.Fatal(err)
	}
	timeline, err := recorder.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if timeline.CriticalPathStartedOffsetNanos != 30 || timeline.TotalCriticalPathNanos != 40 || timeline.UnattributedCriticalPathNanos != 5 {
		t.Fatalf("timeline=%+v", timeline)
	}
}

func TestPhase5StageRecorderTimelineFeedsCanonicalTrialContract(t *testing.T) {
	clock := newPhase5FakeClock()
	recorder, _ := NewPhase5StageRecorder(clock.Now)
	if err := recorder.MarkCriticalPathStart(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"finalization_gap", "final_guest_provision", "final_execution", "teardown"} {
		token, err := recorder.Start(name, Phase5StageMeasured)
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(10 * time.Nanosecond)
		if err := recorder.End(token); err != nil {
			t.Fatal(err)
		}
	}
	timeline, err := recorder.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	record := validPhase5TrialRecord(t, "original_unchanged", "cold_end_to_end")
	record.CriticalPathStartedOffsetNanos = timeline.CriticalPathStartedOffsetNanos
	record.TrialEndedOffsetNanos = timeline.TrialEndedOffsetNanos
	record.TotalCriticalPathNanos = timeline.TotalCriticalPathNanos
	record.UnattributedCriticalPathNanos = timeline.UnattributedCriticalPathNanos
	record.Stages = timeline.Stages
	if _, err := EncodePhase5TrialRecord(record); err != nil {
		t.Fatalf("recorder timeline violates trial contract: %v", err)
	}
}

func TestPhase5StageRecorderFailsClosedOnReuseUnknownStagesAndClockRegression(t *testing.T) {
	clock := newPhase5FakeClock()
	recorder, _ := NewPhase5StageRecorder(clock.Now)
	if _, err := recorder.Start("unknown", Phase5StageMeasured); err == nil {
		t.Fatal("accepted unknown stage")
	}
	if err := recorder.MarkCriticalPathStart(); err != nil {
		t.Fatal(err)
	}
	token, err := recorder.Start("finalization_gap", Phase5StageMeasured)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Start("finalization_gap", Phase5StageMeasured); err == nil {
		t.Fatal("accepted duplicate stage")
	}
	if _, err := recorder.Finalize(); !errors.Is(err, ErrPhase5StageActive) {
		t.Fatalf("active finalize err=%v", err)
	}
	clock.Rewind(time.Nanosecond)
	if err := recorder.End(token); !errors.Is(err, ErrPhase5ClockRegression) {
		t.Fatalf("regression err=%v", err)
	}
}

func TestPhase5ProcessResidentBytesIsObservable(t *testing.T) {
	if measured := phase5ProcessResidentBytes(); measured == 0 {
		t.Fatal("resident memory unavailable")
	}
}
