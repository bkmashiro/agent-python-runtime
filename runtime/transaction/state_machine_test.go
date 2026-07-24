package transaction

import "testing"

func TestTransactionTransitionsRejectTerminalReplayAndImpossibleStrengthening(t *testing.T) {
	valid := [][2]TransactionState{
		{TransactionOpen, TransactionAborting},
		{TransactionOpen, TransactionPendingApproval},
		{TransactionOpen, TransactionCommitting},
		{TransactionOpen, TransactionReconciliationRequired},
		{TransactionAborting, TransactionRollingBack},
		{TransactionAborting, TransactionCompensating},
		{TransactionAborting, TransactionAborted},
		{TransactionRollingBack, TransactionRolledBack},
		{TransactionRollingBack, TransactionCompensating},
		{TransactionCompensating, TransactionCompensated},
		{TransactionPendingApproval, TransactionCommitting},
		{TransactionCommitting, TransactionCommitted},
		{TransactionReconciliationRequired, TransactionRollingBack},
	}
	for _, transition := range valid {
		if err := ValidateTransactionTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", transition[0], transition[1], err)
		}
	}

	invalid := [][2]TransactionState{
		{TransactionCommitted, TransactionRolledBack},
		{TransactionRolledBack, TransactionOpen},
		{TransactionCompensated, TransactionRolledBack},
		{TransactionOpen, TransactionRolledBack},
		{TransactionPendingApproval, TransactionCommitted},
		{TransactionReconciliationRequired, TransactionPendingApproval},
	}
	for _, transition := range invalid {
		if err := ValidateTransactionTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestOperationAndAttemptTransitionsRequireStagingLeaseAndObservedOutcome(t *testing.T) {
	operationValid := [][2]OperationState{
		{OperationProposed, OperationStaged},
		{OperationStaged, OperationAwaitingUserApproval},
		{OperationAwaitingUserApproval, OperationReady},
		{OperationReady, OperationApplying},
		{OperationApplying, OperationApplied},
		{OperationApplying, OperationReconciliationRequired},
		{OperationApplied, OperationRollingBack},
		{OperationRollingBack, OperationRolledBack},
		{OperationApplied, OperationCompensationRequired},
		{OperationCompensationRequired, OperationCompensating},
		{OperationCompensating, OperationCompensated},
	}
	for _, transition := range operationValid {
		if err := ValidateOperationTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", transition[0], transition[1], err)
		}
	}
	operationInvalid := [][2]OperationState{
		{OperationProposed, OperationApplied},
		{OperationStaged, OperationApplied},
		{OperationReady, OperationApplied},
		{OperationApplied, OperationRolledBack},
		{OperationCompensated, OperationRolledBack},
		{OperationReconciliationRequired, OperationReady},
	}
	for _, transition := range operationInvalid {
		if err := ValidateOperationTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}

	attemptValid := [][2]AttemptState{
		{AttemptLeased, AttemptDispatching},
		{AttemptDispatching, AttemptSucceeded},
		{AttemptDispatching, AttemptFailed},
		{AttemptDispatching, AttemptAmbiguous},
		{AttemptLeased, AttemptExpired},
	}
	for _, transition := range attemptValid {
		if err := ValidateAttemptTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", transition[0], transition[1], err)
		}
	}
	for _, transition := range [][2]AttemptState{
		{AttemptLeased, AttemptSucceeded},
		{AttemptExpired, AttemptDispatching},
		{AttemptAmbiguous, AttemptSucceeded},
	} {
		if err := ValidateAttemptTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}

func TestAbortDispositionUsesWeakestTruthfulEffectGuarantee(t *testing.T) {
	tests := []struct {
		name string
		mix  EffectMix
		want AbortDisposition
	}{
		{name: "read only", mix: EffectMix{}, want: AbortWithoutUndo},
		{name: "reversible", mix: EffectMix{Reversible: true}, want: AbortRollbackRequired},
		{name: "compensatable requires policy", mix: EffectMix{Compensatable: true}, want: AbortCompensationRequired},
		{name: "compensatable auto", mix: EffectMix{Compensatable: true, AutoCompensate: true}, want: AbortAutoCompensate},
		{name: "mixed", mix: EffectMix{Reversible: true, Compensatable: true, AutoCompensate: true}, want: AbortRollbackAndCompensate},
		{name: "staged irreversible", mix: EffectMix{Reversible: true, StagedIrreversible: true}, want: AbortRejectIntentAndRollback},
		{name: "committed irreversible", mix: EffectMix{Reversible: true, CommittedIrreversible: true}, want: AbortIrreversibleCommitted},
		{name: "ambiguous dominates", mix: EffectMix{Reversible: true, Compensatable: true, AutoCompensate: true, Ambiguous: true}, want: AbortReconciliationRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyAbort(test.mix); got != test.want {
				t.Fatalf("ClassifyAbort(%+v) = %q, want %q", test.mix, got, test.want)
			}
		})
	}
}
