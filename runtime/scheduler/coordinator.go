package scheduler

import (
	"context"
)

type AttemptCanceler interface {
	Cancel(context.Context, string) (Termination, error)
}

type ReclaimObserver interface {
	Observe(context.Context, Termination) (ReclaimReport, error)
}

func (scheduler *Scheduler) EvictAttempt(ctx context.Context, canceler AttemptCanceler, observer ReclaimObserver, attemptID string) (TaskSnapshot, error) {
	if scheduler == nil || ctx == nil || canceler == nil || observer == nil || !boundedIdentifier(attemptID) {
		return TaskSnapshot{}, ErrInvalidTask
	}
	scheduler.mu.Lock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		scheduler.mu.Unlock()
		return TaskSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State != AttemptEvictionRequested {
		scheduler.mu.Unlock()
		return TaskSnapshot{}, ErrConflict
	}
	scheduler.mu.Unlock()

	termination, err := canceler.Cancel(ctx, attemptID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if !termination.ExecutorTerminated || termination.AttemptID != attemptID {
		return TaskSnapshot{}, ErrConflict
	}
	if termination.CompletionWon {
		if tracker, ok := observer.(AttemptReclaimTracker); ok {
			tracker.Forget(attemptID)
		}
		return scheduler.completeEvictionRace(attemptID)
	}
	report, err := observer.Observe(ctx, termination)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if !report.ExecutorTerminated {
		return TaskSnapshot{}, ErrConflict
	}
	return scheduler.ConfirmReclaimed(attemptID, report)
}
