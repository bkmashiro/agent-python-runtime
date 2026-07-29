package scheduler

import (
	"context"
	"errors"
	"time"
)

const maxLiveMemoryControlSamples = uint32(1_000_000)

type LiveMemoryReader interface {
	Read() (LiveMemorySnapshot, error)
}

type VictimDispatcher interface {
	Dispatch(context.Context, []AttemptSnapshot) error
}

type LiveMemoryControlLoopConfig struct {
	Scheduler  *Scheduler
	Profiles   *ProfileStore
	Controller *GreedController
	Reader     LiveMemoryReader
	Dispatcher VictimDispatcher
	Interval   time.Duration
	MaxSamples uint32
}

type LiveMemoryControlStep struct {
	Snapshot            LiveMemorySnapshot
	Window              ControlWindow
	Decision            GreedDecision
	Victims             []AttemptSnapshot
	VictimsInsufficient bool
	DispatchCompleted   bool
}

type LiveMemoryControlReport struct {
	Samples                   uint32
	VictimBatches             uint32
	VictimsDispatched         uint32
	InsufficientVictimWindows uint32
	Last                      LiveMemoryControlStep
}

type LiveMemoryControlLoop struct {
	config  LiveMemoryControlLoopConfig
	tracker *LiveControlWindowTracker
}

func NewLiveMemoryControlLoop(config LiveMemoryControlLoopConfig) (*LiveMemoryControlLoop, error) {
	if config.Scheduler == nil || config.Profiles == nil || config.Controller == nil || config.Reader == nil || config.Dispatcher == nil ||
		config.Interval <= 0 || config.Interval > time.Minute || config.MaxSamples == 0 || config.MaxSamples > maxLiveMemoryControlSamples ||
		config.Profiles.CurrentReservationQuantileBPS() != config.Controller.CurrentQuantileBPS() {
		return nil, ErrInvalidLiveMemoryConfig
	}
	return &LiveMemoryControlLoop{config: config, tracker: NewLiveControlWindowTracker()}, nil
}

func (loop *LiveMemoryControlLoop) Run(ctx context.Context) (LiveMemoryControlReport, error) {
	var report LiveMemoryControlReport
	if loop == nil || ctx == nil || loop.tracker == nil {
		return report, ErrInvalidLiveMemoryConfig
	}
	for report.Samples < loop.config.MaxSamples {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		step, err := loop.Step(ctx)
		if !step.Snapshot.SampledAt.IsZero() {
			if recordErr := report.record(step); recordErr != nil {
				return report, recordErr
			}
		}
		if err != nil {
			return report, err
		}
		if report.Samples == loop.config.MaxSamples {
			return report, nil
		}
		timer := time.NewTimer(loop.config.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return report, ctx.Err()
		case <-timer.C:
		}
	}
	return report, nil
}

func (report *LiveMemoryControlReport) record(step LiveMemoryControlStep) error {
	if report == nil || report.Samples == ^uint32(0) {
		return ErrCapacity
	}
	report.Samples++
	report.Last = step
	if step.VictimsInsufficient {
		report.InsufficientVictimWindows++
	}
	if step.DispatchCompleted {
		if uint64(report.VictimsDispatched)+uint64(len(step.Victims)) > uint64(^uint32(0)) {
			return ErrCapacity
		}
		report.VictimBatches++
		report.VictimsDispatched += uint32(len(step.Victims))
	}
	return nil
}

func (loop *LiveMemoryControlLoop) Step(ctx context.Context) (LiveMemoryControlStep, error) {
	var step LiveMemoryControlStep
	if loop == nil || ctx == nil || loop.tracker == nil {
		return step, ErrInvalidLiveMemoryConfig
	}
	if err := ctx.Err(); err != nil {
		return step, err
	}
	snapshot, err := loop.config.Reader.Read()
	if err != nil {
		return step, err
	}
	started, evicted := loop.config.Scheduler.liveControlCounters()
	window, err := loop.tracker.Build(loop.config.Scheduler, snapshot, started, evicted)
	if err != nil {
		return step, err
	}
	decision, err := loop.config.Controller.Apply(loop.config.Profiles, window)
	if err != nil {
		return step, err
	}
	step = LiveMemoryControlStep{Snapshot: snapshot, Window: window, Decision: decision}
	if snapshot.CurrentBytes <= loop.config.Scheduler.config.HighBytes {
		return step, nil
	}
	victims, err := loop.config.Scheduler.RequestEvictions(snapshot.CurrentBytes)
	if errors.Is(err, ErrInsufficientReclaim) {
		step.VictimsInsufficient = true
		return step, nil
	}
	if err != nil {
		return step, err
	}
	step.Victims = append([]AttemptSnapshot(nil), victims...)
	if len(step.Victims) == 0 {
		return step, nil
	}
	if err := loop.config.Dispatcher.Dispatch(ctx, step.Victims); err != nil {
		return step, err
	}
	step.DispatchCompleted = true
	return step, nil
}

func (scheduler *Scheduler) liveControlCounters() (started, evicted uint64) {
	if scheduler == nil {
		return 0, 0
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	for _, attempt := range scheduler.attempts {
		if !attempt.snapshot.StartedAt.IsZero() {
			started++
		}
		if attempt.snapshot.State == AttemptReclaimed {
			evicted++
		}
	}
	return started, evicted
}
