//go:build linux

package scheduler

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveMemoryControlLoopCurrentCgroupSmoke(t *testing.T) {
	if os.Getenv("APYRUN_S9_RUN") != "1" {
		t.Skip("set APYRUN_S9_RUN=1 for the bounded Linux cgroup smoke")
	}
	reader, err := NewCurrentCgroupV2MemoryReader()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	maximum := baseline.MaximumBytes
	target, high, critical := maximum*80/100, maximum*90/100, maximum*95/100
	if baseline.CurrentBytes >= target {
		t.Fatalf("cgroup is already above the smoke target: %#v", baseline)
	}
	scheduler, err := New(Config{
		TargetBytes: target, HighBytes: high, CriticalBytes: critical, HardBytes: maximum,
		MaxTasks: 8, MaxAttempts: 16, RetryMarginBytes: 1 << 20, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	profileOptions := profileConfig()
	profileOptions.HardBytes = maximum
	profileOptions.UnknownReservationBytes = target
	profileOptions.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	controllerOptions := greedConfig()
	controllerOptions.MinimumAttempts = 1
	controller, err := NewGreedController(controllerOptions)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := NewLiveMemoryControlLoop(LiveMemoryControlLoopConfig{
		Scheduler: scheduler, Profiles: store, Controller: controller, Reader: reader,
		Dispatcher: victimDispatcherFunc(func(context.Context, []AttemptSnapshot) error { return nil }),
		Interval:   time.Millisecond, MaxSamples: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples != 2 || report.VictimsDispatched != 0 || report.InsufficientVictimWindows != 0 || report.Last.Snapshot.MaximumBytes != maximum {
		t.Fatalf("report=%#v", report)
	}
}
