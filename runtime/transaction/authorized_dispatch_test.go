package transaction

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizedIrreversibleCompletionRequiresConsumedExactApprovalAndReceipt(t *testing.T) {
	now := time.Unix(2100, 0).UTC()
	coordinator := NewCoordinator(NewMemoryLedger(), &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_authorized_provider", CatalogDigest: testDigest("authorized-provider-catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "fake-mail-v1", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, PolicyVersion: "mail-policy-v1", ArgumentDigest: testDigest("send-arguments")})
	if err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	credential := CommitCredential{Token: token}
	claims := AuthorityClaims{AuthorityID: "approval_provider", TransactionID: tx.ID, OperationID: operation.ID, ManifestDigest: operation.ManifestDigest, Source: CommitSourceUser, ActorID: "owner", ExpiresAt: now.Add(time.Minute)}
	if _, err := coordinator.RegisterApproval(credential, claims); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.BeginDispatch(DispatchRequest{OperationID: operation.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: operation.ManifestDigest}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dispatch before authorize err=%v", err)
	}
	if _, err := coordinator.Authorize(credential); err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(DispatchRequest{OperationID: operation.ID, Kind: AttemptApply, Ordinal: 1, LeaseDuration: time.Minute, ProviderRequestDigest: operation.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	request := CompleteDispatchRequest{OperationID: operation.ID, AttemptID: dispatch.Attempt.ID, Outcome: DispatchSucceeded, ProviderReceiptDigest: testDigest("provider-receipt")}
	if _, err := coordinator.CompleteDispatch(request); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("public completion err=%v", err)
	}
	if _, err := coordinator.CompleteAuthorizedDispatch(CommitCredential{Token: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}, request); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("wrong token completion err=%v", err)
	}
	withoutReceipt := request
	withoutReceipt.ProviderReceiptDigest = ""
	if _, err := coordinator.CompleteAuthorizedDispatch(credential, withoutReceipt); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("missing receipt completion err=%v", err)
	}
	completion, err := coordinator.CompleteAuthorizedDispatch(credential, request)
	if err != nil || completion.Operation.State != OperationApplied || completion.Attempt.State != AttemptSucceeded || completion.Attempt.ProviderReceiptDigest != request.ProviderReceiptDigest {
		t.Fatalf("completion=%+v err=%v", completion, err)
	}
	if _, err := coordinator.CompleteAuthorizedDispatch(credential, request); err != nil {
		t.Fatalf("authorized replay err=%v", err)
	}
	final, err := coordinator.FinalizeWorkflow(tx.ID)
	if err != nil || final.State != TransactionCommitted {
		t.Fatalf("final=%+v err=%v", final, err)
	}
}
