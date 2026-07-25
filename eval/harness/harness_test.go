package harness_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/harness"
)

func roots(t *testing.T) (string, string, string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	return filepath.Join(root, "../dataset/v1"), filepath.Join(root, "../prompts/manifest-v1.json"), filepath.Join(root, "../schemas")
}

func TestScriptedSmokeRejectsPromptDrift(t *testing.T) {
	datasetRoot, prompt, schemas := roots(t)
	promptDir := t.TempDir()
	if err := os.CopyFS(promptDir, os.DirFS(filepath.Dir(prompt))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(promptDir, "hybrid-v1.txt")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	config := harness.Config{DatasetRoot: datasetRoot, PromptManifestPath: filepath.Join(promptDir, "manifest-v1.json"), SchemaDir: schemas, OutputDir: filepath.Join(t.TempDir(), "run"), RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("1", 64), GuestArtifactDigest: "sha256:" + strings.Repeat("2", 64)}
	if _, err := harness.RunSmoke(config); err == nil {
		t.Fatal("prompt drift was accepted")
	}
}

func TestScriptedSmokeIsDeterministicAndConditionIsolated(t *testing.T) {
	datasetRoot, prompt, schemas := roots(t)
	config := func(out string) harness.Config {
		return harness.Config{DatasetRoot: datasetRoot, PromptManifestPath: prompt, SchemaDir: schemas, OutputDir: out, RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("1", 64), GuestArtifactDigest: "sha256:" + strings.Repeat("2", 64)}
	}
	firstOut := filepath.Join(t.TempDir(), "run")
	first, err := harness.RunSmoke(config(firstOut))
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.RunSmoke(config(filepath.Join(t.TempDir(), "run")))
	if err != nil {
		t.Fatal(err)
	}
	if first.RecordSetDigest != second.RecordSetDigest || first.Trials != 30 || first.ComparisonDecision != "inconclusive" {
		t.Fatalf("unexpected summaries: %+v %+v", first, second)
	}
	if replayed, err := harness.VerifyReplay(firstOut, schemas, datasetRoot, prompt); err != nil || replayed != first {
		t.Fatalf("replay failed: %+v %v", replayed, err)
	}
	firstRecords, _ := filepath.Glob(filepath.Join(firstOut, "records", "*.json"))
	data, _ := os.ReadFile(firstRecords[0])
	if err := os.WriteFile(firstRecords[0], append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.VerifyReplay(firstOut, schemas, datasetRoot, prompt); err == nil {
		t.Fatal("replay accepted a tampered record")
	}
	out := filepath.Join(t.TempDir(), "run")
	if _, err := harness.RunSmoke(config(out)); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(out, "records", "*.json"))
	specs, _ := filepath.Glob(filepath.Join(out, "specs", "*.json"))
	if len(paths) != 30 || len(specs) != 30 {
		t.Fatalf("records=%d specs=%d", len(paths), len(specs))
	}
	if _, err := harness.RunSmoke(config(out)); err == nil {
		t.Fatal("harness silently overwrote an existing trial set")
	}
	var simpleHybrid, compoundHybrid, compoundDirect bool
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			t.Fatal("invalid record")
		}
		metrics := raw["metrics"].(map[string]any)
		if strings.Contains(path, "simple_read") && strings.Contains(path, "-hybrid.json") {
			simpleHybrid = metrics["direct_calls"].(float64) == 1 && metrics["workflow_runs"].(float64) == 0
		} else if strings.Contains(path, "-hybrid.json") {
			compoundHybrid = compoundHybrid || metrics["workflow_runs"].(float64) == 1 && metrics["model_tool_round_trips"].(float64) == 2
		} else if strings.Contains(path, "-direct-only.json") {
			compoundDirect = compoundDirect || metrics["direct_calls"].(float64) == 4 && metrics["model_tool_round_trips"].(float64) == 4
		}
	}
	if !simpleHybrid || !compoundHybrid || !compoundDirect {
		t.Fatalf("condition isolation simple=%v compound_hybrid=%v compound_direct=%v", simpleHybrid, compoundHybrid, compoundDirect)
	}
	comparisonData, _ := os.ReadFile(filepath.Join(out, "comparison.json"))
	var comparison map[string]any
	if json.Unmarshal(comparisonData, &comparison) != nil {
		t.Fatal("invalid comparison")
	}
	aggregates := comparison["aggregates"].([]any)
	byCondition := make(map[string]map[string]any)
	for _, raw := range aggregates {
		aggregate := raw.(map[string]any)
		byCondition[aggregate["condition"].(string)] = aggregate
	}
	if byCondition["direct-only"]["median_model_tool_round_trips"] != float64(4) || byCondition["direct-only"]["median_total_tokens"] != float64(440) || byCondition["python-only"]["unnecessary_workflow_rate"] != float64(1) || byCondition["hybrid"]["useful_workflow_rate"] != float64(1) {
		t.Fatalf("comparison aggregates are not derived from trial records: %#v", byCondition)
	}
	driftSchemas := t.TempDir()
	if err := os.CopyFS(driftSchemas, os.DirFS(schemas)); err != nil {
		t.Fatal(err)
	}
	driftPath := filepath.Join(driftSchemas, "comparison-v1.schema.json")
	driftBytes, _ := os.ReadFile(driftPath)
	if err := os.WriteFile(driftPath, append(driftBytes, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.VerifyReplay(out, driftSchemas, datasetRoot, prompt); err == nil {
		t.Fatal("replay accepted schema drift")
	}
	byCondition["direct-only"]["median_total_tokens"] = float64(999)
	fabricated, _ := json.MarshalIndent(comparison, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "comparison.json"), append(fabricated, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.VerifyReplay(out, schemas, datasetRoot, prompt); err == nil {
		t.Fatal("replay accepted fabricated aggregates")
	}
	if err := os.WriteFile(filepath.Join(out, "comparison.json"), comparisonData, 0600); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(comparisonData, &comparison) != nil {
		t.Fatal("invalid comparison")
	}
	comparison["decision"] = "promote"
	comparison["decision_reasons"] = []any{"fabricated"}
	promoted, _ := json.MarshalIndent(comparison, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "comparison.json"), append(promoted, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(out, "summary.json")
	summaryData, _ := os.ReadFile(summaryPath)
	var summary map[string]any
	if json.Unmarshal(summaryData, &summary) != nil {
		t.Fatal("invalid summary")
	}
	summary["comparison_decision"] = "promote"
	promotedSummary, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(summaryPath, append(promotedSummary, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.VerifyReplay(out, schemas, datasetRoot, prompt); err == nil {
		t.Fatal("replay accepted scripted promotion")
	}
	symlinkOut := filepath.Join(t.TempDir(), "run")
	if _, err := harness.RunSmoke(config(symlinkOut)); err != nil {
		t.Fatal(err)
	}
	externalRecords := filepath.Join(t.TempDir(), "records")
	if err := os.Rename(filepath.Join(symlinkOut, "records"), externalRecords); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalRecords, filepath.Join(symlinkOut, "records")); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.VerifyReplay(symlinkOut, schemas, datasetRoot, prompt); err == nil {
		t.Fatal("replay accepted symlinked record directory")
	}
}
