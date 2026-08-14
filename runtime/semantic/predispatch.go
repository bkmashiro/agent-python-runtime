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

// PreDispatchLauncher makes physical concurrency an explicit Host decision. The
// semantic consumer never creates a goroutine or hidden task.
type PreDispatchLauncher interface {
	Launch(func()) error
}

// PreDispatchBudget atomically consumes distinct Host-authored reservation
// identities. It is private to one Run and is not a durable quota store.
type PreDispatchBudget struct {
	mu           sync.Mutex
	remaining    uint32
	reservations map[string]struct{}
}

func NewPreDispatchBudget(maxPhysicalReads uint32) (*PreDispatchBudget, error) {
	if maxPhysicalReads == 0 {
		return nil, ErrPreDispatchBudgetExhausted
	}
	return &PreDispatchBudget{remaining: maxPhysicalReads, reservations: make(map[string]struct{})}, nil
}

func (budget *PreDispatchBudget) reserve(identity string) error {
	if budget == nil || !digestPattern.MatchString(identity) {
		return ErrPreDispatchBudgetExhausted
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.remaining == 0 {
		return ErrPreDispatchBudgetExhausted
	}
	if _, exists := budget.reservations[identity]; exists {
		return ErrPreDispatchBudgetExhausted
	}
	budget.reservations[identity] = struct{}{}
	budget.remaining--
	return nil
}

type PreDispatchSnapshot struct {
	PhysicalIssues   uint32
	PhysicalStarts   uint32
	PhysicalFinishes uint32
	LogicalClaims    uint32
	RejectedClaims   uint32
	Disposition      streaming.ObservationDisposition
}

// SemanticPreDispatch is a Run-private, one-call controller. It is both the
// explicit physical-start target and the Broker's exact dynamic-boundary
// claimer. Original Python remains unchanged.
type SemanticPreDispatch struct {
	mu          sync.Mutex
	call        QualifiedCall
	claim       ObservationClaim
	identity    streaming.ObservationIdentity
	prepared    *capability.PreparedPreDispatch
	budget      *PreDispatchBudget
	done        chan struct{}
	started     bool
	closed      bool
	record      *streaming.StagedObservation
	runErr      error
	issues      uint32
	physical    uint32
	finished    uint32
	logical     uint32
	rejected    uint32
	disposition streaming.ObservationDisposition
}

func NewSemanticPreDispatch(call QualifiedCall, plan *capability.Plan, budget *PreDispatchBudget) (*SemanticPreDispatch, error) {
	if !call.valid() || plan == nil || budget == nil || call.binding.PlanSHA256 != plan.Identity() {
		return nil, ErrPreDispatchInvalid
	}
	prepared, err := plan.PreparePreDispatch(call.capability, call.CanonicalArguments())
	if err != nil || !bytes.Equal(prepared.Arguments(), call.canonicalArguments) {
		return nil, ErrPreDispatchInvalid
	}
	claim := call.ExpectedObservationClaim()
	identity := streaming.ObservationIdentity{
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
	if identity.Validate(true) != nil {
		return nil, ErrPreDispatchInvalid
	}
	return &SemanticPreDispatch{
		call: call.clone(), claim: claim, identity: identity, prepared: prepared,
		budget: budget, done: make(chan struct{}),
	}, nil
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
	if err := controller.budget.reserve(controller.claim.BudgetReservationSHA256); err != nil {
		controller.mu.Unlock()
		return err
	}
	controller.started = true
	controller.issues++
	controller.mu.Unlock()

	if err := launcher.Launch(func() { controller.execute(ctx) }); err != nil {
		controller.mu.Lock()
		controller.runErr = err
		controller.disposition = streaming.ObservationFailed
		controller.closeDoneLocked()
		controller.mu.Unlock()
		return err
	}
	return nil
}

func (controller *SemanticPreDispatch) execute(ctx context.Context) {
	controller.mu.Lock()
	controller.physical++
	controller.mu.Unlock()
	result, err := controller.prepared.Call(ctx)
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.finished++
	if err != nil {
		controller.runErr = err
		controller.disposition = streaming.ObservationFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			controller.disposition = streaming.ObservationCancelled
		}
		controller.closeDoneLocked()
		return
	}
	record, err := streaming.NewStagedObservation(controller.identity, result)
	if err != nil {
		controller.runErr = err
		controller.disposition = streaming.ObservationFailed
		controller.closeDoneLocked()
		return
	}
	controller.record = record
	controller.disposition = streaming.ObservationReady
	controller.closeDoneLocked()
}

// Claim is called by Broker only after normal capability and schema validation.
// A configured but mismatching staged call fails closed; it never falls back to
// a second live physical request.
func (controller *SemanticPreDispatch) Claim(ctx context.Context, capabilityName string, arguments json.RawMessage) (json.RawMessage, error) {
	if controller == nil {
		return nil, ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if !controller.started {
		controller.mu.Unlock()
		return nil, ErrPreDispatchNotStarted
	}
	expectedCapability := controller.call.capability
	expectedArguments := append([]byte(nil), controller.call.canonicalArguments...)
	done := controller.done
	controller.mu.Unlock()
	if capabilityName != expectedCapability || !bytes.Equal(arguments, expectedArguments) {
		controller.mu.Lock()
		controller.rejected++
		controller.mu.Unlock()
		return nil, ErrPreDispatchClaimMismatch
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
	}
	controller.mu.Lock()
	runErr := controller.runErr
	record := controller.record
	claim := controller.claim
	identity := controller.identity
	controller.mu.Unlock()
	if runErr != nil {
		controller.mu.Lock()
		controller.rejected++
		controller.mu.Unlock()
		return nil, runErr
	}
	if !CanClaimStagedObservation(controller.call, claim).Allowed() {
		controller.mu.Lock()
		controller.rejected++
		controller.mu.Unlock()
		return nil, ErrPreDispatchClaimMismatch
	}
	result, err := record.Consume(identity)
	if err != nil {
		controller.mu.Lock()
		controller.rejected++
		controller.mu.Unlock()
		return nil, err
	}
	controller.mu.Lock()
	controller.logical++
	controller.disposition = streaming.ObservationConsumed
	controller.mu.Unlock()
	return json.RawMessage(result), nil
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
	controller.mu.Unlock()
	<-done
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.record == nil {
		return controller.runErr
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
	controller.mu.Unlock()
	<-done
	controller.mu.Lock()
	if controller.disposition != streaming.ObservationReady {
		controller.mu.Unlock()
		return nil
	}
	record := controller.record
	controller.mu.Unlock()
	if record == nil {
		return nil
	}
	disposition := streaming.ObservationCancelled
	if success {
		disposition = streaming.ObservationOrphaned
	}
	if err := record.Terminate(disposition); err != nil {
		return err
	}
	controller.mu.Lock()
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
		LogicalClaims: controller.logical, RejectedClaims: controller.rejected, Disposition: controller.disposition,
	}
}

func (controller *SemanticPreDispatch) closeDoneLocked() {
	if !controller.closed {
		close(controller.done)
		controller.closed = true
	}
}
