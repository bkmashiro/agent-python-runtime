package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/corpus"
)

func TestExecuteCaseThroughRealGuest(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("..", "..", "dist", "pysolate.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	item := corpus.Case{
		SchemaVersion: 1,
		ID:            "fixture/real-guest",
		Source:        `result = lookup(key="price") * inputs["quantity"]`,
		Inputs:        json.RawMessage(`{"quantity":2}`),
		Expected:      json.RawMessage(`42`),
		Origin:        corpus.Origin{Dataset: "fixture", Revision: "test", CaseID: "real-guest"},
		Tools:         []corpus.ToolSpec{{Name: "lookup", PythonPath: "lookup", AllowEarlyRead: true}},
		Replay:        []corpus.Replay{{Tool: "lookup", Args: json.RawMessage(`{"key":"price"}`), Value: json.RawMessage(`21`)}},
	}
	ctx := context.Background()
	rows := executeCase(ctx, wasm, "sha256:test", item, config{prepared: "copy", execution: "normal", iterations: 2, timeout: time.Minute})
	if len(rows) != 2 {
		t.Fatalf("rows=%#v", rows)
	}
	for _, row := range rows {
		if !row.Passed || string(row.Value) != "42" || row.SetupNS <= 0 || row.RunNS <= 0 {
			t.Fatalf("row=%#v", row)
		}
	}
}

func TestSelectCases(t *testing.T) {
	items := []corpus.Case{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	selected := selectCases(items, "", 2)
	if len(selected) != 2 || selected[1].ID != "b" {
		t.Fatalf("selected=%#v", selected)
	}
	selected = selectCases(items, "c", 0)
	if len(selected) != 1 || selected[0].ID != "c" {
		t.Fatalf("selected=%#v", selected)
	}
}
