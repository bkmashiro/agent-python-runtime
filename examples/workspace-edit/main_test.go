package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceEditAcceptanceWithRealGuest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	result, err := executeAcceptance(ctx, guest)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Price int    `json:"price"`
		Note  string `json:"note"`
		Audit string `json:"audit"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatal(err)
	}
	if value.Price != 123 || value.Note != "Approved local catalog record" || value.Audit != "created" {
		t.Fatalf("unexpected result: %#v", value)
	}
	if result.Before == result.After || len(result.Changes.Changes) != 3 || result.ExternalWrites != 1 {
		t.Fatalf("unexpected acceptance evidence: %#v", result)
	}
}

func TestCatalogConnectionIsReadOnly(t *testing.T) {
	database, err := openCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO catalog VALUES ('MSFT', 'must fail')`); err == nil {
		t.Fatal("read-only catalog accepted a write")
	}
}

func TestAuditWriteIsIdempotent(t *testing.T) {
	audit := &idempotentAudit{records: make(map[string]string)}
	request := json.RawMessage(`{"request_id":"same","message":"one"}`)
	for range 2 {
		if _, err := audit.call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if audit.count() != 1 {
		t.Fatalf("writes=%d, want 1", audit.count())
	}
	if _, err := audit.call(context.Background(), json.RawMessage(`{"request_id":"same","message":"different"}`)); err == nil {
		t.Fatal("idempotency key conflict was accepted")
	}
}
