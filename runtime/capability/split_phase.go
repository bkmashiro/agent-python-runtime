package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrSplitPhaseUnavailable = errors.New("split-phase Host call is unavailable")
	ErrSplitPhaseDuplicate   = errors.New("split-phase Host call identity is duplicate")
	ErrSplitPhaseMismatch    = errors.New("split-phase Host call does not match its submitted occurrence")
	ErrSplitPhaseConsumed    = errors.New("split-phase Host call is already consumed")
)

type SplitPhaseLimits struct {
	MaxCalls       uint32
	MaxCostUnits   uint64
	MaxResultBytes uint64
}

func (limits SplitPhaseLimits) valid() bool {
	return limits.MaxCalls > 0 && limits.MaxCostUnits > 0 && limits.MaxResultBytes > 0
}

type SplitPhaseEvent struct {
	SlotID      string
	CallID      string
	Disposition string
	AtNanos     int64
}

type SplitPhaseSnapshot struct {
	Submitted           uint32
	PhysicalStarts      uint32
	PhysicalFinishes    uint32
	LogicalClaims       uint32
	Consumed            uint32
	Discarded           uint32
	Cancelled           uint32
	Failed              uint32
	PhysicalResultBytes uint64
	MaximumConcurrent   uint32
	Events              []SplitPhaseEvent
}

type splitPhaseEntry struct {
	slotID        string
	call          request
	request       []byte
	prepared      *PreparedPreDispatch
	cancel        context.CancelFunc
	done          chan struct{}
	outcome       StagedCapabilityOutcome
	runErr        error
	materializing bool
	consumed      bool
	discarded     bool
}

// SplitPhaseTable owns a bounded set of physical read attempts for one Run.
// Broker.Call remains the only owner of logical operation indices and receipts.
type SplitPhaseTable struct {
	mu                  sync.Mutex
	plan                *Plan
	limits              SplitPhaseLimits
	startedAt           time.Time
	entriesBySlot       map[string]*splitPhaseEntry
	entriesByCall       map[string]*splitPhaseEntry
	closed              bool
	reservedCostUnits   uint64
	reservedResultBytes uint64
	active              uint32
	snapshot            SplitPhaseSnapshot
}

func NewSplitPhaseTable(plan *Plan, limits SplitPhaseLimits) (*SplitPhaseTable, error) {
	if plan == nil || plan.Identity() == "" || !limits.valid() || limits.MaxCalls > plan.MaxCalls() {
		return nil, ErrSplitPhaseUnavailable
	}
	return &SplitPhaseTable{
		plan: plan, limits: limits, startedAt: time.Now(),
		entriesBySlot: make(map[string]*splitPhaseEntry, limits.MaxCalls),
		entriesByCall: make(map[string]*splitPhaseEntry, limits.MaxCalls),
	}, nil
}

// Submit validates and starts one qualified physical read without entering the
// Broker logical call ledger.
func (table *SplitPhaseTable) Submit(ctx context.Context, slotID string, raw []byte) error {
	if table == nil || ctx == nil || !validIdentity(slotID) {
		return ErrSplitPhaseUnavailable
	}
	call, err := decodeSplitPhaseRequest(raw)
	if err != nil {
		return err
	}
	prepared, err := table.plan.PreparePreDispatch(call.Capability, call.Arguments)
	if err != nil {
		return ErrSplitPhaseUnavailable
	}
	qualification, ok := table.plan.PreDispatch(call.Capability)
	if !ok || !qualification.Eligible() {
		return ErrSplitPhaseUnavailable
	}
	contract := qualification.Contract()
	canonicalArguments := prepared.Arguments()
	call.Arguments = canonicalArguments
	canonicalRequest, err := json.Marshal(call)
	if err != nil || len(canonicalRequest) == 0 || len(canonicalRequest) > maxCallBytes {
		return ErrSplitPhaseUnavailable
	}

	table.mu.Lock()
	if table.closed {
		table.mu.Unlock()
		return ErrSplitPhaseUnavailable
	}
	if _, exists := table.entriesBySlot[slotID]; exists {
		table.mu.Unlock()
		return ErrSplitPhaseDuplicate
	}
	if _, exists := table.entriesByCall[call.CallID]; exists {
		table.mu.Unlock()
		return ErrSplitPhaseDuplicate
	}
	if uint32(len(table.entriesBySlot)) >= table.limits.MaxCalls ||
		table.reservedCostUnits+uint64(contract.CostUnits) > table.limits.MaxCostUnits ||
		table.reservedResultBytes+contract.MaxResultBytes > table.limits.MaxResultBytes {
		table.mu.Unlock()
		return ErrSplitPhaseUnavailable
	}
	operationContext, cancel := context.WithCancel(ctx)
	entry := &splitPhaseEntry{
		slotID: slotID, call: call, request: canonicalRequest, prepared: prepared,
		cancel: cancel, done: make(chan struct{}),
	}
	table.entriesBySlot[slotID] = entry
	table.entriesByCall[call.CallID] = entry
	table.reservedCostUnits += uint64(contract.CostUnits)
	table.reservedResultBytes += contract.MaxResultBytes
	table.snapshot.Submitted++
	table.recordLocked(entry, "submitted")
	table.mu.Unlock()

	go table.execute(operationContext, entry)
	return nil
}

