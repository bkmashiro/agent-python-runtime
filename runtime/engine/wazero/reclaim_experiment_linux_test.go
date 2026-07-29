//go:build linux

package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"golang.org/x/sys/unix"
)

type e1ObservationSink struct {
	mu         sync.Mutex
	footprints map[string]enginecontract.FootprintObservation
	reclaims   map[string]enginecontract.MemoryReclaimObservation
}

func newE1ObservationSink() *e1ObservationSink {
	return &e1ObservationSink{
		footprints: make(map[string]enginecontract.FootprintObservation),
		reclaims:   make(map[string]enginecontract.MemoryReclaimObservation),
	}
}

func (*e1ObservationSink) ShouldSample(string) bool  { return true }
func (*e1ObservationSink) ShouldObserve(string) bool { return true }
func (sink *e1ObservationSink) Observe(observation enginecontract.FootprintObservation) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.footprints[observation.AttemptID] = observation
}
func (sink *e1ObservationSink) ObserveReclaim(observation enginecontract.MemoryReclaimObservation) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.reclaims[observation.AttemptID] = observation
}
func (sink *e1ObservationSink) reclaimObservation(attemptID string) (enginecontract.MemoryReclaimObservation, bool) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	reclaim, ok := sink.reclaims[attemptID]
	return reclaim, ok
}

type e1Sample struct {
	AttemptID           string `json:"attempt_id"`
	CancelToReturnNS    int64  `json:"cancel_to_return_ns"`
	CancelToReclaimedNS int64  `json:"cancel_to_reclaimed_ns"`
	CloseDurationNS     int64  `json:"close_duration_ns"`
	RefillAfterReturnNS int64  `json:"refill_after_return_ns"`
	VirtualBytes        uint64 `json:"virtual_bytes"`
	RSSBytes            uint64 `json:"rss_bytes"`
	PSSBytes            uint64 `json:"pss_bytes"`
	PrivateDirtyBytes   uint64 `json:"private_dirty_bytes"`
	AnonymousBytes      uint64 `json:"anonymous_bytes"`
	MappingCount        uint32 `json:"mapping_count"`
}

type e1Quantiles struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
	Max int64 `json:"max"`
}

type e1Report struct {
	Schema                  string      `json:"schema"`
	DirtyPercent            int         `json:"dirty_percent"`
	Repetitions             int         `json:"repetitions"`
	MemoryPages             uint32      `json:"memory_pages"`
	MemoryBytes             uint64      `json:"memory_bytes"`
	ExpectedDirtyFloorBytes uint64      `json:"expected_dirty_floor_bytes"`
	CancelToReturnNS        e1Quantiles `json:"cancel_to_return_ns"`
	CancelToReclaimedNS     e1Quantiles `json:"cancel_to_reclaimed_ns"`
	CloseDurationNS         e1Quantiles `json:"close_duration_ns"`
	RefillAfterReturnNS     e1Quantiles `json:"refill_after_return_ns"`
	Samples                 []e1Sample  `json:"samples"`
}

