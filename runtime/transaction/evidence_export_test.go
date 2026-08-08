package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	recomputedInvalid := first
	recomputedInvalid.Transaction.Mode = ""
	recomputedInvalid.EvidenceDigest, err = ComputeTransactionEvidenceDigest(recomputedInvalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTransactionEvidenceDigest(recomputedInvalid); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("recomputed invalid evidence err=%v", err)
	}
	recomputedMetrics := first
	recomputedMetrics.Metrics.OperationTotal++
	recomputedMetrics.EvidenceDigest, err = ComputeTransactionEvidenceDigest(recomputedMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTransactionEvidenceDigest(recomputedMetrics); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("recomputed invalid metrics err=%v", err)
	}
	recomputedHistory := first
	recomputedHistory.Transitions = append([]EvidenceTransition(nil), first.Transitions...)
	recomputedHistory.Transitions[len(recomputedHistory.Transitions)-1].To = string(TransactionRejected)
	recomputedHistory.EvidenceDigest, err = ComputeTransactionEvidenceDigest(recomputedHistory)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTransactionEvidenceDigest(recomputedHistory); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("recomputed invalid history err=%v", err)
	}
	recomputedNullCollection := first
	recomputedNullCollection.Operations = nil
	recomputedNullCollection.EvidenceDigest, err = ComputeTransactionEvidenceDigest(recomputedNullCollection)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTransactionEvidenceDigest(recomputedNullCollection); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("recomputed null collection err=%v", err)
	}
	recomputedExpectedState := first
	recomputedExpectedState.Attempts = append([]EvidenceAttempt(nil), first.Attempts...)
	recomputedExpectedState.Attempts[0].ExpectedOperationState = OperationProposed
	recomputedExpectedState.EvidenceDigest, err = ComputeTransactionEvidenceDigest(recomputedExpectedState)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyTransactionEvidenceDigest(recomputedExpectedState); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("recomputed invalid expected operation state err=%v", err)
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

func TestDecodeTransactionEvidenceRejectsDuplicateKeysBeforeTypedDecode(t *testing.T) {
	ledger := NewMemoryLedger()
	seedSQLiteLedger(t, ledger)
	value, err := BuildTransactionEvidence(ledger, "tx_sql", time.Unix(20, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	rootNeedle := []byte(fmt.Sprintf(`"schema_version":%q,`, value.SchemaVersion))
	transactionNeedle := []byte(fmt.Sprintf(`"transaction_id":%q,`, value.Transaction.ID))
	runNeedle := []byte(fmt.Sprintf(`"run_id":%q,`, value.Transaction.RunID))
	cases := [][]byte{
		bytes.Replace(encoded, rootNeedle, append(append([]byte(nil), rootNeedle...), rootNeedle...), 1),
		bytes.Replace(encoded, transactionNeedle, append(append([]byte(nil), transactionNeedle...), transactionNeedle...), 1),
		bytes.Replace(encoded, runNeedle, append(append([]byte(nil), runNeedle...), []byte(fmt.Sprintf(`"run_\u0069d":%q,`, value.Transaction.RunID))...), 1),
	}
	for index, candidate := range cases {
		if bytes.Equal(candidate, encoded) {
			t.Fatalf("case %d did not inject duplicate key", index)
		}
		if _, err := DecodeAndVerifyTransactionEvidence(candidate); !errors.Is(err, ErrInvalidEvidence) {
			t.Fatalf("case %d err=%v", index, err)
		}
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
