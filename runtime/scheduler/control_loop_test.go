package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type liveMemoryReaderFunc func() (LiveMemorySnapshot, error)

func (function liveMemoryReaderFunc) Read() (LiveMemorySnapshot, error) { return function() }

type victimDispatcherFunc func(context.Context, []AttemptSnapshot) error

func (function victimDispatcherFunc) Dispatch(ctx context.Context, victims []AttemptSnapshot) error {
	return function(ctx, victims)
}

func newControlLoopFixture(t *testing.T, snapshots []LiveMemorySnapshot, maxSamples uint32, dispatch victimDispatcherFunc) (*LiveMemoryControlLoop, *Scheduler) {
	t.Helper()
	now := time.Unix(500, 0).UTC()
	scheduler, err := New(testConfig(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	storeConfig := profileConfig()
	storeConfig.HardBytes = 100
	storeConfig.UnknownReservationBytes = 40
	storeConfig.ReservationQuantileBPS = 9500
	store, err := NewProfileStore(storeConfig)
	if err != nil {
		t.Fatal(err)
	}
	controllerConfig := greedConfig()
	controllerConfig.MinimumAttempts = 1
	controller, err := NewGreedController(controllerConfig)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	index := 0
	reader := liveMemoryReaderFunc(func() (LiveMemorySnapshot, error) {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(snapshots) {
			return LiveMemorySnapshot{}, errors.New("fixture exhausted")
		}
		value := snapshots[index]
		index++
		return value, nil
	})
	loop, err := NewLiveMemoryControlLoop(LiveMemoryControlLoopConfig{
		Scheduler: scheduler, Profiles: store, Controller: controller, Reader: reader, Dispatcher: dispatch,
		Interval: time.Millisecond, MaxSamples: maxSamples,
	})
	if err != nil {
		t.Fatal(err)
	}
	return loop, scheduler
}

func controlSnapshot(second int64, current uint64) LiveMemorySnapshot {
	return LiveMemorySnapshot{SampledAt: time.Unix(second, 0).UTC(), CurrentBytes: current, MaximumBytes: 100}
}

func TestLiveMemoryControlLoopDispatchesAtomicVictims(t *testing.T) {
	var dispatched []AttemptSnapshot
	loop, scheduler := newControlLoopFixture(t, []LiveMemorySnapshot{controlSnapshot(1, 91)}, 1, func(_ context.Context, victims []AttemptSnapshot) error {
		dispatched = append(dispatched, victims...)
		return nil
	})
	attempt := submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	report, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples != 1 || report.VictimsDispatched != 1 || len(dispatched) != 1 || dispatched[0].AttemptID != attempt.AttemptID {
		t.Fatalf("report=%#v dispatched=%#v", report, dispatched)
	}
	if scheduler.Snapshot().Attempts[0].State != AttemptEvictionRequested {
		t.Fatalf("victim not atomically marked: %#v", scheduler.Snapshot())
	}
}

func TestLiveMemoryControlLoopResamplesWhenVictimsAreInsufficient(t *testing.T) {
	dispatchCalls := 0
	loop, scheduler := newControlLoopFixture(t, []LiveMemorySnapshot{controlSnapshot(1, 91), controlSnapshot(2, 70)}, 2, func(context.Context, []AttemptSnapshot) error {
		dispatchCalls++
		return nil
	})
	attempt := submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	if _, err := scheduler.UpdateReclaimable(attempt.AttemptID, 10); err != nil {
		t.Fatal(err)
	}
	report, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Samples != 2 || report.InsufficientVictimWindows != 1 || report.VictimsDispatched != 0 || dispatchCalls != 0 {
		t.Fatalf("report=%#v calls=%d", report, dispatchCalls)
	}
	if scheduler.Snapshot().Attempts[0].State != AttemptRunning || scheduler.Snapshot().Pressured {
		t.Fatalf("insufficient selection mutated state or failed to recover: %#v", scheduler.Snapshot())
	}
}

func TestLiveMemoryControlLoopDispatcherFailureRetainsCreditAndEvidence(t *testing.T) {
	dispatchErr := errors.New("dispatcher failed")
	loop, scheduler := newControlLoopFixture(t, []LiveMemorySnapshot{controlSnapshot(1, 91)}, 1, func(context.Context, []AttemptSnapshot) error {
		return dispatchErr
	})
	submitRunning(t, scheduler, TaskSpec{TaskID: "task", ProfileKey: "profile", Lane: LaneSpeculative, ReservationBytes: 40, MaxEvictions: 2})
	report, err := loop.Run(context.Background())
	if !errors.Is(err, dispatchErr) || report.Samples != 1 || len(report.Last.Victims) != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	snapshot := scheduler.Snapshot()
	if snapshot.ReservedBytes != 40 || snapshot.Attempts[0].State != AttemptEvictionRequested {
		t.Fatalf("dispatcher failure released credit or lost victim: %#v", snapshot)
	}
}

func TestLiveMemoryControlLoopStopsOnReaderError(t *testing.T) {
	readerErr := errors.New("reader failed")
	loop, err := NewLiveMemoryControlLoop(LiveMemoryControlLoopConfig{
		Scheduler: mustScheduler(t), Profiles: mustProfileStore(t), Controller: mustGreedController(t),
		Reader:     liveMemoryReaderFunc(func() (LiveMemorySnapshot, error) { return LiveMemorySnapshot{}, readerErr }),
		Dispatcher: victimDispatcherFunc(func(context.Context, []AttemptSnapshot) error { return nil }), Interval: time.Millisecond, MaxSamples: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := loop.Run(context.Background())
	if !errors.Is(err, readerErr) || report.Samples != 0 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestLiveMemoryControlLoopCancellationInterruptsWait(t *testing.T) {
	loop, _ := newControlLoopFixture(t, []LiveMemorySnapshot{controlSnapshot(1, 70)}, 2, func(context.Context, []AttemptSnapshot) error { return nil })
	loop.config.Interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loop.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error=%v", err)
	}
}

func mustScheduler(t *testing.T) *Scheduler {
	t.Helper()
	value, err := New(testConfig(time.Now))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustProfileStore(t *testing.T) *ProfileStore {
	t.Helper()
	config := profileConfig()
	config.HardBytes = 100
	config.UnknownReservationBytes = 40
	config.ReservationQuantileBPS = 9500
	value, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGreedController(t *testing.T) *GreedController {
	t.Helper()
	config := greedConfig()
	config.MinimumAttempts = 1
	value, err := NewGreedController(config)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
