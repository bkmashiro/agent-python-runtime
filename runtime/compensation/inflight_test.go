package compensation

import (
	"context"
	"errors"
	"testing"
)

func TestDifferentPlansCannotCompensateSameEffectConcurrently(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	controller, err := NewController(Config{
		Contracts: []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
			executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
		}}},
		Validator: provider, Executor: provider, Authorizer: provider, MaxPlans: 2, MaxRecords: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := effect("effect-1", "goal-1", "tool.create", 0, "provider:object-1", "v1")
	validated, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	planOnly, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunPlan, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, executeErr := controller.Execute(context.Background(), validated, agentReview(validated))
		completed <- executeErr
	}()
	<-provider.entered
	_, previewErr := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}})
	_, secondErr := controller.Execute(context.Background(), planOnly, agentReview(planOnly))
	close(provider.release)
	firstErr := <-completed
	if firstErr != nil || !errors.Is(previewErr, ErrEffectInProgress) || !errors.Is(secondErr, ErrEffectInProgress) {
		t.Fatalf("first_error=%v preview_error=%v second_error=%v", firstErr, previewErr, secondErr)
	}
	if provider.executions.Load() != 1 {
		t.Fatalf("provider executions=%d", provider.executions.Load())
	}
}
