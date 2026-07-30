package engine_test

import (
	"context"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestInvocationRefContextRoundTrip(t *testing.T) {
	ref := runtimeconfig.InvocationRef{
		AgentRunID: "agent-run-123", TurnSeq: 4, OutputItemSeq: 2, SegmentSeq: 0,
		InvocationID: "python-invocation-789", InvocationAttempt: 1, ExecutionID: "exec-456",
	}
	ctx, err := engine.WithInvocationRef(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := engine.InvocationRefFromContext(ctx)
	if !ok || got != ref {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
}

func TestInvocationRefContextRejectsPartialOrUnboundedMetadata(t *testing.T) {
	valid := runtimeconfig.InvocationRef{
		AgentRunID: "agent-run", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "execution",
	}
	cases := []runtimeconfig.InvocationRef{
		{},
		{AgentRunID: valid.AgentRunID, InvocationID: valid.InvocationID, ExecutionID: valid.ExecutionID},
		{AgentRunID: "agent run", InvocationID: valid.InvocationID, InvocationAttempt: 1, ExecutionID: valid.ExecutionID},
		{AgentRunID: valid.AgentRunID, InvocationID: valid.InvocationID, InvocationAttempt: 1, ExecutionID: string(make([]byte, 129))},
	}
	for _, candidate := range cases {
		if _, err := engine.WithInvocationRef(context.Background(), candidate); !errors.Is(err, engine.ErrInvalidInvocationRef) {
			t.Fatalf("candidate=%+v err=%v", candidate, err)
		}
	}
}
