package scheduler

import (
	"sort"
	"time"
)

type PressureLevel string

const (
	PressureNormal   PressureLevel = "normal"
	PressureHigh     PressureLevel = "high"
	PressureCritical PressureLevel = "critical"
	PressureHard     PressureLevel = "hard"
)

type ReclaimReport struct {
	ExecutorTerminated     bool
	ObservedFootprintBytes uint64
	ReclaimedBytes         uint64
}

func (scheduler *Scheduler) ObserveMemory(observedBytes uint64) PressureLevel {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.observeMemoryLocked(observedBytes)
}

func (scheduler *Scheduler) observeMemoryLocked(observedBytes uint64) PressureLevel {
	scheduler.observedBytes = observedBytes
	switch {
	case observedBytes >= scheduler.config.HardBytes:
		scheduler.pressured = true
		return PressureHard
	case observedBytes >= scheduler.config.CriticalBytes:
		scheduler.pressured = true
		return PressureCritical
	case observedBytes > scheduler.config.HighBytes:
		scheduler.pressured = true
		return PressureHigh
	case observedBytes <= scheduler.config.TargetBytes:
		scheduler.pressured = false
		return PressureNormal
	case scheduler.pressured:
		return PressureHigh
	default:
		return PressureNormal
	}
}

func (scheduler *Scheduler) UpdateReclaimable(attemptID string, reclaimableBytes uint64) (AttemptSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return AttemptSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State != AttemptRunning {
		return AttemptSnapshot{}, ErrConflict
	}
	if reclaimableBytes > scheduler.config.HardBytes {
		return AttemptSnapshot{}, ErrConflict
	}
	attempt.snapshot.ReclaimableBytes = reclaimableBytes
	return attempt.snapshot, nil
}

func (scheduler *Scheduler) Pin(attemptID string) (AttemptSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return AttemptSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State == AttemptPinned {
		return attempt.snapshot, nil
	}
	if attempt.snapshot.State != AttemptRunning {
		return AttemptSnapshot{}, ErrConflict
	}
	attempt.snapshot.State = AttemptPinned
	return attempt.snapshot, nil
}

func (scheduler *Scheduler) RequestEvictions(observedBytes uint64) ([]AttemptSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.observeMemoryLocked(observedBytes)
	if observedBytes <= scheduler.config.HighBytes {
		return nil, nil
	}
	recoveryBytes := observedBytes - scheduler.config.TargetBytes
	candidates := make([]*attemptRecord, 0)
	for _, attempt := range scheduler.attempts {
		if attempt.snapshot.State != AttemptRunning || attempt.snapshot.Lane != LaneSpeculative {
			continue
		}
		task := scheduler.tasks[attempt.snapshot.TaskID]
		if task == nil || task.snapshot.MaxEvictions == 0 || task.snapshot.Evictions >= task.snapshot.MaxEvictions {
			continue
		}
		candidates = append(candidates, attempt)
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftAttempt, rightAttempt := candidates[left].snapshot, candidates[right].snapshot
		leftBytes, rightBytes := expectedReclaim(leftAttempt), expectedReclaim(rightAttempt)
		if leftBytes != rightBytes {
			return leftBytes > rightBytes
		}
		if !leftAttempt.StartedAt.Equal(rightAttempt.StartedAt) {
			return leftAttempt.StartedAt.After(rightAttempt.StartedAt)
		}
		if leftAttempt.Priority != rightAttempt.Priority {
			return leftAttempt.Priority < rightAttempt.Priority
		}
		return leftAttempt.AttemptID < rightAttempt.AttemptID
	})
	var selected []*attemptRecord
	var expected uint64
	for _, candidate := range candidates {
		selected = append(selected, candidate)
		value := expectedReclaim(candidate.snapshot)
		if value > ^uint64(0)-expected {
			expected = ^uint64(0)
		} else {
			expected += value
		}
		if expected >= recoveryBytes {
			break
		}
	}
	if expected < recoveryBytes {
		return nil, ErrInsufficientReclaim
	}
	result := make([]AttemptSnapshot, 0, len(selected))
	for _, attempt := range selected {
		attempt.snapshot.State = AttemptEvictionRequested
		result = append(result, attempt.snapshot)
	}
	return result, nil
}

func expectedReclaim(attempt AttemptSnapshot) uint64 {
	if attempt.ReclaimableBytes != 0 {
		return attempt.ReclaimableBytes
	}
	return attempt.ReservedBytes
}

func (scheduler *Scheduler) ConfirmReclaimed(attemptID string, report ReclaimReport) (TaskSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return TaskSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State != AttemptEvictionRequested || !report.ExecutorTerminated {
		return TaskSnapshot{}, ErrConflict
	}
	if report.ObservedFootprintBytes > scheduler.config.HardBytes || report.ReclaimedBytes > scheduler.config.HardBytes || scheduler.reservedBytes < attempt.snapshot.ReservedBytes {
		return TaskSnapshot{}, ErrConflict
	}
	now := scheduler.config.Clock().UTC()
	attempt.snapshot.State = AttemptReclaimed
	attempt.snapshot.CompletedAt = now
	attempt.snapshot.ReclaimableBytes = report.ReclaimedBytes
	scheduler.reservedBytes -= attempt.snapshot.ReservedBytes

	task := scheduler.tasks[attempt.snapshot.TaskID]
	task.snapshot.Evictions++
	floor := saturatingAdd(report.ObservedFootprintBytes, scheduler.config.RetryMarginBytes, scheduler.config.HardBytes)
	if floor > task.snapshot.ReservationFloor {
		task.snapshot.ReservationFloor = floor
	}
	if task.snapshot.MaxEvictions > 0 && task.snapshot.Evictions >= task.snapshot.MaxEvictions {
		task.snapshot.Lane = LaneGuaranteed
	}
	delay := task.spec.RetryDelay
	if delay == 0 {
		delay = scheduler.config.DefaultRetryDelay
	}
	task.snapshot.State = TaskWaitingRetry
	task.snapshot.NotBefore = now.Add(delay)
	task.snapshot.CurrentAttemptID = ""
	scheduler.queue = append(scheduler.queue, task.snapshot.TaskID)
	return task.snapshot, nil
}

func saturatingAdd(left, right, maximum uint64) uint64 {
	if left >= maximum || right > maximum-left {
		return maximum
	}
	return left + right
}

func retryReady(state TaskState, notBefore, now time.Time) bool {
	return (state == TaskQueued || state == TaskWaitingRetry) && !notBefore.After(now)
}