func (table *SplitPhaseTable) execute(ctx context.Context, entry *splitPhaseEntry) {
	table.mu.Lock()
	table.snapshot.PhysicalStarts++
	table.active++
	if table.active > table.snapshot.MaximumConcurrent {
		table.snapshot.MaximumConcurrent = table.active
	}
	table.recordLocked(entry, "running")
	table.mu.Unlock()

	outcome, runErr := entry.prepared.Call(ctx)

	table.mu.Lock()
	table.active--
	table.snapshot.PhysicalFinishes++
	table.snapshot.PhysicalResultBytes += outcome.PhysicalResultBytes
	entry.outcome = outcome
	entry.runErr = runErr
	disposition := "ready"
	if runErr != nil {
		disposition = "failed"
		table.snapshot.Failed++
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			disposition = "cancelled"
			table.snapshot.Failed--
			table.snapshot.Cancelled++
		}
	}
	table.recordLocked(entry, disposition)
	close(entry.done)
	table.mu.Unlock()
}

// Materialize routes the original request through Broker so logical budget,
// operation order, schemas and receipts remain on the unchanged path.
func (table *SplitPhaseTable) Materialize(ctx context.Context, slotID string, broker *Broker) ([]byte, error) {
	if table == nil || broker == nil || ctx == nil {
		return nil, ErrSplitPhaseUnavailable
	}
	table.mu.Lock()
	entry, ok := table.entriesBySlot[slotID]
	if !ok || table.closed {
		table.mu.Unlock()
		return nil, ErrSplitPhaseUnavailable
	}
	if entry.materializing || entry.consumed || entry.discarded {
		table.mu.Unlock()
		return nil, ErrSplitPhaseConsumed
	}
	entry.materializing = true
	requestCopy := append([]byte(nil), entry.request...)
	table.recordLocked(entry, "materialize")
	table.mu.Unlock()
	return broker.Call(ctx, requestCopy)
}

// Claim is invoked only from Broker after logical call admission and schema
// validation. Exact call ID targeting prevents an ordinary call from consuming
// another pending physical result.
func (table *SplitPhaseTable) Claim(_ context.Context, _ string, _ json.RawMessage) (StagedCapabilityOutcome, error) {
	return StagedCapabilityOutcome{}, ErrStagedObservationNotTargeted
}

func (table *SplitPhaseTable) ClaimCall(ctx context.Context, callID, capabilityName string, arguments json.RawMessage) (StagedCapabilityOutcome, error) {
	if table == nil || ctx == nil {
		return StagedCapabilityOutcome{}, ErrSplitPhaseUnavailable
	}
	table.mu.Lock()
	entry, targeted := table.entriesByCall[callID]
	if !targeted {
		table.mu.Unlock()
		return StagedCapabilityOutcome{}, ErrStagedObservationNotTargeted
	}
	if !entry.materializing || entry.call.Capability != capabilityName || !bytes.Equal(entry.call.Arguments, arguments) {
		table.mu.Unlock()
		return StagedCapabilityOutcome{}, ErrSplitPhaseMismatch
	}
	if entry.consumed || entry.discarded {
		table.mu.Unlock()
		return StagedCapabilityOutcome{}, ErrSplitPhaseConsumed
	}
	done := entry.done
	table.mu.Unlock()

	select {
	case <-ctx.Done():
		return StagedCapabilityOutcome{}, ctx.Err()
	case <-done:
	}

	table.mu.Lock()
	defer table.mu.Unlock()
	if entry.runErr != nil {
		return StagedCapabilityOutcome{}, entry.runErr
	}
	if entry.outcome.Validate() != nil || entry.consumed || entry.discarded {
		return StagedCapabilityOutcome{}, ErrSplitPhaseMismatch
	}
	entry.consumed = true
	table.snapshot.LogicalClaims++
	table.snapshot.Consumed++
	table.recordLocked(entry, "consumed")
	return entry.outcome, nil
}

// Finalize joins every physical attempt. Unclaimed work is cancelled or
// discarded without creating logical Broker evidence.
func (table *SplitPhaseTable) Finalize(_ bool) error {
	if table == nil {
		return ErrSplitPhaseUnavailable
	}
	table.mu.Lock()
	if table.closed {
		table.mu.Unlock()
		return nil
	}
	table.closed = true
	entries := make([]*splitPhaseEntry, 0, len(table.entriesBySlot))
	for _, entry := range table.entriesBySlot {
		entries = append(entries, entry)
		if !entry.consumed {
			entry.cancel()
		}
	}
	table.mu.Unlock()

	for _, entry := range entries {
		<-entry.done
	}
	table.mu.Lock()
	for _, entry := range entries {
		if entry.consumed || entry.discarded {
			continue
		}
		entry.discarded = true
		table.snapshot.Discarded++
		table.recordLocked(entry, "discarded")
	}
	table.mu.Unlock()
	return nil
}

func (table *SplitPhaseTable) Snapshot() SplitPhaseSnapshot {
	if table == nil {
		return SplitPhaseSnapshot{}
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	copy := table.snapshot
	copy.Events = append([]SplitPhaseEvent(nil), table.snapshot.Events...)
	return copy
}

func (table *SplitPhaseTable) recordLocked(entry *splitPhaseEntry, disposition string) {
	table.snapshot.Events = append(table.snapshot.Events, SplitPhaseEvent{
		SlotID: entry.slotID, CallID: entry.call.CallID, Disposition: disposition,
		AtNanos: time.Since(table.startedAt).Nanoseconds(),
	})
}

func decodeSplitPhaseRequest(raw []byte) (request, error) {
	var call request
	if len(raw) == 0 || len(raw) > maxCallBytes || !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return call, ErrSplitPhaseUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil || !validIdentity(call.CallID) || !validName(call.Capability) || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		return request{}, ErrSplitPhaseUnavailable
	}
	return call, nil
}
