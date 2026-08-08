package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}

type IDSource interface {
	New(prefix string) (string, error)
}

type coordinatorLedger interface {
	Ledger
	createTransaction(Transaction) error
	createOperation(Operation) error
	createAttempt(Attempt) error
	findAttemptByProviderRequest(string, string) (Attempt, error)
	transitionAttempt(string, uint64, AttemptState, AttemptState, time.Time) (Attempt, error)
	completeAttempt(string, uint64, AttemptState, string, time.Time) (Attempt, error)
	reconcileAttempt(string, uint64, AttemptState, string, string, time.Time) (Attempt, error)
	transitionTransaction(string, uint64, TransactionState, TransactionState, time.Time) (Transaction, error)
	transitionOperation(string, uint64, OperationState, OperationState, time.Time) (Operation, error)
	registerApproval(approvalRecord) error
	findApproval(string) (approvalRecord, error)
	consumeApprovalAndAuthorize(string, time.Time) (Operation, approvalRecord, error)
}

type Coordinator struct {
	mu        sync.Mutex
	ledger    coordinatorLedger
	ids       IDSource
	now       func() time.Time
	authority AuthorityVerifier
}

type BeginRequest struct {
	RunID         string
	CatalogDigest string
	Mode          TransactionMode
}

type ProposeRequest struct {
	TransactionID  string
	ToolID         string
	HandlerVersion string
	EffectClass    EffectClass
	Policy         PolicyOutcome
	PolicyVersion  string
	ArgumentDigest string
}

type CommitSource string

const (
	CommitSourceAgent CommitSource = "agent"
	CommitSourceUser  CommitSource = "user"
)

type AuthorityClaims struct {
	AuthorityID    string
	TransactionID  string
	OperationID    string
	ManifestDigest string
	Source         CommitSource
	SourceRunID    string
	ActorID        string
	PhaseGrantID   string
	ExpiresAt      time.Time
	Consumed       bool
}

type AuthorityVerifier interface {
	Verify(token string) (AuthorityClaims, error)
	Consume(token string) error
}

type ApprovalEvidence struct {
	AuthorityID    string
	TransactionID  string
	OperationID    string
	ManifestDigest string
	Source         CommitSource
	SourceRunID    string
	ActorID        string
	PhaseGrantID   string
	ExpiresAt      time.Time
	RegisteredAt   time.Time
	ConsumedAt     time.Time
}

type approvalRecord struct {
	ApprovalEvidence
	TokenDigest string
}

type CommitCredential struct {
	Token string
}

func NewCoordinator(ledger coordinatorLedger, ids IDSource, now func() time.Time, authority AuthorityVerifier) *Coordinator {
	return &Coordinator{ledger: ledger, ids: ids, now: now, authority: authority}
}

func (coordinator *Coordinator) InspectTransaction(id string) (Transaction, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(id) {
		return Transaction{}, ErrInvalidInput
	}
	return coordinator.ledger.GetTransaction(id)
}

func (coordinator *Coordinator) InspectAttempt(id string) (Attempt, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.ledger.GetAttempt(id)
}

func (coordinator *Coordinator) InspectOperation(id string) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(id) {
		return Operation{}, ErrInvalidInput
	}
	return coordinator.ledger.GetOperation(id)
}

type Inspection struct {
	Transaction Transaction
	Operations  []Operation
	Attempts    []Attempt
	Transitions []Transition
	AbortPlan   AbortPlan
}

func (coordinator *Coordinator) Inspect(transactionID string, autoCompensateTools map[string]bool) (Inspection, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(transactionID) {
		return Inspection{}, ErrInvalidInput
	}
	value, err := coordinator.ledger.GetTransaction(transactionID)
	if err != nil {
		return Inspection{}, err
	}
	operations, err := coordinator.ledger.ListOperations(transactionID)
	if err != nil {
		return Inspection{}, err
	}
	attempts, err := coordinator.ledger.ListAttempts(transactionID)
	if err != nil {
		return Inspection{}, err
	}
	transitions, err := coordinator.ledger.ListTransitions(transactionID)
	if err != nil {
		return Inspection{}, err
	}
	plan, err := BuildAbortPlan(operations, autoCompensateTools)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Transaction: value, Operations: operations, Attempts: attempts, Transitions: transitions, AbortPlan: plan}, nil
}

