package durable

import (
	"errors"
	"fmt"
)

// ReplayMismatchLocation identifies the privacy-safe part of a strict replay
// that diverged. It never contains source, arguments, outcomes, or exception
// text.
type ReplayMismatchLocation string

const (
	ReplayMismatchLocationArtifact ReplayMismatchLocation = "artifact"
	ReplayMismatchLocationCall     ReplayMismatchLocation = "call"
	ReplayMismatchLocationResult   ReplayMismatchLocation = "result"
	// ReplayMismatchLocationTerminal is retained as a descriptive alias. The
	// wire label is the fixed structural field "result".
	ReplayMismatchLocationTerminal ReplayMismatchLocation = ReplayMismatchLocationResult
)

// ReplayMismatchReason identifies the first structural difference at Location.
// The values are deliberately coarse so they can be logged or shown by a CLI
// without exposing protected replay payloads.
type ReplayMismatchReason string

const (
	ReplayMismatchReasonArtifactIdentity    ReplayMismatchReason = "artifact"
	ReplayMismatchReasonCallExcess          ReplayMismatchReason = "excess"
	ReplayMismatchReasonCallUnconsumed      ReplayMismatchReason = "unconsumed"
	ReplayMismatchReasonCallSequence        ReplayMismatchReason = "sequence"
	ReplayMismatchReasonCallID              ReplayMismatchReason = "call_id"
	ReplayMismatchReasonCallTool            ReplayMismatchReason = "tool"
	ReplayMismatchReasonCallOperationKey    ReplayMismatchReason = "operation_key"
	ReplayMismatchReasonCallArguments       ReplayMismatchReason = "arguments"
	ReplayMismatchReasonResult              ReplayMismatchReason = "result"
	ReplayMismatchReasonTerminalValue       ReplayMismatchReason = ReplayMismatchReasonResult
	ReplayMismatchReasonTerminalStdout      ReplayMismatchReason = "stdout"
	ReplayMismatchReasonTerminalTransformed ReplayMismatchReason = "transformed"
	ReplayMismatchReasonTerminalError       ReplayMismatchReason = "error"
)

// ReplayMismatchError is the safe, typed diagnostic for a strict offline
// replay divergence. Sequence is meaningful only for a call mismatch. It is
// intentionally limited to structural location/reason data; callers that are
// authorized to inspect payloads must do so from the private bundle itself.
type ReplayMismatchError struct {
	Location    ReplayMismatchLocation
	Reason      ReplayMismatchReason
	Sequence    uint32
	HasSequence bool
}

func (err *ReplayMismatchError) Error() string {
	if err == nil {
		return ErrBundleMismatch.Error()
	}
	if err.HasSequence {
		return fmt.Sprintf("%s: location=%s reason=%s sequence=%d", ErrBundleMismatch, err.Location, err.Reason, err.Sequence)
	}
	return fmt.Sprintf("%s: location=%s reason=%s", ErrBundleMismatch, err.Location, err.Reason)
}

func (err *ReplayMismatchError) Unwrap() error { return ErrBundleMismatch }

func replayMismatch(location ReplayMismatchLocation, reason ReplayMismatchReason, sequence *uint32) error {
	err := &ReplayMismatchError{Location: location, Reason: reason}
	if sequence != nil {
		err.Sequence = *sequence
		err.HasSequence = true
	}
	return err
}

func replayMismatchAt(location ReplayMismatchLocation, reason ReplayMismatchReason, sequence uint32) error {
	return replayMismatch(location, reason, &sequence)
}

// ReplayErrorCategory is the stable, privacy-safe category used by the
// pysolate-replay CLI. It is intentionally coarser than ReplayMismatchReason.
type ReplayErrorCategory string

const (
	ReplayErrorCategoryUsage    ReplayErrorCategory = "usage"
	ReplayErrorCategoryBundle   ReplayErrorCategory = "bundle"
	ReplayErrorCategoryArtifact ReplayErrorCategory = "artifact"
	ReplayErrorCategoryCall     ReplayErrorCategory = "call"
	ReplayErrorCategoryResult   ReplayErrorCategory = "result"
	// ReplayErrorCategoryTerminal is retained as a descriptive alias.
	ReplayErrorCategoryTerminal ReplayErrorCategory = ReplayErrorCategoryResult
	ReplayErrorCategoryRuntime  ReplayErrorCategory = "runtime"
)

// ErrorCategory classifies an operation failure without formatting its error
// text. It is safe to put in logs or a machine-readable CLI response.
func ErrorCategory(err error) ReplayErrorCategory {
	if err == nil {
		return ""
	}
	var mismatch *ReplayMismatchError
	if errors.As(err, &mismatch) && mismatch != nil {
		switch mismatch.Location {
		case ReplayMismatchLocationArtifact:
			return ReplayErrorCategoryArtifact
		case ReplayMismatchLocationCall:
			return ReplayErrorCategoryCall
		case ReplayMismatchLocationTerminal:
			return ReplayErrorCategoryTerminal
		}
		return ReplayErrorCategoryRuntime
	}
	if errors.Is(err, ErrBundleInvalid) {
		return ReplayErrorCategoryBundle
	}
	return ReplayErrorCategoryRuntime
}
