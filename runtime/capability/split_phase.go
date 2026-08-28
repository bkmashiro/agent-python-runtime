package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	Submitted                        uint32
	Reused                           uint32
	PhysicalStarts                   uint32
	PhysicalFinishes                 uint32
	LogicalClaims                    uint32
	Consumed                         uint32
	Discarded                        uint32
	Cancelled                        uint32
	Failed                           uint32
	PhysicalResultBytes              uint64
	MaximumConcurrent                uint32
	EventsDropped                    uint32
	CandidatesPrepared               uint32
	CandidatesAdopted                uint32
	CandidatesRejected               uint32
	CanonicalStarts                  uint32
	JobsLinearized                   uint32
	JobsMaterialized                 uint32
	Validations                      uint32
	ValidationFailures               uint32
	ValidationCostUnits              uint64
	ProviderValidationPhysicalEvents uint32
	Events                           []SplitPhaseEvent
}

type splitPhaseEntry struct {
	slotID                 string
	call                   request
	request                []byte
	prepared               *PreparedPreDispatch
	preparedPLM            *PreparedPLM
	cancel                 context.CancelFunc
	done                   chan struct{}
	outcome                StagedCapabilityOutcome
	runErr                 error
	materializing          bool
	consumed               bool
	discarded              bool
	plmContract            *PLMContract
	certificate            *CandidateCertificate
	candidateState         CandidateState
	jobState               JobState
	canonical              bool
	rejected               bool
	stableFailureValidated bool
}

// SplitPhaseTable owns a bounded set of physical Future attempts for one Run.
// Broker.Call remains the only owner of logical operation indices and receipts.
type SplitPhaseTable struct {
	mu                  sync.Mutex
	owner               *Broker
	runIdentity         string
	plan                *Plan
	limits              SplitPhaseLimits
	eventLimit          uint32
	startedAt           time.Time
	entriesBySlot       map[string]*splitPhaseEntry
	entriesByCall       map[string]*splitPhaseEntry
	closed              bool
	reservedCostUnits   uint64
	reservedResultBytes uint64
	active              uint32
	snapshot            SplitPhaseSnapshot
}

func NewSplitPhaseTable(owner *Broker, limits SplitPhaseLimits) (*SplitPhaseTable, error) {
	if owner == nil {
		return nil, ErrSplitPhaseUnavailable
	}
	plan := owner.CapabilityPlan()
	if plan == nil || plan.Identity() == "" || owner.RunIdentity() == "" || !limits.valid() || limits.MaxCalls > plan.MaxCalls() {
		return nil, ErrSplitPhaseUnavailable
	}
	table := &SplitPhaseTable{
		owner: owner, runIdentity: owner.RunIdentity(), plan: plan, limits: limits, startedAt: time.Now(), eventLimit: splitPhaseEventLimit(limits.MaxCalls),
		entriesBySlot: make(map[string]*splitPhaseEntry, limits.MaxCalls),
		entriesByCall: make(map[string]*splitPhaseEntry, limits.MaxCalls),
	}
	if err := owner.AttachStagedClaimer(table); err != nil {
		return nil, err
	}
	return table, nil
}

func (table *SplitPhaseTable) RunIdentity() string {
	if table == nil {
		return ""
	}
	return table.runIdentity
}

func (table *SplitPhaseTable) PlanIdentity() string {
	if table == nil || table.plan == nil {
		return ""
	}
	return table.plan.Identity()
}

// IssueOrReuse is the historical predecessor entry point. Gate 5 removes it
// after the compiler and Guest ABI use PrepareOrReuse.
func (table *SplitPhaseTable) IssueOrReuse(ctx context.Context, slotID string, raw []byte) error {
	return table.issue(ctx, slotID, raw, nil, nil)
}

// PrepareOrReuse starts or reuses one Host-private PLM candidate. It creates no
// logical Broker call or receipt. Contracts and validators come only from the
// sealed Plan; the caller cannot widen temporal admission.
func (table *SplitPhaseTable) PrepareOrReuse(ctx context.Context, slotID string, raw []byte, contract PLMContract, certificate CandidateCertificate) error {
	if contract.Validate() != nil || contract.PrepareEffect == PrepareNone {
		return ErrSplitPhaseUnavailable
	}
	return table.issue(ctx, slotID, raw, &contract, &certificate)
}

