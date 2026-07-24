package transaction

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryLedger struct {
	mu               sync.RWMutex
	transactions     map[string]Transaction
	operations       map[string]Operation
	attempts         map[string]Attempt
	operationIndex   map[string]string
	attemptKeys      map[string]string
	providerRequests map[string]string
	transitions      map[string][]Transition
}

func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{
		transactions:     make(map[string]Transaction),
		operations:       make(map[string]Operation),
		attempts:         make(map[string]Attempt),
		operationIndex:   make(map[string]string),
		attemptKeys:      make(map[string]string),
		providerRequests: make(map[string]string),
		transitions:      make(map[string][]Transition),
	}
}

func (ledger *MemoryLedger) createTransaction(value Transaction) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if !validIdentifier(value.ID) || !validIdentifier(value.RunID) || !digestPattern.MatchString(value.CatalogDigest) ||
		(value.Mode != TransactionModeDirect && value.Mode != TransactionModeWorkflow) ||
		value.State != TransactionOpen || value.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	if _, exists := ledger.transactions[value.ID]; exists {
		return ErrAlreadyExists
	}
	value.Version = 1
	value.UpdatedAt = value.CreatedAt
	ledger.transactions[value.ID] = value
	ledger.appendTransition(value.ID, "transaction", value.ID, "", string(value.State), value.CreatedAt)
	return nil
}

func (ledger *MemoryLedger) GetTransaction(id string) (Transaction, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	value, exists := ledger.transactions[id]
	if !exists {
		return Transaction{}, ErrNotFound
	}
	return value, nil
}

func (ledger *MemoryLedger) transitionTransaction(id string, version uint64, from, to TransactionState, observedAt time.Time) (Transaction, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	value, exists := ledger.transactions[id]
	if !exists {
		return Transaction{}, ErrNotFound
	}
	if value.Version != version || value.State != from {
		return Transaction{}, ErrConflict
	}
	if err := ValidateTransactionTransition(from, to); err != nil {
		return Transaction{}, err
	}
	value.State = to
	value.Version++
	value.UpdatedAt = observedAt
	ledger.transactions[id] = value
	ledger.appendTransition(id, "transaction", id, string(from), string(to), observedAt)
	return value, nil
}

func (ledger *MemoryLedger) createOperation(value Operation) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if _, exists := ledger.transactions[value.TransactionID]; !exists {
		return ErrNotFound
	}
	if !validIdentifier(value.ID) || value.Index == 0 || !validIdentifier(value.ToolID) ||
		!validIdentifier(value.HandlerVersion) || !validIdentifier(value.PolicyVersion) ||
		!validEffectClass(value.EffectClass) || !validPolicy(value.Policy) ||
		!validInitialOperationState(value.State) || !digestPattern.MatchString(value.ArgumentDigest) ||
		!digestPattern.MatchString(value.ManifestDigest) || value.CreatedAt.IsZero() {
		return ErrInvalidInput
	}
	if _, exists := ledger.operations[value.ID]; exists {
		return ErrAlreadyExists
	}
	indexKey := fmt.Sprintf("%s:%d", value.TransactionID, value.Index)
	if _, exists := ledger.operationIndex[indexKey]; exists {
		return ErrAlreadyExists
	}
	value.Version = 1
	ledger.operations[value.ID] = value
	ledger.operationIndex[indexKey] = value.ID
	ledger.appendTransition(value.TransactionID, "operation", value.ID, "", string(value.State), value.CreatedAt)
	return nil
}

func (ledger *MemoryLedger) GetOperation(id string) (Operation, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	value, exists := ledger.operations[id]
	if !exists {
		return Operation{}, ErrNotFound
	}
	return value, nil
}

func (ledger *MemoryLedger) ListOperations(transactionID string) ([]Operation, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if _, exists := ledger.transactions[transactionID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]Operation, 0)
	for _, operation := range ledger.operations {
		if operation.TransactionID == transactionID {
			result = append(result, operation)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Index < result[right].Index })
	return result, nil
}

func (ledger *MemoryLedger) transitionOperation(id string, version uint64, from, to OperationState, observedAt time.Time) (Operation, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	value, exists := ledger.operations[id]
	if !exists {
		return Operation{}, ErrNotFound
	}
	if value.Version != version || value.State != from {
		return Operation{}, ErrConflict
	}
	if err := ValidateOperationTransition(from, to); err != nil {
		return Operation{}, err
	}
	value.State = to
	value.Version++
	value.UpdatedAt = observedAt
	ledger.operations[id] = value
	ledger.appendTransition(value.TransactionID, "operation", id, string(from), string(to), observedAt)
	return value, nil
}

