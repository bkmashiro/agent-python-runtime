package capability_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestCapabilitySpecCanonicalizationAndPlanIdentity(t *testing.T) {
	first := capability.NewRegistry()
	firstSpec := testSpec()
	firstSpec.InputSchema = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	if err := first.Register(firstSpec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	firstPlan, err := first.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}

	second := capability.NewRegistry()
	secondSpec := testSpec()
	secondSpec.InputSchema = json.RawMessage(`{
		"additionalProperties": false,
		"required": ["path"],
		"properties": {"path": {"type": "string"}},
		"type": "object"
	}`)
	if err := second.Register(secondSpec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	secondPlan, err := second.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Identity() != secondPlan.Identity() {
		t.Fatalf("schema formatting changed identity: %s != %s", firstPlan.Identity(), secondPlan.Identity())
	}

	mutations := []func(*capability.Spec){
		func(spec *capability.Spec) { spec.Version = "pysolate.workspace.read-text.v2" },
		func(spec *capability.Spec) { spec.Description = "Read a different semantic projection." },
		func(spec *capability.Spec) { spec.EffectClass = capability.EffectExternalRead },
		func(spec *capability.Spec) { spec.Playback = capability.PlaybackCaptured },
		func(spec *capability.Spec) { spec.InputSchema = json.RawMessage(`{"type":"object"}`) },
		func(spec *capability.Spec) { spec.OutputSchema = json.RawMessage(`{"type":"object"}`) },
		func(spec *capability.Spec) { spec.Python.ResultField = "body" },
	}
	for index, mutate := range mutations {
		registry := capability.NewRegistry()
		spec := testSpec()
		mutate(&spec)
		if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
			t.Fatal(err)
		}
		plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Identity() == firstPlan.Identity() {
			t.Fatalf("mutation %d did not change plan identity", index)
		}
	}
}