// PrepareRuntimePLM builds the candidate certificate entirely from Host-owned
// Run, Plan, adapter and sealed-source state. The Guest supplies only the
// compiler-emitted occurrence slot and the ordinary canonical request.
func (table *SplitPhaseTable) PrepareRuntimePLM(ctx context.Context, slotID string, raw []byte, sourceSealIdentity string) error {
	if table == nil || ctx == nil || !validSHA256Identity(sourceSealIdentity) {
		return ErrSplitPhaseUnavailable
	}
	call, siteID, occurrence, err := runtimePLMCall(slotID, raw)
	if err != nil {
		return err
	}
	contract, ok := table.plan.PLMContract(call.Capability)
	if !ok || (contract.Temporal != TemporalImmutable && contract.Temporal != TemporalCurrent) {
		return ErrSplitPhaseUnavailable
	}
	prepared, err := table.plan.PreparePLM(call.Capability, call.Arguments)
	if err != nil {
		return ErrSplitPhaseUnavailable
	}
	arguments := prepared.Arguments()
	argumentsDigest := sha256.Sum256(arguments)
	resourceIdentity, err := prepared.ResourceIdentity()
	if err != nil {
		return ErrSplitPhaseUnavailable
	}
	providerSessionIdentity, err := prepared.ProviderSessionIdentity(ctx)
	if err != nil {
		return ErrSplitPhaseUnavailable
	}
	certificate := CandidateCertificate{
		Binding: CandidateBinding{
			RunIdentity: table.RunIdentity(), PlanIdentity: table.PlanIdentity(), SourceSealIdentity: sourceSealIdentity,
			SiteID: siteID, Occurrence: occurrence, Capability: call.Capability, HandlerIdentity: prepared.HandlerIdentity(),
			ArgumentsSHA256: fmt.Sprintf("sha256:%x", argumentsDigest[:]), AuthorityEpoch: table.PlanIdentity(),
			ProviderSessionIdentity: providerSessionIdentity,
		},
		Temporal: TemporalEvidence{Mode: contract.Temporal, ResourceIdentity: resourceIdentity},
	}
	return table.PrepareOrReuse(ctx, slotID, raw, contract, certificate)
}

func (table *SplitPhaseTable) LinearizeRuntimePLM(ctx context.Context, slotID string, raw []byte, sourceSealIdentity string) ([]byte, error) {
	if table == nil || ctx == nil || !validSHA256Identity(sourceSealIdentity) {
		return nil, ErrSplitPhaseUnavailable
	}
	call, siteID, occurrence, err := runtimePLMCall(slotID, raw)
	if err != nil {
		return nil, err
	}
	return table.LinearizeAndMaterialize(ctx, slotID, PLMLogicalContext{
		SourceSealIdentity: sourceSealIdentity, SiteID: siteID, Occurrence: occurrence,
		AuthorityEpoch: table.PlanIdentity(), ActualArguments: append(json.RawMessage(nil), call.Arguments...),
	})
}

func runtimePLMCall(slotID string, raw []byte) (request, string, uint32, error) {
	call, err := decodeSplitPhaseRequest(raw)
	if err != nil || !strings.HasPrefix(slotID, "slot-") {
		return request{}, "", 0, ErrSplitPhaseUnavailable
	}
	separator := strings.LastIndexByte(slotID, '-')
	if separator <= len("slot-") || separator == len(slotID)-1 {
		return request{}, "", 0, ErrSplitPhaseUnavailable
	}
	occurrence64, err := strconv.ParseUint(slotID[separator+1:], 10, 32)
	siteID := strings.TrimPrefix(slotID[:separator], "slot-")
	if err != nil || occurrence64 == 0 || !validIdentity(siteID) || call.CallID != "plm-"+siteID+"-"+strconv.FormatUint(occurrence64, 10) {
		return request{}, "", 0, ErrSplitPhaseUnavailable
	}
	return call, siteID, uint32(occurrence64), nil
}

