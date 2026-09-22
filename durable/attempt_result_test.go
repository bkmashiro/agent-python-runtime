package durable

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

func TestClassifyAttemptResultReturnsControlStatesAsData(t *testing.T) {
	output := pysolate.Output{Value: json.RawMessage(`{"ok":true}`)}
	cases := []struct {
		name    string
		err     error
		state   State
		failure FailureKind
		parked  bool
	}{
		{name: "completed", state: StateCompleted},
		{name: "parked", err: &ParkError{Kind: ParkWait, RunID: "run", WaitID: "wait", Reason: "approval"}, state: StateParked, parked: true},
		{name: "blocked", err: errors.Join(ErrBlocked, errors.New("history diverged")), state: StateBlocked, failure: FailureRecovery},
		{name: "python failed", err: &pysolate.PythonError{Message: "bad input"}, state: StateFailed, failure: FailurePython},
		{name: "cancelled", err: ErrCancelled, state: StateCancelled, failure: FailureCancellation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := classifyAttemptResult(output, tc.err)
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tc.state || (result.Park != nil) != tc.parked {
				t.Fatalf("result=%+v", result)
			}
			if tc.failure == "" {
				if result.Failure != nil {
					t.Fatalf("unexpected failure: %+v", result.Failure)
				}
			} else if result.Failure == nil || result.Failure.Kind != tc.failure || result.Failure.Message == "" {
				t.Fatalf("failure=%+v", result.Failure)
			}
		})
	}
}

func TestClassifyAttemptResultKeepsInfrastructureFailureAsError(t *testing.T) {
	want := errors.New("sqlite unavailable")
	result, err := classifyAttemptResult(pysolate.Output{}, want)
	if !errors.Is(err, want) || result.State != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAttemptWaitPreservesLegacyErrorsForStructuredStates(t *testing.T) {
	cases := []struct {
		name   string
		result Result
		check  func(error) bool
	}{
		{name: "parked", result: Result{State: StateParked, Park: &Park{Kind: ParkWait, RunID: "run"}}, check: func(err error) bool { return errors.Is(err, ErrParked) }},
		{name: "blocked", result: Result{State: StateBlocked, Failure: &Failure{Kind: FailureRecovery, Message: "unsafe outcome"}}, check: func(err error) bool { return errors.Is(err, ErrBlocked) }},
		{name: "python", result: Result{State: StateFailed, Failure: &Failure{Kind: FailurePython, Message: "bad input"}}, check: func(err error) bool {
			var pythonErr *pysolate.PythonError
			return errors.As(err, &pythonErr) && pythonErr.Message == "bad input"
		}},
		{name: "cancelled", result: Result{State: StateCancelled, Failure: &Failure{Kind: FailureCancellation, Message: "cancelled"}}, check: func(err error) bool { return errors.Is(err, ErrCancelled) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempt := &Attempt{done: make(chan struct{}), result: tc.result}
			close(attempt.done)
			_, err := attempt.Wait(context.Background())
			if !tc.check(err) {
				t.Fatalf("unexpected error=%v", err)
			}
		})
	}
}
