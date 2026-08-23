package compensation

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestPreviewRejectsExcessDependenciesBeforeValidation(t *testing.T) {
	p := &fakeProvider{}
	c := testController(t, p, []ToolContract{{
		Capability: "tool.create",
		Strategies: []Strategy{executable("delete", SemanticsExact, 1, "tool.delete", ApprovalAgentReview)},
	}})
	r := effect("effect-1", "goal-1", "tool.create", 0, "target-1", "v1")
	for i := 0; i <= maxDependenciesPerEffect; i++ {
		r.DependsOn = append(r.DependsOn, fmt.Sprintf("dependency-%d", i))
	}
	_, err := c.Preview(context.Background(), PreviewRequest{
		EffectGroupID: "goal-1",
		Mode:          DryRunValidate,
		Receipts:      []EffectReceipt{r},
	})
	if !errors.Is(err, ErrInvalidRequest) || p.validationCalls != 0 {
		t.Fatalf("error=%v validation_calls=%d", err, p.validationCalls)
	}
}
