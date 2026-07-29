package scheduler

import (
	"context"
	"fmt"
)

type AttemptReclaimTracker interface {
	Track(string) error
	Forget(string)
}

type CoordinatedVictimDispatcherConfig struct {
	Scheduler *Scheduler
	Canceler  AttemptCanceler
	Observer  ReclaimObserver
	Tracker   AttemptReclaimTracker
}

type CoordinatedVictimDispatcher struct {
	scheduler *Scheduler
	canceler  AttemptCanceler
	observer  ReclaimObserver
	tracker   AttemptReclaimTracker
}

func NewCoordinatedVictimDispatcher(config CoordinatedVictimDispatcherConfig) (*CoordinatedVictimDispatcher, error) {
	if config.Scheduler == nil || config.Canceler == nil || config.Observer == nil {
		return nil, ErrInvalidConfig
	}
	return &CoordinatedVictimDispatcher{scheduler: config.Scheduler, canceler: config.Canceler, observer: config.Observer, tracker: config.Tracker}, nil
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
		if _, err := dispatcher.scheduler.EvictAttempt(ctx, dispatcher.canceler, dispatcher.observer, victim.AttemptID); err != nil {
			return fmt.Errorf("dispatch victim %s: %w", victim.AttemptID, err)
		}
	}
	return nil
}

var _ VictimDispatcher = (*CoordinatedVictimDispatcher)(nil)
