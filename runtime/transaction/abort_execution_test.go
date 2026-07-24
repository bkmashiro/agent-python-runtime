package transaction_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/effect"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type abortIDs struct{ next int }

func (ids *abortIDs) New(prefix string) (string, error) {
	ids.next++
	return prefix + "_abort_" + string(rune('a'+ids.next)), nil
}

func abortDigest(seed byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = "0123456789abcdef"[(int(seed)+index)%16]
	}
	return "sha256:" + string(value)
}

func TestCoordinatorExecutesMixedAbortTruthfully(t *testing.T) {
	now := time.Unix(1200, 0).UTC()
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &abortIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_abort", CatalogDigest: abortDigest(1), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	configStore := effect.NewConfigStore(map[string]string{"feature": "off"})
	configOperation, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "config.set", HandlerVersion: "v1", EffectClass: transaction.EffectReversible, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(2)})
	if err != nil {
		t.Fatal(err)
	}
	configDispatch, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: configOperation.ID, Kind: transaction.AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(3)})
	if err != nil {
		t.Fatal(err)
	}
	configReceipt, err := configStore.Apply(configDispatch.Attempt.ID, "feature", 1, "on")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: configOperation.ID, AttemptID: configDispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	reservationStore := effect.NewReservationStore()
	reservationOperation, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "inventory.reserve", HandlerVersion: "v1", EffectClass: transaction.EffectCompensatable, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(4)})
	if err != nil {
		t.Fatal(err)
	}
	reserveDispatch, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: reservationOperation.ID, Kind: transaction.AttemptApply, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(5)})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := reservationStore.Reserve(reserveDispatch.Attempt.ID, "sku_1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: reservationOperation.ID, AttemptID: reserveDispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}

	step, err := coordinator.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{"inventory.reserve": true}, CompensationAuthorized: true, Ordinal: 3, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(6)})
	if err != nil {
		t.Fatal(err)
	}
	if step.Dispatch == nil || step.Dispatch.Attempt.Kind != transaction.AttemptRollback || step.Dispatch.Operation.ID != configOperation.ID {
		t.Fatalf("first abort step=%+v", step)
	}
	if _, err := configStore.Rollback(step.Dispatch.Attempt.ID, configReceipt.UndoToken); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: configOperation.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}

	step, err = coordinator.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{"inventory.reserve": true}, CompensationAuthorized: true, Ordinal: 4, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(7)})
	if err != nil {
		t.Fatal(err)
	}
	if step.Dispatch == nil || step.Dispatch.Attempt.Kind != transaction.AttemptCompensate || step.Dispatch.Operation.ID != reservationOperation.ID {
		t.Fatalf("second abort step=%+v", step)
	}
	if _, err := reservationStore.Compensate(step.Dispatch.Attempt.ID, reserved.ReservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: reservationOperation.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}

	step, err = coordinator.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{"inventory.reserve": true}, CompensationAuthorized: true, Ordinal: 5, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(8)})
	if err != nil {
		t.Fatal(err)
	}
	if !step.Done || step.Transaction.State != transaction.TransactionCompensated || configStore.Value("feature") != "off" || reservationStore.Active(reserved.ReservationID) {
		t.Fatalf("final abort step=%+v feature=%q reservation_active=%v", step, configStore.Value("feature"), reservationStore.Active(reserved.ReservationID))
	}
	inspection, err := coordinator.Inspect(tx.ID, map[string]bool{"inventory.reserve": true})
	if err != nil || inspection.Operations[0].State != transaction.OperationRolledBack || inspection.Operations[1].State != transaction.OperationCompensated {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestCoordinatorResumesRollbackFromSQLiteAfterReopen(t *testing.T) {
	now := time.Unix(1400, 0).UTC()
	path := filepath.Join(t.TempDir(), "abort-reopen.db")
	ledger, err := transaction.OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := &abortIDs{}
	coordinator := transaction.NewCoordinator(ledger, ids, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_reopen_abort", CatalogDigest: abortDigest(13), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "config.set", HandlerVersion: "v1", EffectClass: transaction.EffectReversible, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(14)})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: op.ID, Kind: transaction.AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(15)})
	if err != nil {
		t.Fatal(err)
	}
	store := effect.NewConfigStore(map[string]string{"feature": "off"})
	receipt, err := store.Apply(apply.Attempt.ID, "feature", 1, "on")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: apply.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := transaction.OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	now = time.Unix(1401, 0).UTC()
	restartedIDs := &abortIDs{next: 20}
	restarted := transaction.NewCoordinator(reopened, restartedIDs, func() time.Time { return now }, nil)
	step, err := restarted.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(1)})
	if err != nil || step.Dispatch == nil || step.Dispatch.Attempt.Kind != transaction.AttemptRollback {
		t.Fatalf("reopened abort step=%+v err=%v", step, err)
	}
	if _, err := store.Rollback(step.Dispatch.Attempt.ID, receipt.UndoToken); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	final, err := restarted.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, Ordinal: 3, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(2)})
	if err != nil || !final.Done || final.Transaction.State != transaction.TransactionRolledBack || store.Value("feature") != "off" {
		t.Fatalf("reopened final=%+v value=%q err=%v", final, store.Value("feature"), err)
	}
}

