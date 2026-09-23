package durable

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

func TestReplayMismatchIsTypedAndPayloadFree(t *testing.T) {
	err := replayMismatchAt(ReplayMismatchLocationCall, ReplayMismatchReasonCallArguments, 7)
	var mismatch *ReplayMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, ErrBundleMismatch) {
		t.Fatalf("err=%v details=%+v", err, mismatch)
	}
	if mismatch.Location != ReplayMismatchLocationCall || mismatch.Reason != ReplayMismatchReasonCallArguments || !mismatch.HasSequence || mismatch.Sequence != 7 {
		t.Fatalf("details=%+v", mismatch)
	}
	if got := err.Error(); got != "offline replay mismatch: location=call reason=arguments sequence=7" {
		t.Fatalf("safe error=%q", got)
	}
	for _, private := range []string{"secret", "different", "exception text"} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("mismatch leaked %q: %v", private, err)
		}
	}
}

func TestOfflineJournalReportsFirstTypedCallReason(t *testing.T) {
	base := BundleCall{
		Sequence: 0, CallID: "call-0", Tool: "expected",
		OperationKey: operationKey("run", 0), Arguments: json.RawMessage(`{"secret":"saved"}`),
		State: CallCompleted, Outcome: json.RawMessage(`{"value":true}`),
	}
	for _, test := range []struct {
		name   string
		tool   string
		args   json.RawMessage
		reason ReplayMismatchReason
	}{
		{"tool", "other", base.Arguments, ReplayMismatchReasonCallTool},
		{"operation key", "expected", base.Arguments, ReplayMismatchReasonCallOperationKey},
		{"arguments", "expected", json.RawMessage(`{"secret":"changed"}`), ReplayMismatchReasonCallArguments},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := base
			if test.name == "operation key" {
				call.OperationKey = "wrong"
			}
			journal := &offlineJournal{runID: "run", calls: []BundleCall{call}}
			_, err := journal.Call(context.Background(), test.tool, test.args, nil)
			var mismatch *ReplayMismatchError
			if !errors.As(err, &mismatch) || mismatch.Location != ReplayMismatchLocationCall || mismatch.Reason != test.reason || mismatch.Sequence != 0 {
				t.Fatalf("err=%v details=%+v", err, mismatch)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "changed") {
				t.Fatalf("call mismatch leaked payload: %v", err)
			}
		})
	}

	journal := &offlineJournal{runID: "run", calls: []BundleCall{base}}
	if _, err := journal.Call(context.Background(), "expected", base.Arguments, nil); err != nil {
		t.Fatal(err)
	}
	_, err := journal.Call(context.Background(), "expected", base.Arguments, nil)
	var mismatch *ReplayMismatchError
	if !errors.As(err, &mismatch) || mismatch.Reason != ReplayMismatchReasonCallExcess || mismatch.Sequence != 1 {
		t.Fatalf("excess err=%v details=%+v", err, mismatch)
	}
}

func TestCompareReplayReportsTypedTerminalReason(t *testing.T) {
	expected := pysolate.Output{Value: json.RawMessage(`{"answer":42}`), Stdout: "safe\n", Transformed: "ast"}
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	run := BundleRun{Status: StatusCompleted, Outcome: encoded}
	for _, test := range []struct {
		name   string
		actual pysolate.Output
		reason ReplayMismatchReason
	}{
		{"value", pysolate.Output{Value: json.RawMessage(`{"answer":0}`), Stdout: "safe\n", Transformed: "ast"}, ReplayMismatchReasonTerminalValue},
		{"stdout", pysolate.Output{Value: expected.Value, Stdout: "private\n", Transformed: "ast"}, ReplayMismatchReasonTerminalStdout},
		{"transformed", pysolate.Output{Value: expected.Value, Stdout: expected.Stdout, Transformed: "other"}, ReplayMismatchReasonTerminalTransformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := compareReplay(run, test.actual, nil)
			var mismatch *ReplayMismatchError
			if !errors.As(err, &mismatch) || mismatch.Location != ReplayMismatchLocationTerminal || mismatch.Reason != test.reason || mismatch.HasSequence {
				t.Fatalf("err=%v details=%+v", err, mismatch)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "other") {
				t.Fatalf("terminal mismatch leaked payload: %v", err)
			}
		})
	}
}
