package transaction

import (
	"crypto/sha256"
	"encoding/hex"
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
	transitionTransaction(string, uint64, TransactionState, TransactionState, time.Time) (Transaction, error)
	transitionOperation(string, uint64, OperationState, OperationState, time.Time) (Operation, error)
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
	OperationID string
	AttemptID   string
	Outcome     DispatchOutcome
}

type DispatchCompletion struct {
	Transaction Transaction
	Operation   Operation
	Attempt     Attempt
}

func (coordinator *Coordinator) BeginDispatch(request DispatchRequest) (Dispatch, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if !validIdentifier(request.OperationID) || request.Ordinal == 0 || request.LeaseDuration <= 0 ||
		request.LeaseDuration > 5*time.Minute || !digestPattern.MatchString(request.ProviderRequestDigest) {
		return Dispatch{}, ErrInvalidInput
	}
	operation, err := coordinator.ledger.GetOperation(request.OperationID)
	if err != nil {
		return Dispatch{}, err
	}
	expected, active, ok := dispatchStates(request.Kind)
	if !ok || operation.State != expected {
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

func (coordinator *Coordinator) CompleteDispatch(request CompleteDispatchRequest) (DispatchCompletion, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
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
	now := coordinator.now().UTC()
	if attempt.State == AttemptDispatching {
		attempt, err = coordinator.ledger.transitionAttempt(attempt.ID, attempt.Version, AttemptDispatching, attemptTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if attempt.State != attemptTarget {
		return DispatchCompletion{}, ErrConflict
	}
	_, operationActive, _ := dispatchStates(attempt.Kind)
	if operation.State == operationActive {
		operation, err = coordinator.ledger.transitionOperation(operation.ID, operation.Version, operation.State, operationTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	} else if operation.State != operationTarget {
		return DispatchCompletion{}, ErrConflict
	}
	transactionTarget := transactionCompletionTarget(transaction, request.Outcome)
	if transactionTarget != "" && transaction.State != transactionTarget {
		transaction, err = coordinator.ledger.transitionTransaction(transaction.ID, transaction.Version, transaction.State, transactionTarget, now)
		if err != nil {
			return DispatchCompletion{}, err
		}
	}
	return DispatchCompletion{Transaction: transaction, Operation: operation, Attempt: attempt}, nil
}

func transactionCompletionTarget(value Transaction, outcome DispatchOutcome) TransactionState {
	if outcome == DispatchAmbiguous {
		return TransactionReconciliationRequired
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

func dispatchStates(kind AttemptKind) (OperationState, OperationState, bool) {
	switch kind {
	case AttemptApply:
		return OperationReady, OperationApplying, true
	case AttemptRollback:
		return OperationRollingBack, OperationRollingBack, true
	case AttemptCompensate:
		return OperationCompensating, OperationCompensating, true
	default:
		return "", "", false
	}
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

func (coordinator *Coordinator) Authorize(credential CommitCredential) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.authority == nil || credential.Token == "" {
		return Operation{}, ErrAuthorityDenied
	}
	claims, err := coordinator.authority.Verify(credential.Token)
	if err != nil || !validIdentifier(claims.AuthorityID) || !validIdentifier(claims.ActorID) ||
		!validIdentifier(claims.TransactionID) || !validIdentifier(claims.OperationID) {
		return Operation{}, ErrAuthorityDenied
	}
	transaction, err := coordinator.ledger.GetTransaction(claims.TransactionID)
	if err != nil {
		return Operation{}, ErrAuthorityDenied
	}
	operation, err := coordinator.ledger.GetOperation(claims.OperationID)
	if err != nil || operation.TransactionID != transaction.ID {
		return Operation{}, ErrAuthorityDenied
	}
	if claims.Consumed {
		if operation.State == OperationReady {
			return operation, nil
		}
		return Operation{}, ErrAuthorityDenied
	}
	if operation.State == OperationReady {
		return Operation{}, ErrAlreadyAuthorized
	}
	if claims.ManifestDigest != operation.ManifestDigest || !digestPattern.MatchString(claims.ManifestDigest) {
		return Operation{}, ErrDigestMismatch
	}
	now := coordinator.now().UTC()
	if !claims.ExpiresAt.After(now) {
		return Operation{}, ErrExpired
	}

	switch operation.State {
	case OperationAwaitingAgentCommit:
		if claims.Source != CommitSourceAgent || !validIdentifier(claims.SourceRunID) ||
			!validIdentifier(claims.PhaseGrantID) {
			return Operation{}, ErrAuthorityDenied
		}
		if claims.SourceRunID == transaction.RunID {
			return Operation{}, ErrSameRunCommit
		}
	case OperationAwaitingUserApproval:
		if claims.Source != CommitSourceUser || claims.SourceRunID != "" || claims.PhaseGrantID != "" {
			return Operation{}, ErrAuthorityDenied
		}
	default:
		return Operation{}, ErrAuthorityDenied
	}

	if err := coordinator.authority.Consume(credential.Token); err != nil {
		return Operation{}, ErrAuthorityDenied
	}
	return coordinator.ledger.transitionOperation(
		operation.ID, operation.Version, operation.State, OperationReady, now,
	)
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
