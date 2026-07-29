package scheduler

import (
	"context"
	"errors"
	"fmt"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type AttemptReclaimTracker interface {
	Track(string) error
	CaptureFootprint(enginecontract.FootprintObservation) error
	Forget(string)
}

type AttemptReclaimForgetter interface {
	Forget(string)
}

type AttemptFootprintSampler interface {
	SampleActiveFootprint(string) (enginecontract.FootprintObservation, error)
}

type CoordinatedVictimDispatcherConfig struct {
	Scheduler *Scheduler
	Canceler  AttemptCanceler
	Observer  ReclaimObserver
	Tracker   AttemptReclaimTracker
	Sampler   AttemptFootprintSampler
}

type CoordinatedVictimDispatcher struct {
	scheduler *Scheduler
	canceler  AttemptCanceler
	observer  ReclaimObserver
	tracker   AttemptReclaimTracker
	sampler   AttemptFootprintSampler
}

func NewCoordinatedVictimDispatcher(config CoordinatedVictimDispatcherConfig) (*CoordinatedVictimDispatcher, error) {
	if config.Scheduler == nil || config.Canceler == nil || config.Observer == nil || config.Sampler != nil && config.Tracker == nil {
		return nil, ErrInvalidConfig
	}
	return &CoordinatedVictimDispatcher{
		scheduler: config.Scheduler, canceler: config.Canceler, observer: config.Observer,
		tracker: config.Tracker, sampler: config.Sampler,
	}, nil
}

func (dispatcher *CoordinatedVictimDispatcher) Dispatch(ctx context.Context, victims []AttemptSnapshot) error {
	if dispatcher == nil || dispatcher.scheduler == nil || dispatcher.canceler == nil || dispatcher.observer == nil || ctx == nil || len(victims) == 0 {
		return ErrInvalidTask
	}
	seen := make(map[string]struct{}, len(victims))
	for _, victim := range victims {
		if !boundedIdentifier(victim.AttemptID) {
			return ErrInvalidTask
		}
		if _, duplicate := seen[victim.AttemptID]; duplicate {
			return ErrConflict
		}
		seen[victim.AttemptID] = struct{}{}
	}
	for _, victim := range victims {
		if err := ctx.Err(); err != nil {
			return err
		}
		if dispatcher.tracker != nil {
			if err := dispatcher.tracker.Track(victim.AttemptID); err != nil {
				return fmt.Errorf("track victim %s: %w", victim.AttemptID, err)
			}
		}
		if dispatcher.sampler != nil {
			observation, err := dispatcher.sampler.SampleActiveFootprint(victim.AttemptID)
			if err != nil && !errors.Is(err, enginecontract.ErrActiveFootprintNotFound) {
				dispatcher.tracker.Forget(victim.AttemptID)
				return fmt.Errorf("sample victim %s: %w", victim.AttemptID, err)
			}
			if err == nil {
				if err := dispatcher.tracker.CaptureFootprint(observation); err != nil {
					dispatcher.tracker.Forget(victim.AttemptID)
					return fmt.Errorf("capture victim %s: %w", victim.AttemptID, err)
				}
			}
		}
		task, err := dispatcher.scheduler.EvictAttempt(ctx, dispatcher.canceler, dispatcher.observer, victim.AttemptID)
		if err != nil {
			return fmt.Errorf("dispatch victim %s: %w", victim.AttemptID, err)
		}
		if task.State == TaskCompleted && dispatcher.tracker != nil {
			dispatcher.tracker.Forget(victim.AttemptID)
		}
	}
	return nil
}

var _ VictimDispatcher = (*CoordinatedVictimDispatcher)(nil)
