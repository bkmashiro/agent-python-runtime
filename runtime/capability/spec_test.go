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
		func(spec *capability.Spec) {
			spec.EffectClass = capability.EffectExternalRead
			spec.ReadOnly = true
			spec.Idempotent = true
			spec.PreDispatch = preDispatchArgument("path")
		},
	}
	for index, mutate := range mutations {
		registry := capability.NewRegistry()
		spec := testSpec()
		mutate(&spec)
		var handler capability.Handler = noopHandler
		if spec.Playback == capability.PlaybackCaptured {
			handler = evidenceHandler{}
		}
		if err := registry.Register(spec, basicGrant(t), handler); err != nil {
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

func TestPreDispatchV1LimitsBindPlanIdentity(t *testing.T) {
	identity := func(maxResultBytes uint64, costUnits uint32) string {
		t.Helper()
		registry := capability.NewRegistry()
		spec := testSpec()
		spec.EffectClass = capability.EffectExternalRead
		spec.ReadOnly, spec.Idempotent = true, true
		spec.PreDispatch = preDispatchArgument("path")
		spec.PreDispatch.MaxResultBytes = maxResultBytes
		spec.PreDispatch.CostUnits = costUnits
		if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
			t.Fatal(err)
		}
		plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
		if err != nil {
			t.Fatal(err)
		}
		return plan.Identity()
	}
	base := identity(1024, 1)
	if base == identity(2048, 1) || base == identity(1024, 2) {
		t.Fatal("pre-dispatch v1 limit did not change Plan identity")
	}
}

func TestCapabilityPlanIdentityBindsApprovalLease(t *testing.T) {
	identity := func(lease uint64) string {
		registry := capability.NewRegistry()
		spec := testSpec()
		spec.Approval = &capability.ApprovalRequirement{Mode: capability.ApprovalLease, LeaseMilliseconds: lease}
		if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
			t.Fatal(err)
		}
		plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
		if err != nil {
			t.Fatal(err)
		}
		return plan.Identity()
	}
	if identity(1000) == identity(2000) {
		t.Fatal("approval lease did not change Plan identity")
	}
}

func TestCapabilityPlanIdentityBindsPreDispatchContract(t *testing.T) {
	identity := func(namespace string) string {
		registry := capability.NewRegistry()
		spec := testSpec()
		spec.ReadOnly, spec.Idempotent, spec.PreDispatch = true, true, preDispatchArgument("path")
		spec.PreDispatch.Resource.Namespace = namespace
		if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
			t.Fatal(err)
		}
		plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
		if err != nil {
			t.Fatal(err)
		}
		return plan.Identity()
	}
	if identity("workspace") == identity("repository") {
		t.Fatal("resource contract did not affect capability plan identity")
	}
}

