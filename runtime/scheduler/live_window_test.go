package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestLiveControlWindowTracksOOMDeltaAndSchedulerPressure(t *testing.T) {
	scheduler, err := New(Config{
		TargetBytes: 800, HighBytes: 900, CriticalBytes: 950, HardBytes: 1000,
		MaxTasks: 8, MaxAttempts: 8, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewLiveControlWindowTracker()
	first := LiveMemorySnapshot{
		SampledAt: time.Unix(1, 0).UTC(), CurrentBytes: 850, MaximumBytes: 1000,
		Events: MemoryEvents{OOM: 10, OOMKill: 2}, Pressure: MemoryPSI{Some: PSIRecord{Avg10BPS: 25}, Full: PSIRecord{Avg10BPS: 5}},
	}
	window, err := tracker.Build(scheduler, first, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if window.OOMEvents != 0 || window.MemoryUtilizationBPS != 8500 || window.Pressure != PressureNormal ||
		window.MemorySomeAvg10BPS != 25 || window.MemoryFullAvg10BPS != 5 {
		t.Fatalf("first window = %#v", window)
	}
	second := first
	second.SampledAt = time.Unix(2, 0).UTC()
	second.CurrentBytes = 960
	second.Events.OOM = 12
	second.Events.OOMKill = 3
	window, err = tracker.Build(scheduler, second, 120, 4)
	if err != nil {
		t.Fatal(err)
	}
	if window.OOMEvents != 3 || window.MemoryUtilizationBPS != 9600 || window.Pressure != PressureCritical {
		t.Fatalf("second window = %#v", window)
	}
	regressed := second
	regressed.SampledAt = time.Unix(3, 0).UTC()
	regressed.Events.OOM = 1
	if _, err := tracker.Build(scheduler, regressed, 130, 4); !errors.Is(err, ErrLiveMemoryCounterReset) {
		t.Fatalf("counter reset error = %v", err)
	}
}

func TestLiveControlWindowRejectsSchedulerHardLimitAboveCgroup(t *testing.T) {
	scheduler, err := New(Config{TargetBytes: 800, HighBytes: 900, CriticalBytes: 1500, HardBytes: 2000, MaxTasks: 1, MaxAttempts: 1, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := LiveMemorySnapshot{SampledAt: time.Unix(1, 0).UTC(), CurrentBytes: 500, MaximumBytes: 1000}
	if _, err := NewLiveControlWindowTracker().Build(scheduler, snapshot, 0, 0); !errors.Is(err, ErrInvalidLiveMemoryConfig) {
		t.Fatalf("hard-limit mismatch error = %v", err)
	}
}

func TestGreedControllerTightensOnMemoryPSI(t *testing.T) {
	config := greedConfig()
	config.MaximumSomePSIAvg10BPS = 100
	config.MaximumFullPSIAvg10BPS = 50
	controller, err := NewGreedController(config)
	if err != nil {
		t.Fatal(err)
	}
	profileConfig := profileConfig()
	profileConfig.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(profileConfig)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Apply(store, ControlWindow{
		AttemptsStarted: 1000, MemoryUtilizationBPS: 8000, Pressure: PressureNormal,
		MemorySomeAvg10BPS: 101,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedMoreConservative || decision.Reason != "memory_some_psi" {
		t.Fatalf("some PSI decision = %#v", decision)
	}
	decision, err = controller.Apply(store, ControlWindow{
		AttemptsStarted: 1000, MemoryUtilizationBPS: 8000, Pressure: PressureNormal,
		MemoryFullAvg10BPS: 51,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != GreedMoreConservative || decision.Reason != "memory_full_psi" {
		t.Fatalf("full PSI decision = %#v", decision)
	}
}
