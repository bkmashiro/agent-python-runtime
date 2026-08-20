package wazero

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSemanticAnalysisLifecycleStoreAggregatesBodyFreeEvidence(t *testing.T) {
	var store semanticAnalysisLifecycleStore
	store.add(SemanticAnalysisLifecycleEvidence{
		Invocations: 1, ModuleInstantiations: 1, InitializeCalls: 1, RuntimeInitCalls: 1, Successes: 1,
		InstantiateNanos: 2, InitializeNanos: 3, RuntimeInitNanos: 5, AnalyzeNanos: 7, CloseNanos: 11,
	})
	store.add(SemanticAnalysisLifecycleEvidence{
		Invocations: 1, ModuleInstantiations: 1, InitializeCalls: 1, RuntimeInitCalls: 1, Failures: 1,
		InstantiateNanos: 13, InitializeNanos: 17, RuntimeInitNanos: 19, AnalyzeNanos: 23, CloseNanos: 29,
	})
	got := store.get()
	if got.SchemaVersion != SemanticAnalysisLifecycleSchemaVersion || got.Invocations != 2 || got.ModuleInstantiations != 2 ||
		got.InitializeCalls != 2 || got.RuntimeInitCalls != 2 || got.Successes != 1 || got.Failures != 1 ||
		got.InstantiateNanos != 15 || got.InitializeNanos != 20 || got.RuntimeInitNanos != 24 ||
		got.AnalyzeNanos != 30 || got.CloseNanos != 40 {
		t.Fatalf("evidence=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"source", "result", "request", "response"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("lifecycle evidence leaked body-bearing field %q: %s", forbidden, encoded)
		}
	}
}

func TestNilEngineSemanticAnalysisLifecycleEvidenceIsTypedEmpty(t *testing.T) {
	var engine *Engine
	got := engine.SemanticAnalysisLifecycleEvidence()
	if got.SchemaVersion != SemanticAnalysisLifecycleSchemaVersion || got.Invocations != 0 {
		t.Fatalf("evidence=%+v", got)
	}
}
