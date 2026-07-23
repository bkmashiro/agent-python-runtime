package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryLedgerPreservesOrderedCopiesAndRejectsCrossTransactionOperations(t *testing.T) {
	ledger := NewMemoryLedger()
	tx := Transaction{
		ID:            "tx_test_1",
		RunID:         "run-test-1",
		CatalogDigest: testDigest("catalog"),
		Mode:          TransactionModeWorkflow,
		State:         TransactionOpen,
		CreatedAt:     time.Unix(1, 0).UTC(),
	}
	if err := ledger.createTransaction(tx); err != nil {
		t.Fatal(err)
	}
	if err := ledger.createTransaction(tx); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate transaction error = %v, want ErrAlreadyExists", err)
	}

	operations := []Operation{
		{ID: "op_2", TransactionID: tx.ID, Index: 2, ToolID: "inventory.reserve", HandlerVersion: "v1", EffectClass: EffectCompensatable, Policy: PolicyAutoCommit, PolicyVersion: "policy-v1", State: OperationProposed, ArgumentDigest: testDigest("two"), ManifestDigest: testDigest("manifest-two"), CreatedAt: time.Unix(1, 0).UTC()},
		{ID: "op_1", TransactionID: tx.ID, Index: 1, ToolID: "config.set", HandlerVersion: "v1", EffectClass: EffectReversible, Policy: PolicyAutoCommit, PolicyVersion: "policy-v1", State: OperationProposed, ArgumentDigest: testDigest("one"), ManifestDigest: testDigest("manifest-one"), CreatedAt: time.Unix(1, 0).UTC()},
	}
	for _, operation := range operations {
		if err := ledger.createOperation(operation); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.createOperation(Operation{ID: "op_cross", TransactionID: "tx_missing", Index: 3}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-transaction operation error = %v, want ErrNotFound", err)
	}

	got, err := ledger.ListOperations(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 2 {
		t.Fatalf("operations not returned in index order: %+v", got)
	}
	got[0].ToolID = "mutated"
	again, err := ledger.ListOperations(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].ToolID != "config.set" {
		t.Fatalf("caller mutated ledger state: %+v", again[0])
	}
}

func TestMemoryLedgerCompareAndSwapAllowsOneConcurrentTransition(t *testing.T) {
	ledger := NewMemoryLedger()
	tx := Transaction{ID: "tx_cas", RunID: "run-cas", CatalogDigest: testDigest("catalog"), Mode: TransactionModeDirect, State: TransactionOpen, CreatedAt: time.Unix(2, 0).UTC()}
	if err := ledger.createTransaction(tx); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, target := range []TransactionState{TransactionAborting, TransactionCommitting} {
		wg.Add(1)
		go func(target TransactionState) {
			defer wg.Done()
			_, err := ledger.transitionTransaction(tx.ID, 1, TransactionOpen, target, time.Unix(3, 0).UTC())
			results <- err
		}(target)
	}
	wg.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatalf("unexpected CAS error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want one each", success, conflict)
	}

	transitions, err := ledger.ListTransitions(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 2 || transitions[0].Sequence != 1 || transitions[1].Sequence != 2 {
		t.Fatalf("unexpected transition journal: %+v", transitions)
	}
}

func TestMemoryLedgerAttemptIdentityLeaseAndTerminalReplay(t *testing.T) {
	ledger := NewMemoryLedger()
	tx := Transaction{ID: "tx_attempt", RunID: "run-attempt", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow, State: TransactionOpen, CreatedAt: time.Unix(4, 0).UTC()}
	if err := ledger.createTransaction(tx); err != nil {
		t.Fatal(err)
	}
	op := Operation{ID: "op_attempt", TransactionID: tx.ID, Index: 1, ToolID: "mail.send", HandlerVersion: "v1", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, PolicyVersion: "policy-v1", State: OperationReady, ArgumentDigest: testDigest("args"), ManifestDigest: testDigest("manifest"), CreatedAt: time.Unix(4, 0).UTC()}
	if err := ledger.createOperation(op); err != nil {
		t.Fatal(err)
	}

	attempt := Attempt{
		ID:                     "att_1",
		TransactionID:          tx.ID,
		OperationID:            op.ID,
		Kind:                   AttemptApply,
		Ordinal:                1,
		State:                  AttemptLeased,
		ExpectedOperationState: OperationReady,
		LeaseID:                "lease_1",
		LeaseExpiresAt:         time.Unix(10, 0).UTC(),
		ProviderRequestDigest:  testDigest("provider-request"),
		CreatedAt:              time.Unix(4, 0).UTC(),
	}
	if err := ledger.createAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	invalidLease := attempt
	invalidLease.ID = "att_expired"
	invalidLease.Ordinal = 2
	invalidLease.LeaseExpiresAt = invalidLease.CreatedAt
	if err := ledger.createAttempt(invalidLease); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expired lease creation error = %v, want ErrInvalidInput", err)
	}
	wrongPriorState := attempt
	wrongPriorState.ID = "att_wrong_state"
	wrongPriorState.Ordinal = 3
	wrongPriorState.Kind = AttemptRollback
	wrongPriorState.ExpectedOperationState = OperationRollingBack
	if err := ledger.createAttempt(wrongPriorState); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong expected operation state error = %v, want ErrConflict", err)
	}
	duplicate := attempt
	duplicate.ID = "att_2"
	duplicate.ProviderRequestDigest = testDigest("changed-provider-request")
	if err := ledger.createAttempt(duplicate); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate operation/kind/ordinal error = %v, want ErrAlreadyExists even when provider digest changes", err)
	}
	expiring := attempt
	expiring.ID = "att_expiring"
	expiring.Ordinal = 4
	expiring.LeaseID = "lease_4"
	expiring.LeaseExpiresAt = time.Unix(6, 0).UTC()
	if err := ledger.createAttempt(expiring); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.transitionAttempt(expiring.ID, 1, AttemptLeased, AttemptDispatching, time.Unix(6, 0).UTC()); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired dispatch error = %v, want ErrExpired", err)
	}
	if _, err := ledger.transitionAttempt(attempt.ID, 1, AttemptLeased, AttemptDispatching, time.Unix(5, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	terminal, err := ledger.transitionAttempt(attempt.ID, 2, AttemptDispatching, AttemptAmbiguous, time.Unix(6, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ledger.transitionAttempt(attempt.ID, terminal.Version, AttemptAmbiguous, AttemptAmbiguous, time.Unix(7, 0).UTC())
	if err != nil {
		t.Fatalf("terminal replay should return existing attempt: %v", err)
	}
	if replayed.Version != terminal.Version || !replayed.UpdatedAt.Equal(terminal.UpdatedAt) {
		t.Fatalf("terminal replay mutated attempt: before=%+v after=%+v", terminal, replayed)
	}
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
