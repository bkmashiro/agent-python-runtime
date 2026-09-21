package corpus

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestReplayProviderMatchesExactRequestsAndCounts(t *testing.T) {
	caseSpec := Case{
		SchemaVersion: 1,
		ID:            "fixture/replay",
		Source:        "result=1",
		Inputs:        json.RawMessage(`{}`),
		Expected:      json.RawMessage(`1`),
		Origin:        Origin{Dataset: "fixture", Revision: "abc", CaseID: "replay-counts"},
		Tools:         []ToolSpec{{Name: "market/prices", PythonPath: "market.get_prices", AllowEarlyRead: true}},
		Replay:        []Replay{{Tool: "market/prices", Args: json.RawMessage(`{"symbols":["AAPL"]}`), Value: json.RawMessage(`{"AAPL":125}`), Count: 2}},
	}
	provider, err := NewReplayProvider(caseSpec)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := provider.Tools(context.Background())
	if err != nil || len(definitions) != 1 {
		t.Fatalf("definitions=%#v err=%v", definitions, err)
	}
	call := definitions[0].Spec.Call
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, callErr := call(context.Background(), json.RawMessage(`{ "symbols": ["AAPL"] }`))
			if callErr != nil {
				t.Errorf("call: %v", callErr)
				return
			}
			encoded, _ := json.Marshal(value)
			if string(encoded) != `{"AAPL":125}` {
				t.Errorf("value=%s", encoded)
			}
		}()
	}
	wg.Wait()
	if err := provider.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Verify(); err == nil || !strings.Contains(err.Error(), "unconsumed") {
		t.Fatalf("reset verify err=%v", err)
	}
	for range 2 {
		if _, err := call(context.Background(), json.RawMessage(`{"symbols":["AAPL"]}`)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := call(context.Background(), json.RawMessage(`{"symbols":["AAPL"]}`)); err == nil || !strings.Contains(err.Error(), "unexpected or exhausted") {
		t.Fatalf("unexpected third call err=%v", err)
	}
}

func TestReplayProviderReportsUnconsumedAndUnexpectedCalls(t *testing.T) {
	caseSpec := Case{
		SchemaVersion: 1,
		ID:            "fixture/replay",
		Source:        "result=1",
		Inputs:        json.RawMessage(`{}`),
		Expected:      json.RawMessage(`1`),
		Origin:        Origin{Dataset: "fixture", Revision: "abc", CaseID: "replay-errors"},
		Tools:         []ToolSpec{{Name: "lookup", PythonPath: "lookup"}},
		Replay:        []Replay{{Tool: "lookup", Args: json.RawMessage(`{"key":"ok"}`), Value: json.RawMessage(`1`)}},
	}
	provider, err := NewReplayProvider(caseSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Verify(); err == nil || !strings.Contains(err.Error(), "unconsumed") {
		t.Fatalf("verify err=%v", err)
	}
	definitions, _ := provider.Tools(context.Background())
	if _, err := definitions[0].Spec.Call(context.Background(), json.RawMessage(`{"key":"wrong"}`)); err == nil || !strings.Contains(err.Error(), "unexpected or exhausted") {
		t.Fatalf("unexpected call err=%v", err)
	}
}
