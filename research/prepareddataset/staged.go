package prepareddataset

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInvalidTransition        = errors.New("invalid staged-object transition")
	ErrInvalidObjectID          = errors.New("invalid staged-object identity")
	ErrInvalidLifecycleArgument = errors.New("invalid staged-object lifecycle argument")
)

// State is the physical/logical lifecycle of one Run-private staged object.
type State string

const (
	StatePlanned        State = "Planned"
	StateReadIssued     State = "ReadIssued"
	StateSourceVerified State = "SourceVerified"
	StateDecoded        State = "Decoded"
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
	State          State
	SourceIdentity string
	Metadata       Metadata
	BodyBytes      uint64
	Counters       Counters
	Disposition    string
}

// StagedObject owns one bounded, Run-private decoded array. Its body is held
// only between successful decode and the single terminal claim/orphan/cancel/
// reject decision; no failed or terminal object publishes a partial body.
type StagedObject struct {
	mu sync.Mutex

	id             string
	state          State
	sourceIdentity string
	pending        DecodedArray
	sealed         DecodedArray
	disposition    string
	counters       Counters
}

func NewStagedObject(id string) (*StagedObject, error) {
	if id == "" {
		return nil, ErrInvalidObjectID
	}
	return &StagedObject{id: id, state: StatePlanned}, nil
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

// IssueRead records the physical read issue. The optional byte count is an
// accounting hint from the bounded source reader; omitting it is permitted.
func (object *StagedObject) IssueRead(readBytes ...uint64) error {
	if len(readBytes) > 1 {
		return ErrInvalidLifecycleArgument
	}
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateReadIssued); err != nil {
		return err
	}
	object.counters.PhysicalReads++
	if len(readBytes) == 1 {
		object.counters.PhysicalReadBytes = readBytes[0]
	}
	return nil
}

// VerifySource joins the physical attempt to a source identity. The optional
// value is intentionally opaque and body-free; an omitted value records the
// narrow local verification event without inventing a source digest.
func (object *StagedObject) VerifySource(sourceIdentity ...string) error {
	if len(sourceIdentity) > 1 {
		return ErrInvalidLifecycleArgument
	}
	if object == nil {
		return ErrInvalidTransition
	}
	if len(sourceIdentity) == 1 && sourceIdentity[0] == "" {
		return ErrInvalidLifecycleArgument
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateSourceVerified); err != nil {
		return err
	}
	if len(sourceIdentity) == 1 {
		object.sourceIdentity = sourceIdentity[0]
	}
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
		return invalidTransition(object.state, StateDecoded)
	}
	object.counters.PhysicalDecodes++
	decoded, err := Decode(data)
	if err != nil {
		object.rejectLocked(err)
		return err
	}
	object.pending = decoded
	object.counters.PhysicalDecodedBytes = uint64(len(decoded.Body))
	if err := object.transitionLocked(StateDecoded); err != nil {
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

// Reject records a body-free terminal reason. The reason is optional so callers
// can preserve a typed error without forcing it into an evidence body.
func (object *StagedObject) Reject(reason ...error) error {
	if len(reason) > 1 {
		return ErrInvalidLifecycleArgument
	}
	if object == nil {
		return ErrInvalidTransition
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if err := object.transitionLocked(StateRejected); err != nil {
		return err
	}
	if len(reason) == 1 && reason[0] != nil {
		object.disposition = reason[0].Error()
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
		ID: object.id, State: object.state, SourceIdentity: object.sourceIdentity,
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
		return to == StateDecoded || to == StateCancelled || to == StateRejected
	case StateDecoded:
		return to == StateSealed || to == StateCancelled || to == StateRejected
	case StateSealed:
		return to == StateClaimed || to == StateOrphaned || to == StateCancelled || to == StateRejected
	default:
		return false
	}
}
