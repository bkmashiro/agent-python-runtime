package compensation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidatePreviewSelectsStrongestApplicableFallbackAndRendersInversePython(t *testing.T) {
	provider := &fakeProvider{version: "v2"}
	exact := executable("delete_if_unchanged", SemanticsExact, 100, "calendar.delete_event", ApprovalAgentReview)
	exact.Precondition = "The event version must still equal the original applied version."
	compensating := executable("cancel_and_notify", SemanticsCompensating, 60, "calendar.cancel_event", ApprovalUserRequired)
	compensating.Precondition = "The event must still exist."
	compensating.Risk = "Attendees may receive a new cancellation notification."
	controller := testController(t, provider, []ToolContract{{
		Capability: "calendar.create_event",
		Strategies: []Strategy{
			exact,
			compensating,
			guidance("manual_resolution", 10, "Review attendee changes before cancelling the event."),
		},
	}})
	provider.validate = func(_ EffectReceipt, strategy Strategy) Validation {
		if strategy.ID == "delete_if_unchanged" {
			return Validation{Applicable: false, Reason: "version drift", CurrentVersion: provider.version}
		}
		return Validation{Applicable: true, CurrentVersion: provider.version}
	}

	plan, err := controller.Preview(context.Background(), PreviewRequest{
		EffectGroupID: "goal-42", Mode: DryRunValidate,
		Receipts: []EffectReceipt{effect("effect-1", "goal-42", "calendar.create_event", 0, "event-1", "v1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != PlanSchemaVersion || !strings.HasPrefix(plan.Identity, "sha256:") || len(plan.Steps) != 1 {
		t.Fatalf("plan=%+v", plan)
	}
	step := plan.Steps[0]
	if step.StrategyID != "cancel_and_notify" || step.Semantics != SemanticsCompensating || step.Approval != ApprovalUserRequired || step.ValidationStatus != ValidationApplicable {
		t.Fatalf("step=%+v", step)
	}
	if step.Precondition != compensating.Precondition || step.Risk != compensating.Risk {
		t.Fatalf("review metadata=%+v", step)
	}
	if len(step.Rejections) != 1 || step.Rejections[0].StrategyID != "delete_if_unchanged" || step.Rejections[0].Reason != "version drift" {
		t.Fatalf("rejections=%+v", step.Rejections)
	}
	if !strings.Contains(plan.InversePython, `{"effect": "effect-1", "strategy": "cancel_and_notify", "mode": "apply"}`) || strings.Contains(plan.InversePython, "event-1") {
		t.Fatalf("inverse Python=%q", plan.InversePython)
	}
}

func TestPlanOnlySkipsValidationAndChoosesHighestRankedStrategy(t *testing.T) {
	provider := &fakeProvider{}
	controller := testController(t, provider, []ToolContract{{Capability: "calendar.create_event", Strategies: []Strategy{
		executable("delete_if_unchanged", SemanticsExact, 100, "calendar.delete_event", ApprovalAgentReview),
		guidance("manual_resolution", 10, "Review manually."),
	}}})
	provider.validate = func(_ EffectReceipt, _ Strategy) Validation {
		provider.validationCalls++
		return Validation{Applicable: false, Reason: "must not run"}
	}
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunPlan, Receipts: []EffectReceipt{
		effect("effect-1", "goal-1", "calendar.create_event", 0, "event-1", "v1"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if provider.validationCalls != 0 || plan.Steps[0].StrategyID != "delete_if_unchanged" || plan.Steps[0].ValidationStatus != ValidationNotChecked {
		t.Fatalf("calls=%d step=%+v", provider.validationCalls, plan.Steps[0])
	}
}

func TestPreviewOrdersActualEffectsInReverseTopologicalOrder(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} }}
	contracts := []ToolContract{}
	for _, capability := range []string{"tool.a", "tool.b", "tool.c"} {
		contracts = append(contracts, ToolContract{Capability: capability, Strategies: []Strategy{
			executable("revert", SemanticsExact, 100, capability+".revert", ApprovalAgentReview),
		}})
	}
	controller := testController(t, provider, contracts)
	a := effect("effect-a", "goal-1", "tool.a", 0, "a", "v1")
	b := effect("effect-b", "goal-1", "tool.b", 1, "b", "v1")
	b.DependsOn = []string{"effect-a"}
	c := effect("effect-c", "goal-1", "tool.c", 2, "c", "v1")
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{b, a, c}})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{plan.Steps[0].EffectID, plan.Steps[1].EffectID, plan.Steps[2].EffectID}
	want := []string{"effect-c", "effect-b", "effect-a"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order=%v want=%v", got, want)
		}
	}
}

