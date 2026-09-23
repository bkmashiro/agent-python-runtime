package main

import (
	"errors"
	"fmt"
	"github.com/bkmashiro/agent-python-runtime/durable"
	"strings"
	"testing"
)

func TestCLIMismatchLocationWithoutPayload(t *testing.T) {
	mismatch := &durable.ReplayMismatchError{Location: durable.ReplayMismatchLocationCall, Reason: durable.ReplayMismatchReasonCallArguments, Sequence: 1, HasSequence: true}
	err := fmt.Errorf("private exception and input: %w", mismatch)
	got := cliFailureMessage([]string{"replay"}, err)
	if got != "pysolate-replay: operation failed (category=call reason=arguments sequence=1)" {
		t.Fatal(got)
	}
	mismatch.Reason = "private-json-key"
	if got := cliFailureMessage([]string{"replay"}, err); strings.Contains(got, "private") {
		t.Fatal(got)
	}
	if got := cliFailureMessage([]string{"replay"}, errors.New("private runtime details")); got != "pysolate-replay: operation failed (category=runtime)" {
		t.Fatal(got)
	}
}
