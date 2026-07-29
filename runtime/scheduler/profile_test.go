package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func profileConfig() ProfileConfig {
	return ProfileConfig{
		HardBytes:               128 << 20,
		UnknownReservationBytes: 96 << 20,
		PerAttemptMarginBytes:   5,
		MaxProfiles:             8,
		MaxTrackedAttempts:      8,
		MaxSamplesPerProfile:    8,
		MaxAggregateSamples:     32,
		ColdRuns:                2,
		StableSampleEvery:       3,
		MinimumSamples:          2,
		ReservationQuantileBPS:  10000,
	}
}

func testProfile(artifactCharacter, workload string, bucket RequestSizeBucket) WorkloadProfile {
	return WorkloadProfile{
		ArtifactDigest:    strings.Repeat(artifactCharacter, 64),
		WorkloadClass:     workload,
		RequestSizeBucket: bucket,
		CapabilityPattern: "read_only",
		PolicyClass:       "standard",
	}
}

func observedFootprint(attemptID string, dirty uint64) enginecontract.FootprintObservation {
	return enginecontract.FootprintObservation{
		AttemptID: attemptID, Backend: "wazero", Strategy: enginecontract.StrategyCOWReadySingleUse,
		Status: enginecontract.FootprintObserved, SampledAt: time.Unix(100, 0).UTC(),
		Memory: enginecontract.MemoryFootprint{MappingCount: 1, VirtualBytes: 128 << 20, RSSBytes: dirty, PSSBytes: dirty, PrivateDirtyBytes: dirty, AnonymousBytes: dirty},
	}
}

func TestWorkloadProfileKeyIsCanonicalAndValidated(t *testing.T) {
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	first, err := profile.Key()
	if err != nil {
		t.Fatal(err)
	}
	second, err := profile.Key()
	if err != nil || second != first || !strings.HasPrefix(first, "profile_") || len(first) != len("profile_")+64 {
		t.Fatalf("keys = %q %q err=%v", first, second, err)
	}
	invalid := []WorkloadProfile{
		{},
		testProfile("z", "python_eval", RequestSizeSmall),
		testProfile("a", "contains space", RequestSizeSmall),
		testProfile("a", "python_eval", "arbitrary"),
	}
	for _, value := range invalid {
		if _, err := value.Key(); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("invalid profile %#v error = %v", value, err)
		}
	}
}

