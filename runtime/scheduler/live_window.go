package scheduler

import (
	"errors"
	"sync"
)

var ErrLiveMemoryCounterReset = errors.New("live memory counter reset")

type LiveControlWindowTracker struct {
	mu          sync.Mutex
	initialized bool
	previous    LiveMemorySnapshot
}

func NewLiveControlWindowTracker() *LiveControlWindowTracker {
	return &LiveControlWindowTracker{}
}

func (tracker *LiveControlWindowTracker) Build(scheduler *Scheduler, snapshot LiveMemorySnapshot, attemptsStarted, attemptsEvicted uint64) (ControlWindow, error) {
	if tracker == nil || scheduler == nil {
		return ControlWindow{}, ErrInvalidConfig
	}
	if err := snapshot.Validate(); err != nil {
		return ControlWindow{}, err
	}
	if scheduler.config.HardBytes > snapshot.MaximumBytes {
		return ControlWindow{}, ErrInvalidLiveMemoryConfig
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var oomEvents uint64
	if tracker.initialized {
		if !snapshot.SampledAt.After(tracker.previous.SampledAt) || snapshot.Events.OOM < tracker.previous.Events.OOM || snapshot.Events.OOMKill < tracker.previous.Events.OOMKill {
			return ControlWindow{}, ErrLiveMemoryCounterReset
		}
		oomDelta := snapshot.Events.OOM - tracker.previous.Events.OOM
		killDelta := snapshot.Events.OOMKill - tracker.previous.Events.OOMKill
		if killDelta > ^uint64(0)-oomDelta {
			oomEvents = ^uint64(0)
		} else {
			oomEvents = oomDelta + killDelta
		}
	}
	window := ControlWindow{
		AttemptsStarted: attemptsStarted, AttemptsEvicted: attemptsEvicted,
		MemoryUtilizationBPS: snapshot.UtilizationBPS(), Pressure: scheduler.ObserveMemory(snapshot.CurrentBytes), OOMEvents: oomEvents,
		MemorySomeAvg10BPS: snapshot.Pressure.Some.Avg10BPS, MemoryFullAvg10BPS: snapshot.Pressure.Full.Avg10BPS,
	}
	if err := validateControlWindow(window); err != nil {
		return ControlWindow{}, err
	}
	tracker.previous = snapshot
	tracker.initialized = true
	return window, nil
}
