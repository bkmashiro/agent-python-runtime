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
	approvalIDs := make(map[string]struct{}, len(approvals))
	for index, approval := range approvals {
		if !validEvidenceApproval(approval, transaction.ID, operationIDs) {
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
			value.Metrics.ConsumedApprovals++
		}
	}
	attemptIDs := make(map[string]struct{}, len(attempts))
	reconciledAttemptIDs := make(map[string]struct{})
	for index, attempt := range attempts {
		effectClass := operationEffects[attempt.OperationID]
		if _, ok := operationIDs[attempt.OperationID]; !ok || !validEvidenceAttempt(attempt, transaction.ID) ||
			(effectClass == EffectIrreversible && attempt.State == AttemptSucceeded && attempt.ProviderReceiptDigest == "") {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		if _, duplicate := attemptIDs[attempt.ID]; duplicate {
			return TransactionEvidence{}, ErrInvalidEvidence
		}
		attemptIDs[attempt.ID] = struct{}{}
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
	for index, transition := range transitions {
		if !validEvidenceTransition(transition, transaction.ID, uint64(index+1), operationIDs, attemptIDs, reconciledAttemptIDs) ||
			(!priorObservedAt.IsZero() && transition.ObservedAt.Before(priorObservedAt)) {
			return TransactionEvidence{}, ErrInvalidEvidence
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
	if value.SchemaVersion != TransactionEvidenceSchemaVersion || !digestPattern.MatchString(value.EvidenceDigest) {
		return ErrInvalidEvidence
	}
	expected, err := ComputeTransactionEvidenceDigest(value)
	if err != nil || expected != value.EvidenceDigest {
		return ErrInvalidEvidence
	}
	return nil
}

func DecodeAndVerifyTransactionEvidence(encoded []byte) (TransactionEvidence, error) {
	if len(encoded) == 0 || len(encoded) > 16*1024*1024 {
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

func validEvidenceOperation(value Operation, transactionID string) bool {
	return value.TransactionID == transactionID && validIdentifier(value.ID) && value.Index > 0 && value.Index <= maxEvidenceOperationIndex && validIdentifier(value.ToolID) &&
		validIdentifier(value.HandlerVersion) && validIdentifier(value.PolicyVersion) && validEffectClass(value.EffectClass) && validPolicy(value.Policy) &&
		validOperationState(value.State) && digestPattern.MatchString(value.ArgumentDigest) && digestPattern.MatchString(value.ManifestDigest) &&
		value.Version > 0 && value.Version <= maxEvidenceInteger && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() && !value.UpdatedAt.Before(value.CreatedAt)
}

func validEvidenceApproval(value ApprovalEvidence, transactionID string, operationIDs map[string]struct{}) bool {
	if value.TransactionID != transactionID || !validIdentifier(value.AuthorityID) || !validIdentifier(value.OperationID) ||
		!validIdentifier(value.ActorID) || !digestPattern.MatchString(value.ManifestDigest) ||
		value.RegisteredAt.IsZero() || !value.ExpiresAt.After(value.RegisteredAt) ||
		(!value.ConsumedAt.IsZero() && value.ConsumedAt.Before(value.RegisteredAt)) {
		return false
	}
	if _, exists := operationIDs[value.OperationID]; !exists {
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
	return receiptValid && reconciliationValid && value.TransactionID == transactionID && validIdentifier(value.ID) && validIdentifier(value.OperationID) && validAttemptKind(value.Kind) &&
		value.Ordinal > 0 && value.Ordinal <= maxEvidenceAttemptOrdinal && validAttemptState(value.State) && validOperationState(value.ExpectedOperationState) && validIdentifier(value.LeaseID) &&
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
