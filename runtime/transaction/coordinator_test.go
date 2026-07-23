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
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, newFakeAuthorityVerifier())

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
	ledger := NewMemoryLedger()
	verifier := newFakeAuthorityVerifier()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, verifier)
	tx, err := coordinator.Begin(BeginRequest{RunID: "staging_run", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}

	agentOperation, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "mail.send", EffectClass: EffectIrreversible,
		Policy: PolicyAgentCommitRequired, HandlerVersion: "v1", PolicyVersion: "policy-v1", ArgumentDigest: testDigest("mail-args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if agentOperation.State != OperationAwaitingAgentCommit {
		t.Fatalf("agent operation state = %q", agentOperation.State)
	}
	sameRunToken := verifier.issue(AuthorityClaims{
		AuthorityID: "approval_same", TransactionID: tx.ID, OperationID: agentOperation.ID,
		ManifestDigest: agentOperation.ManifestDigest, Source: CommitSourceAgent,
		SourceRunID: "staging_run", ActorID: "agent", PhaseGrantID: "phase_same",
		ExpiresAt: now.Add(time.Minute),
	})
	if _, err := coordinator.Authorize(CommitCredential{Token: sameRunToken}); !errors.Is(err, ErrSameRunCommit) {
		t.Fatalf("same-run commit error = %v, want ErrSameRunCommit", err)
	}
	laterToken := verifier.issue(AuthorityClaims{
		AuthorityID: "approval_later", TransactionID: tx.ID, OperationID: agentOperation.ID,
		ManifestDigest: agentOperation.ManifestDigest, Source: CommitSourceAgent,
		SourceRunID: "later_run", ActorID: "agent", PhaseGrantID: "phase_later",
		ExpiresAt: now.Add(time.Minute),
	})
	authorized, err := coordinator.Authorize(CommitCredential{Token: laterToken})
	if err != nil {
		t.Fatal(err)
	}
	if authorized.State != OperationReady {
		t.Fatalf("authorized operation state = %q", authorized.State)
	}
}

func TestCoordinatorRequiresTrustedExactUnexpiredUserApproval(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	ledger := NewMemoryLedger()
	verifier := newFakeAuthorityVerifier()
	coordinator := NewCoordinator(ledger, &sequenceIDs{}, func() time.Time { return now }, verifier)
	tx, err := coordinator.Begin(BeginRequest{RunID: "staging_run", CatalogDigest: testDigest("catalog"), Mode: TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(ProposeRequest{
		TransactionID: tx.ID, ToolID: "mail.send", EffectClass: EffectIrreversible,
		Policy: PolicyUserApprovalRequired, HandlerVersion: "v1", PolicyVersion: "policy-v1", ArgumentDigest: testDigest("mail-args"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(CommitCredential{Token: "forged"}); !errors.Is(err, ErrAuthorityDenied) {
		t.Fatalf("unverified approval error = %v, want ErrAuthorityDenied", err)
	}
	mismatchToken := verifier.issue(AuthorityClaims{
		AuthorityID: "approval_mismatch", TransactionID: tx.ID, OperationID: operation.ID,
		ManifestDigest: testDigest("changed-manifest"), Source: CommitSourceUser,
		ActorID: "user", ExpiresAt: now.Add(time.Minute),
	})
	if _, err := coordinator.Authorize(CommitCredential{Token: mismatchToken}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("mismatched approval error = %v, want ErrDigestMismatch", err)
	}
	expiredToken := verifier.issue(AuthorityClaims{
		AuthorityID: "approval_expired", TransactionID: tx.ID, OperationID: operation.ID,
		ManifestDigest: operation.ManifestDigest, Source: CommitSourceUser,
		ActorID: "user", ExpiresAt: now,
	})
	if _, err := coordinator.Authorize(CommitCredential{Token: expiredToken}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired approval error = %v, want ErrExpired", err)
	}
	validToken := verifier.issue(AuthorityClaims{
		AuthorityID: "approval_valid", TransactionID: tx.ID, OperationID: operation.ID,
		ManifestDigest: operation.ManifestDigest, Source: CommitSourceUser,
		ActorID: "user", ExpiresAt: now.Add(time.Minute),
	})
	if _, err := coordinator.Authorize(CommitCredential{Token: validToken}); err != nil {
		t.Fatal(err)
	}
	replayed, err := coordinator.Authorize(CommitCredential{Token: validToken})
	if err != nil || replayed.ID != operation.ID || replayed.State != OperationReady {
		t.Fatalf("approval replay = %+v, %v; want existing ready operation", replayed, err)
	}
	if !verifier.consumed[validToken] {
		t.Fatal("successful authority was not consumed")
	}
}

type fakeAuthorityVerifier struct {
	claims   map[string]AuthorityClaims
	consumed map[string]bool
	next     int
}

func newFakeAuthorityVerifier() *fakeAuthorityVerifier {
	return &fakeAuthorityVerifier{claims: map[string]AuthorityClaims{}, consumed: map[string]bool{}}
}

func (verifier *fakeAuthorityVerifier) issue(claims AuthorityClaims) string {
	verifier.next++
	token := "opaque-token-" + string(rune('0'+verifier.next))
	verifier.claims[token] = claims
	return token
}

func (verifier *fakeAuthorityVerifier) Verify(token string) (AuthorityClaims, error) {
	claims, ok := verifier.claims[token]
	if !ok {
		return AuthorityClaims{}, ErrAuthorityDenied
	}
	claims.Consumed = verifier.consumed[token]
	return claims, nil
}

func (verifier *fakeAuthorityVerifier) Consume(token string) error {
	if _, ok := verifier.claims[token]; !ok || verifier.consumed[token] {
		return ErrAuthorityDenied
	}
	verifier.consumed[token] = true
	return nil
}
