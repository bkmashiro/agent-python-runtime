package resultblob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"sync"
)

const DescriptorSchemaVersion = "pysolate.result-blob-descriptor.v1"

var (
	ErrInvalidLimits     = errors.New("invalid result blob limits")
	ErrInvalidDescriptor = errors.New("invalid result blob descriptor")
	ErrPublicationUnsafe = errors.New("result blob publication is unsafe")
	ErrHashMismatch      = errors.New("result blob hash mismatch")
	ErrLimitExceeded     = errors.New("result blob limit exceeded")
	ErrDuplicateBlob     = errors.New("duplicate result blob")
	ErrBlobMissing       = errors.New("result blob missing")
	ErrLeaseState        = errors.New("invalid result blob lease state")
	ErrStoreClosed       = errors.New("result blob store is closed")

	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	codecPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}_v[1-9][0-9]*$`)
)

type Limits struct {
	MaxEntries       uint32
	MaxBodyBytes     uint64
	MaxRetainedBytes uint64
	MaxMetadataBytes uint32
	MaxLeases        uint32
}

type PublicationGuard struct {
	Completed       bool
	Succeeded       bool
	EffectFree      bool
	TerminalCertain bool
}

type Publication struct {
	RunID              string
	Codec              string
	Metadata           []byte
	BindingSHA256      string
	ExpectedBodySHA256 string
	Guard              PublicationGuard
}

type Descriptor struct {
	SchemaVersion  string `json:"schema_version"`
	RunID          string `json:"run_id"`
	Codec          string `json:"codec"`
	MetadataBytes  uint32 `json:"metadata_bytes"`
	MetadataSHA256 string `json:"metadata_sha256"`
	BodyBytes      uint64 `json:"body_bytes"`
	BodySHA256     string `json:"body_sha256"`
	BindingSHA256  string `json:"binding_sha256"`
	IdentitySHA256 string `json:"identity_sha256"`
}

type descriptorIdentity struct {
	SchemaVersion  string `json:"schema_version"`
	RunID          string `json:"run_id"`
	Codec          string `json:"codec"`
	MetadataBytes  uint32 `json:"metadata_bytes"`
	MetadataSHA256 string `json:"metadata_sha256"`
	BodyBytes      uint64 `json:"body_bytes"`
	BodySHA256     string `json:"body_sha256"`
	BindingSHA256  string `json:"binding_sha256"`
}

func BytesDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (descriptor Descriptor) CanonicalJSON() ([]byte, error) {
	if !descriptor.valid() {
		return nil, ErrInvalidDescriptor
	}
	return json.Marshal(descriptor)
}

func DecodeDescriptor(raw []byte) (Descriptor, error) {
	var descriptor Descriptor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&descriptor) != nil || decoder.Decode(&struct{}{}) != io.EOF || !descriptor.valid() {
		return Descriptor{}, ErrInvalidDescriptor
	}
	canonical, err := json.Marshal(descriptor)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Descriptor{}, ErrInvalidDescriptor
	}
	return descriptor, nil
}

func (descriptor Descriptor) valid() bool {
	if descriptor.SchemaVersion != DescriptorSchemaVersion || !idPattern.MatchString(descriptor.RunID) ||
		!codecPattern.MatchString(descriptor.Codec) || descriptor.MetadataBytes == 0 || descriptor.BodyBytes == 0 ||
		!digestPattern.MatchString(descriptor.MetadataSHA256) || !digestPattern.MatchString(descriptor.BodySHA256) ||
		!digestPattern.MatchString(descriptor.BindingSHA256) || !digestPattern.MatchString(descriptor.IdentitySHA256) {
		return false
	}
	identity := descriptorIdentity{
		SchemaVersion: descriptor.SchemaVersion, RunID: descriptor.RunID, Codec: descriptor.Codec,
		MetadataBytes: descriptor.MetadataBytes, MetadataSHA256: descriptor.MetadataSHA256,
		BodyBytes: descriptor.BodyBytes, BodySHA256: descriptor.BodySHA256, BindingSHA256: descriptor.BindingSHA256,
	}
	return descriptor.IdentitySHA256 == digestJSON(identity)
}

type LeaseState string

const (
	LeaseReady     LeaseState = "ready"
	LeaseClaimed   LeaseState = "claimed"
	LeaseConsumed  LeaseState = "consumed"
	LeaseRejected  LeaseState = "rejected"
	LeaseDiscarded LeaseState = "discarded"
)

type Lease struct {
	id    string
	token string
}

func (lease Lease) ID() string { return lease.id }

type Claim struct {
	Descriptor Descriptor
	Metadata   []byte
	Body       []byte
}

type BlobObservation struct {
	Descriptor Descriptor `json:"descriptor"`
}

type LeaseObservation struct {
	LeaseID        string     `json:"lease_id"`
	BlobID         string     `json:"blob_id"`
	ConsumerSHA256 string     `json:"consumer_sha256"`
	State          LeaseState `json:"state"`
}

type Snapshot struct {
	SchemaVersion string             `json:"schema_version"`
	RunID         string             `json:"run_id"`
	Closed        bool               `json:"closed"`
	EntryCount    uint32             `json:"entry_count"`
	RetainedBytes uint64             `json:"retained_bytes"`
	Blobs         []BlobObservation  `json:"blobs"`
	Leases        []LeaseObservation `json:"leases"`
}

type entry struct {
	descriptor Descriptor
	metadata   []byte
	body       []byte
}

type leaseRecord struct {
	id             string
	token          string
	blobID         string
	consumerSHA256 string
	state          LeaseState
}

type Store struct {
	mu            sync.Mutex
	runID         string
	limits        Limits
	entries       map[string]*entry
	leases        map[string]*leaseRecord
	blobRecords   map[string]BlobObservation
	retainedBytes uint64
	leaseOrdinal  uint64
	closed        bool
}

func NewStore(runID string, limits Limits) (*Store, error) {
	if !idPattern.MatchString(runID) || limits.MaxEntries == 0 || limits.MaxBodyBytes == 0 || limits.MaxRetainedBytes < limits.MaxBodyBytes ||
		limits.MaxMetadataBytes == 0 || limits.MaxLeases == 0 {
		return nil, ErrInvalidLimits
	}
	return &Store{
		runID: runID, limits: limits, entries: make(map[string]*entry), leases: make(map[string]*leaseRecord),
		blobRecords: make(map[string]BlobObservation),
	}, nil
}

func (store *Store) Publish(ctx context.Context, publication Publication, body []byte) (Descriptor, error) {
	if store == nil || ctx == nil {
		return Descriptor{}, ErrPublicationUnsafe
	}
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	if !publication.Guard.Completed || !publication.Guard.Succeeded || !publication.Guard.EffectFree || !publication.Guard.TerminalCertain {
		return Descriptor{}, ErrPublicationUnsafe
	}
	if publication.RunID != store.runID || !codecPattern.MatchString(publication.Codec) ||
		!digestPattern.MatchString(publication.BindingSHA256) || !digestPattern.MatchString(publication.ExpectedBodySHA256) || len(body) == 0 {
		return Descriptor{}, ErrInvalidDescriptor
	}
	metadata, err := canonicalMetadata(publication.Metadata)
	if err != nil {
		return Descriptor{}, ErrInvalidDescriptor
	}
	if uint64(len(body)) > store.limits.MaxBodyBytes || len(metadata) > int(store.limits.MaxMetadataBytes) {
		return Descriptor{}, ErrLimitExceeded
	}
	bodyCopy := append([]byte(nil), body...)
	metadataCopy := append([]byte(nil), metadata...)
	bodySHA := BytesDigest(bodyCopy)
	if bodySHA != publication.ExpectedBodySHA256 {
		return Descriptor{}, ErrHashMismatch
	}
	identity := descriptorIdentity{
		SchemaVersion: DescriptorSchemaVersion, RunID: publication.RunID, Codec: publication.Codec,
		MetadataBytes: uint32(len(metadataCopy)), MetadataSHA256: BytesDigest(metadataCopy),
		BodyBytes: uint64(len(bodyCopy)), BodySHA256: bodySHA, BindingSHA256: publication.BindingSHA256,
	}
	descriptor := Descriptor{
		SchemaVersion: identity.SchemaVersion, RunID: identity.RunID, Codec: identity.Codec,
		MetadataBytes: identity.MetadataBytes, MetadataSHA256: identity.MetadataSHA256,
		BodyBytes: identity.BodyBytes, BodySHA256: identity.BodySHA256, BindingSHA256: identity.BindingSHA256,
		IdentitySHA256: digestJSON(identity),
	}
	if !descriptor.valid() {
		return Descriptor{}, ErrInvalidDescriptor
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	if store.closed {
		return Descriptor{}, ErrStoreClosed
	}
	if _, exists := store.entries[descriptor.IdentitySHA256]; exists {
		return Descriptor{}, ErrDuplicateBlob
	}
	if descriptor.BodyBytes > store.limits.MaxRetainedBytes || uint64(descriptor.MetadataBytes) > store.limits.MaxRetainedBytes-descriptor.BodyBytes {
		return Descriptor{}, ErrLimitExceeded
	}
	entryBytes := descriptor.BodyBytes + uint64(descriptor.MetadataBytes)
	if len(store.entries) >= int(store.limits.MaxEntries) || store.retainedBytes > store.limits.MaxRetainedBytes || entryBytes > store.limits.MaxRetainedBytes-store.retainedBytes {
		return Descriptor{}, ErrLimitExceeded
	}
	store.entries[descriptor.IdentitySHA256] = &entry{descriptor: descriptor, metadata: metadataCopy, body: bodyCopy}
	store.blobRecords[descriptor.IdentitySHA256] = BlobObservation{Descriptor: descriptor}
	store.retainedBytes += entryBytes
	return descriptor, nil
}

func (store *Store) NewLease(blobID, consumerSHA256 string) (Lease, error) {
	if store == nil || !digestPattern.MatchString(blobID) || !digestPattern.MatchString(consumerSHA256) {
		return Lease{}, ErrBlobMissing
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return Lease{}, ErrStoreClosed
	}
	if _, exists := store.entries[blobID]; !exists {
		return Lease{}, ErrBlobMissing
	}
	if len(store.leases) >= int(store.limits.MaxLeases) {
		return Lease{}, ErrLimitExceeded
	}
	store.leaseOrdinal++
	leaseID := BytesDigest([]byte(fmt.Sprintf("pysolate.result-blob-lease.v1\x00%s\x00%s\x00%d", blobID, consumerSHA256, store.leaseOrdinal)))
	token := BytesDigest([]byte("pysolate.result-blob-token.v1\x00" + leaseID))
	record := &leaseRecord{id: leaseID, token: token, blobID: blobID, consumerSHA256: consumerSHA256, state: LeaseReady}
	store.leases[token] = record
	return Lease{id: leaseID, token: token}, nil
}

func (store *Store) Claim(lease Lease) (Claim, error) {
	if store == nil {
		return Claim{}, ErrLeaseState
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return Claim{}, ErrStoreClosed
	}
	record, ok := store.leases[lease.token]
	if !ok || record.id != lease.id || record.state != LeaseReady {
		return Claim{}, ErrLeaseState
	}
	blob, ok := store.entries[record.blobID]
	if !ok {
		return Claim{}, ErrBlobMissing
	}
	record.state = LeaseClaimed
	return Claim{
		Descriptor: blob.descriptor,
		Metadata:   append([]byte(nil), blob.metadata...), Body: append([]byte(nil), blob.body...),
	}, nil
}

func (store *Store) Consume(lease Lease) error { return store.transition(lease, LeaseConsumed) }
func (store *Store) Reject(lease Lease) error  { return store.transition(lease, LeaseRejected) }
func (store *Store) Discard(lease Lease) error { return store.transition(lease, LeaseDiscarded) }

func (store *Store) transition(lease Lease, terminal LeaseState) error {
	if store == nil {
		return ErrLeaseState
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrStoreClosed
	}
	record, ok := store.leases[lease.token]
	if !ok || record.id != lease.id {
		return ErrLeaseState
	}
	switch terminal {
	case LeaseConsumed:
		if record.state != LeaseClaimed {
			return ErrLeaseState
		}
	case LeaseRejected, LeaseDiscarded:
		if record.state != LeaseReady && record.state != LeaseClaimed {
			return ErrLeaseState
		}
	default:
		return ErrLeaseState
	}
	record.state = terminal
	return nil
}

func (store *Store) Close() error {
	if store == nil {
		return ErrStoreClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	for _, lease := range store.leases {
		if lease.state == LeaseReady || lease.state == LeaseClaimed {
			lease.state = LeaseDiscarded
		}
	}
	for id, blob := range store.entries {
		for index := range blob.body {
			blob.body[index] = 0
		}
		blob.body = nil
		for index := range blob.metadata {
			blob.metadata[index] = 0
		}
		blob.metadata = nil
		delete(store.entries, id)
	}
	store.retainedBytes = 0
	store.closed = true
	return nil
}

func (store *Store) Snapshot() Snapshot {
	if store == nil {
		return Snapshot{SchemaVersion: "pysolate.result-blob-snapshot.v1", Closed: true, Blobs: []BlobObservation{}, Leases: []LeaseObservation{}}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	snapshot := Snapshot{
		SchemaVersion: "pysolate.result-blob-snapshot.v1", RunID: store.runID, Closed: store.closed,
		EntryCount: uint32(len(store.entries)), RetainedBytes: store.retainedBytes,
		Blobs: make([]BlobObservation, 0, len(store.blobRecords)), Leases: make([]LeaseObservation, 0, len(store.leases)),
	}
	for _, blob := range store.blobRecords {
		snapshot.Blobs = append(snapshot.Blobs, blob)
	}
	for _, lease := range store.leases {
		snapshot.Leases = append(snapshot.Leases, LeaseObservation{
			LeaseID: lease.id, BlobID: lease.blobID, ConsumerSHA256: lease.consumerSHA256, State: lease.state,
		})
	}
	sort.Slice(snapshot.Blobs, func(i, j int) bool {
		return snapshot.Blobs[i].Descriptor.IdentitySHA256 < snapshot.Blobs[j].Descriptor.IdentitySHA256
	})
	sort.Slice(snapshot.Leases, func(i, j int) bool { return snapshot.Leases[i].LeaseID < snapshot.Leases[j].LeaseID })
	return snapshot
}

func canonicalMetadata(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidDescriptor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrInvalidDescriptor
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrInvalidDescriptor
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrInvalidDescriptor
	}
	return canonical, nil
}

func digestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return BytesDigest(raw)
}
