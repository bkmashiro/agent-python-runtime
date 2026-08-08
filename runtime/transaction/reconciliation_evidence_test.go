package transaction

import (
	"errors"
	"testing"
	"time"
)

func TestReconciledIrreversibleEvidenceIncludesReceiptAndObservation(t *testing.T) {
	now := time.Unix(2200, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{
		RunID: "run_reconciled_evidence", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "fake-mail-v1",
		EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired,
		PolicyVersion: "mail-policy-v1", ArgumentDigest: testDigest("arguments"),
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := CommitCredential{Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	claims := AuthorityClaims{
		AuthorityID: "approval_reconcile", TransactionID: tx.ID, OperationID: op.ID,
		ManifestDigest: op.ManifestDigest, Source: CommitSourceUser, ActorID: "owner",
		ExpiresAt: now.Add(time.Minute),
	}
	if _, err := coordinator.RegisterApproval(credential, claims); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(credential); err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{
		OperationID: op.ID, Kind: AttemptApply, Ordinal: 1,
		LeaseDuration: time.Minute, ProviderRequestDigest: op.ManifestDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(CompleteDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchAmbiguous,
	}); err != nil {
		t.Fatal(err)
	}
	receiptDigest := testDigest("provider-receipt")
	observationDigest := testDigest("readback-observation")
	if _, err := coordinator.ReconcileAuthorizedDispatch(credential, ReconcileDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
		ObservationDigest: observationDigest,
	}); err != ErrAuthorityDenied {
		t.Fatalf("missing receipt err=%v", err)
	}
	if _, err := coordinator.ReconcileAuthorizedDispatch(credential, ReconcileDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
		ProviderReceiptDigest: observationDigest, ObservationDigest: observationDigest,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("identical evidence err=%v", err)
	}
	completion, err := coordinator.ReconcileAuthorizedDispatch(credential, ReconcileDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
		ProviderReceiptDigest: receiptDigest, ObservationDigest: observationDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Attempt.ProviderReceiptDigest != receiptDigest ||
		completion.Attempt.ReconciliationDigest != observationDigest {
		t.Fatalf("attempt=%+v", completion.Attempt)
	}
	replayed, err := coordinator.ReconcileAuthorizedDispatch(credential, ReconcileDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
		ProviderReceiptDigest: receiptDigest, ObservationDigest: observationDigest,
	})
	if err != nil || replayed.Attempt.Version != completion.Attempt.Version {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	if _, err := coordinator.ReconcileAuthorizedDispatch(credential, ReconcileDispatchRequest{
		OperationID: op.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded,
		ProviderReceiptDigest: testDigest("changed-receipt"), ObservationDigest: observationDigest,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting receipt err=%v", err)
	}
	if _, err := coordinator.FinalizeWorkflow(tx.ID); err != nil {
		t.Fatal(err)
	}
	evidence, err := BuildTransactionEvidence(ledger, tx.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Attempts) != 1 || evidence.Metrics.ReconciledAttempts != 1 ||
		evidence.Attempts[0].ProviderReceiptDigest != receiptDigest ||
		evidence.Attempts[0].ReconciliationDigest != observationDigest {
		t.Fatalf("evidence=%+v", evidence)
	}
}