func TestExecuteRequiresExactReviewedPlanFreshAuthorityAndUserApproval(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation {
		return Validation{Applicable: true, CurrentVersion: "v1"}
	}}
	controller := testController(t, provider, []ToolContract{{Capability: "calendar.create_event", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "calendar.delete_event", ApprovalUserRequired),
	}}})
	plan := previewOne(t, controller, "calendar.create_event")

	mutated := plan
	mutated.Steps = append([]Step(nil), plan.Steps...)
	mutated.Steps[0].StrategyID = "forged"
	if _, err := controller.Execute(context.Background(), mutated, Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: digest('a'), UserApprovalSHA256: digest('b')}); !errors.Is(err, ErrReviewMismatch) {
		t.Fatalf("mutated plan error=%v", err)
	}
	sameRun := Review{PlanSHA256: plan.Identity, ReviewerRunID: "run-original-1", AuthoritySHA256: digest('a'), UserApprovalSHA256: digest('b')}
	if _, err := controller.Execute(context.Background(), plan, sameRun); !errors.Is(err, ErrReviewMismatch) {
		t.Fatalf("same-Run error=%v", err)
	}
	if _, err := controller.Execute(context.Background(), plan, Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: "old-authority"}); !errors.Is(err, ErrAuthorityRequired) {
		t.Fatalf("authority error=%v", err)
	}
	if _, err := controller.Execute(context.Background(), plan, Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: digest('a')}); !errors.Is(err, ErrUserApprovalRequired) {
		t.Fatalf("approval error=%v", err)
	}
	provider.denyAuthority = true
	if _, err := controller.Execute(context.Background(), plan, Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: digest('a'), UserApprovalSHA256: digest('b')}); !errors.Is(err, ErrAuthorityRequired) {
		t.Fatalf("Host authority error=%v", err)
	}
	provider.denyAuthority = false
	result, err := controller.Execute(context.Background(), plan, Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: digest('a'), UserApprovalSHA256: digest('b')})
	if err != nil || result.Status != ExecutionCompensated || provider.executionCalls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, provider.executionCalls)
	}
	if result.Receipts[0].OriginalRunID != "run-original-1" || result.Receipts[0].ReviewerRunID != "undo-run-1" || result.Receipts[0].AuthoritySHA256 != digest('a') || result.Receipts[0].UserApprovalSHA256 != digest('b') {
		t.Fatalf("receipt authority binding=%+v", result.Receipts[0])
	}
}

func TestExecuteRevalidatesAllStepsBeforeMutationAndRejectsStalePlan(t *testing.T) {
	provider := &fakeProvider{version: "v1"}
	provider.validate = func(receipt EffectReceipt, _ Strategy) Validation {
		return Validation{Applicable: provider.version == receipt.AfterVersion, Reason: "version drift", CurrentVersion: provider.version}
	}
	controller := testController(t, provider, []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
	}}})
	plan := previewOne(t, controller, "tool.create")
	provider.version = "v2"
	if _, err := controller.Execute(context.Background(), plan, agentReview(plan)); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("stale error=%v", err)
	}
	if provider.executionCalls != 0 || len(controller.Snapshot()) != 0 {
		t.Fatalf("calls=%d journal=%+v", provider.executionCalls, controller.Snapshot())
	}
}

func TestExecuteRecordsPartialFailureAndReplayDoesNotMutateAgain(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} }, failEffect: "effect-a"}
	contracts := []ToolContract{}
	for _, capability := range []string{"tool.a", "tool.b"} {
		contracts = append(contracts, ToolContract{Capability: capability, Strategies: []Strategy{
			executable("revert", SemanticsExact, 100, capability+".revert", ApprovalAgentReview),
		}})
	}
	controller := testController(t, provider, contracts)
	a := effect("effect-a", "goal-1", "tool.a", 0, "a", "v1")
	b := effect("effect-b", "goal-1", "tool.b", 1, "b", "v1")
	b.DependsOn = []string{"effect-a"}
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.Execute(context.Background(), plan, agentReview(plan))
	if !errors.Is(err, ErrExecutionFailed) || result.Status != ExecutionPartiallyCompensated || provider.executionCalls != 2 || len(result.Receipts) != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, provider.executionCalls)
	}
	if result.Receipts[0].Compensates != "effect-b" || result.Receipts[0].Status != ReceiptCompensated || result.Receipts[1].Status != ReceiptFailed {
		t.Fatalf("receipts=%+v", result.Receipts)
	}
	replayed, replayErr := controller.Execute(context.Background(), plan, agentReview(plan))
	if !errors.Is(replayErr, ErrExecutionFailed) || replayed.Status != result.Status || provider.executionCalls != 2 {
		t.Fatalf("replay=%+v err=%v calls=%d", replayed, replayErr, provider.executionCalls)
	}
}

