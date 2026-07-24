package transaction

import (
	"sort"
)

type AbortPlan struct {
	Disposition              AbortDisposition
	RollbackOperationIDs     []string
	CompensationOperationIDs []string
	AutoCompensate           bool
}

func BuildAbortPlan(values []Operation, autoCompensateTools map[string]bool) (AbortPlan, error) {
	operations := append([]Operation(nil), values...)
	sort.Slice(operations, func(left, right int) bool { return operations[left].Index < operations[right].Index })
	transactionID := ""
	seenIndexes := make(map[uint32]struct{}, len(operations))
	mix := EffectMix{}
	allCompensationsAutomatic := true
	compensationCount := 0
	plan := AbortPlan{}
	for _, operation := range operations {
		if !validIdentifier(operation.ID) || !validIdentifier(operation.TransactionID) || operation.Index == 0 ||
			!validEffectClass(operation.EffectClass) || !knownOperationState(operation.State) {
			return AbortPlan{}, ErrInvalidInput
		}
		if transactionID == "" {
			transactionID = operation.TransactionID
		} else if operation.TransactionID != transactionID {
			return AbortPlan{}, ErrInvalidInput
		}
		if _, exists := seenIndexes[operation.Index]; exists {
			return AbortPlan{}, ErrInvalidInput
		}
		seenIndexes[operation.Index] = struct{}{}
		switch operation.State {
		case OperationApplying, OperationRollingBack, OperationCompensating, OperationReconciliationRequired:
			mix.Ambiguous = true
		case OperationApplied:
			switch operation.EffectClass {
			case EffectReversible:
				mix.Reversible = true
				plan.RollbackOperationIDs = append(plan.RollbackOperationIDs, operation.ID)
			case EffectCompensatable:
				mix.Compensatable = true
				compensationCount++
				allCompensationsAutomatic = allCompensationsAutomatic && autoCompensateTools[operation.ToolID]
				plan.CompensationOperationIDs = append(plan.CompensationOperationIDs, operation.ID)
			case EffectIrreversible:
				mix.CommittedIrreversible = true
			}
		case OperationRollbackFailed:
			if operation.EffectClass != EffectReversible {
				return AbortPlan{}, ErrInvalidInput
			}
			mix.Reversible = true
			plan.RollbackOperationIDs = append(plan.RollbackOperationIDs, operation.ID)
		case OperationCompensationRequired, OperationCompensationFailed:
			if operation.EffectClass != EffectCompensatable {
				return AbortPlan{}, ErrInvalidInput
			}
			mix.Compensatable = true
			compensationCount++
			allCompensationsAutomatic = allCompensationsAutomatic && autoCompensateTools[operation.ToolID]
			plan.CompensationOperationIDs = append(plan.CompensationOperationIDs, operation.ID)
		case OperationProposed, OperationStaged, OperationAwaitingAgentCommit, OperationAwaitingUserApproval, OperationReady:
			if operation.EffectClass == EffectIrreversible {
				mix.StagedIrreversible = true
			}
		}
	}
	reverseStrings(plan.RollbackOperationIDs)
	reverseStrings(plan.CompensationOperationIDs)
	mix.AutoCompensate = compensationCount > 0 && allCompensationsAutomatic
	plan.AutoCompensate = mix.AutoCompensate
	plan.Disposition = ClassifyAbort(mix)
	return plan, nil
}

func reverseStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func knownOperationState(state OperationState) bool {
	switch state {
	case OperationProposed, OperationStaged, OperationAwaitingAgentCommit, OperationAwaitingUserApproval,
		OperationReady, OperationApplying, OperationApplied, OperationFailedRetryable, OperationFailedTerminal,
		OperationReconciliationRequired, OperationRollingBack, OperationRolledBack, OperationRollbackFailed,
		OperationCompensationRequired, OperationCompensating, OperationCompensated, OperationCompensationFailed,
		OperationDenied, OperationExpired:
		return true
	default:
		return false
	}
}
