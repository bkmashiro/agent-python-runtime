package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type testIDs struct{ next int }

func (ids *testIDs) New(prefix string) (string, error) {
	ids.next++
	return prefix + "_cli_" + string(rune('0'+ids.next)), nil
}

func TestExecuteExportsStrictDigestOnlyEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	ledger, err := transaction.OpenSQLiteLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	coordinator := transaction.NewCoordinator(ledger, &testIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{
		RunID: "run_cli", CatalogDigest: digest("catalog"), Mode: transaction.TransactionModeWorkflow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Propose(transaction.ProposeRequest{
		TransactionID: tx.ID, ToolID: "fixture.read", HandlerVersion: "v1", EffectClass: transaction.EffectReadOnly,
		Policy: transaction.PolicyAutoCommit, PolicyVersion: "policy-v1", ArgumentDigest: digest("secret-argument-never-exported"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := execute([]string{"-db", path, "-transaction", tx.ID}, &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(200, 0).UTC() }})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var evidence transaction.TransactionEvidence
	if err := json.Unmarshal(stdout.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	schemaData, err := os.ReadFile("../../abi/v1/transaction-evidence-v2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const schemaURL = "https://agent-runtime.dev/abi/v1/transaction-evidence-v2.schema.json"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("generated evidence rejected by ABI schema: %v\n%s", err, stdout.Bytes())
	}
	if _, err := transaction.DecodeAndVerifyTransactionEvidence(stdout.Bytes()); err != nil {
		t.Fatalf("generated evidence digest rejected: %v", err)
	}
	tampered := bytes.Replace(stdout.Bytes(), []byte(`"run_id":"run_cli"`), []byte(`"run_id":"run_changed"`), 1)
	if bytes.Equal(tampered, stdout.Bytes()) {
		t.Fatal("tamper seam missing")
	}
	if _, err := transaction.DecodeAndVerifyTransactionEvidence(tampered); err == nil {
		t.Fatal("tampered CLI evidence accepted")
	}
	if evidence.Transaction.ID != tx.ID || evidence.EvidenceDigest == "" || evidence.GeneratedAt != time.Unix(200, 0).UTC() {
		t.Fatalf("evidence=%+v", evidence)
	}
	if bytes.Contains(stdout.Bytes(), []byte("secret-argument-never-exported")) {
		t.Fatalf("raw argument leaked: %s", stdout.Bytes())
	}
}

func TestExecuteFailsClosedWithoutLeakingPath(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "credential-secret.db")
	var stdout, stderr bytes.Buffer
	code := execute([]string{"-db", secretPath, "-transaction", "tx_missing"}, &stdout, &stderr, dependencies{})
	if code == 0 || stdout.Len() != 0 || bytes.Contains(stderr.Bytes(), []byte(secretPath)) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(secretPath); !os.IsNotExist(err) {
		t.Fatalf("inspection created missing ledger: %v", err)
	}
}

func digest(value string) string {
	// Test-only fixed-width canonical digest; the value is intentionally not embedded.
	const hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return "sha256:" + hex
}
