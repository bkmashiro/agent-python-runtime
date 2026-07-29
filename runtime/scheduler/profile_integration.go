package scheduler

import (
	"errors"
	"time"
)

type ProfiledTaskSpec struct {
	TaskID       string
	Profile      WorkloadProfile
	Lane         Lane
	Priority     int32
	MaxEvictions uint32
	RetryDelay   time.Duration
}

func (scheduler *Scheduler) SubmitProfiled(store *ProfileStore, spec ProfiledTaskSpec) (TaskSnapshot, ReservationEstimate, error) {
	if scheduler == nil || store == nil {
		return TaskSnapshot{}, ReservationEstimate{}, ErrInvalidProfile
	}
	estimate, err := store.Estimate(spec.Profile)
	if err != nil {
		return TaskSnapshot{}, ReservationEstimate{}, err
	}
	profileKey, err := store.EnsureProfile(spec.Profile)
	if err != nil {
		return TaskSnapshot{}, ReservationEstimate{}, err
	}
	if profileKey != estimate.ProfileKey {
		return TaskSnapshot{}, ReservationEstimate{}, ErrConflict
	}
	task, err := scheduler.Submit(TaskSpec{
		TaskID: spec.TaskID, ProfileKey: profileKey, Lane: spec.Lane, Priority: spec.Priority,
		ReservationBytes: estimate.ReservationBytes, MaxEvictions: spec.MaxEvictions, RetryDelay: spec.RetryDelay,
	})
	if err != nil {
		return TaskSnapshot{}, ReservationEstimate{}, err
	}
	return task, estimate, nil
}

func (scheduler *Scheduler) AdmitProfiled(store *ProfileStore) (AttemptSnapshot, error) {
	if scheduler == nil || store == nil {
		return AttemptSnapshot{}, ErrInvalidProfile
	}
	// Lock order is ProfileStore then Scheduler. This keeps the quantile and
	// samples stable through credit reservation and attempt registration.
	store.mu.Lock()
	defer store.mu.Unlock()
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	attempt, err := scheduler.admitLocked(func(task TaskSnapshot) (uint64, error) {
		record := store.profiles[task.ProfileKey]
		if record == nil {
			return 0, ErrInvalidProfile
		}
		estimate := store.estimateLocked(record.profile, task.ProfileKey)
		reservation := estimate.ReservationBytes
		if task.Evictions > 0 || task.Lane == LaneGuaranteed {
			if task.ReservationFloor > reservation {
				reservation = task.ReservationFloor
			}
		}
		return reservation, nil
	})
	if err != nil {
		return AttemptSnapshot{}, err
	}
	task := scheduler.tasks[attempt.TaskID]
	if task == nil {
		releaseErr := scheduler.releaseAdmissionLocked(attempt.AttemptID)
		return AttemptSnapshot{}, errors.Join(ErrInvalidProfile, releaseErr)
	}
	record := store.profiles[task.snapshot.ProfileKey]
	if record == nil {
		releaseErr := scheduler.releaseAdmissionLocked(attempt.AttemptID)
		return AttemptSnapshot{}, errors.Join(ErrInvalidProfile, releaseErr)
	}
	if err := store.registerAttemptLocked(attempt.AttemptID, record.profile, record.key); err != nil {
		releaseErr := scheduler.releaseAdmissionLocked(attempt.AttemptID)
		return AttemptSnapshot{}, errors.Join(err, releaseErr)
	}
	return attempt, nil
}

func (scheduler *Scheduler) ReleaseAdmission(attemptID string) error {
	if scheduler == nil || !boundedIdentifier(attemptID) {
		return ErrInvalidTask
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.releaseAdmissionLocked(attemptID)
}

func (scheduler *Scheduler) releaseAdmissionLocked(attemptID string) error {
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return ErrNotFound
	}
	if attempt.snapshot.State == AttemptAdmissionReleased {
		return nil
	}
	if attempt.snapshot.State != AttemptAdmitted || scheduler.reservedBytes < attempt.snapshot.ReservedBytes {
		return ErrConflict
	}
	task := scheduler.tasks[attempt.snapshot.TaskID]
	if task == nil || task.snapshot.CurrentAttemptID != attemptID {
		return ErrConflict
	}
	scheduler.reservedBytes -= attempt.snapshot.ReservedBytes
	attempt.snapshot.State = AttemptAdmissionReleased
	attempt.snapshot.CompletedAt = scheduler.config.Clock().UTC()
	task.snapshot.State = TaskQueued
	task.snapshot.CurrentAttemptID = ""
	task.snapshot.NotBefore = time.Time{}
	scheduler.queue = append(scheduler.queue, task.snapshot.TaskID)
	return nil
}
