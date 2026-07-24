package transaction

import (
	"errors"
	"testing"
	"time"
)

type sequenceIDs struct{ next int }

func (ids *sequenceIDs) New(prefix string) (string, error) {
	ids.next++
	return prefix + "_host_" + string(rune('0'+ids.next)), nil
}

func TestCoordinatorCreatesHostOwnedTransactionsAndLimitsDirectMode(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	ledger := NewMemoryLedger()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, nil)

	if _, err := coordinator.Begin(BeginRequest{RunID: "run_1", CatalogDigest: "forged", Mode: TransactionModeDirect}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid digest error = %v, want ErrInvalidInput", err)
	}
	tx, err := coordinator.Begin(BeginRequest{RunID: "run_1", CatalogDigest: testDigest("catalog"), Mode: TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	if tx.ID == "" || tx.Version != 1 || tx.State != TransactionOpen || !tx.CreatedAt.Equal(now) {
		t.Fatalf("unexpected Host transaction: %+v", tx)
	}
	operation, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID,
		ToolID:        "config.get",
		EffectClass:   EffectReadOnly,
		Policy:        PolicyAutoCommit, HandlerVersion: "v1", PolicyVersion: "policy-v1",
		ArgumentDigest: testDigest("args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Index != 1 || operation.State != OperationReady || operation.ManifestDigest == "" {
		t.Fatalf("unexpected operation: %+v", operation)
	}
	if _, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "config.get", EffectClass: EffectReadOnly,
		Policy: PolicyAutoCommit, HandlerVersion: "v1", PolicyVersion: "policy-v1", ArgumentDigest: testDigest("args2"),
	}); !errors.Is(err, ErrDirectTransactionLimit) {
		t.Fatalf("second direct operation error = %v, want ErrDirectTransactionLimit", err)
	}
}

func TestCoordinatorMapsHostPolicyAndRejectsSameRunAgentCommit(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	coordinator := NewCoordinator(NewMemoryLedger(), &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "staging_run", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	agentOperation, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", EffectClass: EffectIrreversible, Policy: PolicyAgentCommitRequired, HandlerVersion: "v1", PolicyVersion: "policy-v1", ArgumentDigest: testDigest("mail-args")})
	if err != nil {
		t.Fatal(err)
	}
	sameClaims := AuthorityClaims{AuthorityID: "approval_same", TransactionID: tx.ID, OperationID: agentOperation.ID, ManifestDigest: agentOperation.ManifestDigest, Source: CommitSourceAgent, SourceRunID: "staging_run", ActorID: "agent", PhaseGrantID: "phase_same", ExpiresAt: now.Add(time.Minute)}
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: "same-run-token-0123456789abcdef0123456789abcdef"}, sameClaims); !errors.Is(err, ErrSameRunCommit) {
		t.Fatalf("same-run commit error = %v", err)
	}
	laterToken := "later-run-token-0123456789abcdef0123456789abcdef"
	laterClaims := sameClaims
	laterClaims.AuthorityID = "approval_later"
	laterClaims.SourceRunID = "later_run"
	laterClaims.PhaseGrantID = "phase_later"
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: laterToken}, laterClaims); err != nil {
		t.Fatal(err)
	}
	authorized, err := coordinator.Authorize(CommitCredential{Token: laterToken})
	if err != nil || authorized.State != OperationReady {
		t.Fatalf("authorized=%+v err=%v", authorized, err)
	}
}

func TestCoordinatorRequiresTrustedExactUnexpiredUserApproval(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	coordinator := NewCoordinator(NewMemoryLedger(), &sequenceIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(BeginRequest{RunID: "staging_run", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", EffectClass: EffectIrreversible, Policy: PolicyUserApprovalRequired, HandlerVersion: "v1", PolicyVersion: "policy-v1", ArgumentDigest: testDigest("mail-args")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(CommitCredential{Token: "forged-token-0123456789abcdef0123456789abcdef"}); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("unregistered approval error = %v", err)
	}
	mismatch := AuthorityClaims{AuthorityID: "approval_mismatch", TransactionID: tx.ID, OperationID: operation.ID, ManifestDigest: testDigest("changed-manifest"), Source: CommitSourceUser, ActorID: "user", ExpiresAt: now.Add(time.Minute)}
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: "mismatch-token-0123456789abcdef0123456789abcdef"}, mismatch); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("mismatched approval error = %v", err)
	}
	expired := mismatch
	expired.AuthorityID = "approval_expired"
	expired.ManifestDigest = operation.ManifestDigest
	expired.ExpiresAt = now
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: "expired-token-0123456789abcdef0123456789abcdef"}, expired); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired approval error = %v", err)
	}
	validToken := "valid-token-0123456789abcdef0123456789abcdef012345"
	valid := expired
	valid.AuthorityID = "approval_valid"
	valid.ExpiresAt = now.Add(time.Minute)
	if _, err := coordinator.RegisterApproval(CommitCredential{Token: validToken}, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(CommitCredential{Token: validToken}); err != nil {
		t.Fatal(err)
	}
	replayed, err := coordinator.Authorize(CommitCredential{Token: validToken})
	if err != nil || replayed.ID != operation.ID || replayed.State != OperationReady {
		t.Fatalf("approval replay = %+v, %v", replayed, err)
	}
}