func (table *SplitPhaseTable) issue(ctx context.Context, slotID string, raw []byte, plmContract *PLMContract, certificate *CandidateCertificate) error {
	if table == nil || ctx == nil || !validIdentity(slotID) {
		return ErrSplitPhaseUnavailable
	}
	call, err := decodeSplitPhaseRequest(raw)
	if err != nil {
		return err
	}
	var prepared *PreparedPreDispatch
	var preparedPLM *PreparedPLM
	var canonicalArguments json.RawMessage
	costUnits := uint32(1)
	maxResultBytes := uint64(maxCallBytes)
	if plmContract != nil {
		sealed, ok := table.plan.PLMContract(call.Capability)
		if !ok || sealed != *plmContract || certificate == nil {
			return ErrSplitPhaseUnavailable
		}
		preparedPLM, err = table.plan.PreparePLM(call.Capability, call.Arguments)
		if err != nil {
			return ErrSplitPhaseUnavailable
		}
		canonicalArguments = preparedPLM.Arguments()
		costUnits, maxResultBytes = plmContract.CostUnits, plmContract.MaxResultBytes
		call.Arguments = canonicalArguments
		if validatePLMPreparation(table, preparedPLM, call, *plmContract, *certificate) != nil {
			return ErrSplitPhaseUnavailable
		}
	} else {
		prepared, err = table.plan.PreparePreDispatch(call.Capability, call.Arguments)
		if err != nil {
			return ErrSplitPhaseUnavailable
		}
		if qualification, ok := table.plan.PreDispatch(call.Capability); ok && qualification.Eligible() {
			contract := qualification.Contract()
			costUnits, maxResultBytes = contract.CostUnits, contract.MaxResultBytes
		}
		canonicalArguments = prepared.Arguments()
		call.Arguments = canonicalArguments
	}
	canonicalRequest, err := json.Marshal(call)
	if err != nil || len(canonicalRequest) == 0 || len(canonicalRequest) > maxCallBytes {
		return ErrSplitPhaseUnavailable
	}

	table.mu.Lock()
	if table.closed {
		table.mu.Unlock()
		return ErrSplitPhaseUnavailable
	}
	if existing, exists := table.entriesBySlot[slotID]; exists {
		if existing.materializing || existing.consumed || existing.discarded {
			table.mu.Unlock()
			return ErrSplitPhaseConsumed
		}
		if !bytes.Equal(existing.request, canonicalRequest) || table.entriesByCall[call.CallID] != existing ||
			!samePLMPreparation(existing, plmContract, certificate) {
			table.mu.Unlock()
			return ErrSplitPhaseMismatch
		}
		table.snapshot.Reused = saturatingIncrement(table.snapshot.Reused)
		table.recordLocked(existing, "reused")
		table.mu.Unlock()
		return nil
	}
	if _, exists := table.entriesByCall[call.CallID]; exists {
		table.mu.Unlock()
		return ErrSplitPhaseMismatch
	}
	if uint32(len(table.entriesBySlot)) >= table.limits.MaxCalls ||
		table.reservedCostUnits+uint64(costUnits) > table.limits.MaxCostUnits ||
		table.reservedResultBytes+maxResultBytes > table.limits.MaxResultBytes {
		table.mu.Unlock()
		return ErrSplitPhaseUnavailable
	}
	operationContext, cancel := context.WithCancel(ctx)
	entry := &splitPhaseEntry{
		slotID: slotID, call: call, request: canonicalRequest, prepared: prepared, preparedPLM: preparedPLM,
		cancel: cancel, done: make(chan struct{}),
	}
	if plmContract != nil {
		contractCopy, certificateCopy := *plmContract, *certificate
		entry.plmContract, entry.certificate = &contractCopy, &certificateCopy
		entry.candidateState = CandidatePrepared
		table.snapshot.CandidatesPrepared++
	}
	table.entriesBySlot[slotID] = entry
	table.entriesByCall[call.CallID] = entry
	table.reservedCostUnits += uint64(costUnits)
	table.reservedResultBytes += maxResultBytes
	table.snapshot.Submitted++
	table.recordLocked(entry, "submitted")
	table.mu.Unlock()

	go table.execute(operationContext, entry)
	return nil
}

