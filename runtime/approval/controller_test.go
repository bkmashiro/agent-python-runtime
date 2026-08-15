package approval_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/approval"
)

func TestControllerApprovesExactlyOnceAndKeepsBodySafeAudit(t *testing.T) {
	controller := approval.NewController()
	proposal := approval.Proposal{
		RunID: "execution-1", PlanSHA256: "sha256:" + strings.Repeat("a", 64),
		CallID: "parent:program:1", ParentCallID: "parent", Capability: "danger.delete",
		Arguments: []byte(`{"secret":"do-not-store"}`), Lease: time.Second,
	}
	result := make(chan error, 1)
	go func() {
		permit, err := controller.Authorize(context.Background(), proposal)
		if err == nil {
			err = controller.Complete(permit.RequestID, "ok")
		}
		result <- err
	}()
	request := waitForRequest(t, controller)
	if err := controller.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := controller.Approve(request.RequestID); !errors.Is(err, approval.ErrDecisionFinal) {
		t.Fatalf("duplicate decision error = %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	records := controller.Snapshot()
	if len(records) != 1 || records[0].Status != approval.StatusApproved || !records[0].Executed || records[0].DispatchOutcome != "ok" || records[0].ParentCallID != "parent" {
		t.Fatalf("records=%#v", records)
	}
	if strings.Contains(records[0].ArgumentsSHA256, "do-not-store") {
		t.Fatalf("audit leaked arguments: %#v", records[0])
	}
}

func TestApprovedPermitCanBeAbortedBeforeDispatchWithoutExecution(t *testing.T) {
	controller := approval.NewController()
	result := make(chan error, 1)
	go func() {
		_, err := controller.Authorize(context.Background(), approval.Proposal{RunID: "execution-1", PlanSHA256: "sha256:" + strings.Repeat("a", 64), CallID: "call-1", Capability: "danger.delete", Arguments: []byte(`{}`), Lease: 200 * time.Millisecond})
		result <- err
	}()
	request := waitForRequest(t, controller)
	if err := controller.Approve(request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := controller.AbortApproved(request.RequestID, "cancelled_before_dispatch"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Complete(request.RequestID, "ok"); !errors.Is(err, approval.ErrNotApproved) {
		t.Fatalf("aborted permit completed: %v", err)
	}
	record := controller.Snapshot()[0]
	if record.Executed || record.DispatchOutcome != "cancelled_before_dispatch" || record.CompletedAt == nil {
		t.Fatalf("record=%+v", record)
	}
}

func TestControllerRejectsExpiresAndCancelsWithoutPermit(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*approval.Controller, approval.Request, context.CancelFunc) error
		want error
	}{
		{"reject", func(controller *approval.Controller, request approval.Request, _ context.CancelFunc) error {
			return controller.Reject(request.RequestID)
		}, approval.ErrRejected},
		{"expire", func(_ *approval.Controller, _ approval.Request, _ context.CancelFunc) error { return nil }, approval.ErrExpired},
		{"cancel", func(_ *approval.Controller, _ approval.Request, cancel context.CancelFunc) error {
			cancel()
			return nil
		}, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := approval.NewController()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			lease := time.Second
			if test.name == "expire" {
				lease = 15 * time.Millisecond
			}
			go func() {
				_, err := controller.Authorize(ctx, approval.Proposal{RunID: "execution-1", PlanSHA256: "sha256:" + strings.Repeat("a", 64), CallID: "call-1", Capability: "danger.delete", Arguments: []byte(`{}`), Lease: lease})
				result <- err
			}()
			request := waitForRequest(t, controller)
			if err := test.act(controller, request, cancel); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, test.want) {
				t.Fatalf("Authorize error=%v want=%v", err, test.want)
			}
			if err := controller.Approve(request.RequestID); !errors.Is(err, approval.ErrDecisionFinal) {
				t.Fatalf("late approval error=%v", err)
			}
			records := controller.Snapshot()
			if len(records) != 1 || records[0].Executed {
				t.Fatalf("records=%#v", records)
			}
		})
	}
}

func waitForRequest(t *testing.T, controller *approval.Controller) approval.Request {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if records := controller.Snapshot(); len(records) == 1 {
			return records[0].Request
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval request was not published")
	return approval.Request{}
}
