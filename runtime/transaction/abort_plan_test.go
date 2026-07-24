package transaction

import "testing"

func TestBuildAbortPlanUsesReverseOrderAndDistinctRollbackCompensation(t *testing.T) {
	operations := []Operation{
		{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReversible, State: OperationApplied},
		{ID: "op_2", TransactionID: "tx_1", Index: 2, EffectClass: EffectCompensatable, State: OperationApplied, ToolID: "inventory.reserve"},
		{ID: "op_3", TransactionID: "tx_1", Index: 3, EffectClass: EffectReversible, State: OperationApplied},
	}
	plan, err := BuildAbortPlan(operations, map[string]bool{"inventory.reserve": true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Disposition != AbortRollbackAndCompensate || len(plan.RollbackOperationIDs) != 2 ||
		plan.RollbackOperationIDs[0] != "op_3" || plan.RollbackOperationIDs[1] != "op_1" ||
		len(plan.CompensationOperationIDs) != 1 || plan.CompensationOperationIDs[0] != "op_2" || !plan.AutoCompensate {
		t.Fatalf("unexpected mixed abort plan: %+v", plan)
	}
}

func TestBuildAbortPlanReportsWeakestTruthfulGuarantee(t *testing.T) {
	tests := []struct {
		name       string
		operations []Operation
		want       AbortDisposition
	}{
		{name: "staged irreversible plus reversible", operations: []Operation{
			{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReversible, State: OperationApplied},
			{ID: "op_2", TransactionID: "tx_1", Index: 2, EffectClass: EffectIrreversible, State: OperationAwaitingUserApproval},
		}, want: AbortRejectIntentAndRollback},
		{name: "irreversible committed dominates", operations: []Operation{
			{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReversible, State: OperationApplied},
			{ID: "op_2", TransactionID: "tx_1", Index: 2, EffectClass: EffectIrreversible, State: OperationApplied},
		}, want: AbortIrreversibleCommitted},
		{name: "applying is ambiguous", operations: []Operation{
			{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReversible, State: OperationApplying},
		}, want: AbortReconciliationRequired},
		{name: "compensation failed remains required", operations: []Operation{
			{ID: "op_1", TransactionID: "tx_1", Index: 1, ToolID: "inventory.reserve", EffectClass: EffectCompensatable, State: OperationCompensationFailed},
		}, want: AbortCompensationRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildAbortPlan(test.operations, nil)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Disposition != test.want {
				t.Fatalf("disposition=%q plan=%+v", plan.Disposition, plan)
			}
		})
	}
}

func TestBuildAbortPlanRejectsCrossTransactionAndDuplicateIndexes(t *testing.T) {
	if _, err := BuildAbortPlan([]Operation{
		{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReadOnly, State: OperationApplied},
		{ID: "op_2", TransactionID: "tx_2", Index: 2, EffectClass: EffectReadOnly, State: OperationApplied},
	}, nil); err == nil {
		t.Fatal("cross-transaction plan was accepted")
	}
	if _, err := BuildAbortPlan([]Operation{
		{ID: "op_1", TransactionID: "tx_1", Index: 1, EffectClass: EffectReadOnly, State: OperationApplied},
		{ID: "op_2", TransactionID: "tx_1", Index: 1, EffectClass: EffectReadOnly, State: OperationApplied},
	}, nil); err == nil {
		t.Fatal("duplicate operation index was accepted")
	}
}