func (coordinator *Coordinator) FindDispatch(transactionID, providerRequestDigest string) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(transactionID) || !digestPattern.MatchString(providerRequestDigest) {
		return DispatchCompletion{}, ErrInvalidInput
	}
	attempt, err := coordinator.ledger.findAttemptByProviderRequest(transactionID, providerRequestDigest)
	if err != nil {
		return DispatchCompletion{}, err
	}
	operation, err := coordinator.ledger.GetOperation(attempt.OperationID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	transaction, err := coordinator.ledger.GetTransaction(transactionID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	return DispatchCompletion{Transaction: transaction, Operation: operation, Attempt: attempt}, nil
}

func (coordinator *Coordinator) Begin(request BeginRequest) (Transaction, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if request.RunID == "" || !digestPattern.MatchString(request.CatalogDigest) ||
		(request.Mode != TransactionModeDirect && request.Mode != TransactionModeWorkflow) {
		return Transaction{}, ErrInvalidInput
	}
	id, err := coordinator.ids.New("tx")
	if err != nil {
		return Transaction{}, fmt.Errorf("generate transaction id: %w", err)
	}
	now := coordinator.now().UTC()
	value := Transaction{
		ID: id, RunID: request.RunID, CatalogDigest: request.CatalogDigest,
		Mode: request.Mode, State: TransactionOpen, CreatedAt: now,
	}
	if err := coordinator.ledger.createTransaction(value); err != nil {
		return Transaction{}, err
	}
	return coordinator.ledger.GetTransaction(id)
}

func (coordinator *Coordinator) FinalizeWorkflow(transactionID string) (Transaction, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(transactionID) {
		return Transaction{}, ErrInvalidInput
	}
	value, err := coordinator.ledger.GetTransaction(transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if value.Mode != TransactionModeWorkflow || value.State != TransactionOpen {
		return Transaction{}, ErrConflict
	}
	operations, err := coordinator.ledger.ListOperations(transactionID)
	if err != nil {
		return Transaction{}, err
	}
	for _, operation := range operations {
		if operation.State != OperationApplied {
			return Transaction{}, ErrConflict
		}
	}
	return coordinator.ledger.transitionTransaction(value.ID, value.Version, TransactionOpen, TransactionCommitted, coordinator.now().UTC())
}

func (coordinator *Coordinator) Propose(request ProposeRequest) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	transaction, err := coordinator.ledger.GetTransaction(request.TransactionID)
	if err != nil {
		return Operation{}, err
	}
	if transaction.State != TransactionOpen || !validIdentifier(request.ToolID) ||
		!validIdentifier(request.HandlerVersion) || !validIdentifier(request.PolicyVersion) ||
		!validEffectClass(request.EffectClass) || !validPolicy(request.Policy) ||
		!digestPattern.MatchString(request.ArgumentDigest) {
		return Operation{}, ErrInvalidInput
	}
	operations, err := coordinator.ledger.ListOperations(request.TransactionID)
	if err != nil {
		return Operation{}, err
	}
	if transaction.Mode == TransactionModeDirect && len(operations) != 0 {
		return Operation{}, ErrDirectTransactionLimit
	}
	id, err := coordinator.ids.New("op")
	if err != nil {
		return Operation{}, fmt.Errorf("generate operation id: %w", err)
	}
	state := stateForPolicy(request.Policy)
	now := coordinator.now().UTC()
	index := uint32(len(operations) + 1)
	value := Operation{
		ID: id, TransactionID: transaction.ID, Index: index,
		ToolID: request.ToolID, HandlerVersion: request.HandlerVersion,
		EffectClass: request.EffectClass, Policy: request.Policy, PolicyVersion: request.PolicyVersion,
		State: state, ArgumentDigest: request.ArgumentDigest,
		ManifestDigest: manifestDigest(transaction, request, index), CreatedAt: now, UpdatedAt: now,
	}
	if err := coordinator.ledger.createOperation(value); err != nil {
		return Operation{}, err
	}
	return coordinator.ledger.GetOperation(id)
}

type DispatchRequest struct {
	OperationID           string
	Kind                  AttemptKind
	Ordinal               uint32
	LeaseDuration         time.Duration
	ProviderRequestDigest string
}

type Dispatch struct {
	Operation Operation
	Attempt   Attempt
}

type DispatchOutcome string

const (
	DispatchSucceeded DispatchOutcome = "succeeded"
	DispatchFailed    DispatchOutcome = "failed"
	DispatchAmbiguous DispatchOutcome = "ambiguous"
)

type CompleteDispatchRequest struct {
	OperationID           string
	AttemptID             string
	Outcome               DispatchOutcome
	ProviderReceiptDigest string
}

type DispatchCompletion struct {
	Transaction Transaction
	Operation   Operation
	Attempt     Attempt
}

func (coordinator *Coordinator) BeginDispatch(request DispatchRequest) (Dispatch, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.beginDispatchLocked(request, false)
}

func (coordinator *Coordinator) beginDispatchLocked(request DispatchRequest, undoAuthorized bool) (Dispatch, error) {
	if request.Kind != AttemptApply && !undoAuthorized {
		return Dispatch{}, ErrAuthorityDenied
	}
	if !validIdentifier(request.OperationID) || request.Ordinal == 0 || request.LeaseDuration <= 0 ||
		request.LeaseDuration > 5*time.Minute || !digestPattern.MatchString(request.ProviderRequestDigest) {
		return Dispatch{}, ErrInvalidInput
	}
	operation, err := coordinator.ledger.GetOperation(request.OperationID)
	if err != nil {
		return Dispatch{}, err
	}
	expected, active, ok := dispatchStates(request.Kind, operation.State)
	if !ok {
		return Dispatch{}, ErrConflict
	}
	attemptID, err := coordinator.ids.New("att")
	if err != nil {
		return Dispatch{}, fmt.Errorf("generate attempt id: %w", err)
	}
	leaseID, err := coordinator.ids.New("lease")
	if err != nil {
		return Dispatch{}, fmt.Errorf("generate lease id: %w", err)
	}
	now := coordinator.now().UTC()
	attempt := Attempt{
		ID: attemptID, TransactionID: operation.TransactionID, OperationID: operation.ID,
		Kind: request.Kind, Ordinal: request.Ordinal, State: AttemptLeased,
		ExpectedOperationState: expected, LeaseID: leaseID, LeaseExpiresAt: now.Add(request.LeaseDuration),
		ProviderRequestDigest: request.ProviderRequestDigest, CreatedAt: now, UpdatedAt: now,
	}
	if err := coordinator.ledger.createAttempt(attempt); err != nil {
		return Dispatch{}, err
	}
	attempt, err = coordinator.ledger.GetAttempt(attemptID)
	if err != nil {
		return Dispatch{}, err
	}
	attempt, err = coordinator.ledger.transitionAttempt(attempt.ID, attempt.Version, AttemptLeased, AttemptDispatching, now)
	if err != nil {
		return Dispatch{}, err
	}
	if operation.State != active {
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, operation.State, active, now)
		if err != nil {
			return Dispatch{}, err
		}
	}
	return Dispatch{Operation: operation, Attempt: attempt}, nil
}