func TestProfileStoreUsesColdThenDeterministicSparseSampling(t *testing.T) {
	store, err := NewProfileStore(profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	want := []bool{true, true, false, false, true, false}
	for index, wantSample := range want {
		attemptID := fmt.Sprintf("attempt:%d", index+1)
		if err := store.RegisterAttempt(attemptID, profile); err != nil {
			t.Fatal(err)
		}
		if got := store.ShouldSample(attemptID); got != wantSample {
			t.Fatalf("run %d sample=%v want=%v", index+1, got, wantSample)
		}
		if wantSample {
			if err := store.RecordObservation(observedFootprint(attemptID, uint64(index+1))); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot := store.Snapshot()
	if snapshot.ProfileCount != 1 || snapshot.TrackedAttempts != 0 || snapshot.ObservedSamples != 3 || snapshot.FailedSamples != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestProfileStoreBoundsProfilesAttemptsAndRejectsChangedDuplicate(t *testing.T) {
	config := profileConfig()
	config.MaxProfiles = 1
	config.MaxTrackedAttempts = 1
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	first := testProfile("a", "first", RequestSizeSmall)
	if err := store.RegisterAttempt("attempt:1", first); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterAttempt("attempt:1", first); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterAttempt("attempt:1", testProfile("a", "changed", RequestSizeSmall)); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed duplicate error = %v", err)
	}
	if err := store.RegisterAttempt("attempt:2", first); !errors.Is(err, ErrCapacity) {
		t.Fatalf("attempt bound error = %v", err)
	}
	if !store.ShouldSample("attempt:1") {
		t.Fatal("first cold attempt was not sampled")
	}
	if err := store.RecordObservation(observedFootprint("attempt:1", 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterAttempt("attempt:2", testProfile("b", "second", RequestSizeSmall)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("profile bound error = %v", err)
	}
}

func TestFailedFootprintDoesNotEnterReservationSamples(t *testing.T) {
	store, err := NewProfileStore(profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "python_eval", RequestSizeSmall)
	if err := store.RegisterAttempt("attempt:1", profile); err != nil || !store.ShouldSample("attempt:1") {
		t.Fatalf("register/sample error = %v", err)
	}
	failed := enginecontract.FootprintObservation{
		AttemptID: "attempt:1", Backend: "wazero", Strategy: enginecontract.StrategyCOWReadySingleUse,
		Status: enginecontract.FootprintFailed, SampledAt: time.Unix(100, 0).UTC(), ErrorCode: "read_failed",
	}
	if err := store.RecordObservation(failed); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	if snapshot.ObservedSamples != 0 || snapshot.FailedSamples != 1 || snapshot.TrackedAttempts != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestProfileEstimateUsesExactThenBoundedHierarchicalFallback(t *testing.T) {
	config := profileConfig()
	config.ColdRuns = 100
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	exact := testProfile("a", "python_eval", RequestSizeSmall)
	sibling := testProfile("a", "python_eval", RequestSizeLarge)
	addProfileSamples(t, store, exact, 10)
	addProfileSamples(t, store, sibling, 30, 40)

	estimate, err := store.Estimate(exact)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateWorkload || estimate.SampleCount != 3 || estimate.DirtyQuantileBytes != 40 || estimate.ReservationBytes != 45 {
		t.Fatalf("workload estimate = %#v", estimate)
	}
	addProfileSamples(t, store, exact, 20)
	estimate, err = store.Estimate(exact)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateExact || estimate.SampleCount != 2 || estimate.DirtyQuantileBytes != 20 || estimate.ReservationBytes != 25 {
		t.Fatalf("exact estimate = %#v", estimate)
	}

	artifactFallback := testProfile("a", "new_workload", RequestSizeMedium)
	estimate, err = store.Estimate(artifactFallback)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateArtifact || estimate.DirtyQuantileBytes != 40 || estimate.ReservationBytes != 45 {
		t.Fatalf("artifact estimate = %#v", estimate)
	}
	globalFallback := testProfile("b", "unknown_workload", RequestSizeUnknown)
	estimate, err = store.Estimate(globalFallback)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateGlobal || estimate.DirtyQuantileBytes != 40 || estimate.ReservationBytes != 45 {
		t.Fatalf("global estimate = %#v", estimate)
	}
}

func TestProfileEstimateUsesUnknownWithoutEnoughEvidence(t *testing.T) {
	store, err := NewProfileStore(profileConfig())
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := store.Estimate(testProfile("a", "cold", RequestSizeUnknown))
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateUnknown || estimate.SampleCount != 0 || estimate.ReservationBytes != profileConfig().UnknownReservationBytes {
		t.Fatalf("unknown estimate = %#v", estimate)
	}
}

func TestProfileEstimateKeepsNewestBoundedAggregateSamples(t *testing.T) {
	config := profileConfig()
	config.ColdRuns = 100
	config.MaxAggregateSamples = 2
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	addProfileSamples(t, store, testProfile("a", "old", RequestSizeSmall), 100, 200)
	addProfileSamples(t, store, testProfile("b", "new", RequestSizeSmall), 10, 20)
	estimate, err := store.Estimate(testProfile("c", "cold", RequestSizeUnknown))
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Source != EstimateGlobal || estimate.SampleCount != 2 || estimate.DirtyQuantileBytes != 20 {
		t.Fatalf("bounded recent estimate = %#v", estimate)
	}
}

func TestProfileStoreIsConcurrentSinkSafe(t *testing.T) {
	config := profileConfig()
	config.MaxTrackedAttempts = 128
	config.ColdRuns = 128
	store, err := NewProfileStore(config)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile("a", "concurrent", RequestSizeSmall)
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			attemptID := fmt.Sprintf("concurrent:%d", index)
			if err := store.RegisterAttempt(attemptID, profile); err != nil {
				t.Errorf("RegisterAttempt(%d): %v", index, err)
				return
			}
			if !store.ShouldSample(attemptID) {
				t.Errorf("ShouldSample(%d) = false", index)
				return
			}
			store.Observe(observedFootprint(attemptID, uint64(index+1)))
		}()
	}
	wait.Wait()
	snapshot := store.Snapshot()
	if snapshot.ObservedSamples != 64 || snapshot.TrackedAttempts != 0 || snapshot.DroppedObservations != 0 {
		t.Fatalf("concurrent snapshot = %#v", snapshot)
	}
}

func addProfileSamples(t *testing.T, store *ProfileStore, profile WorkloadProfile, values ...uint64) {
	t.Helper()
	profileKey, err := profile.Key()
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range values {
		attemptID := fmt.Sprintf("seed:%s:%d:%d", profileKey[len("profile_"):len("profile_")+8], index, store.Snapshot().ObservedSamples)
		if err := store.RegisterAttempt(attemptID, profile); err != nil {
			t.Fatal(err)
		}
		if !store.ShouldSample(attemptID) {
			t.Fatalf("seed attempt %q was unexpectedly skipped", attemptID)
		}
		if err := store.RecordObservation(observedFootprint(attemptID, value)); err != nil {
			t.Fatal(err)
		}
	}
}

var _ enginecontract.FootprintSink = (*ProfileStore)(nil)
