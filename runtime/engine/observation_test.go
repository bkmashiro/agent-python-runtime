package engine_test

import (
	"context"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func TestObservationContextRoundTrip(t *testing.T) {
	session, err := observe.NewSession(observe.Off, nil, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := engine.WithObservationSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := engine.ObservationSessionFromContext(ctx)
	if !ok || got != session {
		t.Fatalf("got=%p ok=%v", got, ok)
	}
	if _, err := engine.WithObservationSession(context.Background(), nil); err == nil {
		t.Fatal("nil session accepted")
	}
}
