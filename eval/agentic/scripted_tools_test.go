package agentic

import (
	"context"
	"encoding/json"
	"testing"
)

func TestScriptedToolRuntimeExecutesExactHostOwnedFixture(t *testing.T) {
	runtime, err := NewScriptedToolRuntime([]ScriptedTool{{
		ToolID:      "fixture.get",
		InputSchema: json.RawMessage(`{"additionalProperties":false,"properties":{"scenario_id":{"type":"string"},"seed":{"type":"integer"}},"required":["scenario_id","seed"],"type":"object"}`),
		EffectClass: "read_only",
	}}, []ScriptedExpectedCall{{
		Name: "fixture.get", Arguments: json.RawMessage(`{"scenario_id":"dev_simple_read_001","seed":101}`),
		Result: json.RawMessage(`{"scenario_id":"dev_simple_read_001","status":"verified","value":101}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := runtime.TrustedPrepareWithPreboundTools()
	if err != nil || prepare == "" {
		t.Fatalf("prepare missing: err=%v", err)
	}
	result, err := runtime.InvokeDirect(context.Background(), "run-1", "call-1", "fixture.get", json.RawMessage(`{"seed":101,"scenario_id":"dev_simple_read_001"}`))
	if err != nil || string(result) != `{"scenario_id":"dev_simple_read_001","status":"verified","value":101}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	trace := runtime.Trace()
	if len(trace) != 1 || trace[0].Name != "fixture.get" ||
		!canonicalJSONEqual(trace[0].Arguments, json.RawMessage(`{"seed":101,"scenario_id":"dev_simple_read_001"}`)) {
		t.Fatalf("trace=%+v", trace)
	}
}

func TestScriptedToolRuntimeRejectsCallDrift(t *testing.T) {
	runtime, err := NewScriptedToolRuntime([]ScriptedTool{{
		ToolID:      "workspace.read_text",
		InputSchema: json.RawMessage(`{"additionalProperties":false,"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`),
		EffectClass: "read_only",
	}}, []ScriptedExpectedCall{{
		Name: "workspace.read_text", Arguments: json.RawMessage(`{"path":"input.txt"}`), Result: json.RawMessage(`{"content":"ok"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InvokeDirect(context.Background(), "run-2", "call-1", "workspace.read_text", json.RawMessage(`{"path":"other.txt"}`)); err == nil {
		t.Fatal("drifted call admitted")
	}
	if len(runtime.Trace()) != 1 || runtime.Complete() {
		t.Fatalf("trace=%+v complete=%v", runtime.Trace(), runtime.Complete())
	}
}