func (ledger *MemoryLedger) createAttempt(value Attempt) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	operation, exists := ledger.operations[value.OperationID]
	if !exists || operation.TransactionID != value.TransactionID {
		return ErrNotFound
	}
	if !validIdentifier(value.ID) || !validIdentifier(value.LeaseID) || value.Ordinal == 0 ||
		value.State != AttemptLeased || !validAttemptKind(value.Kind) ||
		!validAttemptPriorState(value.Kind, value.ExpectedOperationState) ||
		!digestPattern.MatchString(value.ProviderRequestDigest) || value.CreatedAt.IsZero() ||
		!value.LeaseExpiresAt.After(value.CreatedAt) {
		return ErrInvalidInput
	}
	if operation.State != value.ExpectedOperationState {
		return ErrConflict
	}
	if _, exists := ledger.attempts[value.ID]; exists {
		return ErrAlreadyExists
	}
	stableKey := fmt.Sprintf("%s:%s:%s:%d", value.TransactionID, value.OperationID, value.Kind, value.Ordinal)
	if _, exists := ledger.attemptKeys[stableKey]; exists {
		return ErrAlreadyExists
	}
	providerKey := value.TransactionID + ":" + value.ProviderRequestDigest
	if _, exists := ledger.providerRequests[providerKey]; exists {
		return ErrAlreadyExists
	}
	value.Version = 1
	ledger.attempts[value.ID] = value
	ledger.attemptKeys[stableKey] = value.ID
	ledger.providerRequests[providerKey] = value.ID
	ledger.appendTransition(value.TransactionID, "attempt", value.ID, "", string(value.State), value.CreatedAt)
	return nil
}

func (ledger *MemoryLedger) GetAttempt(id string) (Attempt, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	value, exists := ledger.attempts[id]
	if !exists {
		return Attempt{}, ErrNotFound
	}
	return value, nil
}

func (ledger *MemoryLedger) ListAttempts(transactionID string) ([]Attempt, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if _, exists := ledger.transactions[transactionID]; !exists {
		return nil, ErrNotFound
	}
	result := make([]Attempt, 0)
	for _, attempt := range ledger.attempts {
		if attempt.TransactionID == transactionID {
			result = append(result, attempt)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftIndex := ledger.operations[result[left].OperationID].Index
		rightIndex := ledger.operations[result[right].OperationID].Index
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		if result[left].Ordinal != result[right].Ordinal {
			return result[left].Ordinal < result[right].Ordinal
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (ledger *MemoryLedger) findAttemptByProviderRequest(transactionID, providerRequestDigest string) (Attempt, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	id, exists := ledger.providerRequests[transactionID+":"+providerRequestDigest]
	if !exists {
		return Attempt{}, ErrNotFound
	}
	value, exists := ledger.attempts[id]
	if !exists {
		return Attempt{}, ErrConflict
	}
	return value, nil
}

func (ledger *MemoryLedger) transitionAttempt(id string, version uint64, from, to AttemptState, observedAt time.Time) (Attempt, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	value, exists := ledger.attempts[id]
	if !exists {
		return Attempt{}, ErrNotFound
	}
	if value.Version != version || value.State != from {
		return Attempt{}, ErrConflict
	}
	if from == to && attemptTerminal(from) {
		return value, nil
	}
	if from == AttemptLeased && to == AttemptDispatching {
		if !observedAt.Before(value.LeaseExpiresAt) {
			return Attempt{}, ErrExpired
		}
		operation, exists := ledger.operations[value.OperationID]
		if !exists || operation.TransactionID != value.TransactionID || operation.State != value.ExpectedOperationState {
			return Attempt{}, ErrConflict
		}
	}
	if err := ValidateAttemptTransition(from, to); err != nil {
		return Attempt{}, err
	}
	value.State = to
	value.Version++
	value.UpdatedAt = observedAt
	ledger.attempts[id] = value
	ledger.appendTransition(value.TransactionID, "attempt", id, string(from), string(to), observedAt)
	return value, nil
}

func (ledger *MemoryLedger) ListTransitions(transactionID string) ([]Transition, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if _, exists := ledger.transactions[transactionID]; !exists {
		return nil, ErrNotFound
	}
	values := ledger.transitions[transactionID]
	return append([]Transition(nil), values...), nil
}

func (ledger *MemoryLedger) appendTransition(transactionID, entityType, entityID, from, to string, observedAt time.Time) {
	sequence := uint64(len(ledger.transitions[transactionID]) + 1)
	ledger.transitions[transactionID] = append(ledger.transitions[transactionID], Transition{
		Sequence: sequence, TransactionID: transactionID, EntityType: entityType,
		EntityID: entityID, From: from, To: to, ObservedAt: observedAt,
	})
}

func validInitialOperationState(state OperationState) bool {
	switch state {
	case OperationProposed, OperationStaged, OperationAwaitingAgentCommit,
		OperationAwaitingUserApproval, OperationReady, OperationDenied:
		return true
	default:
		return false
	}
}

func validAttemptKind(kind AttemptKind) bool {
	return kind == AttemptApply || kind == AttemptRollback || kind == AttemptCompensate
}

func validAttemptPriorState(kind AttemptKind, state OperationState) bool {
	switch kind {
	case AttemptApply:
		return state == OperationReady
	case AttemptRollback:
		return state == OperationRollingBack
	case AttemptCompensate:
		return state == OperationCompensating
	default:
		return false
	}
}

func attemptTerminal(state AttemptState) bool {
	switch state {
	case AttemptSucceeded, AttemptFailed, AttemptAmbiguous, AttemptExpired:
		return true
	default:
		return false
	}
}
