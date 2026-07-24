package transaction

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteLedgerRejectsSchemaDriftAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	ledger, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.db.Exec(`DROP INDEX attempts_transaction_order`); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.db.Exec(`CREATE INDEX attempts_wrong_order ON attempts(transaction_id)`); err != nil {
		t.Fatal(err)
	}
	tx, err := ledger.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	wrongDigest, err := computeSQLiteSchemaDigest(tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE schema_migrations SET schema_digest=? WHERE version=1`, wrongDigest); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLiteLedger(path); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("schema drift error=%v", err)
	}

	target := filepath.Join(t.TempDir(), "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "ledger-link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLiteLedger(link); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("symlink error=%v", err)
	}
}

func TestSQLiteLedgerRejectsNewerSchemaAndUsesPrivateFileMode(t *testing.T) {
	if _, err := OpenSQLiteLedger("relative.db"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("relative path error=%v", err)
	}
	path := filepath.Join(t.TempDir(), "schema.db")
	ledger, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
	if _, err := ledger.db.Exec(`INSERT INTO schema_migrations(version,applied_at_ns,schema_digest) VALUES(2,?,?)`, time.Now().UTC().UnixNano(), testDigest("future-schema")); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLiteLedger(path); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("newer schema error=%v", err)
	}
}

func TestSQLiteLedgerCoordinatorCompletesAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.db")
	ledger, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(30, 0).UTC()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	transactionValue, err := coordinator.Begin(BeginRequest{RunID: "run-reopen", CatalogDigest: testDigest("catalog-reopen"), Mode: TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(ProposeRequest{TransactionID: transactionValue.ID, ToolID: "demo.read", HandlerVersion: "v1", EffectClass: EffectReadOnly, Policy: PolicyAutoCommit, PolicyVersion: "policy-v1", ArgumentDigest: testDigest("args-reopen")})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: operation.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: testDigest("provider-reopen")})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	now = time.Unix(31, 0).UTC()
	recovered := NewCoordinator(reopened, &sequenceIDs{}, func() time.Time { return now }, nil)
	completion, err := recovered.CompleteDispatch(CompleteDispatchRequest{OperationID: operation.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Transaction.State != TransactionCommitted || completion.Operation.State != OperationApplied || completion.Attempt.State != AttemptSucceeded {
		t.Fatalf("completion=%+v", completion)
	}
	inspection, err := recovered.Inspect(transactionValue.ID, nil)
	if err != nil || len(inspection.Operations) != 1 || len(inspection.Attempts) != 1 || len(inspection.Transitions) != 8 {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestSQLiteLedgerReopensJournalAndDispatchingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	ledger, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	seedSQLiteLedger(t, ledger)
	if _, err := ledger.transitionAttempt("att_sql", 1, AttemptLeased, AttemptDispatching, time.Unix(11, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mode := ""
	if err := reopened.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	attempt, err := reopened.findAttemptByProviderRequest("tx_sql", testDigest("provider"))
	if err != nil || attempt.State != AttemptDispatching || attempt.Version != 2 {
		t.Fatalf("attempt=%+v err=%v", attempt, err)
	}
	operations, err := reopened.ListOperations("tx_sql")
	if err != nil || len(operations) != 1 || operations[0].ID != "op_sql" {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
	transitions, err := reopened.ListTransitions("tx_sql")
	if err != nil || len(transitions) != 4 {
		t.Fatalf("transitions=%+v err=%v", transitions, err)
	}
	for index, transition := range transitions {
		if transition.Sequence != uint64(index+1) {
			t.Fatalf("transition order=%+v", transitions)
		}
	}
}

func TestSQLiteLedgerCASAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cas.db")
	left, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err := left.createTransaction(Transaction{ID: "tx_cas_sql", RunID: "run-sql", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow, State: TransactionOpen, CreatedAt: time.Unix(20, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, candidate := range []struct {
		ledger *SQLiteLedger
		state  TransactionState
	}{{left, TransactionCommitted}, {right, TransactionRejected}} {
		wait.Add(1)
		go func(candidate struct {
			ledger *SQLiteLedger
			state  TransactionState
		}) {
			defer wait.Done()
			<-start
			_, err := candidate.ledger.transitionTransaction("tx_cas_sql", 1, TransactionOpen, candidate.state, time.Unix(21, 0).UTC())
			results <- err
		}(candidate)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected CAS error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	transitions, err := right.ListTransitions("tx_cas_sql")
	if err != nil || len(transitions) != 2 || transitions[1].Sequence != 2 {
		t.Fatalf("transitions=%+v err=%v", transitions, err)
	}
}

func TestSQLiteLedgerRejectsDuplicateProviderIdentity(t *testing.T) {
	ledger, err := OpenSQLiteLedger(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	seedSQLiteLedger(t, ledger)
	duplicate := Attempt{
		ID: "att_sql_2", TransactionID: "tx_sql", OperationID: "op_sql", Kind: AttemptApply, Ordinal: 2,
		State: AttemptLeased, ExpectedOperationState: OperationReady, LeaseID: "lease_sql_2",
		LeaseExpiresAt: time.Unix(20, 0).UTC(), ProviderRequestDigest: testDigest("provider"), CreatedAt: time.Unix(10, 0).UTC(),
	}
	if err := ledger.createAttempt(duplicate); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate provider identity error=%v", err)
	}
}

func seedSQLiteLedger(t *testing.T, ledger coordinatorLedger) {
	t.Helper()
	if err := ledger.createTransaction(Transaction{ID: "tx_sql", RunID: "run-sql", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow, State: TransactionOpen, CreatedAt: time.Unix(10, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.createOperation(Operation{
		ID: "op_sql", TransactionID: "tx_sql", Index: 1, ToolID: "demo.read", HandlerVersion: "v1",
		EffectClass: EffectReadOnly, Policy: PolicyAutoCommit, PolicyVersion: "policy-v1", State: OperationReady,
		ArgumentDigest: testDigest("arguments"), ManifestDigest: testDigest("manifest"), CreatedAt: time.Unix(10, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.createAttempt(Attempt{
		ID: "att_sql", TransactionID: "tx_sql", OperationID: "op_sql", Kind: AttemptApply, Ordinal: 1,
		State: AttemptLeased, ExpectedOperationState: OperationReady, LeaseID: "lease_sql",
		LeaseExpiresAt: time.Unix(20, 0).UTC(), ProviderRequestDigest: testDigest("provider"), CreatedAt: time.Unix(10, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}
