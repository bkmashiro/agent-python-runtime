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
}
