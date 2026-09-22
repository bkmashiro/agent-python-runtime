package perfdiag

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledContextDoesNotRecord(t *testing.T) {
	span := Start(context.Background(), "disabled")
	span.End()
	var collector *Collector
	if got := collector.Snapshot(); got != nil {
		t.Fatalf("nil collector snapshot = %#v", got)
	}
}

func TestDisabledStartEndDoesNotAllocate(t *testing.T) {
	allocs := testing.AllocsPerRun(1000, func() {
		span := Start(context.Background(), "disabled")
		span.End()
	})
	if allocs != 0 {
		t.Fatalf("disabled Start/End allocations = %v, want 0", allocs)
	}
}

func TestSpanCountsSuccessAndErrorPhases(t *testing.T) {
	collector := NewCollector()
	ctx := WithCollector(context.Background(), collector)
	run := func(phase string, fail bool) error {
		span := Start(ctx, phase)
		defer span.End()
		if fail {
			return errors.New("expected phase error")
		}
		return nil
	}
	if err := run("success", false); err != nil {
		t.Fatal(err)
	}
	if err := run("error", true); err == nil {
		t.Fatal("expected phase error")
	}
	entries := collector.Snapshot()
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	for _, entry := range entries {
		if entry.Stats.Count != 1 {
			t.Fatalf("phase %q count=%d, want 1", entry.Phase, entry.Stats.Count)
		}
	}
}

func TestCollectorAggregatesConcurrentPhases(t *testing.T) {
	collector := NewCollector()
	ctx := WithCollector(context.Background(), collector)
	const workers = 8
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			span := Start(ctx, "execute")
			time.Sleep(time.Microsecond)
			span.End()
			collector.Add("tool.service", time.Nanosecond)
			done <- struct{}{}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	entries := collector.Snapshot()
	if len(entries) != 2 || entries[0].Phase != "execute" || entries[0].Stats.Count != workers || entries[1].Stats.Count != workers {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestWriteAllocationDeltaAndPhases(t *testing.T) {
	dir := t.TempDir()
	allocPath := filepath.Join(dir, "alloc.json")
	phasePath := filepath.Join(dir, "phases.json")
	if err := WriteAllocationDelta(allocPath, AllocationStats{TotalAlloc: 10, Mallocs: 2, Frees: 1}, AllocationStats{TotalAlloc: 25, Mallocs: 5, Frees: 3, HeapAlloc: 7}); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(allocPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Kind      string          `json:"kind"`
		Measured  AllocationStats `json:"measured_delta"`
		HeapScope string          `json:"heap_scope"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != "measured_allocation_delta" || payload.Measured.TotalAlloc != 15 || payload.Measured.Mallocs != 3 || payload.Measured.Frees != 2 || payload.Measured.HeapAlloc != 7 || payload.HeapScope == "" {
		t.Fatalf("allocation payload=%+v", payload)
	}
	collector := NewCollector()
	collector.Add("marshal", time.Nanosecond)
	if err := WritePhases(phasePath, collector.Snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAllocationSnapshotProducesProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allocs.pb.gz")
	if err := WriteAllocationSnapshot(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("allocation profile is empty")
	}
}
