package resultblob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testLimits() Limits {
	return Limits{MaxEntries: 2, MaxBodyBytes: 64, MaxRetainedBytes: 96, MaxMetadataBytes: 256, MaxLeases: 4}
}

func testPublication(body []byte) Publication {
	return Publication{
		RunID: "run-1", Codec: "opaque_test_v1", Metadata: []byte(`{"kind":"test","shape":[2,2]}`),
		BindingSHA256: digestA, ExpectedBodySHA256: BytesDigest(body),
		Guard: NewPublicationGuard(publicationauth.Mint()),
	}
}

func TestPublishOwnsImmutableBodyAndMetadataCopies(t *testing.T) {
	store, err := NewStore("run-1", testLimits())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("immutable-body")
	publication := testPublication(body)
	descriptor, err := store.Publish(context.Background(), publication, body)
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'X'
	publication.Metadata[2] = 'X'
	lease, err := store.NewLease(descriptor.IdentitySHA256, digestB)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(lease)
	if err != nil {
		t.Fatal(err)
	}
	if claim.LeaseID != lease.ID() || claim.BlobID != descriptor.IdentitySHA256 || claim.ConsumerSHA256 != digestB {
		t.Fatalf("claim identity mismatch: %+v", claim)
	}
	if string(claim.Body) != "immutable-body" || string(claim.Metadata) != `{"kind":"test","shape":[2,2]}` {
		t.Fatalf("claim body=%q metadata=%s", claim.Body, claim.Metadata)
	}
	claim.Body[0] = 'Y'
	claim.Metadata[2] = 'Y'
	if err := store.Consume(lease); err != nil {
		t.Fatal(err)
	}
	second, err := store.NewLease(descriptor.IdentitySHA256, digestA)
	if err != nil {
		t.Fatal(err)
	}
	secondClaim, err := store.Claim(second)
	if err != nil || string(secondClaim.Body) != "immutable-body" || string(secondClaim.Metadata) != `{"kind":"test","shape":[2,2]}` {
		t.Fatalf("second=%+v err=%v", secondClaim, err)
	}
}

func TestPublishIsAtomicAndFailClosed(t *testing.T) {
	body := []byte("payload")
	cases := []struct {
		name   string
		mutate func(*Publication)
		ctx    context.Context
	}{
		{"missing_internal_authority", func(p *Publication) { p.Guard = PublicationGuard{} }, context.Background()},
		{"hash", func(p *Publication) { p.ExpectedBodySHA256 = digestA }, context.Background()},
		{"cancelled", func(*Publication) {}, cancelledContext()},
	}
	for _, candidate := range cases {
		t.Run(candidate.name, func(t *testing.T) {
			store, _ := NewStore("run-1", testLimits())
			publication := testPublication(body)
			candidate.mutate(&publication)
			if _, err := store.Publish(candidate.ctx, publication, body); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if snapshot := store.Snapshot(); snapshot.EntryCount != 0 || snapshot.RetainedBytes != 0 || len(snapshot.Blobs) != 0 {
				t.Fatalf("partial publication: %+v", snapshot)
			}
		})
	}
}

func TestStoreBoundsAndCanonicalMetadata(t *testing.T) {
	store, _ := NewStore("run-1", testLimits())
	body := bytes.Repeat([]byte{'x'}, 65)
	if _, err := store.Publish(context.Background(), testPublication(body), body); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversize err=%v", err)
	}
	body = []byte("ok")
	for _, metadata := range [][]byte{[]byte(`{"z":1,"a":2}`), []byte(`{"a":1,"a":2}`), []byte(`{"a":1} trailing`)} {
		publication := testPublication(body)
		publication.Metadata = metadata
		if _, err := store.Publish(context.Background(), publication, body); !errors.Is(err, ErrInvalidDescriptor) {
			t.Fatalf("metadata=%s err=%v", metadata, err)
		}
	}
	wrongRun := testPublication(body)
	wrongRun.RunID = "run-2"
	if _, err := store.Publish(context.Background(), wrongRun, body); !errors.Is(err, ErrInvalidDescriptor) {
		t.Fatalf("cross-run publication err=%v", err)
	}
}

func TestPublicationRejectsOversizedCodecAndMetadataBeforeCanonicalization(t *testing.T) {
	limits := testLimits()
	limits.MaxMetadataBytes = 16
	store, err := NewStore("run-1", limits)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("body")
	publication := testPublication(body)
	publication.Codec = "a" + string(bytes.Repeat([]byte("x"), 1024*1024)) + "_v1"
	if _, err := store.Publish(context.Background(), publication, body); !errors.Is(err, ErrInvalidDescriptor) {
		t.Fatalf("oversized codec err=%v", err)
	}
	publication = testPublication(body)
	publication.Metadata = bytes.Repeat([]byte("{"), 1024*1024)
	if _, err := store.Publish(context.Background(), publication, body); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized metadata should fail before JSON parse, err=%v", err)
	}
	if snapshot := store.Snapshot(); snapshot.EntryCount != 0 || snapshot.RetainedBytes != 0 {
		t.Fatalf("failed publications leaked state: %+v", snapshot)
	}
}

