package durable

import (
	"context"
	"errors"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

// AttemptState describes a successful control-plane transition. Execution
// failures remain errors; parking is an expected, resumable result.
type AttemptState string

const (
	AttemptCompleted AttemptState = "completed"
	AttemptParked    AttemptState = "parked"
)

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

// AdvanceResult is the result of one fresh deterministic attempt.
type AdvanceResult struct {
	State  AttemptState
	Output pysolate.Output
	Park   *Park
}

// AttemptController is the narrow lifecycle boundary needed by admission and
// scheduling. Implementations own persistence, replay, effects, and Guests.
type AttemptController interface {
	Advance(context.Context, string) (AdvanceResult, error)
	Cancel(context.Context, string) error
}

// Advance executes one attempt and represents durable parking as data rather
// than an error. Resume remains available for compatibility with direct callers.
func (runner *Runner) Advance(ctx context.Context, runID string) (AdvanceResult, error) {
	output, err := runner.Resume(ctx, runID)
	if err == nil {
		return AdvanceResult{State: AttemptCompleted, Output: output}, nil
	}
	parked, ok := asPark(err)
	if ok {
		return AdvanceResult{State: AttemptParked, Output: output, Park: parked}, nil
	}
	return AdvanceResult{Output: output}, err
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
