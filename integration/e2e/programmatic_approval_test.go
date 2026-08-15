package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/approval"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestProgrammaticSurfaceUsesSameBrokerForOrderedRealGuestCalls(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	plan := programmaticPlan(t, nil, &calls, 2)

	directBroker, err := capability.NewBroker(capability.Config{RunIdentity: "direct-execution", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := directBroker.Call(context.Background(), []byte(`{"call_id":"direct-1","capability":"tools.increment","arguments":{"value":1}}`))
	if err != nil || !strings.Contains(string(direct), `"value":2`) {
		t.Fatalf("direct=%s err=%v", direct, err)
	}

	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, "program-parent")
	if err != nil {
		t.Fatal(err)
	}
	var programBroker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "program-execution", Plan: plan, ProgrammaticParentCallID: "program-parent"})
		programBroker = created
		return created, createErr
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request := []byte(`{"run_id":"programmatic-real-guest","code":"first = tools.increment(1)\nsecond = tools.increment(first)\nresult = {'first': first, 'second': second}","inputs":{}}`)
	payload, err := runner.Run(context.Background(), request, presentation.PythonPrelude)
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	if string(response.Result) != `{"first":2,"second":3}` || response.CapabilityPlanSHA256 == nil || *response.CapabilityPlanSHA256 != plan.Identity() {
		t.Fatalf("response=%+v payload=%s", response, payload)
	}
	receipts := programBroker.SnapshotReceipts()
	if len(receipts) != 2 || receipts[0].CallID != "program-parent:program:1" || receipts[1].CallID != "program-parent:program:2" ||
		receipts[0].ParentCallID != "program-parent" || receipts[1].ParentCallID != "program-parent" || calls.Load() != 3 {
		t.Fatalf("receipts=%#v total handler calls=%d", receipts, calls.Load())
	}
}

func TestHotApprovalResumesSameRealGuestWithoutReplay(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	controller := approval.NewController()
	var calls atomic.Uint32
	plan := programmaticPlan(t, &capability.ApprovalRequirement{Mode: capability.ApprovalLease, LeaseMilliseconds: 2000}, &calls, 1)
	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, "approval-parent")
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	config.Mechanisms.ApprovalSuspension = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: "approval-execution", Plan: plan, ProgrammaticParentCallID: "approval-parent",
			ApprovalSuspension: true, ApprovalController: controller,
		})
		broker = created
		return created, createErr
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"approval-real-guest","code":"before = 41\napproved = tools.increment(before)\nresult = {'before': before, 'after': approved, 'continued': before + 1}","inputs":{}}`)
	type runResult struct {
		payload []byte
		err     error
	}
	finished := make(chan runResult, 1)
	go func() {
		payload, runErr := runner.Run(context.Background(), request, presentation.PythonPrelude)
		finished <- runResult{payload: payload, err: runErr}
	}()
	pending := waitForE2EApproval(t, controller)
	if calls.Load() != 0 || pending.ParentCallID != "approval-parent" || pending.CallID != "approval-parent:program:1" {
		t.Fatalf("pending=%+v calls=%d", pending, calls.Load())
	}
	if err := controller.Approve(pending.RequestID); err != nil {
		t.Fatal(err)
	}
	result := <-finished
	if result.err != nil {
		t.Fatal(result.err)
	}
	response := decodeRealGuestResponse(t, request, result.payload)
	if string(response.Result) != `{"after":42,"before":41,"continued":42}` || calls.Load() != 1 || broker.CallCount() != 1 {
		t.Fatalf("response=%+v calls=%d payload=%s", response, calls.Load(), result.payload)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, receipts := controller.Snapshot(), broker.SnapshotReceipts()
	if len(records) != 1 || len(receipts) != 1 || !records[0].Executed || records[0].DispatchOutcome != "ok" ||
		receipts[0].ApprovalRequestID != records[0].RequestID || receipts[0].ParentCallID != records[0].ParentCallID {
		t.Fatalf("records=%#v receipts=%#v", records, receipts)
	}
}

func TestRealGuestApprovalRejectExpireAndCancelDoNotDispatch(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		lease uint64
		act   func(*approval.Controller, approval.Request, context.CancelFunc)
	}{
		{"reject", 2000, func(controller *approval.Controller, request approval.Request, _ context.CancelFunc) {
			_ = controller.Reject(request.RequestID)
		}},
		{"expire", 20, func(*approval.Controller, approval.Request, context.CancelFunc) {}},
		{"cancel", 2000, func(_ *approval.Controller, _ approval.Request, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := approval.NewController()
			var calls atomic.Uint32
			plan := programmaticPlan(t, &capability.ApprovalRequirement{Mode: capability.ApprovalLease, LeaseMilliseconds: test.lease}, &calls, 1)
			presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, "terminal-parent")
			if err != nil {
				t.Fatal(err)
			}
			config := runtimeconfig.DefaultRunConfig()
			config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
			config.Mechanisms.ProgrammaticToolCalling = true
			config.Mechanisms.ApprovalSuspension = true
			runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
				return capability.NewBroker(capability.Config{RunIdentity: "terminal-execution", Plan: plan, ProgrammaticParentCallID: "terminal-parent", ApprovalSuspension: true, ApprovalController: controller})
			}}).New(context.Background(), wasm, config)
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := []byte(`{"run_id":"approval-terminal-real-guest","code":"result = tools.increment(1)","inputs":{}}`)
			done := make(chan error, 1)
			go func() { _, runErr := runner.Run(ctx, request, presentation.PythonPrelude); done <- runErr }()
			pending := waitForE2EApproval(t, controller)
			test.act(controller, pending, cancel)
			runErr := <-done
			if test.name == "cancel" && runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Fatalf("cancel error=%v", runErr)
			}
			if calls.Load() != 0 {
				t.Fatalf("handler dispatched %d times", calls.Load())
			}
			records := controller.Snapshot()
			if len(records) != 1 || records[0].Executed {
				t.Fatalf("records=%#v", records)
			}
		})
	}
}

func programmaticPlan(t *testing.T, requirement *capability.ApprovalRequirement, calls *atomic.Uint32, maxCalls uint32) *capability.Plan {
	t.Helper()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"increment"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "tools.increment", Version: "pysolate.tools.increment.v1", Description: "Increment one integer.",
		EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.tools.increment.handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "tools", Method: "increment", Arguments: []string{"value"}, ResultField: "value"}, Approval: requirement,
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		var input struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]int{"value": input.Value + 1})
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func decodeRealGuestResponse(t *testing.T, request, payload []byte) runtimeconfig.RunResponse {
	t.Helper()
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decoded, payload)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func waitForE2EApproval(t *testing.T, controller *approval.Controller) approval.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, record := range controller.Snapshot() {
			if record.Status == approval.StatusWaiting {
				return record.Request
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval request was not observed")
	return approval.Request{}
}