func TestEngineCancellationUnmapsServedCOWSlot(t *testing.T) {
	dirtyPercent := boundedE1Env(t, "APYRUN_E1_DIRTY_PERCENT", 25, 1, 100)
	repetitions := boundedE1Env(t, "APYRUN_E1_REPETITIONS", 1, 1, 100)
	memoryPages := uint32(boundedE1Env(t, "APYRUN_E1_MEMORY_PAGES", int(e1DefaultMemoryPages), 1, 2048))
	sink := newE1ObservationSink()
	runner, err := (Factory{
		PreparedCapacity:    1,
		PreparedMaxCapacity: 1,
		Strategy:            enginecontract.StrategyCOWReadySingleUse,
		ReclaimSink:         sink,
	}).New(context.Background(), e1DirtyLoopReactor(dirtyPercent, uint64(unix.Getpagesize()), memoryPages), runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	engine := runner.(*Engine)
	defer engine.Close(context.Background())

	memoryBytes := uint64(memoryPages) * wasmLinearPageSize
	requestedDirtyBytes := memoryBytes * uint64(dirtyPercent) / 100
	pageSize := uint64(unix.Getpagesize())
	expectedDirtyFloor := requestedDirtyBytes - requestedDirtyBytes%pageSize
	if expectedDirtyFloor > pageSize {
		expectedDirtyFloor -= pageSize
	}
	samples := make([]e1Sample, 0, repetitions)
	for repetition := 0; repetition < repetitions; repetition++ {
		attemptID := fmt.Sprintf("e1:%d:%d:%d", memoryPages, dirtyPercent, repetition)
		ctx, cancelParent := context.WithCancel(context.Background())
		ctx, err = enginecontract.WithAttemptIdentity(ctx, attemptID)
		if err != nil {
			t.Fatal(err)
		}
		runDone := make(chan error, 1)
		go func() {
			_, runErr := engine.Run(ctx, []byte(`{"run_id":"e1","code":"loop","inputs":{}}`), "")
			runDone <- runErr
		}()
		waitForE1Attempt(t, engine, runDone)
		var footprint enginecontract.FootprintObservation
		waitForE1(t, 2*time.Second, func() bool {
			observation, sampleErr := engine.SampleActiveFootprint(attemptID)
			if sampleErr != nil || observation.Status != enginecontract.FootprintObserved {
				return false
			}
			footprint = observation
			return footprint.Memory.VirtualBytes == memoryBytes && footprint.Memory.PrivateDirtyBytes >= expectedDirtyFloor
		}, "active mapping did not reach deterministic dirty floor")
		cancelAt := time.Now().UTC()
		cancelParent()
		var runErr error
		select {
		case runErr = <-runDone:
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled attempt did not terminate")
		}
		returnedAt := time.Now().UTC()
		if runErr == nil || !errors.Is(runErr, context.Canceled) && !strings.Contains(runErr.Error(), "context canceled") {
			t.Fatalf("cancel error = %v", runErr)
		}
		reclaim, ok := sink.reclaimObservation(attemptID)
		if !ok {
			t.Fatalf("missing reclaim observation for %s", attemptID)
		}
		if err := footprint.Validate(); err != nil || footprint.Status != enginecontract.FootprintObserved {
			t.Fatalf("footprint = %#v err=%v", footprint, err)
		}
		if err := reclaim.Validate(); err != nil || reclaim.Status != enginecontract.ReclaimReleased {
			t.Fatalf("reclaim = %#v err=%v", reclaim, err)
		}
		if footprint.Memory.VirtualBytes != memoryBytes || footprint.Memory.PrivateDirtyBytes < expectedDirtyFloor {
			t.Fatalf("dirty footprint below deterministic floor: got=%#v floor=%d", footprint.Memory, expectedDirtyFloor)
		}
		if _, err := engine.SampleActiveFootprint(attemptID); !errors.Is(err, errActiveFootprintNotFound) {
			t.Fatalf("completed attempt remained in active registry: %v", err)
		}
		cancelToReclaimed := reclaim.ObservedAt.Sub(cancelAt)
		if cancelToReclaimed < 0 {
			t.Fatalf("reclaim preceded cancellation: %s", cancelToReclaimed)
		}
		refillStarted := time.Now()
		waitForE1(t, 2*time.Second, func() bool {
			state := engine.PreparedPoolState()
			return state.Ready == 1 && state.Leased == 0 && state.Executing == 0 && state.Retiring == 0
		}, "prepared pool did not refill")
		samples = append(samples, e1Sample{
			AttemptID: attemptID, CancelToReturnNS: returnedAt.Sub(cancelAt).Nanoseconds(),
			CancelToReclaimedNS: cancelToReclaimed.Nanoseconds(), CloseDurationNS: reclaim.CloseDuration.Nanoseconds(),
			RefillAfterReturnNS: time.Since(refillStarted).Nanoseconds(), VirtualBytes: footprint.Memory.VirtualBytes,
			RSSBytes: footprint.Memory.RSSBytes, PSSBytes: footprint.Memory.PSSBytes,
			PrivateDirtyBytes: footprint.Memory.PrivateDirtyBytes, AnonymousBytes: footprint.Memory.AnonymousBytes,
			MappingCount: footprint.Memory.MappingCount,
		})
	}
	report := e1Report{
		Schema: "apyrun.e1-cancel-reclaim/v1", DirtyPercent: dirtyPercent, Repetitions: repetitions,
		MemoryPages: memoryPages, MemoryBytes: memoryBytes, ExpectedDirtyFloorBytes: expectedDirtyFloor, Samples: samples,
		CancelToReturnNS:    quantilesE1(samples, func(sample e1Sample) int64 { return sample.CancelToReturnNS }),
		CancelToReclaimedNS: quantilesE1(samples, func(sample e1Sample) int64 { return sample.CancelToReclaimedNS }),
		CloseDurationNS:     quantilesE1(samples, func(sample e1Sample) int64 { return sample.CloseDurationNS }),
		RefillAfterReturnNS: quantilesE1(samples, func(sample e1Sample) int64 { return sample.RefillAfterReturnNS }),
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("E1_METRIC %s", encoded)
}

func boundedE1Env(t *testing.T, name string, fallback, minimum, maximum int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		t.Fatalf("%s is outside [%d,%d]", name, minimum, maximum)
	}
	return parsed
}

func waitForE1Attempt(t *testing.T, engine *Engine, runDone <-chan error) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case runErr := <-runDone:
			t.Fatalf("Run exited before execute: %v", runErr)
		case <-ticker.C:
			if engine.PreparedPoolState().Executing == 1 {
				return
			}
		case <-deadline.C:
			t.Fatal("attempt did not start")
		}
	}
}

func waitForE1(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}

func quantilesE1(samples []e1Sample, selectValue func(e1Sample) int64) e1Quantiles {
	values := make([]int64, len(samples))
	for index, sample := range samples {
		values[index] = selectValue(sample)
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	pick := func(percent int) int64 {
		rank := (percent*len(values) + 99) / 100
		if rank < 1 {
			rank = 1
		}
		return values[rank-1]
	}
	return e1Quantiles{P50: pick(50), P95: pick(95), P99: pick(99), Max: values[len(values)-1]}
}

var _ enginecontract.FootprintSink = (*e1ObservationSink)(nil)
var _ enginecontract.ReclaimSink = (*e1ObservationSink)(nil)