type AbortStepRequest struct {
	TransactionID          string
	AutoCompensateTools    map[string]bool
	CompensationAuthorized bool
	Ordinal                uint32
	LeaseDuration          time.Duration
	ProviderRequestDigest  string
}

type AbortStep struct {
	Transaction     Transaction
	Plan            AbortPlan
	Dispatch        *Dispatch
	PriorCompletion *DispatchCompletion
	Done            bool
}

func (coordinator *Coordinator) BeginAbortStep(request AbortStepRequest) (AbortStep, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(request.TransactionID) || request.Ordinal == 0 || request.LeaseDuration <= 0 ||
		request.LeaseDuration > 5*time.Minute || !digestPattern.MatchString(request.ProviderRequestDigest) || len(request.AutoCompensateTools) > 1024 {
		return AbortStep{}, ErrInvalidInput
	}
	for toolID, allowed := range request.AutoCompensateTools {
		if !allowed || !validIdentifier(toolID) {
			return AbortStep{}, ErrInvalidInput
		}
	}
	value, err := coordinator.ledger.GetTransaction(request.TransactionID)
	if err != nil {
		return AbortStep{}, err
	}
	if value.Mode != TransactionModeWorkflow {
		return AbortStep{}, ErrConflict
	}
	priorAttempt, findErr := coordinator.ledger.findAttemptByProviderRequest(value.ID, request.ProviderRequestDigest)
	if findErr == nil {
		return coordinator.resumeAbortStepLocked(value, priorAttempt)
	}
	if !errors.Is(findErr, ErrNotFound) {
		return AbortStep{}, findErr
	}
	if value.State != TransactionOpen && value.State != TransactionAborting &&
		value.State != TransactionRollingBack && value.State != TransactionCompensating {
		return AbortStep{}, ErrConflict
	}
	operations, err := coordinator.ledger.ListOperations(value.ID)
	if err != nil {
		return AbortStep{}, err
	}
	plan, err := BuildAbortPlan(operations, request.AutoCompensateTools)
	if err != nil {
		return AbortStep{}, err
	}
	if plan.Disposition == AbortReconciliationRequired || plan.Disposition == AbortIrreversibleCommitted {
		return AbortStep{}, ErrConflict
	}
	if len(plan.CompensationOperationIDs) > 0 && !plan.AutoCompensate && !request.CompensationAuthorized {
		return AbortStep{Transaction: value, Plan: plan}, ErrCompensationAuthorizationRequired
	}
	now := coordinator.now().UTC()
	if value.State == TransactionOpen {
		value, err = coordinator.ledger.transitionTransaction(value.ID, value.Version, TransactionOpen, TransactionAborting, now)
		if err != nil {
			return AbortStep{}, err
		}
	}
	if len(plan.RollbackOperationIDs) > 0 {
		if value.State == TransactionAborting {
			value, err = coordinator.ledger.transitionTransaction(value.ID, value.Version, TransactionAborting, TransactionRollingBack, now)
			if err != nil {
				return AbortStep{}, err
			}
		}
		operation, getErr := coordinator.ledger.GetOperation(plan.RollbackOperationIDs[0])
		if getErr != nil {
			return AbortStep{}, getErr
		}
		if operation.State != OperationApplied && operation.State != OperationRollbackFailed {
			return AbortStep{}, ErrConflict
		}
		dispatch, dispatchErr := coordinator.beginDispatchLocked(DispatchRequest{OperationID: operation.ID, Kind: AttemptRollback, Ordinal: request.Ordinal, LeaseDuration: request.LeaseDuration, ProviderRequestDigest: request.ProviderRequestDigest}, true)
		if dispatchErr != nil {
			return AbortStep{}, dispatchErr
		}
		return AbortStep{Transaction: value, Plan: plan, Dispatch: &dispatch}, nil
	}
	if len(plan.CompensationOperationIDs) > 0 {
		if value.State == TransactionAborting || value.State == TransactionRollingBack {
			from := value.State
			value, err = coordinator.ledger.transitionTransaction(value.ID, value.Version, from, TransactionCompensating, now)
			if err != nil {
				return AbortStep{}, err
			}
		}
		operation, getErr := coordinator.ledger.GetOperation(plan.CompensationOperationIDs[0])
		if getErr != nil {
			return AbortStep{}, getErr
		}
		if operation.State == OperationApplied {
			operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, OperationApplied, OperationCompensationRequired, now)
			if err != nil {
				return AbortStep{}, err
			}
		}
		if operation.State != OperationCompensationRequired && operation.State != OperationCompensationFailed {
			return AbortStep{}, ErrConflict
		}
		dispatch, dispatchErr := coordinator.beginDispatchLocked(DispatchRequest{OperationID: operation.ID, Kind: AttemptCompensate, Ordinal: request.Ordinal, LeaseDuration: request.LeaseDuration, ProviderRequestDigest: request.ProviderRequestDigest}, true)
		if dispatchErr != nil {
			return AbortStep{}, dispatchErr
		}
		return AbortStep{Transaction: value, Plan: plan, Dispatch: &dispatch}, nil
	}
	target := TransactionAborted
	switch value.State {
	case TransactionRollingBack:
		target = TransactionRolledBack
	case TransactionCompensating:
		target = TransactionCompensated
	}
	if value.State != target {
		value, err = coordinator.ledger.transitionTransaction(value.ID, value.Version, value.State, target, now)
		if err != nil {
			return AbortStep{}, err
		}
	}
	return AbortStep{Transaction: value, Plan: plan, Done: true}, nil
}