func TestCompareDelegationRequiresExactSubsetAndBoundedCalls(t *testing.T) {
	parent := sealedPlan(t, 4, testSpec(), basicGrant(t))
	child := sealedPlan(t, 2, testSpec(), basicGrant(t))
	decision := capability.CompareDelegation(parent, child)
	if !decision.Allowed || decision.Reason != capability.DelegationAllowed || decision.ReservedCalls != 2 {
		t.Fatalf("valid subset decision=%+v", decision)
	}

	widerCalls := sealedPlan(t, 5, testSpec(), basicGrant(t))
	if decision := capability.CompareDelegation(parent, widerCalls); decision.Allowed || decision.Reason != capability.DelegationCallsWidened {
		t.Fatalf("wider calls decision=%+v", decision)
	}

	extra := testSpec()
	extra.Name = "workspace.read_other"
	extra.Version = "pysolate.workspace.read-other.v1"
	extra.HandlerIdentity = "pysolate.workspace-other.v1"
	extra.Python.Method = "read_other"
	extra.Python.GlobalAlias = "read_other"
	additionalCapability := sealedPlanWithSpecs(t, 2, []capability.Spec{testSpec(), extra}, []capability.Grant{basicGrant(t), basicGrant(t)})
	if decision := capability.CompareDelegation(parent, additionalCapability); decision.Allowed || decision.Reason != capability.DelegationCapabilityWidened {
		t.Fatalf("additional capability decision=%+v", decision)
	}

	changedSpec := testSpec()
	changedSpec.Description = "A different Host-owned contract."
	if decision := capability.CompareDelegation(parent, sealedPlan(t, 2, changedSpec, basicGrant(t))); decision.Allowed || decision.Reason != capability.DelegationSpecMismatch {
		t.Fatalf("changed spec decision=%+v", decision)
	}

	changedGrant, err := capability.NewGrant(json.RawMessage(`{"root":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	if decision := capability.CompareDelegation(parent, sealedPlan(t, 2, testSpec(), changedGrant)); decision.Allowed || decision.Reason != capability.DelegationGrantMismatch {
		t.Fatalf("changed grant decision=%+v", decision)
	}
}

func TestCapabilitySpecRequiresHostQualifiedPreDispatchConjunction(t *testing.T) {
	valid := testSpec()
	valid.EffectClass = capability.EffectExternalRead
	valid.ReadOnly = true
	valid.Idempotent = true
	valid.PreDispatch = preDispatchArgument("path")
	registry := capability.NewRegistry()
	if err := registry.Register(valid, basicGrant(t), noopHandler); err != nil {
		t.Fatalf("qualified speculative read rejected: %v", err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	qualification, ok := plan.PreDispatch(valid.Name)
	if !ok || !qualification.Eligible() {
		t.Fatalf("qualification=%+v ok=%v", qualification, ok)
	}

	for name, mutate := range map[string]func(*capability.Spec){
		"not read only":                 func(spec *capability.Spec) { spec.ReadOnly = false },
		"not idempotent":                func(spec *capability.Spec) { spec.Idempotent = false },
		"missing pre-dispatch contract": func(spec *capability.Spec) { spec.PreDispatch = nil },
		"workspace write":               func(spec *capability.Spec) { spec.EffectClass = capability.EffectWorkspaceWrite },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			registry := capability.NewRegistry()
			if err := registry.Register(candidate, basicGrant(t), noopHandler); err != capability.ErrInvalidTool {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCapabilitySpecRejectsIncompletePreDispatchContracts(t *testing.T) {
	mutations := map[string]func(*capability.Spec){
		"freshness":         func(spec *capability.Spec) { spec.PreDispatch.Freshness = "latest" },
		"unclaimed":         func(spec *capability.Spec) { spec.PreDispatch.Unclaimed = "ignore" },
		"privacy absent":    func(spec *capability.Spec) { spec.PreDispatch.Privacy = "" },
		"privacy unknown":   func(spec *capability.Spec) { spec.PreDispatch.Privacy = "global" },
		"coalescing absent": func(spec *capability.Spec) { spec.PreDispatch.Coalescing = "" },
		"coalescing inferred": func(spec *capability.Spec) {
			spec.PreDispatch.Coalescing = "infer_from_idempotency"
		},
		"result budget absent": func(spec *capability.Spec) { spec.PreDispatch.MaxResultBytes = 0 },
		"result budget too large": func(spec *capability.Spec) {
			spec.PreDispatch.MaxResultBytes = (1 << 20) + 1
		},
		"cost absent":        func(spec *capability.Spec) { spec.PreDispatch.CostUnits = 0 },
		"namespace":          func(spec *capability.Spec) { spec.PreDispatch.Resource.Namespace = "" },
		"missing key":        func(spec *capability.Spec) { spec.PreDispatch.Resource.Argument = "" },
		"two keys":           func(spec *capability.Spec) { spec.PreDispatch.Resource.Constant = "fixed" },
		"unknown argument":   func(spec *capability.Spec) { spec.PreDispatch.Resource.Argument = "missing" },
		"missing projection": func(spec *capability.Spec) { spec.Python = nil },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			spec := testSpec()
			spec.ReadOnly, spec.Idempotent, spec.PreDispatch = true, true, preDispatchArgument("path")
			mutate(&spec)
			if err := capability.NewRegistry().Register(spec, basicGrant(t), noopHandler); err != capability.ErrInvalidTool {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestStreamingObservationBindingIsPlanAndGrantBound(t *testing.T) {
	registry := capability.NewRegistry()
	spec := testSpec()
	spec.ReadOnly, spec.Idempotent, spec.PreDispatch = true, true, preDispatchArgument("path")
	if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := plan.StreamingObservationBinding(spec.Name)
	if !ok || binding.Capability != spec.Name || binding.HandlerIdentity != spec.HandlerIdentity || binding.PlanSHA256 != plan.Identity() || binding.SpecSHA256 == "" || binding.GrantPolicySHA256 == "" {
		t.Fatalf("binding = %+v ok=%v", binding, ok)
	}
	if _, ok := plan.StreamingObservationBinding("missing"); ok {
		t.Fatal("missing capability received binding")
	}
	if strings.Contains(plan.StreamingPythonPrelude(), `_stream_eager_calls[`) {
		t.Fatal("capability metadata alone activated legacy eager dispatch")
	}
}

func TestStreamingPythonPreludeExcludesWriteCapabilities(t *testing.T) {
	registry := capability.NewRegistry()
	read := testSpec()
	read.Name = "fixture.read"
	read.Version = "fixture.read.v1"
	read.Description = "read"
	read.EffectClass = capability.EffectExternalRead
	read.Python = &capability.PythonProjection{Module: "fixture", Method: "read", Arguments: []string{"path"}}
	write := read
	write.Name = "fixture.write"
	write.Version = "fixture.write.v1"
	write.Description = "write"
	write.EffectClass = capability.EffectWorkspaceWrite
	write.Python = &capability.PythonProjection{Module: "fixture", Method: "write", Arguments: []string{"path"}}
	for _, spec := range []capability.Spec{read, write} {
		if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	streamingPrelude := plan.StreamingPythonPrelude()
	if !strings.Contains(streamingPrelude, "fixture.read") || strings.Contains(streamingPrelude, "fixture.write") {
		t.Fatalf("unsafe streaming projection: %s", streamingPrelude)
	}
	if !strings.Contains(plan.PythonPrelude(), "fixture.write") {
		t.Fatal("final plan lost write capability")
	}
}

func TestCapabilitySpecRejectsInvalidSchemaAndProjection(t *testing.T) {
	for name, mutate := range map[string]func(*capability.Spec){
		"invalid input schema": func(spec *capability.Spec) { spec.InputSchema = json.RawMessage(`{"type":`) },
		"external schema ref": func(spec *capability.Spec) {
			spec.InputSchema = json.RawMessage(`{"$ref":"https://example.test/schema.json"}`)
		},
		"invalid Python name":        func(spec *capability.Spec) { spec.Python.Method = "not-valid" },
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
		"Python builtin":             func(spec *capability.Spec) { spec.Python.GlobalAlias = "len" },
		"Guest result name":          func(spec *capability.Spec) { spec.Python.GlobalAlias = "result" },
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
	spec := testSpec()
	spec.ReadOnly, spec.Idempotent, spec.PreDispatch = true, true, preDispatchArgument("path")
	if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	prelude := plan.PythonPrelude()
	for _, fragment := range []string{
		"def _capability_proxy_0(path):",
		`_capability_call("workspace.read_text", {"path": path})["content"]`,
		"workspace.read_text = _capability_proxy_0",
		"read_text = workspace.read_text",
	} {
		if !strings.Contains(prelude, fragment) {
			t.Fatalf("generated prelude missing %q:\n%s", fragment, prelude)
		}
	}
	specs := plan.Specs()
	specs[0].Python.Arguments[0] = "mutated"
	specs[0].InputSchema[0] = 'x'
	specs[0].PreDispatch.Resource.Namespace = "mutated"
	fresh := plan.Specs()[0]
	if fresh.Python.Arguments[0] != "path" || !json.Valid(fresh.InputSchema) || fresh.PreDispatch.Resource.Namespace != "workspace" {
		t.Fatalf("Plan.Specs leaked mutable state: %#v", fresh)
	}
	projections := plan.PreDispatchPythonProjections()
	if len(projections) != 1 || projections[0].Module != "workspace" || projections[0].Method != "read_text" {
		t.Fatalf("pre-dispatch projections=%#v", projections)
	}
	projections[0].Arguments[0] = "mutated"
	if freshProjection := plan.PreDispatchPythonProjections()[0]; freshProjection.Arguments[0] != "path" {
		t.Fatalf("pre-dispatch projection leaked mutable state: %#v", freshProjection)
	}
}

func sealedPlan(t *testing.T, maxCalls uint32, spec capability.Spec, grant capability.Grant) *capability.Plan {
	t.Helper()
	return sealedPlanWithSpecs(t, maxCalls, []capability.Spec{spec}, []capability.Grant{grant})
}

func sealedPlanWithSpecs(t *testing.T, maxCalls uint32, specs []capability.Spec, grants []capability.Grant) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	for index, spec := range specs {
		if err := registry.Register(spec, grants[index], noopHandler); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func preDispatchArgument(argument string) *capability.PreDispatchContract {
	return &capability.PreDispatchContract{
		Resource:       capability.ResourceReference{Namespace: "workspace", Argument: argument},
		Freshness:      capability.FreshnessPlanEpoch,
		Unclaimed:      capability.UnclaimedDiscardWithDisposition,
		Privacy:        capability.PreDispatchPrivacyExactPartition,
		Coalescing:     capability.PreDispatchCoalescingForbidden,
		MaxResultBytes: 1 << 20,
		CostUnits:      1,
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
			Module:      "workspace",
			Method:      "read_text",
			GlobalAlias: "read_text",
			Arguments:   []string{"path"},
			ResultField: "content",
		},
	}
}
