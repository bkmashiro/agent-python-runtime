package transaction

import "fmt"

type TransactionState string

const (
	TransactionOpen                   TransactionState = "open"
	TransactionAborting               TransactionState = "aborting"
	TransactionAborted                TransactionState = "aborted"
	TransactionRollingBack            TransactionState = "rolling_back"
	TransactionRolledBack             TransactionState = "rolled_back"
	TransactionPartiallyReverted      TransactionState = "partially_reverted"
	TransactionCompensating           TransactionState = "compensating"
	TransactionCompensated            TransactionState = "compensated"
	TransactionPartiallyCompensated   TransactionState = "partially_compensated"
	TransactionPendingApproval        TransactionState = "pending_approval"
	TransactionCommitting             TransactionState = "committing"
	TransactionCommitted              TransactionState = "committed"
	TransactionReconciliationRequired TransactionState = "reconciliation_required"
	TransactionRejected               TransactionState = "rejected"
	TransactionExpired                TransactionState = "expired"
)

type OperationState string

const (
	OperationProposed               OperationState = "proposed"
	OperationStaged                 OperationState = "staged"
	OperationAwaitingAgentCommit    OperationState = "awaiting_agent_commit"
	OperationAwaitingUserApproval   OperationState = "awaiting_user_approval"
	OperationReady                  OperationState = "ready"
	OperationApplying               OperationState = "applying"
	OperationApplied                OperationState = "applied"
	OperationFailedRetryable        OperationState = "failed_retryable"
	OperationFailedTerminal         OperationState = "failed_terminal"
	OperationReconciliationRequired OperationState = "reconciliation_required"
	OperationRollingBack            OperationState = "rolling_back"
	OperationRolledBack             OperationState = "rolled_back"
	OperationRollbackFailed         OperationState = "rollback_failed"
	OperationCompensationRequired   OperationState = "compensation_required"
	OperationCompensating           OperationState = "compensating"
	OperationCompensated            OperationState = "compensated"
	OperationCompensationFailed     OperationState = "compensation_failed"
	OperationDenied                 OperationState = "denied"
	OperationExpired                OperationState = "expired"
)

type AttemptState string

const (
	AttemptLeased      AttemptState = "leased"
	AttemptDispatching AttemptState = "dispatching"
	AttemptSucceeded   AttemptState = "succeeded"
	AttemptFailed      AttemptState = "failed"
	AttemptAmbiguous   AttemptState = "ambiguous"
	AttemptExpired     AttemptState = "expired"
)

var transactionTransitions = map[TransactionState]map[TransactionState]struct{}{
	TransactionOpen: states(
		TransactionAborting, TransactionPendingApproval, TransactionCommitting,
		TransactionCommitted, TransactionRejected, TransactionExpired, TransactionReconciliationRequired,
	),
	TransactionAborting: states(
		TransactionAborted, TransactionRollingBack, TransactionCompensating,
		TransactionPartiallyReverted, TransactionPartiallyCompensated,
		TransactionReconciliationRequired, TransactionRejected,
	),
	TransactionRollingBack: states(
		TransactionRolledBack, TransactionCompensating, TransactionPartiallyReverted, TransactionReconciliationRequired,
	),
	TransactionCompensating: states(
		TransactionCompensated, TransactionPartiallyCompensated, TransactionReconciliationRequired,
	),
	TransactionPendingApproval: states(TransactionCommitting, TransactionRejected, TransactionExpired),
	TransactionCommitting:      states(TransactionCommitted, TransactionRejected, TransactionReconciliationRequired),
	TransactionReconciliationRequired: states(
		TransactionOpen, TransactionAborting, TransactionRollingBack, TransactionPartiallyReverted,
		TransactionCompensating, TransactionPartiallyCompensated, TransactionCommitted, TransactionRejected,
	),
}