func (coordinator *Coordinator) resumeAbortStepLocked(value Transaction, attempt Attempt) (AbortStep, error) {
	if attempt.TransactionID != value.ID || (attempt.Kind != AttemptRollback && attempt.Kind != AttemptCompensate) {
		return AbortStep{}, ErrConflict
	}
	operation, err := coordinator.ledger.GetOperation(attempt.OperationID)
	if err != nil || operation.TransactionID != value.ID {
		return AbortStep{}, ErrConflict
	}
	now := coordinator.now().UTC()
	switch attempt.State {
	case AttemptLeased:
		_, active, ok := dispatchStates(attempt.Kind, attempt.ExpectedOperationState)
		if !ok || operation.State != attempt.ExpectedOperationState {
			return AbortStep{}, ErrConflict
		}
		attempt, err = coordinator.ledger.transitionAttempt(attempt.ID, attempt.Version, AttemptLeased, AttemptDispatching, now)
		if err != nil {
			return AbortStep{}, err
		}
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, operation.State, active, now)
		if err != nil {
			return AbortStep{}, err
		}
		dispatch := Dispatch{Operation: operation, Attempt: attempt}
		return AbortStep{Transaction: value, Dispatch: &dispatch}, nil
	case AttemptDispatching:
		completion, completeErr := coordinator.completeDispatchLocked(CompleteDispatchRequest{OperationID: operation.ID, AttemptID: attempt.ID, Outcome: DispatchAmbiguous})
		if completeErr != nil {
			return AbortStep{}, completeErr
		}
		return AbortStep{Transaction: completion.Transaction, PriorCompletion: &completion}, ErrConflict
	case AttemptSucceeded, AttemptFailed:
		outcome := DispatchSucceeded
		if attempt.State == AttemptFailed {
			outcome = DispatchFailed
		}
		completion, completeErr := coordinator.completeDispatchLocked(CompleteDispatchRequest{OperationID: operation.ID, AttemptID: attempt.ID, Outcome: outcome})
		if completeErr != nil {
			return AbortStep{}, completeErr
		}
		return AbortStep{Transaction: completion.Transaction, PriorCompletion: &completion}, nil
	default:
		return AbortStep{}, ErrConflict
	}
}