func TestCoordinatorRecordsFailedRollbackAsPartialAndReplaysTerminalStep(t *testing.T) {
	now := time.Unix(1450, 0).UTC()
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &abortIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_partial_rollback", CatalogDigest: abortDigest(3), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "config.set", HandlerVersion: "v1", EffectClass: transaction.EffectReversible, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(4)})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: op.ID, Kind: transaction.AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(5)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: apply.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	request := transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(6)}
	step, err := coordinator.BeginAbortStep(request)
	if err != nil || step.Dispatch == nil {
		t.Fatalf("rollback step=%+v err=%v", step, err)
	}
	failed, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: transaction.DispatchFailed})
	if err != nil || failed.Transaction.State != transaction.TransactionPartiallyReverted || failed.Operation.State != transaction.OperationRollbackFailed {
		t.Fatalf("failed rollback=%+v err=%v", failed, err)
	}
	replayed, err := coordinator.BeginAbortStep(request)
	if err != nil || replayed.PriorCompletion == nil || replayed.PriorCompletion.Transaction.State != transaction.TransactionPartiallyReverted {
		t.Fatalf("terminal replay=%+v err=%v", replayed, err)
	}
}

func TestCoordinatorMarksDispatchingAbortReplayAmbiguous(t *testing.T) {
	now := time.Unix(1460, 0).UTC()
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &abortIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_abort_replay", CatalogDigest: abortDigest(7), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "config.set", HandlerVersion: "v1", EffectClass: transaction.EffectReversible, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(8)})
	if err != nil {
		t.Fatal(err)
	}
	apply, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: op.ID, Kind: transaction.AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(9)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: apply.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	request := transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(10)}
	if step, err := coordinator.BeginAbortStep(request); err != nil || step.Dispatch == nil {
		t.Fatalf("initial abort step=%+v err=%v", step, err)
	}
	replayed, err := coordinator.BeginAbortStep(request)
	if !errors.Is(err, transaction.ErrConflict) || replayed.PriorCompletion == nil || replayed.Transaction.State != transaction.TransactionReconciliationRequired || replayed.PriorCompletion.Operation.State != transaction.OperationReconciliationRequired {
		t.Fatalf("dispatching replay=%+v err=%v", replayed, err)
	}
}

func TestCoordinatorDoesNotAutoCompensateWithoutHostAuthorization(t *testing.T) {
	now := time.Unix(1300, 0).UTC()
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &abortIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_comp_auth", CatalogDigest: abortDigest(9), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "inventory.reserve", HandlerVersion: "v1", EffectClass: transaction.EffectCompensatable, Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy_v1", ArgumentDigest: abortDigest(10)})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: op.ID, Kind: transaction.AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(11)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: op.ID, Kind: transaction.AttemptCompensate, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(12)}); !errors.Is(err, transaction.ErrAuthorityDenied) {
		t.Fatalf("public compensation bypass error=%v", err)
	}
	_, err = coordinator.BeginAbortStep(transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, CompensationAuthorized: false, Ordinal: 2, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(13)})
	if !errors.Is(err, transaction.ErrCompensationAuthorizationRequired) {
		t.Fatalf("unauthorized compensation error=%v", err)
	}
	operation, err := coordinator.InspectOperation(op.ID)
	if err != nil || operation.State != transaction.OperationApplied {
		t.Fatalf("unauthorized compensation mutated operation=%+v err=%v", operation, err)
	}
	request := transaction.AbortStepRequest{TransactionID: tx.ID, AutoCompensateTools: map[string]bool{}, CompensationAuthorized: true, Ordinal: 3, LeaseDuration: time.Minute, ProviderRequestDigest: abortDigest(14)}
	step, err := coordinator.BeginAbortStep(request)
	if err != nil || step.Dispatch == nil || step.Dispatch.Attempt.Kind != transaction.AttemptCompensate {
		t.Fatalf("authorized compensation step=%+v err=%v", step, err)
	}
	failed, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: op.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: transaction.DispatchFailed})
	if err != nil || failed.Transaction.State != transaction.TransactionPartiallyCompensated || failed.Operation.State != transaction.OperationCompensationFailed {
		t.Fatalf("failed compensation=%+v err=%v", failed, err)
	}
	replayed, err := coordinator.BeginAbortStep(request)
	if err != nil || replayed.PriorCompletion == nil || replayed.PriorCompletion.Transaction.State != transaction.TransactionPartiallyCompensated {
		t.Fatalf("failed compensation replay=%+v err=%v", replayed, err)
	}
}