func TestMaxUint32EntryAndLeaseLimitsDoNotConvertThroughInt(t *testing.T) {
	limits := testLimits()
	limits.MaxEntries = ^uint32(0)
	limits.MaxLeases = ^uint32(0)
	store, err := NewStore("run-1", limits)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("body")
	descriptor, err := store.Publish(context.Background(), testPublication(body), body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewLease(descriptor.IdentitySHA256, digestB); err != nil {
		t.Fatal(err)
	}
}

func TestDescriptorIsCanonicalStrictAndObservationIsBodyFree(t *testing.T) {
	store, _ := NewStore("run-1", testLimits())
	body := []byte("secret-body")
	descriptor, err := store.Publish(context.Background(), testPublication(body), body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := descriptor.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDescriptor(raw)
	if err != nil || decoded != descriptor {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	for _, invalid := range [][]byte{
		append([]byte(" "), raw...),
		bytes.Replace(raw, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1),
	} {
		if _, err := DecodeDescriptor(invalid); !errors.Is(err, ErrInvalidDescriptor) {
			t.Fatalf("invalid descriptor accepted: %s", invalid)
		}
	}
	snapshotJSON, err := json.Marshal(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(snapshotJSON, body) || bytes.Contains(snapshotJSON, []byte(`"kind":"test"`)) {
		t.Fatalf("snapshot contains retained body or metadata: %s", snapshotJSON)
	}
}

func TestAggregateLimitIncludesMetadata(t *testing.T) {
	limits := Limits{MaxEntries: 2, MaxBodyBytes: 64, MaxRetainedBytes: 100, MaxMetadataBytes: 64, MaxLeases: 2}
	store, err := NewStore("run-1", limits)
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{'x'}, 40)
	first := testPublication(body)
	if _, err := store.Publish(context.Background(), first, body); err != nil {
		t.Fatal(err)
	}
	second := testPublication(body)
	second.BindingSHA256 = digestB
	if _, err := store.Publish(context.Background(), second, body); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("aggregate limit err=%v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.EntryCount != 1 || snapshot.RetainedBytes != uint64(len(body)+len(first.Metadata)) {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestLeaseStateMachineAndSiblingIsolation(t *testing.T) {
	store, _ := NewStore("run-1", testLimits())
	body := []byte("body")
	descriptor, _ := store.Publish(context.Background(), testPublication(body), body)
	first, _ := store.NewLease(descriptor.IdentitySHA256, digestA)
	second, _ := store.NewLease(descriptor.IdentitySHA256, digestB)
	if first.ID() == second.ID() {
		t.Fatal("sibling leases shared identity")
	}
	if _, err := store.Claim(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(first); !errors.Is(err, ErrLeaseState) {
		t.Fatalf("second claim err=%v", err)
	}
	if err := store.Consume(second); !errors.Is(err, ErrLeaseState) {
		t.Fatalf("sibling consumed claimed lease: %v", err)
	}
	if err := store.Reject(first); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(second); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume(second); err != nil {
		t.Fatal(err)
	}
	states := statesByID(store.Snapshot())
	if states[first.ID()] != LeaseRejected || states[second.ID()] != LeaseConsumed {
		t.Fatalf("states=%v", states)
	}
}

func TestConcurrentClaimHasOneWinner(t *testing.T) {
	store, _ := NewStore("run-1", testLimits())
	body := []byte("body")
	descriptor, _ := store.Publish(context.Background(), testPublication(body), body)
	lease, _ := store.NewLease(descriptor.IdentitySHA256, digestA)
	var wait sync.WaitGroup
	results := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.Claim(lease)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrLeaseState) {
			t.Fatalf("claim err=%v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners=%d", wins)
	}
}

func TestCloseDiscardsOpenLeasesAndReleasesBodies(t *testing.T) {
	store, _ := NewStore("run-1", testLimits())
	body := []byte("body")
	descriptor, _ := store.Publish(context.Background(), testPublication(body), body)
	ready, _ := store.NewLease(descriptor.IdentitySHA256, digestA)
	claimed, _ := store.NewLease(descriptor.IdentitySHA256, digestB)
	if _, err := store.Claim(claimed); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	states := statesByID(snapshot)
	if !snapshot.Closed || snapshot.RetainedBytes != 0 || snapshot.EntryCount != 0 ||
		states[ready.ID()] != LeaseDiscarded || states[claimed.ID()] != LeaseDiscarded {
		t.Fatalf("snapshot=%+v states=%v", snapshot, states)
	}
	if _, err := store.NewLease(descriptor.IdentitySHA256, digestA); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("new lease after close err=%v", err)
	}
}

func statesByID(snapshot Snapshot) map[string]LeaseState {
	states := make(map[string]LeaseState, len(snapshot.Leases))
	for _, lease := range snapshot.Leases {
		states[lease.LeaseID] = lease.State
	}
	return states
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
