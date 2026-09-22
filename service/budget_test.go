package service

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestExecutionBudgetBounds(t *testing.T) {
	for _, v := range []int64{0, -1, 101, math.MaxInt64} {
		if _, err := runDuration(&v, 100*time.Millisecond); err == nil {
			t.Fatalf("accepted timeout %d", v)
		}
	}
	short := int64(10)
	if got, err := runDuration(&short, 100*time.Millisecond); err != nil || got != 10*time.Millisecond {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := runDuration(nil, 0); err != nil || got != 0 {
		t.Fatalf("default changed: %v %v", got, err)
	}
	deadline := time.Now().Add(time.Second)
	parent, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	server := &Server{maxRunDuration: time.Minute}
	ctx, stop, err := server.executionContext(parent, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatal("extended parent deadline")
	}
}
