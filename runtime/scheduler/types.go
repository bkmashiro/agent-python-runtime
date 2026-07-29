// Package scheduler owns bounded Host-side task admission and memory-credit accounting.
package scheduler

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidConfig       = errors.New("invalid scheduler config")
	ErrInvalidTask         = errors.New("invalid scheduler task")
	ErrConflict            = errors.New("scheduler state conflict")
	ErrCapacity            = errors.New("scheduler capacity exhausted")
	ErrNotFound            = errors.New("scheduler record not found")
	ErrNoAdmissibleTask    = errors.New("no task can be admitted")
	ErrInsufficientReclaim = errors.New("insufficient evictable memory")
)

type Lane string

const (
	LaneSpeculative Lane = "speculative"
	LaneGuaranteed  Lane = "guaranteed"
)

type TaskState string

const (
	TaskQueued       TaskState = "queued"
	TaskAdmitted     TaskState = "admitted"
	TaskRunning      TaskState = "running"
	TaskWaitingRetry TaskState = "waiting_retry"
	TaskCompleted    TaskState = "completed"
)

type AttemptState string

const (
	AttemptAdmitted          AttemptState = "admitted"
	AttemptRunning           AttemptState = "running"
	AttemptPinned            AttemptState = "pinned"
	AttemptEvictionRequested AttemptState = "eviction_requested"
	AttemptReclaimed         AttemptState = "reclaimed"
	AttemptCompleted         AttemptState = "completed"
)

type Config struct {
	TargetBytes       uint64
	HighBytes         uint64
	CriticalBytes     uint64
	HardBytes         uint64
	MaxTasks          uint32
	MaxAttempts       uint32
	RetryMarginBytes  uint64
	DefaultRetryDelay time.Duration
	Clock             func() time.Time
}

type TaskSpec struct {
	TaskID           string
	ProfileKey       string
	Lane             Lane
	Priority         int32
	ReservationBytes uint64
	MaxEvictions     uint32
	RetryDelay       time.Duration
}

type TaskSnapshot struct {
	TaskID           string
	ProfileKey       string
	Lane             Lane
	Priority         int32
	ReservationBytes uint64
	ReservationFloor uint64
	MaxEvictions     uint32
	Evictions        uint32
	State            TaskState
	NotBefore        time.Time
	SubmittedAt      time.Time
	Sequence         uint64
	CurrentAttemptID string
}

type AttemptSnapshot struct {
	AttemptID        string
	TaskID           string
	Ordinal          uint32
	Lane             Lane
	Priority         int32
	ReservedBytes    uint64
	ReclaimableBytes uint64
	State            AttemptState
	AdmittedAt       time.Time
	StartedAt        time.Time
	CompletedAt      time.Time
	Sequence         uint64
}

type Snapshot struct {
	ReservedBytes uint64
	ObservedBytes uint64
	Pressured     bool
	Queued        []TaskSnapshot
	Attempts      []AttemptSnapshot
}

func (config Config) validate() error {
	if config.TargetBytes == 0 || config.TargetBytes >= config.HighBytes || config.HighBytes >= config.CriticalBytes || config.CriticalBytes >= config.HardBytes {
		return fmt.Errorf("%w: watermarks must be positive and strictly increasing", ErrInvalidConfig)
	}
	if config.MaxTasks == 0 || config.MaxAttempts == 0 || config.MaxTasks > 1<<20 || config.MaxAttempts > 1<<22 {
		return fmt.Errorf("%w: record bounds are missing or excessive", ErrInvalidConfig)
	}
	if config.DefaultRetryDelay < 0 || config.DefaultRetryDelay > 24*time.Hour || config.Clock == nil {
		return fmt.Errorf("%w: retry delay or clock is invalid", ErrInvalidConfig)
	}
	return nil
}

func (spec TaskSpec) validate(hardBytes uint64) error {
	if !boundedIdentifier(spec.TaskID) || !boundedIdentifier(spec.ProfileKey) {
		return fmt.Errorf("%w: task or profile identity is invalid", ErrInvalidTask)
	}
	if spec.Lane != LaneSpeculative && spec.Lane != LaneGuaranteed {
		return fmt.Errorf("%w: lane is invalid", ErrInvalidTask)
	}
	if spec.ReservationBytes == 0 || spec.ReservationBytes > hardBytes || spec.MaxEvictions > 16 || spec.RetryDelay < 0 || spec.RetryDelay > 24*time.Hour {
		return fmt.Errorf("%w: reservation or retry policy is invalid", ErrInvalidTask)
	}
	if spec.Lane == LaneGuaranteed && spec.MaxEvictions != 0 {
		return fmt.Errorf("%w: guaranteed tasks cannot request eviction", ErrInvalidTask)
	}
	return nil
}

func boundedIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}
