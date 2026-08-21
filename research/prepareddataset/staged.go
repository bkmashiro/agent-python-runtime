package prepareddataset

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrInvalidTransition        = errors.New("invalid staged-object transition")
	ErrInvalidObjectID          = errors.New("invalid staged-object identity")
	ErrInvalidLifecycleArgument = errors.New("invalid staged-object lifecycle argument")
	ErrClaimBindingMismatch     = errors.New("staged-object claim binding mismatch")
)

// State is the physical/logical lifecycle of one Run-private staged object.
type State string

const (
	StatePlanned        State = "Planned"
	StateReadIssued     State = "ReadIssued"
	StateSourceVerified State = "SourceVerified"
	StateTypedStaging   State = "TypedStaging"
	StateSealed         State = "Sealed"
	StateClaimed        State = "Claimed"
	StateOrphaned       State = "Orphaned"
	StateCancelled      State = "Cancelled"
	StateRejected       State = "Rejected"
)

// Counters keep physical preparation separate from the logical claim. All
// fields are updated under the staged object's mutex and read through Snapshot.
type Counters struct {
	PhysicalReads               uint64
	PhysicalReadBytes           uint64
	PhysicalSourceVerifications uint64
	PhysicalDecodes             uint64
	PhysicalDecodedBytes        uint64
	PhysicalSeals               uint64
	PhysicalSealedBytes         uint64
	PhysicalMaterializations    uint64
	PhysicalMaterializedBytes   uint64
	PhysicalCancellations       uint64
	PhysicalOrphans             uint64
	PhysicalRejections          uint64
	LogicalClaims               uint64
	LogicalClaimBytes           uint64
}

// Snapshot is body-free. A caller can inspect lifecycle and accounting without
// gaining a second path to the staged bytes.
type Snapshot struct {
	ID             string
	Receipt        HostReceipt
	State          State
	SourceIdentity string
	Metadata       Metadata
	BodyBytes      uint64
	Counters       Counters
	Disposition    string
}

// HostReceipt is the body-free authority envelope for one physical staging
// attempt. Syntax discovery cannot mint it.
type HostReceipt struct {
	ContractSHA256          string
	PreparationSHA256       string
	FileSHA256              string
	BodySHA256              string
	ExecutionProfileSHA256  string
	PrivacyPartition        string
	Freshness               string
	BudgetReservationSHA256 string
	MaxFileBytes            uint64
	MaxBodyBytes            uint64
}

func (receipt HostReceipt) valid() bool {
	return validSHA256(receipt.ContractSHA256) && validSHA256(receipt.PreparationSHA256) &&
		receipt.FileSHA256 == CanonicalFileSHA256 && receipt.BodySHA256 == CanonicalBodySHA256 &&
		validSHA256(receipt.ExecutionProfileSHA256) && receipt.PrivacyPartition != "" && receipt.Freshness != "" &&
		validSHA256(receipt.BudgetReservationSHA256) && receipt.MaxFileBytes == CanonicalFileBytes && receipt.MaxBodyBytes == CanonicalBodyBytes
}

func validSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

// StagedObject owns one bounded, Run-private decoded array. Its body is held
// only between successful decode and the single terminal claim/orphan/cancel/
// reject decision; no failed or terminal object publishes a partial body.
type StagedObject struct {
	mu sync.Mutex

	id             string
	receipt        HostReceipt
	state          State
	sourceIdentity string
	pending        DecodedArray
	sealed         DecodedArray
	disposition    string
	counters       Counters
}

func NewStagedObject(receipt HostReceipt) (*StagedObject, error) {
	if !receipt.valid() {
		return nil, ErrInvalidObjectID
	}
	return &StagedObject{id: receipt.PreparationSHA256, receipt: receipt, state: StatePlanned}, nil
}

func (object *StagedObject) ID() string {
	if object == nil {
		return ""
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	return object.id
}

func (object *StagedObject) State() State {
	if object == nil {
		return StateRejected
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	return object.state
}

// IssueRead records the physical read issue and its exact bounded byte count.
func (object *StagedObject) IssueRead(readBytes uint64) error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state != StatePlanned {
		return invalidTransition(object.state, StateReadIssued)
	}
	if readBytes != object.receipt.MaxFileBytes {
		return ErrInvalidLifecycleArgument
	}
	if err := object.transitionLocked(StateReadIssued); err != nil {
		return err
	}
	object.counters.PhysicalReads++
	object.counters.PhysicalReadBytes = readBytes
	return nil
}

// VerifySource joins the physical attempt to one exact body-free source identity.
func (object *StagedObject) VerifySource(sourceIdentity string) error {
	if object == nil {
		return ErrInvalidTransition
	}
	if sourceIdentity == "" {
		return ErrInvalidLifecycleArgument
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state != StateReadIssued {
		return invalidTransition(object.state, StateSourceVerified)
	}
	if sourceIdentity != object.receipt.FileSHA256 {
		return ErrInvalidLifecycleArgument
	}
	if err := object.transitionLocked(StateSourceVerified); err != nil {
		return err
	}
	object.sourceIdentity = sourceIdentity
	object.counters.PhysicalSourceVerifications++
	return nil
}

// Decode performs strict decoding while the object is still unpublished. Any
// decoder failure terminates the object as rejected and clears all body state.
func (object *StagedObject) Decode(data []byte) error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state != StateSourceVerified {
		return invalidTransition(object.state, StateTypedStaging)
	}
	object.counters.PhysicalDecodes++
	decoded, err := Decode(data)
	if err != nil {
		object.rejectLocked(err)
		return err
	}
	if decoded.Metadata.FileSHA256 != object.receipt.FileSHA256 || decoded.Metadata.BodySHA256 != object.receipt.BodySHA256 || uint64(len(decoded.Body)) != object.receipt.MaxBodyBytes {
		err := ErrInvalidLifecycleArgument
		object.rejectLocked(err)
		return err
	}
	object.pending = decoded
	object.counters.PhysicalDecodedBytes = uint64(len(decoded.Body))
	if err := object.transitionLocked(StateTypedStaging); err != nil {
		object.pending = DecodedArray{}
		object.rejectLocked(err)
		return err
	}
	return nil
}

// Seal promotes only a complete decoded body to the claimable state.
func (object *StagedObject) Seal() error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateSealed); err != nil {
		return err
	}
	object.sealed = object.pending
	object.pending = DecodedArray{}
	object.counters.PhysicalSeals++
	object.counters.PhysicalSealedBytes = uint64(len(object.sealed.Body))
	return nil
}