var operationTransitions = map[OperationState]map[OperationState]struct{}{
	OperationProposed: operationStates(OperationStaged, OperationDenied),
	OperationStaged: operationStates(
		OperationAwaitingAgentCommit, OperationAwaitingUserApproval,
		OperationReady, OperationDenied, OperationExpired,
	),
	OperationAwaitingAgentCommit:  operationStates(OperationReady, OperationDenied, OperationExpired),
	OperationAwaitingUserApproval: operationStates(OperationReady, OperationDenied, OperationExpired),
	OperationReady: operationStates(
		OperationApplying, OperationDenied, OperationExpired,
	),
	OperationApplying: operationStates(
		OperationApplied, OperationFailedRetryable, OperationFailedTerminal,
		OperationReconciliationRequired,
	),
	OperationFailedRetryable: operationStates(
		OperationApplying, OperationFailedTerminal, OperationReconciliationRequired,
	),
	OperationApplied: operationStates(OperationRollingBack, OperationCompensationRequired),
	OperationRollingBack: operationStates(
		OperationRolledBack, OperationRollbackFailed, OperationReconciliationRequired,
	),
	OperationRollbackFailed: operationStates(OperationRollingBack, OperationReconciliationRequired),
	OperationCompensationRequired: operationStates(
		OperationCompensating, OperationExpired, OperationReconciliationRequired,
	),
	OperationCompensating: operationStates(
		OperationCompensated, OperationCompensationFailed, OperationReconciliationRequired,
	),
	OperationCompensationFailed: operationStates(OperationCompensating, OperationReconciliationRequired),
	OperationReconciliationRequired: operationStates(
		OperationApplied, OperationFailedTerminal, OperationRolledBack, OperationRollbackFailed,
		OperationCompensated, OperationCompensationFailed,
	),
}

var attemptTransitions = map[AttemptState]map[AttemptState]struct{}{
	AttemptLeased:      attemptStates(AttemptDispatching, AttemptExpired),
	AttemptDispatching: attemptStates(AttemptSucceeded, AttemptFailed, AttemptAmbiguous),
}

func states(values ...TransactionState) map[TransactionState]struct{} {
	result := make(map[TransactionState]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func operationStates(values ...OperationState) map[OperationState]struct{} {
	result := make(map[OperationState]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func attemptStates(values ...AttemptState) map[AttemptState]struct{} {
	result := make(map[AttemptState]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func ValidateTransactionTransition(from, to TransactionState) error {
	if _, ok := transactionTransitions[from][to]; !ok {
		return fmt.Errorf("invalid transaction transition %q -> %q", from, to)
	}
	return nil
}

func ValidateOperationTransition(from, to OperationState) error {
	if _, ok := operationTransitions[from][to]; !ok {
		return fmt.Errorf("invalid operation transition %q -> %q", from, to)
	}
	return nil
}

func ValidateAttemptTransition(from, to AttemptState) error {
	if _, ok := attemptTransitions[from][to]; !ok {
		return fmt.Errorf("invalid attempt transition %q -> %q", from, to)
	}
	return nil
}

type EffectMix struct {
	Reversible            bool
	Compensatable         bool
	AutoCompensate        bool
	StagedIrreversible    bool
	CommittedIrreversible bool
	Ambiguous             bool
}

type AbortDisposition string

const (
	AbortWithoutUndo             AbortDisposition = "abort_without_undo"
	AbortRollbackRequired        AbortDisposition = "rollback_required"
	AbortCompensationRequired    AbortDisposition = "compensation_required"
	AbortAutoCompensate          AbortDisposition = "auto_compensate"
	AbortRollbackAndCompensate   AbortDisposition = "rollback_and_compensate"
	AbortRejectIntentAndRollback AbortDisposition = "reject_intent_and_rollback"
	AbortIrreversibleCommitted   AbortDisposition = "irreversible_committed"
	AbortReconciliationRequired  AbortDisposition = "reconciliation_required"
)

func ClassifyAbort(mix EffectMix) AbortDisposition {
	switch {
	case mix.Ambiguous:
		return AbortReconciliationRequired
	case mix.CommittedIrreversible:
		return AbortIrreversibleCommitted
	case mix.StagedIrreversible && mix.Reversible:
		return AbortRejectIntentAndRollback
	case mix.StagedIrreversible && mix.Compensatable:
		return AbortCompensationRequired
	case mix.StagedIrreversible:
		return AbortWithoutUndo
	case mix.Reversible && mix.Compensatable:
		return AbortRollbackAndCompensate
	case mix.Reversible:
		return AbortRollbackRequired
	case mix.Compensatable && mix.AutoCompensate:
		return AbortAutoCompensate
	case mix.Compensatable:
		return AbortCompensationRequired
	default:
		return AbortWithoutUndo
	}
}
