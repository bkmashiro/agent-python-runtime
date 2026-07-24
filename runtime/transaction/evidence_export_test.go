package transaction

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildTransactionEvidenceIsDeterministicBoundedAndDigestOnly(t *testing.T) {
	ledger := NewMemoryLedger()
	seedSQLiteLedger(t, ledger)
	if _, err := ledger.transitionAttempt("att_sql", 1, AttemptLeased, AttemptDispatching, time.Unix(11, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Unix(20, 0).UTC()
	first, err := BuildTransactionEvidence(ledger, "tx_sql", generatedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTransactionEvidence(ledger, "tx_sql", generatedAt)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) || !digestPattern.MatchString(first.EvidenceDigest) {
		t.Fatalf("nondeterministic evidence: %s != %s", firstJSON, secondJSON)
	}
	if err := VerifyTransactionEvidenceDigest(first); err != nil {
		t.Fatal(err)
	}
	tampered := first
	tampered.Transaction.RunID = "run_tampered"
	if err := VerifyTransactionEvidenceDigest(tampered); err == nil {
		t.Fatal("tampered evidence digest accepted")
	}
	if first.Metrics.OperationTotal != 1 || first.Metrics.AttemptTotal != 1 || first.Metrics.DispatchingAttempts != 1 || first.Metrics.TransitionTotal != 4 {
		t.Fatalf("metrics=%+v", first.Metrics)
	}
	encoded := string(firstJSON)
	for _, forbidden := range []string{"arguments\"", "credentials", "authorization", "approval_token", "undo_token", "secret"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(encoded, testDigest("arguments")) || !strings.Contains(encoded, testDigest("provider")) {
		t.Fatalf("digest evidence missing: %s", encoded)
	}
}

func TestBuildTransactionEvidenceRejectsCorruptTransitionHistory(t *testing.T) {
	ledger := NewMemoryLedger()
	seedSQLiteLedger(t, ledger)
	ledger.mu.Lock()
	ledger.transitions["tx_sql"] = append(ledger.transitions["tx_sql"], Transition{
		Sequence: 4, TransactionID: "tx_sql", EntityType: "operation", EntityID: "op_missing",
		From: string(OperationReady), To: string(OperationApplied), ObservedAt: time.Unix(11, 0).UTC(),
	})
	ledger.mu.Unlock()
	if _, err := BuildTransactionEvidence(ledger, "tx_sql", time.Unix(20, 0).UTC()); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("corrupt history error=%v", err)
	}
}

func TestBuildTransactionEvidenceRejectsInvalidIdentityAndTime(t *testing.T) {
	ledger := NewMemoryLedger()
	seedSQLiteLedger(t, ledger)
	if _, err := BuildTransactionEvidence(ledger, "../tx_sql", time.Now()); err == nil {
		t.Fatal("invalid transaction identity accepted")
	}
	if _, err := BuildTransactionEvidence(ledger, "tx_sql", time.Time{}); err == nil {
		t.Fatal("zero evidence time accepted")
	}
}