func (table *SplitPhaseTable) execute(ctx context.Context, entry *splitPhaseEntry) {
	table.mu.Lock()
	if entry.plmContract != nil {
		entry.candidateState = CandidateRunning
	}
	table.snapshot.PhysicalStarts++
	table.active++
	if table.active > table.snapshot.MaximumConcurrent {
		table.snapshot.MaximumConcurrent = table.active
	}
	table.recordLocked(entry, "running")
	table.mu.Unlock()

	var outcome StagedCapabilityOutcome
	var runErr error
	if entry.preparedPLM != nil {
		outcome, runErr = entry.preparedPLM.Call(ctx)
	} else {
		outcome, runErr = entry.prepared.Call(ctx)
	}

	table.mu.Lock()
	table.active--
	table.snapshot.PhysicalFinishes++
	table.snapshot.PhysicalResultBytes += outcome.PhysicalResultBytes
	entry.outcome = outcome
	entry.runErr = runErr
	disposition := "ready"
	if entry.plmContract != nil {
		entry.candidateState = CandidateReady
		entry.certificate.Outcome = CandidateValue
	}
	if runErr != nil {
		disposition = "failed"
		table.snapshot.Failed++
		if entry.plmContract != nil {
			entry.candidateState = CandidateFailed
			entry.certificate.Outcome = CandidateFailure
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			disposition = "cancelled"
			table.snapshot.Failed--
			table.snapshot.Cancelled++
			if entry.plmContract != nil {
				entry.candidateState = CandidateCancelled
			}
		}
	} else if outcome.ErrorCode != "" && entry.plmContract != nil {
		entry.candidateState = CandidateFailed
		entry.certificate.Outcome = CandidateFailure
	}
	table.recordLocked(entry, disposition)
	close(entry.done)
	table.mu.Unlock()
}