func (coordinator *Coordinator) CompleteDispatch(request CompleteDispatchRequest) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.completeDispatchLocked(request)
}

func (coordinator *Coordinator) completeDispatchLocked(request CompleteDispatchRequest) (DispatchCompletion, error) {
	return coordinator.completeDispatchWithAuthorityLocked(request, false)
}

// CompleteAuthorizedDispatch records a successful irreversible provider
// dispatch only when the exact approval credential was already consumed for
// the operation and a provider receipt digest is present.
func (coordinator *Coordinator) CompleteAuthorizedDispatch(credential CommitCredential, request CompleteDispatchRequest) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if request.Outcome != DispatchSucceeded || !digestPattern.MatchString(request.ProviderReceiptDigest) ||
		!coordinator.consumedApprovalMatchesLocked(credential, request.OperationID) {
		return DispatchCompletion{}, ErrAuthorityDenied
	}
	return coordinator.completeDispatchWithAuthorityLocked(request, true)
}

func (coordinator *Coordinator) completeDispatchWithAuthorityLocked(request CompleteDispatchRequest, irreversibleAuthorized bool) (DispatchCompletion, error) {
	operation, err := coordinator.ledger.GetOperation(request.OperationID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	attempt, err := coordinator.ledger.GetAttempt(request.AttemptID)
	if err != nil || attempt.OperationID != operation.ID || attempt.TransactionID != operation.TransactionID {
		return DispatchCompletion{}, ErrNotFound
	}
	transaction, err := coordinator.ledger.GetTransaction(operation.TransactionID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	attemptTarget, operationTarget, ok := completionStates(attempt.Kind, request.Outcome)
	if !ok {
		return DispatchCompletion{}, ErrInvalidInput
	}
	_, operationActive, dispatchKnown := dispatchStates(attempt.Kind, attempt.ExpectedOperationState)
	if !dispatchKnown {
		return DispatchCompletion{}, ErrInvalidInput
	}
	if operation.State != attempt.ExpectedOperationState && operation.State != operationActive && operation.State != operationTarget {
		return DispatchCompletion{}, ErrConflict
	}
	if request.ProviderReceiptDigest != "" && !digestPattern.MatchString(request.ProviderReceiptDigest) {
		return DispatchCompletion{}, ErrInvalidInput
	}
	if request.Outcome != DispatchSucceeded && request.ProviderReceiptDigest != "" {
		return DispatchCompletion{}, ErrInvalidInput
	}
	if request.Outcome == DispatchSucceeded && operation.EffectClass == EffectIrreversible && !irreversibleAuthorized {
		return DispatchCompletion{}, ErrAuthorityDenied
	}
	if attempt.State == attemptTarget && attempt.ProviderReceiptDigest != request.ProviderReceiptDigest {
		return DispatchCompletion{}, ErrConflict
	}
	transactionTarget := transactionCompletionTarget(transaction, attempt.Kind, request.Outcome)
	if transactionTarget != "" && transaction.State != transactionTarget {
		if err := ValidateTransactionTransition(transaction.State, transactionTarget); err != nil {
			return DispatchCompletion{}, err
		}
	}
	now := coordinator.now().UTC()
	if attempt.State == AttemptDispatching {
		attempt, err = coordinator.ledger.completeAttempt(attempt.ID, attempt.Version, attemptTarget, request.ProviderReceiptDigest, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if attempt.State != attemptTarget {
		return DispatchCompletion{}, ErrConflict
	}
	if operation.State == attempt.ExpectedOperationState {
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, operation.State, operationActive, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	}
	if operation.State == operationActive {
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, operation.State, operationTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if operation.State != operationTarget {
		return DispatchCompletion{}, ErrConflict
	}
	if transactionTarget != "" && transaction.State != transactionTarget {
		transaction, err = coordinator.ledger.transitionTransaction(transaction.ID, transaction.Version, transaction.State, transactionTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	}
	return DispatchCompletion{Transaction: transaction, Operation: operation, Attempt: attempt}, nil
}

type ReconcileDispatchRequest struct {
	OperationID           string
	AttemptID             string
	Outcome               DispatchOutcome
	ProviderReceiptDigest string
	ObservationDigest     string
}

func (coordinator *Coordinator) ReconcileDispatch(request ReconcileDispatchRequest) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.reconcileDispatchWithAuthorityLocked(request, false)
}

// ReconcileAuthorizedDispatch permits an irreversible success observation only
// for the exact consumed approval credential bound to the operation.
func (coordinator *Coordinator) ReconcileAuthorizedDispatch(credential CommitCredential, request ReconcileDispatchRequest) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if request.Outcome != DispatchSucceeded || !coordinator.consumedApprovalMatchesLocked(credential, request.OperationID) {
		return DispatchCompletion{}, ErrAuthorityDenied
	}
	return coordinator.reconcileDispatchWithAuthorityLocked(request, true)
}

func (coordinator *Coordinator) reconcileDispatchWithAuthorityLocked(request ReconcileDispatchRequest, irreversibleAuthorized bool) (DispatchCompletion, error) {
	if !validIdentifier(request.OperationID) || !validIdentifier(request.AttemptID) || !digestPattern.MatchString(request.ObservationDigest) ||
		(request.ProviderReceiptDigest != "" && !digestPattern.MatchString(request.ProviderReceiptDigest)) ||
		(request.ProviderReceiptDigest != "" && request.ProviderReceiptDigest == request.ObservationDigest) ||
		(request.Outcome != DispatchSucceeded && request.Outcome != DispatchFailed) ||
		(request.Outcome == DispatchFailed && request.ProviderReceiptDigest != "") {
		return DispatchCompletion{}, ErrInvalidInput
	}
	operation, err := coordinator.ledger.GetOperation(request.OperationID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	if operation.EffectClass == EffectIrreversible && request.Outcome == DispatchSucceeded &&
		(!irreversibleAuthorized || !digestPattern.MatchString(request.ProviderReceiptDigest)) {
		return DispatchCompletion{}, ErrAuthorityDenied
	}
	attempt, err := coordinator.ledger.GetAttempt(request.AttemptID)
	if err != nil || attempt.OperationID != operation.ID || attempt.TransactionID != operation.TransactionID {
		return DispatchCompletion{}, ErrNotFound
	}
	transaction, err := coordinator.ledger.GetTransaction(operation.TransactionID)
	if err != nil {
		return DispatchCompletion{}, err
	}
	attemptTarget, operationTarget, ok := completionStates(attempt.Kind, request.Outcome)
	if !ok {
		return DispatchCompletion{}, ErrInvalidInput
	}
	now := coordinator.now().UTC()
	if attempt.State == AttemptAmbiguous {
		attempt, err = coordinator.ledger.reconcileAttempt(
			attempt.ID, attempt.Version, attemptTarget,
			request.ProviderReceiptDigest, request.ObservationDigest, now,
		)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if attempt.State != attemptTarget || attempt.ProviderReceiptDigest != request.ProviderReceiptDigest ||
		attempt.ReconciliationDigest != request.ObservationDigest {
		return DispatchCompletion{}, ErrConflict
	}
	if operation.State == OperationReconciliationRequired {
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, OperationReconciliationRequired, operationTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if operation.State != operationTarget {
		return DispatchCompletion{}, ErrConflict
	}
	transactionTarget := reconciliationTransactionTarget(transaction.Mode, attempt.Kind, request.Outcome)
	if transaction.State == TransactionReconciliationRequired {
		transaction, err = coordinator.ledger.transitionTransaction(transaction.ID, transaction.Version, TransactionReconciliationRequired, transactionTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if transaction.State != transactionTarget {
		return DispatchCompletion{}, ErrConflict
	}
	return DispatchCompletion{Transaction: transaction, Operation: operation, Attempt: attempt}, nil
}

func reconciliationTransactionTarget(mode TransactionMode, kind AttemptKind, outcome DispatchOutcome) TransactionState {
	if mode == TransactionModeDirect {
		if outcome == DispatchSucceeded {
			return TransactionCommitted
		}
		return TransactionRejected
	}
	switch kind {
	case AttemptApply:
		if outcome == DispatchSucceeded {
			return TransactionOpen
		}
		return TransactionAborting
	case AttemptRollback:
		if outcome == DispatchSucceeded {
			return TransactionRollingBack
		}
		return TransactionPartiallyReverted
	case AttemptCompensate:
		if outcome == DispatchSucceeded {
			return TransactionCompensating
		}
		return TransactionPartiallyCompensated
	default:
		return TransactionReconciliationRequired
	}
}

func transactionCompletionTarget(value Transaction, kind AttemptKind, outcome DispatchOutcome) TransactionState {
	if outcome == DispatchAmbiguous {
		return TransactionReconciliationRequired
	}
	if kind == AttemptRollback {
		if outcome == DispatchFailed {
			return TransactionPartiallyReverted
		}
		return ""
	}
	if kind == AttemptCompensate {
		if outcome == DispatchFailed {
			return TransactionPartiallyCompensated
		}
		return ""
	}
	if outcome == DispatchFailed {
		if value.Mode == TransactionModeDirect {
			return TransactionRejected
		}
		return TransactionAborting
	}
	if value.Mode == TransactionModeDirect {
		return TransactionCommitted
	}
	return ""
}

func dispatchStates(kind AttemptKind, state OperationState) (OperationState, OperationState, bool) {
	switch kind {
	case AttemptApply:
		if state == OperationReady || state == OperationFailedRetryable {
			return state, OperationApplying, true
		}
	case AttemptRollback:
		if state == OperationApplied || state == OperationRollbackFailed {
			return state, OperationRollingBack, true
		}
		if state == OperationRollingBack {
			return state, state, true
		}
	case AttemptCompensate:
		if state == OperationCompensationRequired || state == OperationCompensationFailed {
			return state, OperationCompensating, true
		}
		if state == OperationCompensating {
			return state, state, true
		}
	}
	return "", "", false
}

func completionStates(kind AttemptKind, outcome DispatchOutcome) (AttemptState, OperationState, bool) {
	if outcome == DispatchAmbiguous {
		return AttemptAmbiguous, OperationReconciliationRequired, true
	}
	switch kind {
	case AttemptApply:
		if outcome == DispatchSucceeded {
			return AttemptSucceeded, OperationApplied, true
		}
		if outcome == DispatchFailed {
			return AttemptFailed, OperationFailedTerminal, true
		}
	case AttemptRollback:
		if outcome == DispatchSucceeded {
			return AttemptSucceeded, OperationRolledBack, true
		}
		if outcome == DispatchFailed {
			return AttemptFailed, OperationRollbackFailed, true
		}
	case AttemptCompensate:
		if outcome == DispatchSucceeded {
			return AttemptSucceeded, OperationCompensated, true
		}
		if outcome == DispatchFailed {
			return AttemptFailed, OperationCompensationFailed, true
		}
	}
	return "", "", false
}

func dispatchOutcomeMatchesAttempt(outcome DispatchOutcome, state AttemptState) bool {
	return outcome == DispatchSucceeded && state == AttemptSucceeded ||
		outcome == DispatchFailed && state == AttemptFailed ||
		outcome == DispatchAmbiguous && state == AttemptAmbiguous
}

func (coordinator *Coordinator) RegisterApproval(credential CommitCredential, claims AuthorityClaims) (ApprovalEvidence, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(credential.Token) < 32 || len(credential.Token) > 512 || claims.Consumed ||
		!validIdentifier(claims.AuthorityID) || !validIdentifier(claims.ActorID) ||
		!validIdentifier(claims.TransactionID) || !validIdentifier(claims.OperationID) ||
		!digestPattern.MatchString(claims.ManifestDigest) {
		return ApprovalEvidence{}, ErrAuthorityDenied
	}
	now := coordinator.now().UTC()
	tokenDigest := approvalTokenDigest(credential.Token)
	if prior, findErr := coordinator.ledger.findApproval(tokenDigest); findErr == nil {
		if !approvalRecordMatchesClaims(prior, claims) {
			return ApprovalEvidence{}, ErrConflict
		}
		return prior.ApprovalEvidence, nil
	} else if !errors.Is(findErr, ErrNotFound) {
		return ApprovalEvidence{}, findErr
	}
	if !claims.ExpiresAt.After(now) {
		return ApprovalEvidence{}, ErrExpired
	}
	transaction, err := coordinator.ledger.GetTransaction(claims.TransactionID)
	if err != nil {
		return ApprovalEvidence{}, ErrAuthorityDenied
	}
	operation, err := coordinator.ledger.GetOperation(claims.OperationID)
	if err != nil || operation.TransactionID != transaction.ID {
		return ApprovalEvidence{}, ErrAuthorityDenied
	}
	if claims.ManifestDigest != operation.ManifestDigest {
		return ApprovalEvidence{}, ErrDigestMismatch
	}
	switch operation.State {
	case OperationAwaitingAgentCommit:
		if claims.Source != CommitSourceAgent || !validIdentifier(claims.SourceRunID) || !validIdentifier(claims.PhaseGrantID) {
			return ApprovalEvidence{}, ErrAuthorityDenied
		}
		if claims.SourceRunID == transaction.RunID {
			return ApprovalEvidence{}, ErrSameRunCommit
		}
	case OperationAwaitingUserApproval:
		if claims.Source != CommitSourceUser || claims.SourceRunID != "" || claims.PhaseGrantID != "" {
			return ApprovalEvidence{}, ErrAuthorityDenied
		}
	default:
		return ApprovalEvidence{}, ErrAuthorityDenied
	}
	evidence := ApprovalEvidence{
		AuthorityID: claims.AuthorityID, TransactionID: claims.TransactionID, OperationID: claims.OperationID,
		ManifestDigest: claims.ManifestDigest, Source: claims.Source, SourceRunID: claims.SourceRunID,
		ActorID: claims.ActorID, PhaseGrantID: claims.PhaseGrantID, ExpiresAt: claims.ExpiresAt.UTC(), RegisteredAt: now,
	}
	record := approvalRecord{ApprovalEvidence: evidence, TokenDigest: tokenDigest}
	if err := coordinator.ledger.registerApproval(record); err != nil {
		return ApprovalEvidence{}, err
	}
	return evidence, nil
}

func (coordinator *Coordinator) Authorize(credential CommitCredential) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if credential.Token == "" {
		return Operation{}, ErrAuthorityDenied
	}
	tokenDigest := approvalTokenDigest(credential.Token)
	if _, err := coordinator.ledger.findApproval(tokenDigest); err == nil {
		operation, _, consumeErr := coordinator.ledger.consumeApprovalAndAuthorize(tokenDigest, coordinator.now().UTC())
		return operation, consumeErr
	} else if !errors.Is(err, ErrNotFound) {
		return Operation{}, err
	}
	return Operation{}, ErrAuthorityDenied
}

func approvalTokenDigest(token string) string {
	sum := sha256.Sum256([]byte("approval-token\x00" + token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (coordinator *Coordinator) consumedApprovalMatchesLocked(credential CommitCredential, operationID string) bool {
	if len(credential.Token) < 32 || len(credential.Token) > 512 || !validIdentifier(operationID) {
		return false
	}
	record, err := coordinator.ledger.findApproval(approvalTokenDigest(credential.Token))
	return err == nil && record.OperationID == operationID && !record.ConsumedAt.IsZero()
}

func approvalExpectedState(source CommitSource) OperationState {
	if source == CommitSourceAgent {
		return OperationAwaitingAgentCommit
	}
	if source == CommitSourceUser {
		return OperationAwaitingUserApproval
	}
	return ""
}

func validateApprovalRecord(record approvalRecord) error {
	if !validIdentifier(record.AuthorityID) || !validIdentifier(record.TransactionID) ||
		!validIdentifier(record.OperationID) || !validIdentifier(record.ActorID) ||
		!digestPattern.MatchString(record.ManifestDigest) || !digestPattern.MatchString(record.TokenDigest) ||
		record.RegisteredAt.IsZero() || !record.ExpiresAt.After(record.RegisteredAt) || !record.ConsumedAt.IsZero() ||
		approvalExpectedState(record.Source) == "" {
		return ErrInvalidInput
	}
	if record.Source == CommitSourceUser && (record.SourceRunID != "" || record.PhaseGrantID != "") {
		return ErrInvalidInput
	}
	if record.Source == CommitSourceAgent && (!validIdentifier(record.SourceRunID) || !validIdentifier(record.PhaseGrantID)) {
		return ErrInvalidInput
	}
	return nil
}

func approvalRecordMatchesClaims(record approvalRecord, claims AuthorityClaims) bool {
	return record.AuthorityID == claims.AuthorityID && record.TransactionID == claims.TransactionID &&
		record.OperationID == claims.OperationID && record.ManifestDigest == claims.ManifestDigest && record.Source == claims.Source &&
		record.SourceRunID == claims.SourceRunID && record.ActorID == claims.ActorID && record.PhaseGrantID == claims.PhaseGrantID &&
		record.ExpiresAt.Equal(claims.ExpiresAt.UTC()) && !claims.Consumed
}

func sameApprovalGrant(left, right approvalRecord) bool {
	return left.TokenDigest == right.TokenDigest && approvalRecordMatchesClaims(left, AuthorityClaims{
		AuthorityID: right.AuthorityID, TransactionID: right.TransactionID, OperationID: right.OperationID,
		ManifestDigest: right.ManifestDigest, Source: right.Source, SourceRunID: right.SourceRunID,
		ActorID: right.ActorID, PhaseGrantID: right.PhaseGrantID, ExpiresAt: right.ExpiresAt,
	})
}

func stateForPolicy(policy PolicyOutcome) OperationState {
	switch policy {
	case PolicyDeny:
		return OperationDenied
	case PolicyAgentCommitRequired:
		return OperationAwaitingAgentCommit
	case PolicyUserApprovalRequired:
		return OperationAwaitingUserApproval
	default:
		return OperationReady
	}
}

func validEffectClass(value EffectClass) bool {
	switch value {
	case EffectReadOnly, EffectReversible, EffectCompensatable, EffectIrreversible:
		return true
	default:
		return false
	}
}

func validPolicy(value PolicyOutcome) bool {
	switch value {
	case PolicyDeny, PolicyAutoCommit, PolicyAgentCommitRequired, PolicyUserApprovalRequired:
		return true
	default:
		return false
	}
}

func manifestDigest(transaction Transaction, request ProposeRequest, index uint32) string {
	hash := sha256.New()
	for _, value := range []string{
		transaction.ID, transaction.RunID, transaction.CatalogDigest, string(transaction.Mode),
		fmt.Sprintf("%d", index), request.ToolID, request.HandlerVersion,
		string(request.EffectClass), string(request.Policy), request.PolicyVersion, request.ArgumentDigest,
	} {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
