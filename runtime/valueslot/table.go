package valueslot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"sync"
)

const (
	SchemaVersion         = "pysolate.value-slot.v1"
	EvidenceSchemaVersion = "pysolate.value-slot-evidence.v1"
	maxImmutableBytes     = 1 << 20
)

type Kind string
type ClaimPolicy string
type Strategy string

const (
	KindJSONScalar     Kind = "canonical_json_bool_or_int64.v1"
	KindImmutableBytes Kind = "immutable_bytes.v1"

	ClaimSingleUse   ClaimPolicy = "single_use"
	ClaimPrivateCopy ClaimPolicy = "private_copy"

	StrategyInlineJSON  Strategy = "inline_json"
	StrategyPrivateCopy Strategy = "private_copy"
)

var (
	ErrInvalidSpec    = errors.New("invalid value slot specification")
	ErrInvalidObject  = errors.New("invalid prepared value object")
	ErrInvalidEntry   = errors.New("invalid value slot entry")
	ErrDuplicateSlot  = errors.New("duplicate value slot")
	ErrMissingSlot    = errors.New("value slot is missing")
	ErrAlreadyClaimed = errors.New("value slot claim bound is exhausted")
	ErrClosed         = errors.New("value slot table is closed")
	identityPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
)

type SlotSpec struct {
	SchemaVersion    string      `json:"schema_version"`
	ID               string      `json:"id"`
	SourceOccurrence string      `json:"source_occurrence"`
	ProducerIdentity string      `json:"producer_identity"`
	InputIdentity    string      `json:"input_identity"`
	Kind             Kind        `json:"kind"`
	MaxBytes         uint32      `json:"max_bytes"`
	PrivacyPartition string      `json:"privacy_partition"`
	ClaimPolicy      ClaimPolicy `json:"claim_policy"`
	MaxClaims        uint32      `json:"max_claims"`
}

func (spec SlotSpec) Validate() error {
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = SchemaVersion
	}
	if spec.SchemaVersion != SchemaVersion || !identityPattern.MatchString(spec.ID) ||
		!identityPattern.MatchString(spec.SourceOccurrence) || !identityPattern.MatchString(spec.ProducerIdentity) ||
		!identityPattern.MatchString(spec.InputIdentity) || !identityPattern.MatchString(spec.PrivacyPartition) ||
		spec.MaxBytes == 0 || spec.MaxBytes > maxImmutableBytes || spec.MaxClaims == 0 {
		return ErrInvalidSpec
	}
	switch spec.Kind {
	case KindJSONScalar:
		if spec.MaxBytes > 256 || spec.ClaimPolicy != ClaimSingleUse || spec.MaxClaims != 1 {
			return ErrInvalidSpec
		}
	case KindImmutableBytes:
		if spec.ClaimPolicy != ClaimSingleUse && spec.ClaimPolicy != ClaimPrivateCopy {
			return ErrInvalidSpec
		}
		if spec.ClaimPolicy == ClaimSingleUse && spec.MaxClaims != 1 {
			return ErrInvalidSpec
		}
	default:
		return ErrInvalidSpec
	}
	return nil
}

type PreparedObject struct {
	mu               sync.Mutex
	kind             Kind
	payload          []byte
	producerIdentity string
	inputIdentity    string
	privacyPartition string
	backingIdentity  string
	consumers        uint32
}

func (object *PreparedObject) ConsumerCount() uint32 {
	if object == nil {
		return 0
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	return object.consumers
}

func (object *PreparedObject) CanEvict() bool { return object != nil && object.ConsumerCount() == 0 }

func (object *PreparedObject) addConsumer() {
	object.mu.Lock()
	object.consumers++
	object.mu.Unlock()
}

func (object *PreparedObject) releaseConsumer() {
	object.mu.Lock()
	if object.consumers > 0 {
		object.consumers--
	}
	object.mu.Unlock()
}

func NewPreparedObject(kind Kind, payload []byte, producerIdentity, inputIdentity, privacyPartition string) (*PreparedObject, error) {
	if !identityPattern.MatchString(producerIdentity) || !identityPattern.MatchString(inputIdentity) ||
		!identityPattern.MatchString(privacyPartition) || len(payload) == 0 || len(payload) > maxImmutableBytes {
		return nil, ErrInvalidObject
	}
	switch kind {
	case KindJSONScalar:
		if len(payload) > 256 || !canonicalScalar(payload) {
			return nil, ErrInvalidObject
		}
	case KindImmutableBytes:
	default:
		return nil, ErrInvalidObject
	}
	copyPayload := append([]byte(nil), payload...)
	digest := sha256.New()
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(producerIdentity))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(inputIdentity))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(privacyPartition))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(copyPayload)
	return &PreparedObject{
		kind: kind, payload: copyPayload, producerIdentity: producerIdentity, inputIdentity: inputIdentity, privacyPartition: privacyPartition,
		backingIdentity: "sha256:" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func canonicalScalar(payload []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return false
	}
	switch typed := value.(type) {
	case bool:
		if typed {
			return string(payload) == "true"
		}
		return string(payload) == "false"
	case json.Number:
		integer, err := strconv.ParseInt(string(typed), 10, 64)
		return err == nil && strconv.FormatInt(integer, 10) == string(payload)
	default:
		return false
	}
}