// LinearizeAndMaterialize is the folded V1 L=M operation. Invocation of this
// method is the original logical source point. Host-owned adapter validation
// produces the temporal/provider proof; callers cannot assert proof booleans.
func (table *SplitPhaseTable) LinearizeAndMaterialize(ctx context.Context, slotID string, logical PLMLogicalContext) ([]byte, error) {
	if table == nil || table.owner == nil || ctx == nil {
		return nil, ErrSplitPhaseUnavailable
	}
	table.mu.Lock()
	entry, ok := table.entriesBySlot[slotID]
	if !ok || table.closed || entry.plmContract == nil || entry.certificate == nil || entry.preparedPLM == nil {
		table.mu.Unlock()
		return nil, ErrSplitPhaseUnavailable
	}
	if entry.materializing || entry.consumed || entry.jobState != "" {
		table.mu.Unlock()
		return nil, ErrSplitPhaseConsumed
	}
	entry.materializing = true
	entry.jobState = JobLinearized
	table.snapshot.JobsLinearized++
	contract, candidate, prepared := *entry.plmContract, *entry.certificate, entry.preparedPLM
	if candidate.Outcome == "" {
		candidate.Outcome = CandidateValue
	}
	table.recordLocked(entry, "linearized")
	table.mu.Unlock()

	actualArguments, argumentsErr := canonicalForSchema(prepared.registered.inputSchema, logical.ActualArguments)
	if argumentsErr != nil {
		actualArguments = append(json.RawMessage(nil), logical.ActualArguments...)
	}
	argumentsDigest := sha256.Sum256(actualArguments)
	providerSessionIdentity, providerSessionErr := prepared.ProviderSessionIdentity(ctx)
	actualBinding := CandidateBinding{
		RunIdentity: table.RunIdentity(), PlanIdentity: table.PlanIdentity(), SourceSealIdentity: logical.SourceSealIdentity,
		SiteID: logical.SiteID, Occurrence: logical.Occurrence, Capability: entry.call.Capability, HandlerIdentity: prepared.HandlerIdentity(),
		ArgumentsSHA256: fmt.Sprintf("sha256:%x", argumentsDigest[:]), AuthorityEpoch: logical.AuthorityEpoch,
		ProviderSessionIdentity: providerSessionIdentity,
	}
	actualCall := entry.call
	actualCall.Arguments = append(json.RawMessage(nil), actualArguments...)
	requestCopy, requestErr := json.Marshal(actualCall)
	if requestErr != nil || len(requestCopy) == 0 || len(requestCopy) > maxCallBytes {
		return nil, ErrSplitPhaseUnavailable
	}

	validation := PLMValidationResult{}
	var validationErr error
	if argumentsErr == nil && providerSessionErr == nil && actualBinding == candidate.Binding {
		validation, validationErr = prepared.Validate(ctx, PLMValidationRequest{
			Contract: contract, Certificate: candidate, Logical: logical, Outcome: candidate.Outcome,
		})
	}
	current := LinearizationContext{
		Binding: actualBinding, Temporal: validation.Temporal,
		TemporalValidated:                validationErr == nil && validation.TemporalValid,
		ProviderNonInterferenceValidated: validationErr == nil && validation.ProviderNonInterferenceValid,
		StableFailureValidated:           validationErr == nil && validation.StableFailureValid,
	}

	table.mu.Lock()
	table.snapshot.Validations++
	if validationErr != nil || argumentsErr != nil || providerSessionErr != nil || actualBinding != candidate.Binding || !validation.TemporalValid ||
		!validation.ProviderNonInterferenceValid || (candidate.Outcome == CandidateFailure && contract.Failure == FailureStable && !validation.StableFailureValid) {
		table.snapshot.ValidationFailures++
	}
	table.snapshot.ValidationCostUnits = saturatingAdd64(table.snapshot.ValidationCostUnits, uint64(validation.ValidationCostUnits))
	table.snapshot.ProviderValidationPhysicalEvents = saturatingAdd32(table.snapshot.ProviderValidationPhysicalEvents, validation.ProviderValidationPhysicalEvents)
	entry.stableFailureValidated = current.StableFailureValidated
	decision := DecidePLMLinearization(contract, candidate, current)
	if decision.Action != LinearizationAdopt {
		table.rejectPLMCandidateLocked(entry, decision.Reason)
	} else {
		table.recordLocked(entry, "candidate_validated")
	}
	table.mu.Unlock()

	encoded, callErr := table.owner.Call(ctx, requestCopy)

	table.mu.Lock()
	if callErr != nil {
		entry.jobState = JobFailed
		table.recordLocked(entry, "job_failed")
	} else {
		var logicalResponse response
		if json.Unmarshal(encoded, &logicalResponse) != nil || logicalResponse.Status != "ok" {
			entry.jobState = JobFailed
			table.recordLocked(entry, "job_failed")
		} else {
			entry.jobState = JobCompleted
			table.recordLocked(entry, "job_completed")
		}
		entry.jobState = JobMaterialized
		table.snapshot.JobsMaterialized++
		table.recordLocked(entry, "materialized")
	}
	table.mu.Unlock()
	return encoded, callErr
}

func (table *SplitPhaseTable) rejectPLMCandidateLocked(entry *splitPhaseEntry, reason LinearizationReason) {
	if entry == nil || entry.rejected {
		return
	}
	entry.rejected = true
	entry.canonical = true
	table.snapshot.CandidatesRejected++
	table.snapshot.CanonicalStarts++
	entry.cancel()
	if entry.candidateState == CandidateReady || entry.candidateState == CandidateFailed {
		entry.candidateState = CandidateDiscarded
		entry.discarded = true
		table.snapshot.Discarded++
	}
	table.recordLocked(entry, "candidate_rejected:"+string(reason))
}

