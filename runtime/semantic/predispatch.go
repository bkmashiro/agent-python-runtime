package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

var (
	ErrPreDispatchInvalid         = errors.New("invalid semantic pre-dispatch")
	ErrPreDispatchAlreadyStarted  = errors.New("semantic pre-dispatch already started")
	ErrPreDispatchNotStarted      = errors.New("semantic pre-dispatch has not started")
	ErrPreDispatchBudgetExhausted = errors.New("semantic pre-dispatch budget exhausted")
	ErrPreDispatchClaimMismatch   = errors.New("semantic pre-dispatch claim mismatch")
)

// PreDispatchLauncher makes physical concurrency an explicit Host decision. Launch
// must accept exactly one eventual execution; callers acquire any fallible scheduler
// capacity before Start. This removes an ambiguous "may have launched" error state.
// The semantic consumer never creates a goroutine or hidden task.
type PreDispatchLauncher interface {
	Launch(func())
}

const defaultPreDispatchResultBytesPerRead = uint64(1 << 20)

// PreDispatchBudget atomically consumes distinct Host-authored reservation
// identities and frozen worst-case cost/result bounds. It is private to one
// Run and is not a durable quota store.
type PreDispatchBudget struct {
	mu                   sync.Mutex
	remaining            uint32
	remainingCostUnits   uint64
	remainingResultBytes uint64
	reservations         map[string]struct{}
}

func NewPreDispatchBudget(maxPhysicalReads uint32) (*PreDispatchBudget, error) {
	return NewPreDispatchBudgetWithLimits(maxPhysicalReads, uint64(maxPhysicalReads), uint64(maxPhysicalReads)*defaultPreDispatchResultBytesPerRead)
}

// NewPreDispatchBudgetWithLimits freezes independent per-Run ceilings for
// physical attempts, declared provider cost and worst-case result bytes.
func NewPreDispatchBudgetWithLimits(maxPhysicalReads uint32, maxCostUnits, maxResultBytes uint64) (*PreDispatchBudget, error) {
	if maxPhysicalReads == 0 || maxCostUnits == 0 || maxResultBytes == 0 {
		return nil, ErrPreDispatchBudgetExhausted
	}
	return &PreDispatchBudget{
		remaining: maxPhysicalReads, remainingCostUnits: maxCostUnits, remainingResultBytes: maxResultBytes,
		reservations: make(map[string]struct{}),
	}, nil
}

