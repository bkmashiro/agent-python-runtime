package placement

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDevelopmentCanaryPlanIsFrozenAcrossFiveStrata(t *testing.T) {
	root := filepath.Join("..", "agentic", "placement", "v1")
	planPath := filepath.Join("..", "agentic", "results", "placement-development-canary-prereg-2026-08-11", "plan.json")
	plan, err := LoadDevelopmentCanaryPlan(root, planPath)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{
		"pl-dev_simple_read_001",
		"pl-hp-dedupe-contacts",
		"pl-dev_fanout_join_filter_001",
		"pl-dev_irreversible_staging_001",
		"pl-ba-git-history",
		"pl-ba-native-process",
	}
	gotIDs := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		gotIDs = append(gotIDs, task.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("task IDs=%v", gotIDs)
	}
	if !reflect.DeepEqual(plan.Arms, []string{"direct", "pysolate", "computer"}) ||
		!reflect.DeepEqual(plan.Phases, []string{"scripted_parity", "model"}) ||
		plan.ReplicatesPerArm != 1 || plan.Budgets.PlannedCells != 18 || plan.Budgets.MaxCLITokens != 2_500_000 {
		t.Fatalf("invalid frozen plan: %+v", plan)
	}
	if !plan.FailurePolicy.ModelProgramIsTerminalTrial || plan.FailurePolicy.SingleModelProgramFailureStops ||
		plan.FailurePolicy.PerTaskPromptTuning || plan.FailurePolicy.InfrastructureRepairLimit != 1 {
		t.Fatalf("invalid failure policy: %+v", plan.FailurePolicy)
	}
}

func TestDevelopmentCanaryPlanFailsClosedOnMutation(t *testing.T) {
	root := filepath.Join("..", "agentic", "placement", "v1")
	planPath := filepath.Join("..", "agentic", "results", "placement-development-canary-prereg-2026-08-11", "plan.json")
	plan, err := LoadDevelopmentCanaryPlan(root, planPath)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DevelopmentCanaryPlan){
		"decision-like task": func(value *DevelopmentCanaryPlan) { value.Tasks[0].ID = "pl-evaluation_simple_read_001" },
		"duplicate task":     func(value *DevelopmentCanaryPlan) { value.Tasks[1] = value.Tasks[0] },
		"stop on model fail": func(value *DevelopmentCanaryPlan) { value.FailurePolicy.SingleModelProgramFailureStops = true },
		"per-task tuning":    func(value *DevelopmentCanaryPlan) { value.FailurePolicy.PerTaskPromptTuning = true },
		"identity drift": func(value *DevelopmentCanaryPlan) {
			value.IdentityLockSHA256 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
	} {
		t.Run(name, func(t *testing.T) {
			copyPlan := plan
			copyPlan.Tasks = append([]CanaryTask(nil), plan.Tasks...)
			mutate(&copyPlan)
			if validateDevelopmentCanaryPlan(root, corpus, copyPlan) == nil {
				t.Fatal("mutated plan admitted")
			}
		})
	}
}