// Materialize routes the original request through Broker so logical budget,
// operation order, schemas and receipts remain on the unchanged path.
func (table *SplitPhaseTable) Materialize(ctx context.Context, slotID string) ([]byte, error) {
	if table == nil || table.owner == nil || ctx == nil {
		return nil, ErrSplitPhaseUnavailable
	}
	table.mu.Lock()
	entry, ok := table.entriesBySlot[slotID]
	if !ok || table.closed || entry.plmContract != nil {
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
	return table.owner.Call(ctx, requestCopy)
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
	if entry.canonical {
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
	if entry.plmContract != nil && (entry.runErr != nil || entry.outcome.ErrorCode != "") {
		if ctx.Err() != nil {
			return StagedCapabilityOutcome{}, ctx.Err()
		}
		if entry.runErr != nil || entry.plmContract.Failure == FailureRetryAtLinearize || !entry.stableFailureValidated {
			table.rejectPLMCandidateLocked(entry, LinearizationRetryFailure)
			return StagedCapabilityOutcome{}, ErrStagedObservationNotTargeted
		}
	}
	if entry.runErr != nil {
		return StagedCapabilityOutcome{}, entry.runErr
	}
	if entry.outcome.Validate() != nil || entry.consumed || entry.discarded {
		return StagedCapabilityOutcome{}, ErrSplitPhaseMismatch
	}
	entry.consumed = true
	if entry.plmContract != nil {
		entry.candidateState = CandidateAdopted
		table.snapshot.CandidatesAdopted++
		table.recordLocked(entry, "candidate_adopted")
	}
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
		if entry.plmContract != nil && (entry.candidateState == CandidateReady || entry.candidateState == CandidateFailed) {
			entry.candidateState = CandidateDiscarded
		}
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
	if uint32(len(table.snapshot.Events)) >= table.eventLimit {
		table.snapshot.EventsDropped = saturatingIncrement(table.snapshot.EventsDropped)
		return
	}
	table.snapshot.Events = append(table.snapshot.Events, SplitPhaseEvent{
		SlotID: entry.slotID, CallID: entry.call.CallID, Disposition: disposition,
		AtNanos: time.Since(table.startedAt).Nanoseconds(),
	})
}

func splitPhaseEventLimit(maxCalls uint32) uint32 {
	const hardLimit = uint64(4096)
	limit := uint64(maxCalls)*8 + 8
	if limit > hardLimit {
		limit = hardLimit
	}
	return uint32(limit)
}

func saturatingIncrement(value uint32) uint32 {
	return saturatingAdd32(value, 1)
}

func saturatingAdd32(value, add uint32) uint32 {
	if ^uint32(0)-value < add {
		return ^uint32(0)
	}
	return value + add
}

func saturatingAdd64(value, add uint64) uint64 {
	if ^uint64(0)-value < add {
		return ^uint64(0)
	}
	return value + add
}

func validatePLMPreparation(table *SplitPhaseTable, prepared *PreparedPLM, call request, contract PLMContract, certificate CandidateCertificate) error {
	if table == nil || prepared == nil || prepared.Contract() != contract || !certificate.Binding.valid() ||
		certificate.Temporal.Mode != contract.Temporal || certificate.Temporal.ResourceIdentity == "" ||
		(certificate.Outcome != "" && certificate.Outcome != CandidateValue) {
		return ErrSplitPhaseUnavailable
	}
	digest := sha256.Sum256(prepared.Arguments())
	resourceIdentity, resourceErr := prepared.ResourceIdentity()
	binding := certificate.Binding
	if resourceErr != nil || certificate.Temporal.ResourceIdentity != resourceIdentity || binding.RunIdentity != table.RunIdentity() ||
		binding.PlanIdentity != table.PlanIdentity() || binding.Capability != call.Capability ||
		binding.HandlerIdentity != prepared.HandlerIdentity() || binding.ArgumentsSHA256 != fmt.Sprintf("sha256:%x", digest[:]) {
		return ErrSplitPhaseMismatch
	}
	return nil
}

func samePLMPreparation(entry *splitPhaseEntry, contract *PLMContract, certificate *CandidateCertificate) bool {
	if entry == nil || (entry.plmContract == nil) != (contract == nil) || (entry.certificate == nil) != (certificate == nil) {
		return false
	}
	if contract == nil {
		return true
	}
	return *entry.plmContract == *contract && entry.certificate.Binding == certificate.Binding && entry.certificate.Temporal == certificate.Temporal
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
