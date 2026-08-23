package compensation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestConcurrentPlansReserveJournalCapacityBeforeProviderMutation(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	controller, err := NewController(Config{
		Contracts: []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
			executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
		}}},
		Validator: provider, Executor: provider, Authorizer: provider, MaxPlans: 2, MaxRecords: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{
		effect("effect-1", "goal-1", "tool.create", 0, "provider:object-1", "v1"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-2", Mode: DryRunValidate, Receipts: []EffectReceipt{
		effect("effect-2", "goal-2", "tool.create", 0, "provider:object-2", "v1"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	type execution struct {
		result ExecutionResult
		err    error
	}
	completed := make(chan execution, 1)
	go func() {
		result, executeErr := controller.Execute(context.Background(), first, agentReview(first))
		completed <- execution{result: result, err: executeErr}
	}()
	<-provider.entered
	_, secondErr := controller.Execute(context.Background(), second, agentReview(second))
	close(provider.release)
	firstExecution := <-completed
	if !errors.Is(secondErr, ErrJournalCapacity) || firstExecution.err != nil || firstExecution.result.Status != ExecutionCompensated {
		t.Fatalf("first=%+v second_error=%v", firstExecution, secondErr)
	}
	if provider.executions.Load() != 1 || len(controller.Snapshot()) != 1 {
		t.Fatalf("executions=%d journal=%+v", provider.executions.Load(), controller.Snapshot())
	}
}

type blockingProvider struct {
	entered    chan struct{}
	release    chan struct{}
	executions atomic.Int32
}

func (*blockingProvider) AuthorizeCompensation(context.Context, Plan, Review) error {
	return nil
}

func (*blockingProvider) Validate(context.Context, EffectReceipt, Strategy) (Validation, error) {
	return Validation{Applicable: true}, nil
}

func (provider *blockingProvider) Execute(context.Context, EffectReceipt, Strategy) (ProviderResult, error) {
	if provider.executions.Add(1) == 1 {
		close(provider.entered)
	}
	<-provider.release
	return ProviderResult{Outcome: ProviderCompensated, ReceiptSHA256: digest('f')}, nil
}
