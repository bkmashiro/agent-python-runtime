package prepareddataset

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func newTestStagedObject(label string) (*StagedObject, error) {
	digest := func(value string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value))) }
	return NewStagedObject(HostReceipt{
		ContractSHA256: digest("contract:" + label), PreparationSHA256: digest("prepare:" + label),
		FileSHA256: CanonicalFileSHA256, BodySHA256: CanonicalBodySHA256,
		ExecutionProfileSHA256: digest("profile"), PrivacyPartition: "run-private", Freshness: "plan_epoch",
		BudgetReservationSHA256: digest("budget:" + label), MaxFileBytes: CanonicalFileBytes, MaxBodyBytes: CanonicalBodyBytes,
	})
}

func TestStagedObjectRejectsUnboundHostReceipt(t *testing.T) {
	digest := func(value string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value))) }
	valid := HostReceipt{
		ContractSHA256: digest("contract"), PreparationSHA256: digest("prepare"),
		FileSHA256: CanonicalFileSHA256, BodySHA256: CanonicalBodySHA256,
		ExecutionProfileSHA256: digest("profile"), PrivacyPartition: "run-private", Freshness: "plan_epoch:stream-1",
		BudgetReservationSHA256: digest("budget"), MaxFileBytes: CanonicalFileBytes, MaxBodyBytes: CanonicalBodyBytes,
	}
	for name, mutate := range map[string]func(*HostReceipt){
		"contract":  func(value *HostReceipt) { value.ContractSHA256 = "" },
		"file":      func(value *HostReceipt) { value.FileSHA256 = digest("other-file") },
		"profile":   func(value *HostReceipt) { value.ExecutionProfileSHA256 = "" },
		"privacy":   func(value *HostReceipt) { value.PrivacyPartition = "" },
		"freshness": func(value *HostReceipt) { value.Freshness = "" },
		"budget":    func(value *HostReceipt) { value.MaxBodyBytes-- },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if _, err := NewStagedObject(candidate); !errors.Is(err, ErrInvalidObjectID) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	object, err := NewStagedObject(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes - 1); !errors.Is(err, ErrInvalidLifecycleArgument) {
		t.Fatalf("read err=%v", err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(digest("other-file")); !errors.Is(err, ErrInvalidLifecycleArgument) {
		t.Fatalf("source err=%v", err)
	}
}

func TestStagedObjectClaimBoundMaterializationRejectsDriftBeforeAtomicClaim(t *testing.T) {
	object, err := newTestStagedObject("bound-claim")
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	if err := object.Decode(CanonicalFixture()); err != nil {
		t.Fatal(err)
	}
	if err := object.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := object.ClaimBoundMaterialization(CanonicalFileSHA256, CanonicalBodyBytes); !errors.Is(err, ErrClaimBindingMismatch) {
		t.Fatalf("drifted binding=%v", err)
	}
	if object.State() != StateSealed {
		t.Fatalf("drift consumed object: %s", object.State())
	}
	if err := object.ClaimBoundMaterialization(CanonicalBodySHA256, CanonicalBodyBytes); err != nil {
		t.Fatal(err)
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateClaimed || snapshot.BodyBytes != 0 || snapshot.Counters.LogicalClaims != 1 || snapshot.Counters.LogicalClaimBytes != CanonicalBodyBytes {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestStagedObjectLifecycleAndCounters(t *testing.T) {
	object, err := newTestStagedObject("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := object.State(), StatePlanned; got != want {
		t.Fatalf("initial state = %s, want %s", got, want)
	}
	if err := object.Seal(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("early Seal() error = %v, want invalid transition", err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	if err := object.Decode(CanonicalFixture()); err != nil {
		t.Fatal(err)
	}
	if err := object.Seal(); err != nil {
		t.Fatal(err)
	}
	materialized, err := object.MaterializeStaging()
	if err != nil || len(materialized.Body) != CanonicalBodyBytes || object.State() != StateSealed {
		t.Fatalf("materialized body=%d state=%s err=%v", len(materialized.Body), object.State(), err)
	}
	materialized.Body[0] ^= 1
	claimed, err := object.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Metadata.BodyBytes != CanonicalBodyBytes || len(claimed.Body) != CanonicalBodyBytes {
		t.Fatalf("claimed array = metadata %+v, body %d", claimed.Metadata, len(claimed.Body))
	}
	if _, err := object.Claim(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("replayed Claim() error = %v, want invalid transition", err)
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateClaimed || snapshot.BodyBytes != 0 {
		t.Fatalf("claimed snapshot = %+v", snapshot)
	}
	if got, want := snapshot.Counters.PhysicalReads, uint64(1); got != want {
		t.Fatalf("physical reads = %d, want %d", got, want)
	}
	if got, want := snapshot.Counters.PhysicalReadBytes, uint64(CanonicalFileBytes); got != want {
		t.Fatalf("physical read bytes = %d, want %d", got, want)
	}
	if got, want := snapshot.Counters.PhysicalDecodes, uint64(1); got != want {
		t.Fatalf("physical decodes = %d, want %d", got, want)
	}
	if got, want := snapshot.Counters.PhysicalSeals, uint64(1); got != want {
		t.Fatalf("physical seals = %d, want %d", got, want)
	}
	if snapshot.Counters.PhysicalMaterializations != 1 || snapshot.Counters.PhysicalMaterializedBytes != CanonicalBodyBytes {
		t.Fatalf("materialization counters = %+v", snapshot.Counters)
	}
	if got, want := snapshot.Counters.LogicalClaims, uint64(1); got != want {
		t.Fatalf("logical claims = %d, want %d", got, want)
	}
}

func TestStagedObjectLateCompletionPublishesOnlyAfterSeal(t *testing.T) {
	object, err := newTestStagedObject("run-late")
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-gate
		if err := object.Decode(CanonicalFixture()); err != nil {
			done <- err
			return
		}
		done <- object.Seal()
	}()
	if _, err := object.MaterializeStaging(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("partial materialization error=%v", err)
	}
	if snapshot := object.Snapshot(); snapshot.BodyBytes != 0 || snapshot.State != StateSourceVerified {
		t.Fatalf("partial state leaked: %+v", snapshot)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := object.MaterializeStaging(); err != nil {
		t.Fatal(err)
	}
	if err := object.Orphan(); err != nil {
		t.Fatal(err)
	}
	if snapshot := object.Snapshot(); snapshot.BodyBytes != 0 || snapshot.State != StateOrphaned {
		t.Fatalf("orphan cleanup failed: %+v", snapshot)
	}
}

func TestStagedObjectRejectsDecodeWithoutPartialPublication(t *testing.T) {
	object, err := newTestStagedObject("run-corrupt")
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	corrupt := CanonicalFixture()
	corrupt[CanonicalBodyOffset] ^= 1
	if err := object.Decode(corrupt); !errors.Is(err, ErrBodyDigestMismatch) {
		t.Fatalf("Decode() error = %v, want body digest mismatch", err)
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateRejected || snapshot.BodyBytes != 0 {
		t.Fatalf("rejected snapshot = %+v", snapshot)
	}
	if snapshot.Counters.PhysicalDecodes != 1 || snapshot.Counters.PhysicalDecodedBytes != 0 {
		t.Fatalf("rejected counters = %+v", snapshot.Counters)
	}
	if _, err := object.Claim(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("rejected Claim() error = %v, want invalid transition", err)
	}
}

func TestStagedObjectCancelBeforePhysicalStart(t *testing.T) {
	object, err := newTestStagedObject("run-cancel-before")
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Cancel(); err != nil {
		t.Fatal(err)
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateCancelled || snapshot.BodyBytes != 0 || snapshot.Counters.PhysicalReads != 0 || snapshot.Counters.PhysicalCancellations != 1 {
		t.Fatalf("cancel-before snapshot=%+v", snapshot)
	}
}

func TestStagedObjectTerminalControlsAreOneWay(t *testing.T) {
	for _, terminal := range []struct {
		name string
		fn   func(*StagedObject) error
		want State
	}{
		{"cancel", func(object *StagedObject) error { return object.Cancel() }, StateCancelled},
		{"reject", func(object *StagedObject) error { return object.Reject(errors.New("source drift")) }, StateRejected},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			object, err := newTestStagedObject(terminal.name)
			if err != nil {
				t.Fatal(err)
			}
			if err := terminal.fn(object); err != nil {
				t.Fatal(err)
			}
			if object.State() != terminal.want {
				t.Fatalf("state = %s, want %s", object.State(), terminal.want)
			}
			if err := object.IssueRead(0); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("transition after terminal = %v", err)
			}
		})
	}
}

func TestStagedObjectClaimIsSingleWinnerUnderRace(t *testing.T) {
	object, err := newTestStagedObject("run-race")
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	if err := object.Decode(CanonicalFixture()); err != nil {
		t.Fatal(err)
	}
	if err := object.Seal(); err != nil {
		t.Fatal(err)
	}

	const claimers = 32
	var group sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for range claimers {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := object.Claim(); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
	if got := object.Snapshot().Counters.LogicalClaims; got != 1 {
		t.Fatalf("logical claims = %d, want 1", got)
	}
}