type Entry struct {
	Spec     SlotSpec
	Object   *PreparedObject
	Strategy Strategy
}

type tableEntry struct {
	spec     SlotSpec
	object   *PreparedObject
	strategy Strategy
	claims   uint32
}

type Evidence struct {
	SchemaVersion string `json:"schema_version"`
	Ready         uint32 `json:"ready"`
	Claims        uint32 `json:"claims"`
	CopiedBytes   uint64 `json:"copied_bytes"`
	Discarded     uint32 `json:"discarded"`
	Rejected      uint32 `json:"rejected"`
	Closed        bool   `json:"closed"`
}

// Describe returns detached semantic metadata for Host admission. It never
// exposes the prepared payload or a process pointer to Guest code.
func (table *Table) Describe(slotID string) (SlotSpec, Strategy, string, error) {
	if table == nil {
		return SlotSpec{}, "", "", ErrInvalidEntry
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	entry, ok := table.entries[slotID]
	if !ok {
		return SlotSpec{}, "", "", ErrMissingSlot
	}
	return entry.spec, entry.strategy, entry.object.backingIdentity, nil
}

type Table struct {
	mu       sync.Mutex
	entries  map[string]*tableEntry
	objects  map[*PreparedObject]struct{}
	evidence Evidence
	closed   bool
}

func NewTable(entries []Entry) (*Table, error) {
	if len(entries) == 0 || len(entries) > 64 {
		return nil, ErrInvalidEntry
	}
	table := &Table{
		entries: make(map[string]*tableEntry, len(entries)), objects: make(map[*PreparedObject]struct{}),
		evidence: Evidence{SchemaVersion: EvidenceSchemaVersion},
	}
	for _, candidate := range entries {
		spec := candidate.Spec
		if spec.SchemaVersion == "" {
			spec.SchemaVersion = SchemaVersion
		}
		if spec.Validate() != nil || candidate.Object == nil || len(candidate.Object.payload) > int(spec.MaxBytes) ||
			candidate.Object.kind != spec.Kind || candidate.Object.producerIdentity != spec.ProducerIdentity || candidate.Object.inputIdentity != spec.InputIdentity ||
			candidate.Object.privacyPartition != spec.PrivacyPartition ||
			(spec.Kind == KindJSONScalar && candidate.Strategy != StrategyInlineJSON) ||
			(spec.Kind == KindImmutableBytes && candidate.Strategy != StrategyPrivateCopy) {
			return nil, ErrInvalidEntry
		}
		if _, exists := table.entries[spec.ID]; exists {
			return nil, ErrDuplicateSlot
		}
		table.entries[spec.ID] = &tableEntry{spec: spec, object: candidate.Object, strategy: candidate.Strategy}
		table.objects[candidate.Object] = struct{}{}
		table.evidence.Ready++
	}
	for object := range table.objects {
		object.addConsumer()
	}
	return table, nil
}

func (table *Table) Claim(slotID string) ([]byte, Strategy, error) {
	if table == nil {
		return nil, "", ErrMissingSlot
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if table.closed {
		table.evidence.Rejected++
		return nil, "", ErrClosed
	}
	entry, ok := table.entries[slotID]
	if !ok {
		table.evidence.Rejected++
		return nil, "", ErrMissingSlot
	}
	if entry.claims >= entry.spec.MaxClaims {
		table.evidence.Rejected++
		return nil, "", ErrAlreadyClaimed
	}
	entry.claims++
	payload := append([]byte(nil), entry.object.payload...)
	table.evidence.Claims++
	table.evidence.CopiedBytes += uint64(len(payload))
	return payload, entry.strategy, nil
}

func (table *Table) BackingIdentity(slotID string) string {
	if table == nil {
		return ""
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	entry, ok := table.entries[slotID]
	if !ok {
		return ""
	}
	return entry.object.backingIdentity
}

func (table *Table) Close() error {
	if table == nil {
		return nil
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if table.closed {
		return nil
	}
	for _, entry := range table.entries {
		if entry.claims == 0 {
			table.evidence.Discarded++
		}
	}
	for object := range table.objects {
		object.releaseConsumer()
	}
	table.closed = true
	table.evidence.Closed = true
	return nil
}

func (table *Table) Evidence() Evidence {
	if table == nil {
		return Evidence{SchemaVersion: EvidenceSchemaVersion}
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	return table.evidence
}
