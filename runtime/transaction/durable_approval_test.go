package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableApprovalAuthorizesAtomicallyAndReplays(t *testing.T) {
	now := time.Unix(1700, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_durable_approval", CatalogDigest: testDigest("approval-catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "v1", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("approval-args")})
	if err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	claims := AuthorityClaims{AuthorityID: "approval_durable", TransactionID: tx.ID, OperationID: op.ID, ManifestDigest: op.ManifestDigest, Source: CommitSourceUser, ActorID: "user_owner", ExpiresAt: now.Add(time.Minute)}
	evidence, err := coordinator.RegisterApproval(CommitCredential{Token: token}, claims)
	if err != nil || evidence.AuthorityID != claims.AuthorityID || !evidence.ConsumedAt.IsZero() {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: "short"}, claims); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("short token err=%v", err)
	}
	now = now.Add(time.Second)
	retry, err := coordinator.RegisterApproval(CommitCredential{Token: token}, claims)
	if err != nil || retry.RegisteredAt != evidence.RegisteredAt {
		t.Fatalf("registration retry=%+v err=%v", retry, err)
	}
	authorized, err := coordinator.Authorize(CommitCredential{Token: token})
	if err != nil || authorized.State != OperationReady {
		t.Fatalf("authorized=%+v err=%v", authorized, err)
	}
	consumedRetry, err := coordinator.RegisterApproval(CommitCredential{Token: token}, claims)
	if err != nil || consumedRetry.ConsumedAt != now || consumedRetry.RegisteredAt != evidence.RegisteredAt {
		t.Fatalf("consumed registration retry=%+v err=%v", consumedRetry, err)
	}
	replayed, err := coordinator.Authorize(CommitCredential{Token: token})
	if err != nil || replayed.ID != op.ID || replayed.Version != authorized.Version {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: op.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded}); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("public irreversible completion err=%v", err)
	}
	persisted, err := ledger.GetAttempt(dispatch.Attempt.ID)
	if err != nil || persisted.State != AttemptDispatching || persisted.ProviderReceiptDigest != "" {
		t.Fatalf("missing-receipt completion mutated attempt=%+v err=%v", persisted, err)
	}
	approvals, err := ledger.ListApprovals(tx.ID)
	if err != nil || len(approvals) != 1 || approvals[0].ConsumedAt != now {
		t.Fatalf("approvals=%+v err=%v", approvals, err)
	}
	snapshot, err := ledger.Snapshot(tx.ID)
	if err != nil || len(snapshot.Approvals) != 1 || snapshot.Approvals[0] != approvals[0] {
		t.Fatalf("snapshot approvals=%+v err=%v", snapshot.Approvals, err)
	}
	exported, err := BuildTransactionEvidence(ledger, tx.ID, now.Add(time.Minute))
	if err != nil || len(exported.Approvals) != 1 || exported.Metrics.ApprovalTotal != 1 || exported.Metrics.ConsumedApprovals != 1 {
		t.Fatalf("exported approvals=%+v metrics=%+v err=%v", exported.Approvals, exported.Metrics, err)
	}
	encoded, err := json.Marshal(exported)
	if err != nil || bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, []byte("token_digest")) {
		t.Fatalf("approval export leaked token material: %s err=%v", encoded, err)
	}
}

func TestIrreversibleReconciliationCannotFabricateSuccess(t *testing.T) {
	now := time.Unix(1800, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, op := setupApprovalOperation(t, coordinator)
	token := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	claims := AuthorityClaims{AuthorityID: "approval_reconcile", TransactionID: tx.ID, OperationID: op.ID, ManifestDigest: op.ManifestDigest, Source: CommitSourceUser, ActorID: "owner", ExpiresAt: now.Add(time.Minute)}
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: token}, claims); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(CommitCredential{Token: token}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: op.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: op.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(CompleteDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchAmbiguous}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReconcileDispatch(ReconcileDispatchRequest{OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded, ObservationDigest: testDigest("forged-observation")}); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("irreversible reconciliation success err=%v", err)
	}
	attempt, _ := ledger.GetAttempt(dispatch.Attempt.ID)
	if attempt.State != AttemptAmbiguous || attempt.ProviderReceiptDigest != "" {
		t.Fatalf("attempt=%+v", attempt)
	}
}

func setupApprovalOperation(t *testing.T, coordinator *Coordinator) (Transaction, Operation) {
	t.Helper()
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_approval", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "v1", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("args")})
	if err != nil {
		t.Fatal(err)
	}
	return tx, op
}

func TestSQLiteDurableApprovalSurvivesReopenWithoutRawToken(t *testing.T) {
	now := time.Unix(1800, 0).UTC()
	path := filepath.Join(t.TempDir(), "approval.sqlite")
	ledger, err := OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_sqlite_approval", CatalogDigest: testDigest("sqlite-approval-catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "v1", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, PolicyVersion: "policy_v1", ArgumentDigest: testDigest("sqlite-approval-args")})
	if err != nil {
		t.Fatal(err)
	}
	token := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	claims := AuthorityClaims{AuthorityID: "approval_sqlite", TransactionID: tx.ID, OperationID: op.ID, ManifestDigest: op.ManifestDigest, Source: CommitSourceUser, ActorID: "user_owner", ExpiresAt: now.Add(time.Minute)}
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: token}, claims); err != nil {
		t.Fatal(err)
	}
	var rawTokenRows int
	if err := ledger.db.QueryRow(`SELECT COUNT(*) FROM approvals WHERE token_digest=?`, token).Scan(&rawTokenRows); err != nil || rawTokenRows != 0 {
		t.Fatalf("raw token persisted rows=%d err=%v", rawTokenRows, err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	coordinator = NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	authorized, err := coordinator.Authorize(CommitCredential{Token: token})
	if err != nil || authorized.State != OperationReady {
		t.Fatalf("authorized after reopen=%+v err=%v", authorized, err)
	}
	if replay, err := coordinator.Authorize(CommitCredential{Token: token}); err != nil || replay.Version != authorized.Version {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	approvals, err := ledger.ListApprovals(tx.ID)
	if err != nil || len(approvals) != 1 || approvals[0].ConsumedAt != now {
		t.Fatalf("approvals=%+v err=%v", approvals, err)
	}

}
