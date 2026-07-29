package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func reclaimBridgeFootprint(attemptID string, dirty uint64) enginecontract.FootprintObservation {
	return enginecontract.FootprintObservation{
		AttemptID: attemptID, Backend: "wazero", Strategy: enginecontract.StrategyCOWReadySingleUse,
		Status: enginecontract.FootprintObserved, SampledAt: time.Unix(700, 0).UTC(),
		Memory: enginecontract.MemoryFootprint{MappingCount: 1, VirtualBytes: 128 << 20, RSSBytes: dirty, PSSBytes: dirty, PrivateDirtyBytes: dirty, AnonymousBytes: dirty},
	}
}

func reclaimBridgeObservation(attemptID string, status enginecontract.ReclaimStatus, code string) enginecontract.MemoryReclaimObservation {
	return enginecontract.MemoryReclaimObservation{
		AttemptID: attemptID, ObservedAt: time.Unix(701, 0).UTC(), Backend: "linux_proc_smaps",
		Strategy: enginecontract.StrategyCOWReadySingleUse, Status: status, ErrorCode: code,
	}
}

func TestReclaimEvidenceBridgeForwardsProfileSamples(t *testing.T) {
	profileOptions := profileConfig()
	store, err := NewProfileStore(profileOptions)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	if _, err := store.EnsureProfile(profile); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 1, Profiles: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterAttempt("attempt:profile", profile); err != nil {
		t.Fatal(err)
	}
	sink := bridge.FootprintSink()
	if !sink.ShouldSample("attempt:profile") {
		t.Fatal("profile sink sampling decision was lost")
	}
	sink.Observe(reclaimBridgeFootprint("attempt:profile", 31))
	if snapshot := store.Snapshot(); snapshot.ObservedSamples != 1 || snapshot.TrackedAttempts != 0 {
		t.Fatalf("profile snapshot=%#v", snapshot)
	}
}

func TestReclaimEvidenceBridgeCorrelatesReleasedMapping(t *testing.T) {
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 2})
	if err != nil {
		t.Fatal(err)
	}
	if bridge.ShouldSample("attempt:1") || bridge.ShouldObserve("attempt:1") {
		t.Fatal("untracked attempt was sampled")
	}
	if err := bridge.Track("attempt:1"); err != nil {
		t.Fatal(err)
	}
	if !bridge.ShouldSample("attempt:1") || !bridge.ShouldObserve("attempt:1") {
		t.Fatal("tracked attempt was not sampled")
	}
	bridge.FootprintSink().Observe(reclaimBridgeFootprint("attempt:1", 37))
	bridge.ReclaimSink().ObserveReclaim(reclaimBridgeObservation("attempt:1", enginecontract.ReclaimReleased, ""))
	report, err := bridge.Observe(context.Background(), Termination{AttemptID: "attempt:1", ExecutorTerminated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.ExecutorTerminated || report.ObservedFootprintBytes != 37 || report.ReclaimedBytes != 37 || bridge.ShouldObserve("attempt:1") {
		t.Fatalf("report=%#v stillTracked=%v", report, bridge.ShouldObserve("attempt:1"))
	}
}

func TestReclaimEvidenceBridgeRejectsFailedReclaim(t *testing.T) {
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Track("attempt:1"); err != nil {
		t.Fatal(err)
	}
	bridge.FootprintSink().Observe(reclaimBridgeFootprint("attempt:1", 37))
	bridge.ReclaimSink().ObserveReclaim(reclaimBridgeObservation("attempt:1", enginecontract.ReclaimStillMapped, "mapping_present"))
	if _, err := bridge.Observe(context.Background(), Termination{AttemptID: "attempt:1", ExecutorTerminated: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Observe() error=%v", err)
	}
}

func TestReclaimEvidenceBridgeWaitIsCancelable(t *testing.T) {
	bridge, err := NewReclaimEvidenceBridge(ReclaimEvidenceBridgeConfig{MaxTracked: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := bridge.Track("attempt:1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bridge.Observe(ctx, Termination{AttemptID: "attempt:1", ExecutorTerminated: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Observe() error=%v", err)
	}
}