func (budget *PreDispatchBudget) reserve(identity string, costUnits uint32, resultBytes uint64) error {
	if budget == nil || !digestPattern.MatchString(identity) || costUnits == 0 || resultBytes == 0 {
		return ErrPreDispatchBudgetExhausted
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.remaining == 0 || budget.remainingCostUnits < uint64(costUnits) || budget.remainingResultBytes < resultBytes {
		return ErrPreDispatchBudgetExhausted
	}
	if _, exists := budget.reservations[identity]; exists {
		return ErrPreDispatchBudgetExhausted
	}
	budget.reservations[identity] = struct{}{}
	budget.remaining--
	budget.remainingCostUnits -= uint64(costUnits)
	budget.remainingResultBytes -= resultBytes
	return nil
}

type PreDispatchSnapshot struct {
	PhysicalIssues      uint32
	PhysicalStarts      uint32
	PhysicalFinishes    uint32
	LogicalClaims       uint32
	RejectedClaims      uint32
	ReservedCostUnits   uint64
	ProviderCostUnits   uint64
	ReservedResultBytes uint64
	PhysicalResultBytes uint64
	Disposition         streaming.ObservationDisposition
}

// SemanticPreDispatch is a Run-private, one-call controller. It is both the
// explicit physical-start target and the Broker's exact dynamic-boundary
// claimer. Original Python remains unchanged.
type SemanticPreDispatch struct {
	mu                  sync.Mutex
	call                QualifiedCall
	claim               ObservationClaim
	identity            streaming.ObservationIdentity
	prepared            *capability.PreparedPreDispatch
	budget              *PreDispatchBudget
	done                chan struct{}
	cancel              context.CancelFunc
	started             bool
	closed              bool
	record              *streaming.StagedObservation
	runErr              error
	issues              uint32
	physical            uint32
	finished            uint32
	logical             uint32
	rejected            uint32
	costUnits           uint64
	reservedCostUnits   uint64
	providerCostUnits   uint64
	maxResultBytes      uint64
	reservedResultBytes uint64
	physicalResultBytes uint64
	disposition         streaming.ObservationDisposition
}

func NewSemanticPreDispatch(call QualifiedCall, plan *capability.Plan, budget *PreDispatchBudget) (*SemanticPreDispatch, error) {
	return newSemanticPreDispatch(call, plan, budget, true)
}

func newSemanticPreDispatch(call QualifiedCall, plan *capability.Plan, budget *PreDispatchBudget, requireExclusive bool) (*SemanticPreDispatch, error) {
	if !call.valid() || requireExclusive && !call.exclusiveDynamicCall || plan == nil || budget == nil || call.binding.PlanSHA256 != plan.Identity() {
		return nil, ErrPreDispatchInvalid
	}
	qualification, ok := plan.PreDispatch(call.capability)
	if !ok || !qualification.Eligible() {
		return nil, ErrPreDispatchInvalid
	}
	contract := qualification.Contract()
	prepared, err := plan.PreparePreDispatch(call.capability, call.CanonicalArguments())
	if err != nil || !bytes.Equal(prepared.Arguments(), call.canonicalArguments) {
		return nil, ErrPreDispatchInvalid
	}
	claim := call.ExpectedObservationClaim()
	identity := semanticObservationIdentity(call, claim)
	if identity.Validate(true) != nil {
		return nil, ErrPreDispatchInvalid
	}
	return &SemanticPreDispatch{
		call: call.clone(), claim: claim, identity: identity, prepared: prepared,
		budget: budget, done: make(chan struct{}), costUnits: uint64(contract.CostUnits), maxResultBytes: contract.MaxResultBytes,
	}, nil
}

func semanticObservationIdentity(call QualifiedCall, claim ObservationClaim) streaming.ObservationIdentity {
	return streaming.ObservationIdentity{
		SchemaVersion: streaming.ObservationIdentitySchemaVersion,
		BindingKind:   streaming.ObservationBindingSemanticCall,
		StreamEpoch:   claim.StreamEpoch, WorkflowEpoch: claim.WorkflowEpoch,
		SourceSHA256: claim.SourceSHA256, CallSiteID: claim.CallSiteID,
		ClaimIdentitySHA256: call.ClaimIdentitySHA256(), BudgetReservationSHA256: claim.BudgetReservationSHA256,
		DynamicOccurrence: claim.DynamicOccurrence, ArgumentsSHA256: claim.ArgumentsSHA256,
		Capability: claim.Capability, SpecSHA256: claim.SpecSHA256, HandlerIdentity: claim.HandlerIdentity,
		PlanSHA256: claim.PlanSHA256, GrantPolicySHA256: claim.GrantPolicySHA256,
		FreshnessEpoch: claim.FreshnessEpoch, ExpiryEpoch: claim.ExpiryEpoch,
		PrivacyPartition: claim.PrivacyPartition, ParentLineageSHA256: claim.ParentLineageSHA256,
	}
}

// promoteFinalCall closes a streaming prefix identity over the exact final
// source occurrence before the final Guest can claim it.
func (controller *SemanticPreDispatch) promoteFinalCall(promoted QualifiedCall) error {
	if controller == nil || !promoted.valid() {
		return ErrAnalysisBinding
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	current := controller.call
	if controller.logical != 0 || current.capability != promoted.capability ||
		!bytes.Equal(current.canonicalArguments, promoted.canonicalArguments) ||
		current.argumentsSHA256 != promoted.argumentsSHA256 || current.resourceSHA256 != promoted.resourceSHA256 ||
		current.dynamicOccurrence != promoted.dynamicOccurrence || current.binding != promoted.binding ||
		current.streamEpoch != promoted.streamEpoch || current.workflowEpoch != promoted.workflowEpoch ||
		current.freshnessEpoch != promoted.freshnessEpoch || current.expiryEpoch != promoted.expiryEpoch ||
		current.privacyPartition != promoted.privacyPartition || current.parentLineageSHA256 != promoted.parentLineageSHA256 ||
		current.budgetReservationSHA256 != promoted.budgetReservationSHA256 ||
		current.startLine != promoted.startLine || current.startColumn != promoted.startColumn ||
		current.endLine != promoted.endLine || current.endColumn != promoted.endColumn {
		return ErrAnalysisBinding
	}
	claim := promoted.ExpectedObservationClaim()
	identity := semanticObservationIdentity(promoted, claim)
	if identity.Validate(true) != nil {
		return ErrAnalysisBinding
	}
	if controller.record != nil {
		if err := controller.record.PromoteSemanticIdentity(controller.identity, identity); err != nil {
			return err
		}
	}
	controller.call = promoted.clone()
	controller.claim = claim
	controller.identity = identity
	return nil
}

func (controller *SemanticPreDispatch) Start(ctx context.Context, launcher PreDispatchLauncher) error {
	if controller == nil || launcher == nil {
		return ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if controller.started {
		controller.mu.Unlock()
		return ErrPreDispatchAlreadyStarted
	}
	if err := controller.budget.reserve(controller.claim.BudgetReservationSHA256, uint32(controller.costUnits), controller.maxResultBytes); err != nil {
		controller.mu.Unlock()
		return err
	}
	controller.reservedCostUnits = controller.costUnits
	controller.reservedResultBytes = controller.maxResultBytes
	operationContext, cancel := context.WithCancel(ctx)
	controller.started = true
	controller.cancel = cancel
	controller.issues++
	controller.mu.Unlock()

	launcher.Launch(func() { controller.execute(operationContext) })
	return nil
}

func (controller *SemanticPreDispatch) execute(ctx context.Context) {
	controller.mu.Lock()
	controller.physical++
	controller.providerCostUnits = controller.costUnits
	controller.mu.Unlock()
	result, err := controller.prepared.Call(ctx)
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.finished++
	controller.physicalResultBytes = result.PhysicalResultBytes
	if err != nil {
		controller.runErr = err
		controller.disposition = streaming.ObservationFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			controller.disposition = streaming.ObservationCancelled
		}
		controller.closeDoneLocked()
		return
	}
	encodedOutcome, err := json.Marshal(result)
	if err == nil {
		var record *streaming.StagedObservation
		record, err = streaming.NewStagedObservation(controller.identity, encodedOutcome)
		if err == nil {
			controller.record = record
		}
	}
	if err != nil {
		controller.runErr = err
		controller.disposition = streaming.ObservationFailed
		controller.closeDoneLocked()
		return
	}
	controller.disposition = streaming.ObservationReady
	controller.closeDoneLocked()
}

// Claim is called by Broker only after normal capability and schema validation.
// A configured but mismatching staged call fails closed; it never falls back to
// a second live physical request.
func (controller *SemanticPreDispatch) Claim(ctx context.Context, capabilityName string, arguments json.RawMessage) (capability.StagedCapabilityOutcome, error) {
	if controller == nil {
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if !controller.started {
		controller.mu.Unlock()
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchNotStarted
	}
	expectedCapability := controller.call.capability
	expectedArguments := append([]byte(nil), controller.call.canonicalArguments...)
	done := controller.done
	controller.mu.Unlock()
	if capabilityName != expectedCapability || !bytes.Equal(arguments, expectedArguments) {
		controller.mu.Lock()
		controller.rejected++
		controller.mu.Unlock()
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchClaimMismatch
	}
	select {
	case <-ctx.Done():
		return capability.StagedCapabilityOutcome{}, ctx.Err()
	case <-done:
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	runErr := controller.runErr
	record := controller.record
	claim := controller.claim
	identity := controller.identity
	if runErr != nil {
		controller.rejected++
		return capability.StagedCapabilityOutcome{}, runErr
	}
	if !CanClaimStagedObservation(controller.call, claim).Allowed() {
		controller.rejected++
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchClaimMismatch
	}
	encoded, err := record.Consume(identity)
	if err != nil {
		controller.rejected++
		return capability.StagedCapabilityOutcome{}, err
	}
	var outcome capability.StagedCapabilityOutcome
	if err := json.Unmarshal(encoded, &outcome); err != nil || outcome.Validate() != nil {
		controller.rejected++
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchClaimMismatch
	}
	controller.logical++
	controller.disposition = streaming.ObservationConsumed
	return outcome, nil
}

func (controller *SemanticPreDispatch) TerminateUnclaimed(disposition streaming.ObservationDisposition) error {
	if controller == nil {
		return ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if !controller.started {
		controller.mu.Unlock()
		return ErrPreDispatchNotStarted
	}
	done := controller.done
	cancel := controller.cancel
	controller.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-done
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.record == nil {
		if controller.disposition == streaming.ObservationCancelled {
			return nil
		}
		return controller.runErr
	}
	if controller.disposition != streaming.ObservationReady {
		return nil
	}
	if err := controller.record.Terminate(disposition); err != nil {
		return err
	}
	controller.disposition = disposition
	return nil
}

// Finalize is the Broker lifecycle hook. A normal Run that never reached the
// exact claim is orphaned; a failed Run is cancelled. Already-consumed results
// are left unchanged.
func (controller *SemanticPreDispatch) Finalize(success bool) error {
	if controller == nil {
		return ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if !controller.started {
		controller.mu.Unlock()
		return ErrPreDispatchNotStarted
	}
	done := controller.done
	cancel := controller.cancel
	controller.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-done
	controller.mu.Lock()
	if controller.disposition != streaming.ObservationReady {
		controller.mu.Unlock()
		return nil
	}
	record := controller.record
	if record == nil {
		controller.mu.Unlock()
		return nil
	}
	disposition := streaming.ObservationCancelled
	if success {
		disposition = streaming.ObservationOrphaned
	}
	if err := record.Terminate(disposition); err != nil {
		controller.mu.Unlock()
		return err
	}
	controller.disposition = disposition
	controller.mu.Unlock()
	return nil
}

// ExecuteSemanticPreDispatch is the fail-closed lifecycle owner for the first
// default-off consumer. It covers failures before a Broker is created as well
// as normal dynamic-boundary claims.
func ExecuteSemanticPreDispatch(
	ctx context.Context,
	controller *SemanticPreDispatch,
	launcher PreDispatchLauncher,
	execute func() ([]byte, error),
) ([]byte, error) {
	if controller == nil || execute == nil {
		return nil, ErrPreDispatchInvalid
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := controller.Start(runContext, launcher); err != nil {
		return nil, err
	}
	payload, runErr := execute()
	if runErr != nil {
		cancel()
	}
	finalizeErr := controller.Finalize(runErr == nil)
	return payload, errors.Join(runErr, finalizeErr)
}

func (controller *SemanticPreDispatch) Snapshot() PreDispatchSnapshot {
	if controller == nil {
		return PreDispatchSnapshot{}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return PreDispatchSnapshot{
		PhysicalIssues: controller.issues, PhysicalStarts: controller.physical, PhysicalFinishes: controller.finished,
		LogicalClaims: controller.logical, RejectedClaims: controller.rejected,
		ReservedCostUnits: controller.reservedCostUnits, ProviderCostUnits: controller.providerCostUnits,
		ReservedResultBytes: controller.reservedResultBytes,
		PhysicalResultBytes: controller.physicalResultBytes, Disposition: controller.disposition,
	}
}

func (controller *SemanticPreDispatch) closeDoneLocked() {
	if !controller.closed {
		close(controller.done)
		controller.closed = true
	}
}
