package durable

import (
	"context"
	"errors"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

// State is the harness-facing outcome of one bounded execution attempt.
// Infrastructure failures remain Go errors; expected control and Python
// outcomes are represented as data.
type State string

const (
	StateCompleted State = "completed"
	StateParked    State = "parked"
	StateBlocked   State = "blocked"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// AttemptState and the Attempt constants preserve the original API.
type AttemptState = State

const (
	AttemptCompleted = StateCompleted
	AttemptParked    = StateParked
)

type FailureKind string

const (
	FailurePython       FailureKind = "python"
	FailureRecovery     FailureKind = "recovery"
	FailureCancellation FailureKind = "cancellation"
)

// Failure describes an expected terminal/control outcome without exposing the
// executor's internal error transport to a harness.
type Failure struct {
	Kind    FailureKind `json:"kind"`
	Message string      `json:"message"`
}

// ParkKind identifies the protocol that must make a parked Run runnable again.
type ParkKind string

const (
	ParkWait   ParkKind = "wait"
	ParkLookup ParkKind = "lookup"
)

// Park describes a released attempt. It contains durable identity, not a live
// Guest or resumable Python stack.
type Park struct {
	Kind     ParkKind
	RunID    string
	WaitID   string
	Sequence uint32
	Reason   string
}

// Result is the stable harness-facing result of one fresh deterministic
// attempt. Park and Failure are populated only for their corresponding State.
type Result struct {
	State   State
	Output  pysolate.Output
	Park    *Park
	Failure *Failure
}

// AdvanceResult preserves the original name for existing controllers.
type AdvanceResult = Result

// AttemptController is the narrow lifecycle boundary needed by admission and
// scheduling. Implementations own persistence, replay, effects, and Guests.
type AttemptController interface {
	Advance(context.Context, string) (Result, error)
	Cancel(context.Context, string) error
}

// Advance executes one attempt and represents durable parking as data rather
// than an error. Resume remains available for compatibility with direct callers.
func (runner *Runner) Advance(ctx context.Context, runID string) (Result, error) {
	output, err := runner.Resume(ctx, runID)
	return classifyAttemptResult(output, err)
}

func classifyAttemptResult(output pysolate.Output, err error) (Result, error) {
	if err == nil {
		return Result{State: StateCompleted, Output: output}, nil
	}
	parked, ok := asPark(err)
	if ok {
		return Result{State: StateParked, Output: output, Park: parked}, nil
	}
	if errors.Is(err, ErrBlocked) {
		return Result{State: StateBlocked, Output: output, Failure: &Failure{Kind: FailureRecovery, Message: err.Error()}}, nil
	}
	var pythonErr *pysolate.PythonError
	if errors.As(err, &pythonErr) {
		return Result{State: StateFailed, Output: output, Failure: &Failure{Kind: FailurePython, Message: pythonErr.Error()}}, nil
	}
	if errors.Is(err, ErrCancelled) {
		return Result{State: StateCancelled, Output: output, Failure: &Failure{Kind: FailureCancellation, Message: err.Error()}}, nil
	}
	return Result{Output: output}, err
}

func asPark(err error) (*Park, bool) {
	var parkError *ParkError
	if !errors.As(err, &parkError) {
		return nil, false
	}
	kind := parkError.Kind
	if kind == "" {
		if parkError.WaitID != "" {
			kind = ParkWait
		} else {
			kind = ParkLookup
		}
	}
	return &Park{
		Kind:     kind,
		RunID:    parkError.RunID,
		WaitID:   parkError.WaitID,
		Sequence: parkError.Sequence,
		Reason:   parkError.Reason,
	}, true
}

func parkError(park *Park) error {
	if park == nil {
		return ErrParked
	}
	return &ParkError{
		Kind:     park.Kind,
		RunID:    park.RunID,
		WaitID:   park.WaitID,
		Sequence: park.Sequence,
		Reason:   park.Reason,
	}
}
