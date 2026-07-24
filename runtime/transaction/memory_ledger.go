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
	approvals        map[string]approvalRecord
	authorityIndex   map[string]string
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
		approvals:        make(map[string]approvalRecord),
		authorityIndex:   make(map[string]string),
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
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
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

func (ledger *MemoryLedger) registerApproval(record approvalRecord) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if err := validateApprovalRecord(record); err != nil {
		return err
	}
	if priorDigest, exists := ledger.authorityIndex[record.AuthorityID]; exists {
		prior := ledger.approvals[priorDigest]
		if sameApprovalGrant(prior, record) {
			return nil
		}
		return ErrAlreadyExists
	}
	if prior, exists := ledger.approvals[record.TokenDigest]; exists {
		if sameApprovalGrant(prior, record) {
			return nil
		}
		return ErrAlreadyExists
	}
	operation, exists := ledger.operations[record.OperationID]
	if !exists || operation.TransactionID != record.TransactionID || operation.ManifestDigest != record.ManifestDigest || operation.State != approvalExpectedState(record.Source) {
		return ErrConflict
	}
	record.RegisteredAt = record.RegisteredAt.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	ledger.approvals[record.TokenDigest] = record
	ledger.authorityIndex[record.AuthorityID] = record.TokenDigest
	return nil
}

func (ledger *MemoryLedger) findApproval(tokenDigest string) (approvalRecord, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	record, exists := ledger.approvals[tokenDigest]
	if !exists {
		return approvalRecord{}, ErrNotFound
	}
	return record, nil
}

func (ledger *MemoryLedger) ListApprovals(transactionID string) ([]ApprovalEvidence, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if _, exists := ledger.transactions[transactionID]; !exists {
		return nil, ErrNotFound
	}
	values := make([]ApprovalEvidence, 0)
	for _, record := range ledger.approvals {
		if record.TransactionID == transactionID {
			values = append(values, record.ApprovalEvidence)
		}
	}
	sort.Slice(values, func(left, right int) bool {
		if !values[left].RegisteredAt.Equal(values[right].RegisteredAt) {
			return values[left].RegisteredAt.Before(values[right].RegisteredAt)
		}
		return values[left].AuthorityID < values[right].AuthorityID
	})
	return values, nil
}

