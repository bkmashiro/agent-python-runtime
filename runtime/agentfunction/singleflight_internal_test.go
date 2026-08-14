package agentfunction

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledWaiterRejectsAlreadyCompletedFlight(t *testing.T) {
	for attempt := 0; attempt < 256; attempt++ {
		done := make(chan struct{})
		close(done)
		group := NewFlightGroup()
		group.flights["key"] = &flight{done: done, result: Result{Value: []byte("success")}}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := group.Do(ctx, "key", func() (Result, error) {
			return Result{}, errors.New("unexpected leader")
		})
		if !errors.Is(err, context.Canceled) || len(result.Value) != 0 {
			t.Fatalf("attempt=%d result=%+v err=%v", attempt, result, err)
		}
	}
}