// MaterializeStaging makes one owned physical copy for a fresh Guest without
// advancing the logical state. Dynamic execution must still claim or orphan it.
func (object *StagedObject) MaterializeStaging() (DecodedArray, error) {
	if object == nil {
		return DecodedArray{}, ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state != StateSealed {
		return DecodedArray{}, invalidTransition(object.state, StateSealed)
	}
	materialized := DecodedArray{Metadata: object.sealed.Metadata, Body: append([]byte(nil), object.sealed.Body...)}
	object.counters.PhysicalMaterializations++
	object.counters.PhysicalMaterializedBytes += uint64(len(materialized.Body))
	return materialized, nil
}

// Claim returns an owned copy and consumes the staged object's body. A later
// claim cannot replay the object or increment the logical claim counter.
func (object *StagedObject) Claim() (DecodedArray, error) {
	if object == nil {
		return DecodedArray{}, ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateClaimed); err != nil {
		return DecodedArray{}, err
	}
	claimed := DecodedArray{Metadata: object.sealed.Metadata, Body: append([]byte(nil), object.sealed.Body...)}
	object.sealed = DecodedArray{}
	object.counters.LogicalClaims++
	object.counters.LogicalClaimBytes += uint64(len(claimed.Body))
	return claimed, nil
}

// ClaimBoundMaterialization consumes the exact sealed object after a separate
// Guest transfer has proved the same body identity. It avoids creating a second
// body copy solely to advance the logical lifecycle.
func (object *StagedObject) ClaimBoundMaterialization(bodySHA256 string, bodyBytes uint64) error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state != StateSealed {
		return invalidTransition(object.state, StateClaimed)
	}
	if bodySHA256 != object.receipt.BodySHA256 || bodyBytes != object.receipt.MaxBodyBytes ||
		object.sealed.Metadata.BodySHA256 != bodySHA256 || uint64(len(object.sealed.Body)) != bodyBytes {
		return ErrClaimBindingMismatch
	}
	if err := object.transitionLocked(StateClaimed); err != nil {
		return err
	}
	object.clearBodyLocked()
	object.counters.LogicalClaims++
	object.counters.LogicalClaimBytes += bodyBytes
	return nil
}

func (object *StagedObject) Orphan() error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateOrphaned); err != nil {
		return err
	}
	object.clearBodyLocked()
	object.counters.PhysicalOrphans++
	return nil
}

func (object *StagedObject) Cancel() error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateCancelled); err != nil {
		return err
	}
	object.clearBodyLocked()
	object.counters.PhysicalCancellations++
	return nil
}

// Reject records a body-free terminal reason.
func (object *StagedObject) Reject(reason error) error {
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateRejected); err != nil {
		return err
	}
	if reason != nil {
		object.disposition = reason.Error()
	}
	object.clearBodyLocked()
	object.counters.PhysicalRejections++
	return nil
}

func (object *StagedObject) Snapshot() Snapshot {
	if object == nil {
		return Snapshot{State: StateRejected}
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	bodyBytes := uint64(len(object.pending.Body) + len(object.sealed.Body))
	metadata := object.pending.Metadata
	if object.state == StateSealed {
		metadata = object.sealed.Metadata
	}
	return Snapshot{
		ID: object.id, Receipt: object.receipt, State: object.state, SourceIdentity: object.sourceIdentity,
		Metadata: metadata, BodyBytes: bodyBytes, Counters: object.counters,
		Disposition: object.disposition,
	}
}

func (object *StagedObject) clearBodyLocked() {
	object.pending = DecodedArray{}
	object.sealed = DecodedArray{}
}

func (object *StagedObject) rejectLocked(reason error) {
	object.clearBodyLocked()
	object.disposition = ""
	if reason != nil {
		object.disposition = reason.Error()
	}
	object.state = StateRejected
	object.counters.PhysicalRejections++
}

func (object *StagedObject) transitionLocked(next State) error {
	if !validTransition(object.state, next) {
		return invalidTransition(object.state, next)
	}
	object.state = next
	return nil
}

func invalidTransition(from, to State) error {
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

func validTransition(from, to State) bool {
	switch from {
	case StatePlanned:
		return to == StateReadIssued || to == StateCancelled || to == StateRejected
	case StateReadIssued:
		return to == StateSourceVerified || to == StateCancelled || to == StateRejected
	case StateSourceVerified:
		return to == StateTypedStaging || to == StateCancelled || to == StateRejected
	case StateTypedStaging:
		return to == StateSealed || to == StateCancelled || to == StateRejected
	case StateSealed:
		return to == StateClaimed || to == StateOrphaned || to == StateCancelled || to == StateRejected
	default:
		return false
	}
}