func TestUnclassifiedExecutorErrorRequiresReconciliation(t *testing.T) {
	provider := &fakeProvider{
		validate:     func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} },
		unknownError: "effect-1",
	}
	controller := testController(t, provider, []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
	}}})
	plan := previewOne(t, controller, "tool.create")
	result, err := controller.Execute(context.Background(), plan, agentReview(plan))
	if !errors.Is(err, ErrReconciliationRequired) || result.Status != ExecutionReconciliationNeeded || len(result.Receipts) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Receipts[0].Status != ReceiptReconciliationNeeded || result.Receipts[0].ProviderReceiptSHA256 != "" {
		t.Fatalf("receipt=%+v", result.Receipts[0])
	}
}

func TestPreviewRejectsMultipleMutationsOfOneResourceUntilVersionChainsExist(t *testing.T) {
	provider := &fakeProvider{validate: func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} }}
	controller := testController(t, provider, []ToolContract{
		{Capability: "tool.create", Strategies: []Strategy{executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview)}},
		{Capability: "tool.update", Strategies: []Strategy{executable("restore", SemanticsExact, 100, "tool.restore", ApprovalAgentReview)}},
	})
	created := effect("effect-create", "goal-1", "tool.create", 0, "provider:object-1", "v1")
	updated := effect("effect-update", "goal-1", "tool.update", 1, "provider:object-1", "v2")
	updated.DependsOn = []string{"effect-create"}
	_, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{created, updated}})
	if !errors.Is(err, ErrInvalidRequest) || provider.validationCalls != 0 {
		t.Fatalf("error=%v validation_calls=%d", err, provider.validationCalls)
	}
}

func TestGuidanceOnlyPlanProducesReviewableStepWithoutProviderMutation(t *testing.T) {
	provider := &fakeProvider{}
	controller := testController(t, provider, []ToolContract{{Capability: "mail.send", Strategies: []Strategy{
		guidance("send_correction", 10, "The message cannot be withdrawn; review and send a correction."),
	}}})
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{
		effect("effect-mail", "goal-1", "mail.send", 0, "message-1", "v1"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.InversePython, `{"effect": "effect-mail", "strategy": "send_correction", "mode": "guide"}`) {
		t.Fatalf("inverse Python=%q", plan.InversePython)
	}
	result, err := controller.Execute(context.Background(), plan, agentReview(plan))
	if err != nil || result.Status != ExecutionManualRequired || provider.executionCalls != 0 || len(result.Receipts) != 1 || result.Receipts[0].Status != ReceiptManualRequired {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, provider.executionCalls)
	}
}

func TestAmbiguousAndIrreversibleEffectsRemainExplicitlyNonExecutable(t *testing.T) {
	provider := &fakeProvider{}
	controller := testController(t, provider, []ToolContract{
		{
			Capability:             "payment.charge",
			Strategies:             []Strategy{executable("refund", SemanticsCompensating, 100, "payment.refund", ApprovalUserRequired)},
			ReconciliationGuidance: "Confirm whether the original charge committed before requesting a refund.",
		},
		{
			Capability: "mail.send",
			Strategies: []Strategy{{
				ID: "cannot_withdraw", Semantics: SemanticsIrreversible, Mode: ModeGuidance, Rank: 100,
				Approval: ApprovalAgentReview, Guidance: "The sent message cannot be withdrawn; review a correction instead.",
			}},
		},
	})
	charge := effect("effect-charge", "goal-1", "payment.charge", 1, "charge-1", "v1")
	charge.Outcome = EffectAmbiguous
	charge.AfterVersion = ""
	charge.ResultSHA256 = ""
	mail := effect("effect-mail", "goal-1", "mail.send", 0, "message-1", "v1")
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{mail, charge}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.ReconciliationRequired != 1 || plan.Summary.Irreversible != 1 || plan.Summary.Executable != 0 {
		t.Fatalf("summary=%+v", plan.Summary)
	}
	if plan.Steps[0].ValidationStatus != ValidationReconciliationRequired || plan.Steps[1].Semantics != SemanticsIrreversible {
		t.Fatalf("steps=%+v", plan.Steps)
	}
	result, err := controller.Execute(context.Background(), plan, agentReview(plan))
	if !errors.Is(err, ErrReconciliationRequired) || result.Status != ExecutionReconciliationNeeded || provider.executionCalls != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, provider.executionCalls)
	}
	if len(result.Receipts) != 2 || result.Receipts[0].Status != ReceiptReconciliationNeeded || result.Receipts[1].Status != ReceiptBlocked {
		t.Fatalf("receipts=%+v", result.Receipts)
	}
	if _, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{mail}}); !errors.Is(err, ErrEffectBlocked) {
		t.Fatalf("blocked effect preview error=%v", err)
	}
}

