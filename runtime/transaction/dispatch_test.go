package transaction

import (
	"errors"
	"testing"
	"time"
)

func TestCoordinatorDispatchJournalsAttemptBeforeApplyAndCommitsDirectRead(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, newFakeAuthorityVerifier())
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_dispatch", CatalogDigest: testDigest("catalog"), Mode: TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "demo.echo", HandlerVersion: "v1", EffectClass: EffectReadOnly,
		Policy: PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{
		OperationID: op.ID, Kind: AttemptApply, Ordinal: 1,
		LeaseDuration: time.Minute, ProviderRequestDigest: testDigest("provider-request"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Operation.State != OperationApplying || dispatch.Attempt.State != AttemptDispatching ||
		dispatch.Attempt.ExpectedOperationState != OperationReady || dispatch.Attempt.LeaseID == "" ||
		!dispatch.Attempt.LeaseExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected dispatch journal: %+v", dispatch)
	}
	completed, err := coordinator.CompleteDispatch(CompleteDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Operation.State != OperationApplied || completed.Attempt.State != AttemptSucceeded || completed.Transaction.State != TransactionCommitted {
		t.Fatalf("unexpected direct completion: %+v", completed)
	}
	inspection, err := coordinator.Inspect(tx.ID, nil)
	if err != nil || len(inspection.Operations) != 1 || len(inspection.Attempts) != 1 || len(inspection.Transitions) == 0 || inspection.AbortPlan.Disposition != AbortWithoutUndo {
		t.Fatalf("inspection = %+v, %v", inspection, err)
	}
	inspection.Operations[0].State = OperationRolledBack
	reinspected, err := coordinator.Inspect(tx.ID, nil)
	if err != nil || reinspected.Operations[0].State != OperationApplied {
		t.Fatalf("inspection leaked mutable state: %+v, %v", reinspected, err)
	}
	replayed, err := coordinator.CompleteDispatch(CompleteDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
	})
	if err != nil || replayed.Attempt.Version != completed.Attempt.Version || replayed.Transaction.Version != completed.Transaction.Version {
		t.Fatalf("terminal replay changed state: before=%+v after=%+v err=%v", completed, replayed, err)
	}
}

func TestCoordinatorDispatchReplayRepairsTerminalAttemptPartialState(t *testing.T) {
	now := time.Unix(550, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, newFakeAuthorityVerifier())
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_partial", CatalogDigest: testDigest("catalog"), Mode: TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "demo.echo", HandlerVersion: "v1", EffectClass: EffectReadOnly,
		Policy: PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{
		OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: testDigest("provider"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.transitionAttempt(dispatch.Attempt.ID, dispatch.Attempt.Version, AttemptDispatching, AttemptSucceeded, now); err != nil {
		t.Fatal(err)
	}
	completed, err := coordinator.CompleteDispatch(CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Attempt.State != AttemptSucceeded || completed.Operation.State != OperationApplied || completed.Transaction.State != TransactionCommitted {
		t.Fatalf("partial completion was not repaired: %+v", completed)
	}
}

func TestCoordinatorReconcilesAmbiguousDispatchWithoutReplay(t *testing.T) {
	now := time.Unix(700, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_reconcile", CatalogDigest: testDigest("catalog"), Mode: TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "demo.read", HandlerVersion: "v1", EffectClass: EffectReadOnly, Policy: PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("args")})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: testDigest("provider")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchAmbiguous}); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := ledger.GetAttempt(dispatch.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.transitionAttempt(ambiguous.ID, ambiguous.Version, AttemptAmbiguous, AttemptSucceeded, now); err == nil {
		t.Fatal("generic transition bypassed reconciliation evidence")
	}
	now = time.Unix(701, 0).UTC()
	reconciled, err := coordinator.ReconcileDispatch(ReconcileDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded, ObservationDigest: testDigest("readback")})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Attempt.State != AttemptSucceeded || reconciled.Attempt.ReconciliationDigest != testDigest("readback") || reconciled.Operation.State != OperationApplied || reconciled.Transaction.State != TransactionCommitted {
		t.Fatalf("reconciled=%+v", reconciled)
	}
	replayed, err := coordinator.ReconcileDispatch(ReconcileDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded, ObservationDigest: testDigest("readback")})
	if err != nil || replayed.Attempt.Version != reconciled.Attempt.Version {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err := coordinator.ReconcileDispatch(ReconcileDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchFailed, ObservationDigest: testDigest("changed")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting reconciliation error=%v", err)
	}
	ledger.mu.Lock()
	corrupt := ledger.attempts[dispatch.Attempt.ID]
	corrupt.ReconciliationDigest = ""
	ledger.attempts[dispatch.Attempt.ID] = corrupt
	ledger.mu.Unlock()
	if _, err := BuildTransactionEvidence(ledger, tx.ID, time.Unix(702, 0).UTC()); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("digestless reconciliation evidence error=%v", err)
	}
}

func TestCoordinatorDispatchRejectsInvalidLeaseAndMovesAmbiguousWorkflowToReconciliation(t *testing.T) {
	now := time.Unix(600, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, newFakeAuthorityVerifier())
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_workflow", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "demo.echo", HandlerVersion: "v1", EffectClass: EffectReadOnly,
		Policy: PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: 0, ProviderRequestDigest: testDigest("provider")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero lease error = %v, want ErrInvalidInput", err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Second, ProviderRequestDigest: testDigest("provider")})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := coordinator.CompleteDispatch(CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchAmbiguous})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Attempt.State != AttemptAmbiguous || completed.Operation.State != OperationReconciliationRequired || completed.Transaction.State != TransactionReconciliationRequired {
		t.Fatalf("ambiguous completion did not fail closed: %+v", completed)
	}
	if _, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 2, LeaseDuration: time.Second, ProviderRequestDigest: testDigest("provider-2")}); err == nil {
		t.Fatal("ambiguous operation permitted blind retry")
	}
	now = time.Unix(601, 0).UTC()
	reconciled, err := coordinator.ReconcileDispatch(ReconcileDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchFailed, ObservationDigest: testDigest("readback-not-applied")})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Attempt.State != AttemptFailed || reconciled.Operation.State != OperationFailedTerminal || reconciled.Transaction.State != TransactionAborting {
		t.Fatalf("failed readback reconciliation=%+v", reconciled)
	}
}
