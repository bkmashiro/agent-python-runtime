package agentic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type promptCausalitySummary struct {
	SchemaVersion     string   `json:"schema_version"`
	Status            string   `json:"status"`
	RepositoryCommit  string   `json:"repository_commit"`
	Model             string   `json:"model"`
	Tasks             []string `json:"tasks"`
	ReplicatesPerTask int      `json:"replicates_per_task"`
	Arms              map[string]struct {
		Trials           int    `json:"trials"`
		StrictPasses     int    `json:"strict_passes"`
		OutcomeSuccesses int    `json:"outcome_successes"`
		ProviderCalls    uint32 `json:"provider_calls"`
		ToolCalls        int    `json:"tool_calls"`
		TotalTokens      uint64 `json:"total_tokens"`
	} `json:"arms"`
}

type promptCausalityHashes struct {
	SchemaVersion string                          `json:"schema_version"`
	Files         []struct{ Path, SHA256 string } `json:"files"`
}

func TestPublishedCodexSparkPromptCausalityEvidence(t *testing.T) {
	root := filepath.Join("results", "codex-spark-prompt-causality-2026-08-11")
	var summary promptCausalitySummary
	readJSON := func(path string, value any) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatalf("read %s: %v", path, err)
		}
	}
	readJSON(filepath.Join(root, "summary.json"), &summary)
	if summary.SchemaVersion != "codex-spark-prompt-causality-development-summary/v1" || summary.Status != "development_diagnostic_not_decision_gate" ||
		summary.RepositoryCommit != "2f4b18d6531743ca3539cc21b41d4be100ea9e5d" || summary.Model != "gpt-5.3-codex-spark" || len(summary.Tasks) != 2 || summary.ReplicatesPerTask != 6 || len(summary.Arms) != 3 {
		t.Fatalf("summary=%+v", summary)
	}
	paths, err := filepath.Glob(filepath.Join(root, "trials", "*.json"))
	if err != nil || len(paths) != 36 {
		t.Fatalf("trial count=%d err=%v", len(paths), err)
	}
	type aggregate struct {
		trials, strict, outcome int
		provider                uint32
		tools                   int
		tokens                  uint64
	}
	aggregates := map[string]*aggregate{"control-v4": {}, "exact-plan-v5": {}, "initial-cwd-v6": {}}
	for _, path := range paths {
		var result TrialResult
		readJSON(path, &result)
		if ValidateTrialResult(result) != nil || result.Model != "gpt-5.3-codex-spark" || result.Identity.RepositoryCommit != summary.RepositoryCommit || result.Condition != ConditionPython || result.Replicate >= 6 || result.Metrics == nil {
			t.Fatalf("invalid trial %s: %+v", path, result)
		}
		arm := ""
		for candidate := range aggregates {
			if strings.Contains(filepath.Base(path), "-"+candidate+".json") {
				arm = candidate
				break
			}
		}
		if arm == "" {
			t.Fatalf("unclassified trial %s", path)
		}
		agg := aggregates[arm]
		agg.trials++
		agg.provider += result.ProviderCalls
		agg.tools += result.ToolCalls
		agg.tokens += result.Usage.TotalTokens
		if result.Metrics.StrictPass {
			agg.strict++
		}
		if result.Metrics.OutcomeSuccess {
			agg.outcome++
		}
	}
	for arm, actual := range aggregates {
		expected := summary.Arms[arm]
		if actual.trials != expected.Trials || actual.strict != expected.StrictPasses || actual.outcome != expected.OutcomeSuccesses || actual.provider != expected.ProviderCalls || actual.tools != expected.ToolCalls || actual.tokens != expected.TotalTokens {
			t.Fatalf("arm=%s actual=%+v expected=%+v", arm, actual, expected)
		}
	}
	var hashes promptCausalityHashes
	readJSON(filepath.Join(root, "hashes.json"), &hashes)
	if hashes.SchemaVersion != "codex-spark-prompt-causality-hashes/v1" || len(hashes.Files) != 39 {
		t.Fatalf("hash manifest=%+v", hashes)
	}
	for _, entry := range hashes.Files {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		sum := sha256.Sum256(raw)
		if err != nil || entry.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Fatalf("hash mismatch %s: %v", entry.Path, err)
		}
	}
}
