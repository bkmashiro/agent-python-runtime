package transaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"
)

const (
	TransactionEvidenceSchemaVersion        = "transaction-evidence/v3"
	maxEvidenceOperations                   = 1024
	maxEvidenceAttempts                     = 4096
	maxEvidenceApprovals                    = 1024
	maxEvidenceTransitions                  = 16384
	maxEvidenceOperationIndex               = 4096
	maxEvidenceAttemptOrdinal               = 1024
	maxEvidenceInteger               uint64 = 9007199254740991
)

var ErrInvalidEvidence = errors.New("invalid transaction evidence")

type TransactionEvidence struct {
	SchemaVersion  string                    `json:"schema_version"`
	EvidenceDigest string                    `json:"evidence_digest,omitempty"`
	CorrelationID  string                    `json:"correlation_id"`
	GeneratedAt    time.Time                 `json:"generated_at"`
	Transaction    EvidenceTransaction       `json:"transaction"`
	Operations     []EvidenceOperation       `json:"operations"`
	Attempts       []EvidenceAttempt         `json:"attempts"`
	Approvals      []EvidenceApproval        `json:"approvals"`
	Transitions    []EvidenceTransition      `json:"transitions"`
	Metrics        TransactionEvidenceMetric `json:"metrics"`
}

type EvidenceTransaction struct {
	ID            string           `json:"transaction_id"`
	RunID         string           `json:"run_id"`
	CatalogDigest string           `json:"catalog_digest"`
	Mode          TransactionMode  `json:"mode"`
	State         TransactionState `json:"state"`
	Version       uint64           `json:"version"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type EvidenceOperation struct {
	ID             string         `json:"operation_id"`
	TransactionID  string         `json:"transaction_id"`
	Index          uint32         `json:"index"`
	ToolID         string         `json:"tool_id"`
	HandlerVersion string         `json:"handler_version"`
	EffectClass    EffectClass    `json:"effect_class"`
	Policy         PolicyOutcome  `json:"policy"`
	PolicyVersion  string         `json:"policy_version"`
	State          OperationState `json:"state"`
	ArgumentDigest string         `json:"argument_digest"`
	ManifestDigest string         `json:"manifest_digest"`
	Version        uint64         `json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type EvidenceAttempt struct {
	ID                     string         `json:"attempt_id"`
	TransactionID          string         `json:"transaction_id"`
	OperationID            string         `json:"operation_id"`
	EffectClass            EffectClass    `json:"effect_class"`
	Kind                   AttemptKind    `json:"kind"`
	Ordinal                uint32         `json:"ordinal"`
	State                  AttemptState   `json:"state"`
	ExpectedOperationState OperationState `json:"expected_operation_state"`
	LeaseID                string         `json:"lease_id"`
	LeaseExpiresAt         time.Time      `json:"lease_expires_at"`
	ProviderRequestDigest  string         `json:"provider_request_digest"`
	ProviderReceiptDigest  string         `json:"provider_receipt_digest"`
	ReconciliationDigest   string         `json:"reconciliation_digest"`
	Version                uint64         `json:"version"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

type EvidenceApproval struct {
	AuthorityID    string       `json:"authority_id"`
	TransactionID  string       `json:"transaction_id"`
	OperationID    string       `json:"operation_id"`
	ManifestDigest string       `json:"manifest_digest"`
	Source         CommitSource `json:"source"`
	SourceRunID    string       `json:"source_run_id"`
	ActorID        string       `json:"actor_id"`
	PhaseGrantID   string       `json:"phase_grant_id"`
	ExpiresAt      time.Time    `json:"expires_at"`
	RegisteredAt   time.Time    `json:"registered_at"`
	ConsumedAt     *time.Time   `json:"consumed_at,omitempty"`
}

type EvidenceTransition struct {
	Sequence      uint64    `json:"sequence"`
	TransactionID string    `json:"transaction_id"`
	EntityType    string    `json:"entity_type"`
	EntityID      string    `json:"entity_id"`
	From          string    `json:"from"`
	To            string    `json:"to"`
	ObservedAt    time.Time `json:"observed_at"`
}

type evidenceAttemptIdentity struct {
	operationID string
	kind        AttemptKind
	ordinal     uint32
}

type TransactionEvidenceMetric struct {
	OperationTotal         uint32 `json:"operation_total"`
	AttemptTotal           uint32 `json:"attempt_total"`
	ApprovalTotal          uint32 `json:"approval_total"`
	ConsumedApprovals      uint32 `json:"consumed_approvals"`
	TransitionTotal        uint32 `json:"transition_total"`
	DispatchingAttempts    uint32 `json:"dispatching_attempts"`
	AmbiguousAttempts      uint32 `json:"ambiguous_attempts"`
	AppliedOperations      uint32 `json:"applied_operations"`
	RollbackFailed         uint32 `json:"rollback_failed"`
	CompensationFailed     uint32 `json:"compensation_failed"`
	ReconciliationRequired uint32 `json:"reconciliation_required"`
	ReconciledAttempts     uint32 `json:"reconciled_attempts"`
}

func BuildTransactionEvidence(ledger Ledger, transactionID string, generatedAt time.Time) (TransactionEvidence, error) {
	if ledger == nil || !validIdentifier(transactionID) || generatedAt.IsZero() {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	snapshot, err := ledger.Snapshot(transactionID)
	if err != nil {
		return TransactionEvidence{}, err
	}
	transaction := snapshot.Transaction
	operations := append([]Operation(nil), snapshot.Operations...)
	attempts := append([]Attempt(nil), snapshot.Attempts...)
	approvals := append([]ApprovalEvidence(nil), snapshot.Approvals...)
	transitions := append([]Transition(nil), snapshot.Transitions...)
	sort.Slice(operations, func(left, right int) bool {
		if operations[left].Index != operations[right].Index {
			return operations[left].Index < operations[right].Index
		}
		return operations[left].ID < operations[right].ID
	})
	operationIndexes := make(map[string]uint32, len(operations))
	for _, operation := range operations {
		operationIndexes[operation.ID] = operation.Index
	}
	sort.Slice(attempts, func(left, right int) bool {
		if operationIndexes[attempts[left].OperationID] != operationIndexes[attempts[right].OperationID] {
			return operationIndexes[attempts[left].OperationID] < operationIndexes[attempts[right].OperationID]
		}
		if attempts[left].Kind != attempts[right].Kind {
			return attempts[left].Kind < attempts[right].Kind
		}
		if attempts[left].Ordinal != attempts[right].Ordinal {
			return attempts[left].Ordinal < attempts[right].Ordinal
		}
		return attempts[left].ID < attempts[right].ID
	})
	sort.Slice(transitions, func(left, right int) bool { return transitions[left].Sequence < transitions[right].Sequence })
	if len(operations) > maxEvidenceOperations || len(attempts) > maxEvidenceAttempts || len(approvals) > maxEvidenceApprovals || len(transitions) > maxEvidenceTransitions ||
		!validEvidenceTransaction(transaction) {
		return TransactionEvidence{}, ErrInvalidEvidence
	}

	value := TransactionEvidence{
		SchemaVersion: TransactionEvidenceSchemaVersion,
		CorrelationID: evidenceCorrelationID(transaction),
		GeneratedAt:   generatedAt.UTC(),
		Transaction: EvidenceTransaction{
			ID: transaction.ID, RunID: transaction.RunID, CatalogDigest: transaction.CatalogDigest,
			Mode: transaction.Mode, State: transaction.State, Version: transaction.Version,
			CreatedAt: transaction.CreatedAt.UTC(), UpdatedAt: transaction.UpdatedAt.UTC(),
		},
		Operations:  make([]EvidenceOperation, len(operations)),
		Attempts:    make([]EvidenceAttempt, len(attempts)),
		Approvals:   make([]EvidenceApproval, len(approvals)),
		Transitions: make([]EvidenceTransition, len(transitions)),
	}
	operationIDs := make(map[string]struct{}, len(operations))
	operationEffects := make(map[string]EffectClass, len(operations))
	operationManifests := make(map[string]string, len(operations))
	operationsByID := make(map[string]Operation, len(operations))
	var priorIndex uint32
	for index, operation := range operations {
		if !validEvidenceOperation(operation, transaction.ID) || operation.Index <= priorIndex {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		if _, duplicate := operationIDs[operation.ID]; duplicate {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		priorIndex = operation.Index
		operationIDs[operation.ID] = struct{}{}
		operationEffects[operation.ID] = operation.EffectClass
		operationManifests[operation.ID] = operation.ManifestDigest
		operationsByID[operation.ID] = operation
		value.Operations[index] = EvidenceOperation{
			ID: operation.ID, TransactionID: operation.TransactionID, Index: operation.Index,
			ToolID: operation.ToolID, HandlerVersion: operation.HandlerVersion, EffectClass: operation.EffectClass,
			Policy: operation.Policy, PolicyVersion: operation.PolicyVersion, State: operation.State,
			ArgumentDigest: operation.ArgumentDigest, ManifestDigest: operation.ManifestDigest, Version: operation.Version,
			CreatedAt: operation.CreatedAt.UTC(), UpdatedAt: operation.UpdatedAt.UTC(),
		}
		switch operation.State {
		case OperationApplied:
			value.Metrics.AppliedOperations++
		case OperationRollbackFailed:
			value.Metrics.RollbackFailed++
		case OperationCompensationFailed:
			value.Metrics.CompensationFailed++
		case OperationReconciliationRequired:
			value.Metrics.ReconciliationRequired++
		}
	}
	if !validEvidenceTransactionOperations(transaction, operationsByID) {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	approvalIDs := make(map[string]struct{}, len(approvals))
	consumedApprovals := make(map[string][]time.Time)
	for index, approval := range approvals {
		if !validEvidenceApproval(approval, transaction.ID, operationManifests) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		if _, duplicate := approvalIDs[approval.AuthorityID]; duplicate {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		approvalIDs[approval.AuthorityID] = struct{}{}
		var consumedAt *time.Time
		if !approval.ConsumedAt.IsZero() {
			value := approval.ConsumedAt.UTC()
			consumedAt = &value
		}
		value.Approvals[index] = EvidenceApproval{
			AuthorityID: approval.AuthorityID, TransactionID: approval.TransactionID, OperationID: approval.OperationID,
			ManifestDigest: approval.ManifestDigest, Source: approval.Source, SourceRunID: approval.SourceRunID,
			ActorID: approval.ActorID, PhaseGrantID: approval.PhaseGrantID, ExpiresAt: approval.ExpiresAt.UTC(),
			RegisteredAt: approval.RegisteredAt.UTC(), ConsumedAt: consumedAt,
		}
		if !approval.ConsumedAt.IsZero() {
			consumedApprovals[approval.OperationID] = append(consumedApprovals[approval.OperationID], approval.ConsumedAt)
			value.Metrics.ConsumedApprovals++
		}
	}
	attemptIDs := make(map[string]struct{}, len(attempts))
	attemptIdentities := make(map[evidenceAttemptIdentity]struct{}, len(attempts))
	attemptsByID := make(map[string]Attempt, len(attempts))
	reconciledAttemptIDs := make(map[string]struct{})
	for index, attempt := range attempts {
		effectClass := operationEffects[attempt.OperationID]
		hasConsumedApproval := hasConsumedApprovalBefore(consumedApprovals[attempt.OperationID], attempt.CreatedAt)
		if _, ok := operationIDs[attempt.OperationID]; !ok || !validEvidenceAttempt(attempt, transaction.ID) ||
			(effectClass == EffectIrreversible && attempt.State == AttemptSucceeded &&
				(attempt.ProviderReceiptDigest == "" || !hasConsumedApproval)) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		if _, duplicate := attemptIDs[attempt.ID]; duplicate {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		identity := evidenceAttemptIdentity{operationID: attempt.OperationID, kind: attempt.Kind, ordinal: attempt.Ordinal}
		if _, duplicate := attemptIdentities[identity]; duplicate {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		attemptIdentities[identity] = struct{}{}
		attemptIDs[attempt.ID] = struct{}{}
		attemptsByID[attempt.ID] = attempt
		value.Attempts[index] = EvidenceAttempt{
			ID: attempt.ID, TransactionID: attempt.TransactionID, OperationID: attempt.OperationID, EffectClass: effectClass,
			Kind: attempt.Kind, Ordinal: attempt.Ordinal, State: attempt.State,
			ExpectedOperationState: attempt.ExpectedOperationState, LeaseID: attempt.LeaseID,
			LeaseExpiresAt: attempt.LeaseExpiresAt.UTC(), ProviderRequestDigest: attempt.ProviderRequestDigest,
			ProviderReceiptDigest: attempt.ProviderReceiptDigest, ReconciliationDigest: attempt.ReconciliationDigest,
			Version: attempt.Version, CreatedAt: attempt.CreatedAt.UTC(), UpdatedAt: attempt.UpdatedAt.UTC(),
		}
		if attempt.ReconciliationDigest != "" {
			reconciledAttemptIDs[attempt.ID] = struct{}{}
			value.Metrics.ReconciledAttempts++
		}
		switch attempt.State {
		case AttemptDispatching:
			value.Metrics.DispatchingAttempts++
		case AttemptAmbiguous:
			value.Metrics.AmbiguousAttempts++
		}
	}
	var priorObservedAt time.Time
	latestStates := make(map[string]string, 1+len(operations)+len(attempts))
	observedReconciledAttemptIDs := make(map[string]struct{})
	for index, transition := range transitions {
		if !validEvidenceTransition(transition, transaction.ID, uint64(index+1), operationIDs, attemptIDs, reconciledAttemptIDs) ||
			(!priorObservedAt.IsZero() && transition.ObservedAt.Before(priorObservedAt)) ||
			!validEvidenceTransitionCausality(transition, index, len(transitions), latestStates, operationsByID, attemptsByID) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		if transition.EntityType == "attempt" && transition.From == string(AttemptAmbiguous) &&
			(transition.To == string(AttemptSucceeded) || transition.To == string(AttemptFailed)) {
			if _, duplicate := observedReconciledAttemptIDs[transition.EntityID]; duplicate {
				return TransactionEvidence{}, ErrInvalidEvidence
			}
			observedReconciledAttemptIDs[transition.EntityID] = struct{}{}
		}
		stateKey := transition.EntityType + ":" + transition.EntityID
		priorState, seen := latestStates[stateKey]
		if (!seen && transition.From != "") || (seen && transition.From != priorState) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		latestStates[stateKey] = transition.To
		priorObservedAt = transition.ObservedAt
		value.Transitions[index] = EvidenceTransition{
			Sequence: transition.Sequence, TransactionID: transition.TransactionID,
			EntityType: transition.EntityType, EntityID: transition.EntityID,
			From: transition.From, To: transition.To, ObservedAt: transition.ObservedAt.UTC(),
		}
	}
	if !sameEvidenceIDSet(observedReconciledAttemptIDs, reconciledAttemptIDs) ||
		!validEvidenceEntityTransitionTimes(transaction, operationsByID, attemptsByID, transitions) ||
		!validEvidenceApprovalConsumptions(approvals, transitions) {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	if generatedAt.Before(transaction.UpdatedAt) || (!priorObservedAt.IsZero() && generatedAt.Before(priorObservedAt)) ||
		latestStates["transaction:"+transaction.ID] != string(transaction.State) {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	for _, operation := range operations {
		if latestStates["operation:"+operation.ID] != string(operation.State) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
	}
	for _, attempt := range attempts {
		if latestStates["attempt:"+attempt.ID] != string(attempt.State) {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
	}
	value.Metrics.OperationTotal = uint32(len(value.Operations))
	value.Metrics.AttemptTotal = uint32(len(value.Attempts))
	value.Metrics.ApprovalTotal = uint32(len(value.Approvals))
	value.Metrics.TransitionTotal = uint32(len(value.Transitions))
	if transaction.State == TransactionReconciliationRequired {
		value.Metrics.ReconciliationRequired++
	}
	digest, err := ComputeTransactionEvidenceDigest(value)
	if err != nil {
		return TransactionEvidence{}, err
	}
	value.EvidenceDigest = digest
	return value, nil
}

func ComputeTransactionEvidenceDigest(value TransactionEvidence) (string, error) {
	value.EvidenceDigest = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalidEvidence
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func VerifyTransactionEvidenceDigest(value TransactionEvidence) error {
	if value.SchemaVersion != TransactionEvidenceSchemaVersion || !digestPattern.MatchString(value.EvidenceDigest) || !validExportedTransactionEvidence(value) {
		return ErrInvalidEvidence
	}
	expected, err := ComputeTransactionEvidenceDigest(value)
	if err != nil || expected != value.EvidenceDigest {
		return ErrInvalidEvidence
	}
	return nil
}

func validExportedTransactionEvidence(value TransactionEvidence) bool {
	if value.Operations == nil || value.Attempts == nil || value.Approvals == nil || value.Transitions == nil ||
		len(value.Operations) > maxEvidenceOperations || len(value.Attempts) > maxEvidenceAttempts ||
		len(value.Approvals) > maxEvidenceApprovals || len(value.Transitions) > maxEvidenceTransitions || value.GeneratedAt.IsZero() {
		return false
	}
	transaction := Transaction{
		ID: value.Transaction.ID, RunID: value.Transaction.RunID, CatalogDigest: value.Transaction.CatalogDigest,
		Mode: value.Transaction.Mode, State: value.Transaction.State, Version: value.Transaction.Version,
		CreatedAt: value.Transaction.CreatedAt, UpdatedAt: value.Transaction.UpdatedAt,
	}
	if !validEvidenceTransaction(transaction) || value.CorrelationID != evidenceCorrelationID(transaction) || value.GeneratedAt.Before(transaction.UpdatedAt) {
		return false
	}

	metrics := TransactionEvidenceMetric{}
	operationIDs := make(map[string]struct{}, len(value.Operations))
	operationEffects := make(map[string]EffectClass, len(value.Operations))
	operationIndexes := make(map[string]uint32, len(value.Operations))
	operationManifests := make(map[string]string, len(value.Operations))
	operationsByID := make(map[string]Operation, len(value.Operations))
	var priorIndex uint32
	for _, exported := range value.Operations {
		operation := Operation{
			ID: exported.ID, TransactionID: exported.TransactionID, Index: exported.Index, ToolID: exported.ToolID,
			HandlerVersion: exported.HandlerVersion, EffectClass: exported.EffectClass, Policy: exported.Policy,
			PolicyVersion: exported.PolicyVersion, State: exported.State, ArgumentDigest: exported.ArgumentDigest,
			ManifestDigest: exported.ManifestDigest, Version: exported.Version, CreatedAt: exported.CreatedAt, UpdatedAt: exported.UpdatedAt,
		}
		if !validEvidenceOperation(operation, transaction.ID) || operation.Index <= priorIndex {
			return false
		}
		if _, duplicate := operationIDs[operation.ID]; duplicate {
			return false
		}
		priorIndex = operation.Index
		operationIDs[operation.ID] = struct{}{}
		operationEffects[operation.ID] = operation.EffectClass
		operationIndexes[operation.ID] = operation.Index
		operationManifests[operation.ID] = operation.ManifestDigest
		operationsByID[operation.ID] = operation
		switch operation.State {
		case OperationApplied:
			metrics.AppliedOperations++
		case OperationRollbackFailed:
			metrics.RollbackFailed++
		case OperationCompensationFailed:
			metrics.CompensationFailed++
		case OperationReconciliationRequired:
			metrics.ReconciliationRequired++
		}
	}
	if !validEvidenceTransactionOperations(transaction, operationsByID) {
		return false
	}

	approvalIDs := make(map[string]struct{}, len(value.Approvals))
	consumedApprovals := make(map[string][]time.Time)
	approvals := make([]ApprovalEvidence, 0, len(value.Approvals))
	for _, exported := range value.Approvals {
		var consumedAt time.Time
		if exported.ConsumedAt != nil {
			consumedAt = *exported.ConsumedAt
		}
		approval := ApprovalEvidence{
			AuthorityID: exported.AuthorityID, TransactionID: exported.TransactionID, OperationID: exported.OperationID,
			ManifestDigest: exported.ManifestDigest, Source: exported.Source, SourceRunID: exported.SourceRunID,
			ActorID: exported.ActorID, PhaseGrantID: exported.PhaseGrantID, ExpiresAt: exported.ExpiresAt,
			RegisteredAt: exported.RegisteredAt, ConsumedAt: consumedAt,
		}
		if !validEvidenceApproval(approval, transaction.ID, operationManifests) {
			return false
		}
		if _, duplicate := approvalIDs[approval.AuthorityID]; duplicate {
			return false
		}
		approvalIDs[approval.AuthorityID] = struct{}{}
		approvals = append(approvals, approval)
		if !approval.ConsumedAt.IsZero() {
			consumedApprovals[approval.OperationID] = append(consumedApprovals[approval.OperationID], approval.ConsumedAt)
			metrics.ConsumedApprovals++
		}
	}

	attemptIDs := make(map[string]struct{}, len(value.Attempts))
	attemptIdentities := make(map[evidenceAttemptIdentity]struct{}, len(value.Attempts))
	attemptsByID := make(map[string]Attempt, len(value.Attempts))
	reconciledAttemptIDs := make(map[string]struct{})
	var priorAttempt *EvidenceAttempt
	for index := range value.Attempts {
		exported := value.Attempts[index]
		effectClass, operationExists := operationEffects[exported.OperationID]
		attempt := Attempt{
			ID: exported.ID, TransactionID: exported.TransactionID, OperationID: exported.OperationID,
			Kind: exported.Kind, Ordinal: exported.Ordinal, State: exported.State,
			ExpectedOperationState: exported.ExpectedOperationState, LeaseID: exported.LeaseID,
			LeaseExpiresAt: exported.LeaseExpiresAt, ProviderRequestDigest: exported.ProviderRequestDigest,
			ProviderReceiptDigest: exported.ProviderReceiptDigest, ReconciliationDigest: exported.ReconciliationDigest,
			Version: exported.Version, CreatedAt: exported.CreatedAt, UpdatedAt: exported.UpdatedAt,
		}
		hasConsumedApproval := hasConsumedApprovalBefore(consumedApprovals[attempt.OperationID], attempt.CreatedAt)
		if !operationExists || exported.EffectClass != effectClass || !validEvidenceAttempt(attempt, transaction.ID) ||
			(effectClass == EffectIrreversible && attempt.State == AttemptSucceeded &&
				(attempt.ProviderReceiptDigest == "" || !hasConsumedApproval)) {
			return false
		}
		if _, duplicate := attemptIDs[attempt.ID]; duplicate {
			return false
		}
		identity := evidenceAttemptIdentity{operationID: attempt.OperationID, kind: attempt.Kind, ordinal: attempt.Ordinal}
		if _, duplicate := attemptIdentities[identity]; duplicate {
			return false
		}
		attemptIdentities[identity] = struct{}{}
		if priorAttempt != nil && !evidenceAttemptBefore(*priorAttempt, exported, operationIndexes) {
			return false
		}
		priorAttempt = &value.Attempts[index]
		attemptIDs[attempt.ID] = struct{}{}
		attemptsByID[attempt.ID] = attempt
		if attempt.ReconciliationDigest != "" {
			reconciledAttemptIDs[attempt.ID] = struct{}{}
			metrics.ReconciledAttempts++
		}
		switch attempt.State {
		case AttemptDispatching:
			metrics.DispatchingAttempts++
		case AttemptAmbiguous:
			metrics.AmbiguousAttempts++
		}
	}

	latestStates := make(map[string]string, 1+len(value.Operations)+len(value.Attempts))
	observedReconciledAttemptIDs := make(map[string]struct{})
	verifiedTransitions := make([]Transition, len(value.Transitions))
	var priorObservedAt time.Time
	for index, exported := range value.Transitions {
		transition := Transition{
			Sequence: exported.Sequence, TransactionID: exported.TransactionID, EntityType: exported.EntityType,
			EntityID: exported.EntityID, From: exported.From, To: exported.To, ObservedAt: exported.ObservedAt,
		}
		if !validEvidenceTransition(transition, transaction.ID, uint64(index+1), operationIDs, attemptIDs, reconciledAttemptIDs) ||
			(!priorObservedAt.IsZero() && transition.ObservedAt.Before(priorObservedAt)) ||
			!validEvidenceTransitionCausality(transition, index, len(value.Transitions), latestStates, operationsByID, attemptsByID) {
			return false
		}
		if transition.EntityType == "attempt" && transition.From == string(AttemptAmbiguous) &&
			(transition.To == string(AttemptSucceeded) || transition.To == string(AttemptFailed)) {
			if _, duplicate := observedReconciledAttemptIDs[transition.EntityID]; duplicate {
				return false
			}
			observedReconciledAttemptIDs[transition.EntityID] = struct{}{}
		}
		stateKey := transition.EntityType + ":" + transition.EntityID
		priorState, seen := latestStates[stateKey]
		if (!seen && transition.From != "") || (seen && transition.From != priorState) {
			return false
		}
		latestStates[stateKey] = transition.To
		priorObservedAt = transition.ObservedAt
		verifiedTransitions[index] = transition
	}
	if !sameEvidenceIDSet(observedReconciledAttemptIDs, reconciledAttemptIDs) ||
		!validEvidenceEntityTransitionTimes(transaction, operationsByID, attemptsByID, verifiedTransitions) ||
		!validEvidenceApprovalConsumptions(approvals, verifiedTransitions) {
		return false
	}
	if value.GeneratedAt.Before(priorObservedAt) || latestStates["transaction:"+transaction.ID] != string(transaction.State) {
		return false
	}
	for _, operation := range value.Operations {
		if latestStates["operation:"+operation.ID] != string(operation.State) {
			return false
		}
	}
	for _, attempt := range value.Attempts {
		if latestStates["attempt:"+attempt.ID] != string(attempt.State) {
			return false
		}
	}
	metrics.OperationTotal = uint32(len(value.Operations))
	metrics.AttemptTotal = uint32(len(value.Attempts))
	metrics.ApprovalTotal = uint32(len(value.Approvals))
	metrics.TransitionTotal = uint32(len(value.Transitions))
	if transaction.State == TransactionReconciliationRequired {
		metrics.ReconciliationRequired++
	}
	return metrics == value.Metrics
}

func validEvidenceTransitionCausality(
	transition Transition,
	index, total int,
	latestStates map[string]string,
	operations map[string]Operation,
	attempts map[string]Attempt,
) bool {
	switch transition.EntityType {
	case "transaction":
		return !terminalEvidenceTransactionState(TransactionState(transition.To)) || index == total-1
	case "attempt":
		attempt, ok := attempts[transition.EntityID]
		if !ok {
			return false
		}
		if transition.From == string(AttemptAmbiguous) &&
			(transition.To == string(AttemptSucceeded) || transition.To == string(AttemptFailed)) {
			return latestStates["operation:"+attempt.OperationID] == string(OperationReconciliationRequired)
		}
		if transition.From == "" || AttemptState(transition.To) == AttemptDispatching {
			return latestStates["operation:"+attempt.OperationID] == string(attempt.ExpectedOperationState)
		}
		return true
	case "operation":
		if _, ok := operations[transition.EntityID]; !ok {
			return false
		}
		return operationTransitionHasAttemptCause(transition, latestStates, attempts)
	default:
		return false
	}
}

func operationTransitionHasAttemptCause(transition Transition, latestStates map[string]string, attempts map[string]Attempt) bool {
	from := OperationState(transition.From)
	to := OperationState(transition.To)
	requiresAttemptCause := from == OperationApplying || from == OperationRollingBack || from == OperationCompensating ||
		from == OperationReconciliationRequired || to == OperationApplying || to == OperationRollingBack ||
		to == OperationCompensating || to == OperationReconciliationRequired
	if !requiresAttemptCause {
		return true
	}
	type candidate struct {
		found         bool
		ordinal       uint32
		observedState AttemptState
		requiredState AttemptState
		reconciled    bool
	}
	activeCandidate := candidate{}
	completionCandidate := candidate{}
	for _, attempt := range attempts {
		if attempt.OperationID != transition.EntityID {
			continue
		}
		observed, exists := latestStates["attempt:"+attempt.ID]
		if !exists {
			continue
		}
		observedState := AttemptState(observed)
		expected, active, dispatchOK := dispatchStates(attempt.Kind, attempt.ExpectedOperationState)
		if dispatchOK && from == expected && to == active && (!activeCandidate.found || attempt.Ordinal > activeCandidate.ordinal) {
			activeCandidate = candidate{found: true, ordinal: attempt.Ordinal, observedState: observedState}
		}
		if dispatchOK && (from == active || from == OperationReconciliationRequired) {
			for _, outcome := range []DispatchOutcome{DispatchSucceeded, DispatchFailed, DispatchAmbiguous} {
				attemptTarget, operationTarget, ok := completionStates(attempt.Kind, outcome)
				if !ok || to != operationTarget || (from == OperationReconciliationRequired && outcome == DispatchAmbiguous) {
					continue
				}
				if !completionCandidate.found || attempt.Ordinal > completionCandidate.ordinal {
					completionCandidate = candidate{
						found: true, ordinal: attempt.Ordinal, observedState: observedState,
						requiredState: attemptTarget, reconciled: attempt.ReconciliationDigest != "",
					}
				}
			}
		}
	}
	if activeCandidate.found {
		return activeCandidate.observedState == AttemptDispatching
	}
	if completionCandidate.found {
		if completionCandidate.reconciled && completionCandidate.requiredState != AttemptAmbiguous && from != OperationReconciliationRequired {
			return false
		}
		return completionCandidate.observedState == completionCandidate.requiredState &&
			(from != OperationReconciliationRequired || completionCandidate.reconciled)
	}
	return false
}

func terminalEvidenceTransactionState(state TransactionState) bool {
	switch state {
	case TransactionAborted, TransactionRolledBack, TransactionPartiallyReverted,
		TransactionCompensated, TransactionPartiallyCompensated, TransactionCommitted,
		TransactionRejected, TransactionExpired:
		return true
	default:
		return false
	}
}

func evidenceAttemptBefore(left, right EvidenceAttempt, operationIndexes map[string]uint32) bool {
	if operationIndexes[left.OperationID] != operationIndexes[right.OperationID] {
		return operationIndexes[left.OperationID] < operationIndexes[right.OperationID]
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.Ordinal != right.Ordinal {
		return left.Ordinal < right.Ordinal
	}
	return left.ID < right.ID
}

func hasUniqueJSONKeys(encoded []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if consumeUniqueJSONValue(decoder, 0) != nil {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeUniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalidEvidence
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidEvidence
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidEvidence
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidEvidence
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidEvidence
		}
	default:
		return ErrInvalidEvidence
	}
	return nil
}

func DecodeAndVerifyTransactionEvidence(encoded []byte) (TransactionEvidence, error) {
	if len(encoded) == 0 || len(encoded) > 16*1024*1024 || !hasUniqueJSONKeys(encoded) {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value TransactionEvidence
	if err := decoder.Decode(&value); err != nil {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return TransactionEvidence{}, ErrInvalidEvidence
	}
	if err := VerifyTransactionEvidenceDigest(value); err != nil {
		return TransactionEvidence{}, err
	}
	return value, nil
}

func validEvidenceTransaction(value Transaction) bool {
	return validIdentifier(value.ID) && validIdentifier(value.RunID) && digestPattern.MatchString(value.CatalogDigest) &&
		(value.Mode == TransactionModeDirect || value.Mode == TransactionModeWorkflow) && validTransactionState(value.State) &&
		value.Version > 0 && value.Version <= maxEvidenceInteger && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() && !value.UpdatedAt.Before(value.CreatedAt)
}

func validEvidenceTransactionOperations(transaction Transaction, operations map[string]Operation) bool {
	if transaction.Mode == TransactionModeDirect && len(operations) != 1 {
		return false
	}
	if transaction.State == TransactionCommitted {
		for _, operation := range operations {
			if operation.State != OperationApplied {
				return false
			}
		}
	}
	return true
}

func validEvidenceOperation(value Operation, transactionID string) bool {
	return value.TransactionID == transactionID && validIdentifier(value.ID) && value.Index > 0 && value.Index <= maxEvidenceOperationIndex && validIdentifier(value.ToolID) &&
		validIdentifier(value.HandlerVersion) && validIdentifier(value.PolicyVersion) && validEffectClass(value.EffectClass) && validPolicy(value.Policy) &&
		validOperationState(value.State) && digestPattern.MatchString(value.ArgumentDigest) && digestPattern.MatchString(value.ManifestDigest) &&
		value.Version > 0 && value.Version <= maxEvidenceInteger && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() && !value.UpdatedAt.Before(value.CreatedAt)
}

type evidenceTransitionTimes struct {
	first time.Time
	last  time.Time
}

func validEvidenceEntityTransitionTimes(transaction Transaction, operations map[string]Operation, attempts map[string]Attempt, transitions []Transition) bool {
	ranges := make(map[string]evidenceTransitionTimes, 1+len(operations)+len(attempts))
	for _, transition := range transitions {
		key := transition.EntityType + ":" + transition.EntityID
		value, seen := ranges[key]
		if !seen {
			value.first = transition.ObservedAt
		}
		value.last = transition.ObservedAt
		ranges[key] = value
	}
	matches := func(entityType, id string, createdAt, updatedAt time.Time) bool {
		value, ok := ranges[entityType+":"+id]
		return ok && createdAt.Equal(value.first) && updatedAt.Equal(value.last)
	}
	if !matches("transaction", transaction.ID, transaction.CreatedAt, transaction.UpdatedAt) {
		return false
	}
	for _, operation := range operations {
		if !matches("operation", operation.ID, operation.CreatedAt, operation.UpdatedAt) {
			return false
		}
	}
	for _, attempt := range attempts {
		if !matches("attempt", attempt.ID, attempt.CreatedAt, attempt.UpdatedAt) {
			return false
		}
	}
	return true
}

func validEvidenceApprovalConsumptions(approvals []ApprovalEvidence, transitions []Transition) bool {
	consumedByOperation := make(map[string]struct{})
	for _, approval := range approvals {
		if approval.ConsumedAt.IsZero() {
			continue
		}
		if _, duplicate := consumedByOperation[approval.OperationID]; duplicate {
			return false
		}
		consumedByOperation[approval.OperationID] = struct{}{}
		found := false
		for _, transition := range transitions {
			from := OperationState(transition.From)
			if transition.EntityType == "operation" && transition.EntityID == approval.OperationID &&
				(from == OperationAwaitingUserApproval || from == OperationAwaitingAgentCommit) &&
				transition.To == string(OperationReady) && transition.ObservedAt.Equal(approval.ConsumedAt) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameEvidenceIDSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for id := range left {
		if _, ok := right[id]; !ok {
			return false
		}
	}
	return true
}

func hasConsumedApprovalBefore(consumedAt []time.Time, deadline time.Time) bool {
	for _, value := range consumedAt {
		if !value.After(deadline) {
			return true
		}
	}
	return false
}

func validEvidenceApproval(value ApprovalEvidence, transactionID string, operationManifests map[string]string) bool {
	if value.TransactionID != transactionID || !validIdentifier(value.AuthorityID) || !validIdentifier(value.OperationID) ||
		!validIdentifier(value.ActorID) || !digestPattern.MatchString(value.ManifestDigest) ||
		value.RegisteredAt.IsZero() || !value.ExpiresAt.After(value.RegisteredAt) ||
		(!value.ConsumedAt.IsZero() && (value.ConsumedAt.Before(value.RegisteredAt) || value.ConsumedAt.After(value.ExpiresAt))) {
		return false
	}
	manifestDigest, exists := operationManifests[value.OperationID]
	if !exists || value.ManifestDigest != manifestDigest {
		return false
	}
	if value.Source == CommitSourceUser {
		return value.SourceRunID == "" && value.PhaseGrantID == ""
	}
	return value.Source == CommitSourceAgent && validIdentifier(value.SourceRunID) && validIdentifier(value.PhaseGrantID)
}

func validEvidenceAttempt(value Attempt, transactionID string) bool {
	receiptValid := value.ProviderReceiptDigest == "" ||
		(digestPattern.MatchString(value.ProviderReceiptDigest) && value.State == AttemptSucceeded)
	reconciliationValid := value.ReconciliationDigest == "" ||
		(digestPattern.MatchString(value.ReconciliationDigest) && (value.State == AttemptSucceeded || value.State == AttemptFailed))
	distinctEvidence := value.ProviderReceiptDigest == "" || value.ReconciliationDigest == "" || value.ProviderReceiptDigest != value.ReconciliationDigest
	return receiptValid && reconciliationValid && distinctEvidence && value.TransactionID == transactionID && validIdentifier(value.ID) && validIdentifier(value.OperationID) && validAttemptKind(value.Kind) &&
		value.Ordinal > 0 && value.Ordinal <= maxEvidenceAttemptOrdinal && validAttemptState(value.State) && validAttemptPriorState(value.Kind, value.ExpectedOperationState) && validIdentifier(value.LeaseID) &&
		value.LeaseExpiresAt.After(value.CreatedAt) && digestPattern.MatchString(value.ProviderRequestDigest) && value.Version > 0 && value.Version <= maxEvidenceInteger &&
		!value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() && !value.UpdatedAt.Before(value.CreatedAt)
}

func validEvidenceTransition(value Transition, transactionID string, sequence uint64, operationIDs, attemptIDs, reconciledAttemptIDs map[string]struct{}) bool {
	if value.TransactionID != transactionID || value.Sequence != sequence || value.Sequence > maxEvidenceTransitions ||
		!validIdentifier(value.EntityID) || len(value.From) > 128 || value.To == "" || len(value.To) > 128 || value.ObservedAt.IsZero() {
		return false
	}
	switch value.EntityType {
	case "transaction":
		if value.EntityID != transactionID {
			return false
		}
		if value.From == "" {
			return TransactionState(value.To) == TransactionOpen
		}
		return ValidateTransactionTransition(TransactionState(value.From), TransactionState(value.To)) == nil
	case "operation":
		if _, ok := operationIDs[value.EntityID]; !ok {
			return false
		}
		if value.From == "" {
			return validInitialOperationState(OperationState(value.To))
		}
		return ValidateOperationTransition(OperationState(value.From), OperationState(value.To)) == nil
	case "attempt":
		if _, ok := attemptIDs[value.EntityID]; !ok {
			return false
		}
		if value.From == "" {
			return AttemptState(value.To) == AttemptLeased
		}
		if AttemptState(value.From) == AttemptAmbiguous {
			_, reconciled := reconciledAttemptIDs[value.EntityID]
			return reconciled && (AttemptState(value.To) == AttemptSucceeded || AttemptState(value.To) == AttemptFailed)
		}
		return ValidateAttemptTransition(AttemptState(value.From), AttemptState(value.To)) == nil
	default:
		return false
	}
}

func validTransactionState(value TransactionState) bool {
	switch value {
	case TransactionOpen, TransactionAborting, TransactionAborted, TransactionRollingBack, TransactionRolledBack,
		TransactionPartiallyReverted, TransactionCompensating, TransactionCompensated, TransactionPartiallyCompensated,
		TransactionPendingApproval, TransactionCommitting, TransactionCommitted, TransactionReconciliationRequired,
		TransactionRejected, TransactionExpired:
		return true
	default:
		return false
	}
}

func validOperationState(value OperationState) bool {
	switch value {
	case OperationProposed, OperationStaged, OperationAwaitingAgentCommit, OperationAwaitingUserApproval, OperationReady,
		OperationApplying, OperationApplied, OperationFailedRetryable, OperationFailedTerminal, OperationReconciliationRequired,
		OperationRollingBack, OperationRolledBack, OperationRollbackFailed, OperationCompensationRequired,
		OperationCompensating, OperationCompensated, OperationCompensationFailed, OperationDenied, OperationExpired:
		return true
	default:
		return false
	}
}

func validAttemptState(value AttemptState) bool {
	switch value {
	case AttemptLeased, AttemptDispatching, AttemptSucceeded, AttemptFailed, AttemptAmbiguous, AttemptExpired:
		return true
	default:
		return false
	}
}

func evidenceCorrelationID(value Transaction) string {
	digest := sha256.Sum256([]byte("transaction-evidence-correlation-v1\x00" + value.ID + "\x00" + value.RunID + "\x00" + value.CatalogDigest))
	return "corr_" + hex.EncodeToString(digest[:16])
}