func (ledger *MemoryLedger) consumeApprovalAndAuthorize(tokenDigest string, observedAt time.Time) (Operation, approvalRecord, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	record, exists := ledger.approvals[tokenDigest]
	if !exists {
		return Operation{}, approvalRecord{}, ErrNotFound
	}
	operation, exists := ledger.operations[record.OperationID]
	if !exists || operation.TransactionID != record.TransactionID || operation.ManifestDigest != record.ManifestDigest {
		return Operation{}, approvalRecord{}, ErrConflict
	}
	if !record.ConsumedAt.IsZero() {
		if operation.State == OperationReady {
			return operation, record, nil
		}
		return Operation{}, approvalRecord{}, ErrConflict
	}
	if !record.ExpiresAt.After(observedAt) {
		return Operation{}, approvalRecord{}, ErrExpired
	}
	from := approvalExpectedState(record.Source)
	if operation.State != from || ValidateOperationTransition(from, OperationReady) != nil {
		return Operation{}, approvalRecord{}, ErrConflict
	}
	operation.State = OperationReady
	operation.Version++
	operation.UpdatedAt = observedAt.UTC()
	record.ConsumedAt = operation.UpdatedAt
	ledger.operations[operation.ID] = operation
	ledger.approvals[tokenDigest] = record
	ledger.appendTransition(operation.TransactionID, "operation", operation.ID, string(from), string(OperationReady), operation.UpdatedAt)
	return operation, record, nil
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
	value.CreatedAt = value.CreatedAt.UTC()
	value.UpdatedAt = value.CreatedAt
	value.LeaseExpiresAt = value.LeaseExpiresAt.UTC()
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

func (ledger *MemoryLedger) completeAttempt(id string, version uint64, target AttemptState, receiptDigest string, observedAt time.Time) (Attempt, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if observedAt.IsZero() || (receiptDigest != "" && !digestPattern.MatchString(receiptDigest)) ||
		(target != AttemptSucceeded && target != AttemptFailed && target != AttemptAmbiguous) ||
		(target != AttemptSucceeded && receiptDigest != "") {
		return Attempt{}, ErrInvalidInput
	}
	value, exists := ledger.attempts[id]
	if !exists {
		return Attempt{}, ErrNotFound
	}
	if value.State == target && attemptTerminal(target) {
		if value.ProviderReceiptDigest == receiptDigest {
			return value, nil
		}
		return Attempt{}, ErrConflict
	}
	if value.Version != version || value.State != AttemptDispatching || ValidateAttemptTransition(AttemptDispatching, target) != nil {
		return Attempt{}, ErrConflict
	}
	value.State = target
	value.ProviderReceiptDigest = receiptDigest
	value.Version++
	value.UpdatedAt = observedAt.UTC()
	ledger.attempts[id] = value
	ledger.appendTransition(value.TransactionID, "attempt", id, string(AttemptDispatching), string(target), value.UpdatedAt)
	return value, nil
}

func (ledger *MemoryLedger) reconcileAttempt(id string, version uint64, target AttemptState, observationDigest string, observedAt time.Time) (Attempt, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if !digestPattern.MatchString(observationDigest) || observedAt.IsZero() || (target != AttemptSucceeded && target != AttemptFailed) {
		return Attempt{}, ErrInvalidInput
	}
	value, exists := ledger.attempts[id]
	if !exists {
		return Attempt{}, ErrNotFound
	}
	if value.State == target && value.ReconciliationDigest == observationDigest {
		return value, nil
	}
	if value.Version != version || value.State != AttemptAmbiguous || value.ReconciliationDigest != "" {
		return Attempt{}, ErrConflict
	}
	value.State = target
	value.ReconciliationDigest = observationDigest
	value.Version++
	value.UpdatedAt = observedAt.UTC()
	ledger.attempts[id] = value
	ledger.appendTransition(value.TransactionID, "attempt", id, string(AttemptAmbiguous), string(target), value.UpdatedAt)
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

func (ledger *MemoryLedger) Snapshot(transactionID string) (JournalSnapshot, error) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	transactionValue, exists := ledger.transactions[transactionID]
	if !exists {
		return JournalSnapshot{}, ErrNotFound
	}
	snapshot := JournalSnapshot{
		Transaction: transactionValue,
		Operations:  make([]Operation, 0),
		Attempts:    make([]Attempt, 0),
		Approvals:   make([]ApprovalEvidence, 0),
		Transitions: append([]Transition(nil), ledger.transitions[transactionID]...),
	}
	for _, operation := range ledger.operations {
		if operation.TransactionID == transactionID {
			snapshot.Operations = append(snapshot.Operations, operation)
		}
	}
	sort.Slice(snapshot.Operations, func(left, right int) bool {
		return snapshot.Operations[left].Index < snapshot.Operations[right].Index
	})
	operationIndexes := make(map[string]uint32, len(snapshot.Operations))
	for _, operation := range snapshot.Operations {
		operationIndexes[operation.ID] = operation.Index
	}
	for _, attempt := range ledger.attempts {
		if attempt.TransactionID == transactionID {
			snapshot.Attempts = append(snapshot.Attempts, attempt)
		}
	}
	sort.Slice(snapshot.Attempts, func(left, right int) bool {
		if operationIndexes[snapshot.Attempts[left].OperationID] != operationIndexes[snapshot.Attempts[right].OperationID] {
			return operationIndexes[snapshot.Attempts[left].OperationID] < operationIndexes[snapshot.Attempts[right].OperationID]
		}
		if snapshot.Attempts[left].Kind != snapshot.Attempts[right].Kind {
			return snapshot.Attempts[left].Kind < snapshot.Attempts[right].Kind
		}
		if snapshot.Attempts[left].Ordinal != snapshot.Attempts[right].Ordinal {
			return snapshot.Attempts[left].Ordinal < snapshot.Attempts[right].Ordinal
		}
		return snapshot.Attempts[left].ID < snapshot.Attempts[right].ID
	})
	for _, approval := range ledger.approvals {
		if approval.TransactionID == transactionID {
			snapshot.Approvals = append(snapshot.Approvals, approval.ApprovalEvidence)
		}
	}
	sort.Slice(snapshot.Approvals, func(left, right int) bool {
		if !snapshot.Approvals[left].RegisteredAt.Equal(snapshot.Approvals[right].RegisteredAt) {
			return snapshot.Approvals[left].RegisteredAt.Before(snapshot.Approvals[right].RegisteredAt)
		}
		return snapshot.Approvals[left].AuthorityID < snapshot.Approvals[right].AuthorityID
	})
	return snapshot, nil
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
		return state == OperationReady || state == OperationFailedRetryable
	case AttemptRollback:
		return state == OperationApplied || state == OperationRollbackFailed
	case AttemptCompensate:
		return state == OperationCompensationRequired || state == OperationCompensationFailed
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