func TestCapabilitySpecRejectsInvalidSchemaAndProjection(t *testing.T) {
	for name, mutate := range map[string]func(*capability.Spec){
		"invalid input schema": func(spec *capability.Spec) { spec.InputSchema = json.RawMessage(`{"type":`) },
		"external schema ref": func(spec *capability.Spec) {
			spec.InputSchema = json.RawMessage(`{"$ref":"https://example.test/schema.json"}`)
		},
		"invalid Python name":        func(spec *capability.Spec) { spec.Python.Name = "not-valid" },
		"missing description":        func(spec *capability.Spec) { spec.Description = "" },
		"invalid effect class":       func(spec *capability.Spec) { spec.EffectClass = "get" },
		"invalid playback treatment": func(spec *capability.Spec) { spec.Playback = "retry" },
		"invalid UTF-8 identity":     func(spec *capability.Spec) { spec.Version = string([]byte{0xff}) },
		"invalid UTF-8 schema": func(spec *capability.Spec) {
			spec.InputSchema = json.RawMessage{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
		},
		"invalid UTF-8 result field": func(spec *capability.Spec) { spec.Python.ResultField = string([]byte{0xff}) },
		"Python keyword":             func(spec *capability.Spec) { spec.Python.Arguments = []string{"class"} },
		"reserved helper":            func(spec *capability.Spec) { spec.Python.Arguments = []string{"_capability_call"} },
		"Python builtin":             func(spec *capability.Spec) { spec.Python.Name = "len" },
		"Guest result name":          func(spec *capability.Spec) { spec.Python.Name = "result" },
		"duplicate argument":         func(spec *capability.Spec) { spec.Python.Arguments = []string{"path", "path"} },
	} {
		t.Run(name, func(t *testing.T) {
			registry := capability.NewRegistry()
			spec := testSpec()
			mutate(&spec)
			if err := registry.Register(spec, basicGrant(t), noopHandler); err != capability.ErrInvalidTool {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCapabilitySpecRejectsDuplicatePythonProjection(t *testing.T) {
	registry := capability.NewRegistry()
	first := testSpec()
	if err := registry.Register(first, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	second := testSpec()
	second.Name = "workspace.read_alias"
	second.Version = "pysolate.workspace.read-alias.v1"
	if err := registry.Register(second, basicGrant(t), noopHandler); err != capability.ErrToolExists {
		t.Fatalf("duplicate Python projection error=%v", err)
	}
}

func TestBrokerValidatesSpecInputAndOutput(t *testing.T) {
	var calls atomic.Uint32
	registry := capability.NewRegistry()
	spec := testSpec()
	handler := capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"content":7}`), nil
	})
	if err := registry.Register(spec, basicGrant(t), handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err := broker.Call(context.Background(), []byte(`{"call_id":"zero","capability":"workspace.read_text","capability":"workspace.write_text","arguments":{"path":"note.txt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || broker.Calls() != 0 || !strings.Contains(string(ambiguous), `"code":"invalid_arguments"`) {
		t.Fatalf("ambiguous envelope was accepted: calls=%d broker_calls=%d response=%s", calls.Load(), broker.Calls(), ambiguous)
	}
	invalidUTF8 := append([]byte(`{"call_id":"utf8","capability":"workspace.read_text","arguments":{"path":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}}`)...)
	invalidEncoding, err := broker.Call(context.Background(), invalidUTF8)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || broker.Calls() != 0 || !strings.Contains(string(invalidEncoding), `"code":"invalid_arguments"`) {
		t.Fatalf("invalid UTF-8 envelope was accepted: calls=%d broker_calls=%d response=%s", calls.Load(), broker.Calls(), invalidEncoding)
	}
	invalidInput, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{"unexpected":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || !strings.Contains(string(invalidInput), `"status":"denied"`) || !strings.Contains(string(invalidInput), `"code":"invalid_arguments"`) {
		t.Fatalf("invalid input reached handler: calls=%d response=%s", calls.Load(), invalidInput)
	}
	invalidOutput, err := broker.Call(context.Background(), []byte(`{"call_id":"two","capability":"workspace.read_text","arguments":{"path":"note.txt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !strings.Contains(string(invalidOutput), `"status":"error"`) || !strings.Contains(string(invalidOutput), `"code":"invalid_result"`) {
		t.Fatalf("invalid output was accepted: calls=%d response=%s", calls.Load(), invalidOutput)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 2 || receipts[0].Outcome != "denied" || receipts[1].Outcome != "error" {
		t.Fatalf("unexpected receipts: %#v", receipts)
	}
}

func TestSealedPlanGeneratesPythonProjectionAndDefensiveSpecs(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(testSpec(), basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	prelude := plan.PythonPrelude()
	for _, fragment := range []string{
		"def read_text(path):",
		`_capability_call("workspace.read_text", {"path": path})["content"]`,
	} {
		if !strings.Contains(prelude, fragment) {
			t.Fatalf("generated prelude missing %q:\n%s", fragment, prelude)
		}
	}
	specs := plan.Specs()
	specs[0].Python.Arguments[0] = "mutated"
	specs[0].InputSchema[0] = 'x'
	fresh := plan.Specs()[0]
	if fresh.Python.Arguments[0] != "path" || !json.Valid(fresh.InputSchema) {
		t.Fatalf("Plan.Specs leaked mutable state: %#v", fresh)
	}
}

func testSpec() capability.Spec {
	return capability.Spec{
		Name:            "workspace.read_text",
		Version:         "pysolate.workspace.read-text.v1",
		Description:     "Read one text file from the typed workspace.",
		EffectClass:     capability.EffectWorkspaceRead,
		Playback:        capability.PlaybackLiveOnly,
		HandlerIdentity: "pysolate.workspace-text.v1",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`),
		Python: &capability.PythonProjection{
			Name:        "read_text",
			Arguments:   []string{"path"},
			ResultField: "content",
		},
	}
}
