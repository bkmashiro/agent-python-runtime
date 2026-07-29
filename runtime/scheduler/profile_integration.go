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
	attempt, err := scheduler.Admit()
	if err != nil {
		return AttemptSnapshot{}, err
	}
	scheduler.mu.Lock()
	task := scheduler.tasks[attempt.TaskID]
	profileKey := ""
	if task != nil {
		profileKey = task.snapshot.ProfileKey
	}
	scheduler.mu.Unlock()
	profile, ok := store.profileForKey(profileKey)
	if !ok {
		releaseErr := scheduler.ReleaseAdmission(attempt.AttemptID)
		return AttemptSnapshot{}, errors.Join(ErrInvalidProfile, releaseErr)
	}
	if err := store.RegisterAttempt(attempt.AttemptID, profile); err != nil {
		releaseErr := scheduler.ReleaseAdmission(attempt.AttemptID)
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
