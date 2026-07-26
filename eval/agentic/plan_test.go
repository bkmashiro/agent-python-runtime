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
	if plan.Digest == "" || plan.GlobalBounds.TrialCount != 30 || plan.GlobalBounds.MaxProviderAttempts != 63 || plan.GlobalBounds.MaxPythonRuns != 42 || len(dataset.Tasks) != 20 {
		t.Fatalf("plan=%+v tasks=%d", plan, len(dataset.Tasks))
	}
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	limits, err := plan.LimitsFor(task)
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxProviderCalls != 3 || limits.MaxPythonRuns != 3 || limits.MaxToolCalls != 32 || limits.MaxInputTokens != 60_000 || limits.MaxOutputTokens != 3_072 || limits.MaxTotalTokens != 63_072 || limits.MaxOutputTokensPerCall != 1_024 {
		t.Fatalf("limits=%+v", limits)
	}
	if !plan.Authorizes(task.ID, ConditionHybrid, 0) || plan.Authorizes(task.ID, ConditionHybrid, 1) {
		t.Fatal("authorization boundary failed")
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
