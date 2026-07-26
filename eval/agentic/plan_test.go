package agentic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDevelopmentPilotPlanRecomputesBounds(t *testing.T) {
	root := datasetRoot(t)
	plan, dataset, err := LoadDevelopmentPilotPlan(filepath.Join(root, "development-pilot-plan.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Digest == "" || plan.GlobalBounds.TrialCount != 30 || plan.GlobalBounds.MaxProviderAttempts != 159 || plan.GlobalBounds.MaxPythonRuns != 42 || len(dataset.Tasks) != 20 {
		t.Fatalf("plan=%+v tasks=%d", plan, len(dataset.Tasks))
	}
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	limits, err := plan.LimitsFor(task, ConditionDirect)
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxProviderCalls != 12 || limits.MaxPythonRuns != 3 || limits.MaxToolCalls != 32 || limits.MaxInputTokens != 240_000 || limits.MaxOutputTokens != 12_288 || limits.MaxTotalTokens != 252_288 || limits.MaxOutputTokensPerCall != 1_024 {
		t.Fatalf("limits=%+v", limits)
	}
	pythonLimits, err := plan.LimitsFor(task, ConditionPython)
	if err != nil || pythonLimits.MaxProviderCalls != 3 || pythonLimits.MaxInputTokens != 60_000 {
		t.Fatalf("python limits=%+v err=%v", pythonLimits, err)
	}
	if !plan.Authorizes(task.ID, ConditionHybrid, 0) || plan.Authorizes(task.ID, ConditionHybrid, 1) {
		t.Fatal("authorization boundary failed")
	}
}

func TestLoadDevelopmentPilotPlanAcceptsExplicitGPT41Model(t *testing.T) {
	root := datasetRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "development-pilot-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(content, &document) != nil {
		t.Fatal("decode plan")
	}
	document["model"] = "gpt-4.1"
	mutated, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "gpt-4.1-plan.json")
	if os.WriteFile(path, mutated, 0o600) != nil {
		t.Fatal("write plan")
	}
	plan, _, err := LoadDevelopmentPilotPlan(path, root)
	if err != nil || plan.Model != "gpt-4.1" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestLoadDevelopmentPilotPlanRejectsUnapprovedModel(t *testing.T) {
	root := datasetRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "development-pilot-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(content, &document) != nil {
		t.Fatal("decode plan")
	}
	document["model"] = "arbitrary-provider-alias"
	mutated, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "unapproved-model-plan.json")
	if os.WriteFile(path, mutated, 0o600) != nil {
		t.Fatal("write plan")
	}
	if _, _, err := LoadDevelopmentPilotPlan(path, root); err == nil {
		t.Fatal("unapproved model accepted")
	}
}

func TestSealedEvaluationPlanRecomputesBoundsAndLatinSquare(t *testing.T) {
	root := datasetRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "evaluation-plan.sealed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Status                  string                 `json:"status"`
		Split                   string                 `json:"split"`
		Model                   string                 `json:"model"`
		CurrentDecisionEligible bool                   `json:"current_decision_eligible"`
		TaskIDs                 []string               `json:"task_ids"`
		Replicates              []uint32               `json:"replicates"`
		ConditionOrder          map[string][]Condition `json:"condition_order_by_replicate"`
		Global                  struct {
			TrialCount          uint32 `json:"trial_count"`
			MaxProviderAttempts uint32 `json:"max_provider_attempts"`
			MaxInputTokens      uint64 `json:"max_input_tokens"`
			MaxOutputTokens     uint64 `json:"max_output_tokens"`
			MaxTotalTokens      uint64 `json:"max_total_tokens"`
			MaxToolCalls        uint32 `json:"max_tool_calls"`
			MaxPythonRuns       uint32 `json:"max_python_runs"`
		} `json:"global_bounds"`
	}
	if json.Unmarshal(content, &plan) != nil || plan.Status != "sealed_not_runnable" || plan.Split != "evaluation" || plan.Model != developmentModel || plan.CurrentDecisionEligible || len(plan.TaskIDs) != 10 || len(plan.Replicates) != 3 {
		t.Fatalf("plan=%+v", plan)
	}
	dataset, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	evaluation := map[string]Task{}
	for _, task := range dataset.Tasks {
		if task.Split == "evaluation" {
			evaluation[task.ID] = task
		}
	}
	turns := 0
	attemptsPerReplicate := 0
	for _, id := range plan.TaskIDs {
		task, exists := evaluation[id]
		if !exists {
			t.Fatalf("unknown evaluation task %s", id)
		}
		turns += len(task.Interaction.Turns)
		if task.Track == "stateful_local_tools" {
			attemptsPerReplicate += len(task.Interaction.Turns) * 9
		} else {
			attemptsPerReplicate += len(task.Interaction.Turns) * 3
		}
	}
	attempts := uint32(attemptsPerReplicate * len(plan.Replicates))
	if plan.Global.TrialCount != 90 || plan.Global.MaxProviderAttempts != attempts || plan.Global.MaxInputTokens != uint64(attempts)*20_000 || plan.Global.MaxOutputTokens != uint64(attempts)*1_024 || plan.Global.MaxTotalTokens != plan.Global.MaxInputTokens+plan.Global.MaxOutputTokens || plan.Global.MaxToolCalls != 90*32 || plan.Global.MaxPythonRuns != uint32(turns*2*3) {
		t.Fatalf("turns=%d global=%+v", turns, plan.Global)
	}
	for _, replicate := range []string{"0", "1", "2"} {
		order := plan.ConditionOrder[replicate]
		seen := map[Condition]bool{}
		for _, condition := range order {
			seen[condition] = true
		}
		if len(order) != 3 || !seen[ConditionDirect] || !seen[ConditionPython] || !seen[ConditionHybrid] {
			t.Fatalf("replicate %s order=%v", replicate, order)
		}
	}
}

func TestLoadDevelopmentPilotPlanRejectsTamperedGlobalBound(t *testing.T) {
	root := datasetRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "development-pilot-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(content, &document) != nil {
		t.Fatal("decode plan")
	}
	document["global_bounds"].(map[string]any)["max_provider_attempts"] = float64(62)
	mutated, _ := json.Marshal(document)
	path := filepath.Join(t.TempDir(), "plan.json")
	if os.WriteFile(path, mutated, 0o600) != nil {
		t.Fatal("write plan")
	}
	if _, _, err := LoadDevelopmentPilotPlan(path, root); err == nil {
		t.Fatal("tampered bound accepted")
	}
}
