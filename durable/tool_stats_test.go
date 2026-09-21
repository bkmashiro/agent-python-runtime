package durable

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestToolLatencyStatsAggregatesStableBucketsAndEWMA(t *testing.T) {
	stats, err := NewToolLatencyStats(0.5)
	if err != nil {
		t.Fatal(err)
	}
	stats.ObserveTool(ToolObservation{
		Name: "catalog.read", Version: "v1", Operation: ToolCall,
		Scheduling: ExternalIO, ArgumentBytes: 100,
		QueueDuration: 10 * time.Millisecond, ServiceDuration: 20 * time.Millisecond, ResumeDuration: 5 * time.Millisecond,
		Outcome: ToolSucceeded,
	})
	stats.ObserveTool(ToolObservation{
		Name: "catalog.read", Version: "v1", Operation: ToolCall,
		Scheduling: ExternalIO, ArgumentBytes: 100,
		QueueDuration: 30 * time.Millisecond, ServiceDuration: 40 * time.Millisecond, ResumeDuration: 15 * time.Millisecond,
		Outcome: ToolFailed,
	})
	stats.ObserveTool(ToolObservation{
		Name: "catalog.read", Version: "v1", Operation: ToolLookup,
		Scheduling: ExternalIO, ArgumentBytes: 20 << 10,
		ServiceDuration: time.Millisecond,
		Outcome:         ToolCancelled,
	})

	snapshot := stats.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	call := snapshot[0]
	if call.Operation != ToolCall || call.ArgumentBucket != "le_1k" || call.Count != 2 || call.Failures != 1 || call.Cancellations != 0 {
		t.Fatalf("call estimate=%+v", call)
	}
	if call.MeanQueue != 20*time.Millisecond || call.MeanService != 30*time.Millisecond || call.MeanResume != 10*time.Millisecond {
		t.Fatalf("call means=%+v", call)
	}
	if call.EWMAQueue != 20*time.Millisecond || call.EWMAService != 30*time.Millisecond || call.EWMAResume != 10*time.Millisecond {
		t.Fatalf("call ewma=%+v", call)
	}
	lookup := snapshot[1]
	if lookup.Operation != ToolLookup || lookup.ArgumentBucket != "le_256k" || lookup.Count != 1 || lookup.Cancellations != 1 {
		t.Fatalf("lookup estimate=%+v", lookup)
	}
}

func TestNewToolLatencyStatsRejectsInvalidAlpha(t *testing.T) {
	for _, alpha := range []float64{-0.1, 0, 1.1} {
		if _, err := NewToolLatencyStats(alpha); err == nil {
			t.Fatalf("alpha %v accepted", alpha)
		}
	}
	if _, err := NewToolLatencyStats(1); err != nil {
		t.Fatalf("alpha 1 rejected: %v", err)
	}
}

func TestScheduledToolObservesInlineSuccessAndCancellation(t *testing.T) {
	var observations []ToolObservation
	observer := ToolObserverFunc(func(observation ToolObservation) {
		observations = append(observations, observation)
	})
	tool := Tool{
		Name: "remote", Version: "v2", Scheduling: Inline, Observer: observer,
		Call: func(context.Context, json.RawMessage) (any, error) {
			time.Sleep(time.Millisecond)
			return 42, nil
		},
	}
	call := scheduledTool(tool)
	value, err := call(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil || value != 42 {
		t.Fatalf("value=%v err=%v", value, err)
	}

	cancelled := tool
	cancelled.Call = func(context.Context, json.RawMessage) (any, error) { return nil, context.Canceled }
	if _, err := scheduledTool(cancelled)(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("observations=%+v", observations)
	}
	if observations[0].Name != "remote" || observations[0].Version != "v2" || observations[0].Operation != ToolCall || observations[0].Outcome != ToolSucceeded || observations[0].ArgumentBytes != 7 || observations[0].ServiceDuration <= 0 {
		t.Fatalf("success observation=%+v", observations[0])
	}
	if observations[0].QueueDuration != 0 || observations[0].ResumeDuration != 0 {
		t.Fatalf("inline observation has scheduler waits: %+v", observations[0])
	}
	if observations[1].Outcome != ToolCancelled {
		t.Fatalf("cancel observation=%+v", observations[1])
	}
}
