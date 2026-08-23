package compensation

import (
	"context"
	"errors"
	"testing"
)

func TestDifferentPlanCannotCompensateSameEffectTwice(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} }}
	controller := testController(t, provider, []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
	}}})
	receipt := effect("effect-1", "goal-1", "tool.create", 0, "target-1", "v1")
	validated, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	planOnly, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunPlan, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Execute(context.Background(), validated, agentReview(validated)); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Execute(context.Background(), planOnly, agentReview(planOnly)); !errors.Is(err, ErrAlreadyCompensated) {
		t.Fatalf("second plan error=%v", err)
	}
	if _, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}}); !errors.Is(err, ErrAlreadyCompensated) {
		t.Fatalf("post-compensation preview error=%v", err)
	}
	if provider.executionCalls != 1 {
		t.Fatalf("provider calls=%d", provider.executionCalls)
	}
}

func TestSameEffectIDInDifferentRunsDoesNotCollide(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} }}
	controller := testController(t, provider, []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
	}}})
	for index, runID := range []string{"run-1", "run-2"} {
		receipt := effect("effect-1", "goal-"+runID, "tool.create", 0, "target-"+runID, "v1")
		receipt.RunID = runID
		plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: receipt.EffectGroupID, Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := controller.Execute(context.Background(), plan, agentReview(plan)); err != nil {
			t.Fatalf("run %d: %v", index+1, err)
		}
	}
	if provider.executionCalls != 2 {
		t.Fatalf("provider calls=%d", provider.executionCalls)
	}
}