func TestAmbiguousCompensationOutcomeRequiresReconciliationReceipt(t *testing.T) {
	provider := &fakeProvider{
		validate:        func(_ EffectReceipt, _ Strategy) Validation { return Validation{Applicable: true} },
		reconcileEffect: "effect-1",
	}
	controller := testController(t, provider, []ToolContract{{Capability: "tool.create", Strategies: []Strategy{
		executable("delete", SemanticsExact, 100, "tool.delete", ApprovalAgentReview),
	}}})
	receipt := effect("effect-1", "goal-1", "tool.create", 0, "target-1", "v1")
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunPlan, Receipts: []EffectReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.Execute(context.Background(), plan, agentReview(plan))
	if !errors.Is(err, ErrReconciliationRequired) || result.Status != ExecutionReconciliationNeeded || len(result.Receipts) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Receipts[0].Status != ReceiptReconciliationNeeded || result.Receipts[0].ProviderReceiptSHA256 != digest('e') {
		t.Fatalf("receipt=%+v", result.Receipts[0])
	}
	if _, err := controller.Execute(context.Background(), secondPlan, agentReview(secondPlan)); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatalf("second plan error=%v", err)
	}
	if _, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{receipt}}); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatalf("post-ambiguity preview error=%v", err)
	}
	if provider.executionCalls != 1 {
		t.Fatalf("provider calls=%d", provider.executionCalls)
	}
}

type fakeProvider struct {
	version         string
	validate        func(EffectReceipt, Strategy) Validation
	validationCalls int
	executionCalls  int
	failEffect      string
	reconcileEffect string
	unknownError    string
	denyAuthority   bool
}

func (provider *fakeProvider) AuthorizeCompensation(_ context.Context, _ Plan, _ Review) error {
	if provider.denyAuthority {
		return errors.New("injected authority denial")
	}
	return nil
}

func (provider *fakeProvider) Validate(_ context.Context, receipt EffectReceipt, strategy Strategy) (Validation, error) {
	provider.validationCalls++
	if provider.validate != nil {
		return provider.validate(receipt, strategy), nil
	}
	return Validation{Applicable: true, CurrentVersion: provider.version}, nil
}

func (provider *fakeProvider) Execute(_ context.Context, receipt EffectReceipt, _ Strategy) (ProviderResult, error) {
	provider.executionCalls++
	if receipt.EffectID == provider.unknownError {
		return ProviderResult{}, errors.New("unclassified provider error")
	}
	if receipt.EffectID == provider.failEffect {
		return ProviderResult{Outcome: ProviderNotApplied, ReceiptSHA256: digest('0')}, nil
	}
	if receipt.EffectID == provider.reconcileEffect {
		return ProviderResult{Outcome: ProviderReconciliationNeeded, ReceiptSHA256: digest('e')}, nil
	}
	return ProviderResult{Outcome: ProviderCompensated, ReceiptSHA256: digest('f')}, nil
}

func testController(t *testing.T, provider *fakeProvider, contracts []ToolContract) *Controller {
	t.Helper()
	controller, err := NewController(Config{Contracts: contracts, Validator: provider, Executor: provider, Authorizer: provider, MaxPlans: 16, MaxRecords: 32})
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func previewOne(t *testing.T, controller *Controller, capability string) Plan {
	t.Helper()
	plan, err := controller.Preview(context.Background(), PreviewRequest{EffectGroupID: "goal-1", Mode: DryRunValidate, Receipts: []EffectReceipt{
		effect("effect-1", "goal-1", capability, 0, "target-1", "v1"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func effect(id, group, capability string, index uint32, target, version string) EffectReceipt {
	return EffectReceipt{
		EffectID: id, EffectGroupID: group, RunID: "run-original-1", Capability: capability, OperationIndex: index,
		TargetID: target, AfterVersion: version, ArgumentsSHA256: digest('c'), ResultSHA256: digest('d'), Outcome: EffectApplied,
	}
}

func executable(id string, semantics Semantics, rank uint16, operation string, approval Approval) Strategy {
	return Strategy{ID: id, Semantics: semantics, Mode: ModeExecutable, Rank: rank, Operation: operation, Approval: approval}
}

func guidance(id string, rank uint16, text string) Strategy {
	return Strategy{ID: id, Semantics: SemanticsGuidance, Mode: ModeGuidance, Rank: rank, Guidance: text, Approval: ApprovalAgentReview}
}

func agentReview(plan Plan) Review {
	return Review{PlanSHA256: plan.Identity, ReviewerRunID: "undo-run-1", AuthoritySHA256: digest('a')}
}

func digest(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}
