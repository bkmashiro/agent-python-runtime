package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/approval"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestApprovalRequiredCapabilityDispatchesOnlyAfterSameBrokerApproval(t *testing.T) {
	var calls atomic.Uint32
	plan := approvalPlan(t, &calls)
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "execution-1", Plan: plan}); !errors.Is(err, capability.ErrInvalidBroker) {
		t.Fatalf("approval-required plan without controller error=%v", err)
	}
	controller := approval.NewController()
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "execution-1", Plan: plan, ApprovalSuspension: true, ApprovalController: controller})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan []byte, 1)
	go func() {
		response, _ := broker.Call(context.Background(), []byte(`{"call_id":"delete-1","capability":"danger.delete","arguments":{"resource":"db"}}`))
		result <- response
	}()
	request := waitForBrokerApproval(t, controller)
	if calls.Load() != 0 || request.RunID != "execution-1" || request.CallID != "delete-1" || request.Capability != "danger.delete" || request.ArgumentsSHA256 == "" {
		t.Fatalf("request=%+v calls=%d", request, calls.Load())
	}
	if err := controller.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	response := <-result
	if calls.Load() != 1 || !strings.Contains(string(response), `"deleted":true`) {
		t.Fatalf("response=%s calls=%d", response, calls.Load())
	}
	records := controller.Snapshot()
	if len(records) != 1 || !records[0].Executed || records[0].DispatchOutcome != "ok" {
		t.Fatalf("records=%#v", records)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 1 || receipts[0].ApprovalRequestID != records[0].RequestID || receipts[0].CallID != records[0].CallID || receipts[0].CapabilityPlanSHA256 != records[0].PlanSHA256 {
		t.Fatalf("receipt/audit binding receipts=%#v records=%#v", receipts, records)
	}
}

func TestApprovalRejectionExpiryAndCancellationNeverDispatch(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*approval.Controller, approval.Request, context.CancelFunc)
		code string
	}{
		{"reject", func(controller *approval.Controller, request approval.Request, _ context.CancelFunc) {
			_ = controller.Reject(request.RequestID)
		}, "approval_rejected"},
		{"expire", func(*approval.Controller, approval.Request, context.CancelFunc) {}, "approval_expired"},
		{"cancel", func(_ *approval.Controller, _ approval.Request, cancel context.CancelFunc) { cancel() }, "approval_cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Uint32
			plan := approvalPlan(t, &calls)
			controller := approval.NewController()
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "execution-1", Plan: plan, ApprovalSuspension: true, ApprovalController: controller})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan []byte, 1)
			go func() {
				response, _ := broker.Call(ctx, []byte(`{"call_id":"delete-1","capability":"danger.delete","arguments":{"resource":"db"}}`))
				result <- response
			}()
			request := waitForBrokerApproval(t, controller)
			test.act(controller, request, cancel)
			response := <-result
			if calls.Load() != 0 || !containsCode(response, test.code) {
				t.Fatalf("response=%s calls=%d", response, calls.Load())
			}
		})
	}
}

func approvalPlan(t *testing.T, calls *atomic.Uint32) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	spec := basicSpec("danger.delete", "test.danger-delete.v1")
	spec.InputSchema = json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"}},"required":["resource"],"additionalProperties":false}`)
	spec.OutputSchema = json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`)
	spec.Approval = &capability.ApprovalRequirement{Mode: capability.ApprovalLease, LeaseMilliseconds: 15}
	if err := registry.Register(spec, basicGrant(t), capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"deleted":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func waitForBrokerApproval(t *testing.T, controller *approval.Controller) approval.Request {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if records := controller.Snapshot(); len(records) == 1 {
			return records[0].Request
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("approval request not published; digest=%s", strings.Repeat("x", 1))
	return approval.Request{}
}
