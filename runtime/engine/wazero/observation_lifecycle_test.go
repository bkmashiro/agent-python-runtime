package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

type recoveringObservationRecorder struct {
	failures int
	events   []observe.Event
}

func (recorder *recoveringObservationRecorder) Append(_ context.Context, event observe.Event) error {
	if recorder.failures > 0 {
		recorder.failures--
		return errors.New("sink unavailable")
	}
	recorder.events = append(recorder.events, event)
	return nil
}

func TestBestEffortObservationRecoversAfterStartFailureWithIncompleteTerminalEvent(t *testing.T) {
	recorder := &recoveringObservationRecorder{failures: 1}
	session, err := observe.NewSession(observe.BestEffort, recorder, "exec-recovery")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := observationLifecycle{session: session}
	if err := lifecycle.start(context.Background(), observe.ExecutionStartedPayload{ExecutedCodeSHA256: observationDigest('a')}); err != nil {
		t.Fatal(err)
	}
	if !session.Incomplete() {
		t.Fatal("failed best-effort start did not mark session incomplete")
	}
	if err := lifecycle.complete(context.Background(), observationDigest('b')); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != observe.EventExecutionCompleted || recorder.events[0].Sequence != 1 || recorder.events[0].ParentSequence != nil {
		t.Fatalf("events=%+v", recorder.events)
	}
	var payload observe.ExecutionCompletedPayload
	if err := json.Unmarshal(recorder.events[0].Payload, &payload); err != nil || payload.EvidenceComplete {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func TestRequiredObservationFailurePoisonsRecoveredTerminalEvidence(t *testing.T) {
	recorder := &recoveringObservationRecorder{failures: 1}
	session, err := observe.NewSession(observe.Required, recorder, "exec-required-recovery")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := observationLifecycle{session: session}
	if err := lifecycle.start(context.Background(), observe.ExecutionStartedPayload{ExecutedCodeSHA256: observationDigest('a')}); !errors.Is(err, ErrObservationEvidenceInvalid) {
		t.Fatalf("required start err=%v", err)
	}
	if err := lifecycle.fail(context.Background(), "observation"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.events) != 1 || recorder.events[0].Type != observe.EventExecutionFailed || recorder.events[0].ParentSequence != nil {
		t.Fatalf("events=%+v", recorder.events)
	}
	var payload observe.ExecutionFailedPayload
	if err := json.Unmarshal(recorder.events[0].Payload, &payload); err != nil || payload.EvidenceComplete {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func observationDigest(character byte) string {
	value := make([]byte, 71)
	copy(value, "sha256:")
	for index := len("sha256:"); index < len(value); index++ {
		value[index] = character
	}
	return string(value)
}
