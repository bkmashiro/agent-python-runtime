package agentic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
	PairedComparisons []struct {
		Left                  string  `json:"left"`
		Right                 string  `json:"right"`
		BothPass              int     `json:"both_pass"`
		LeftFailRightPass     int     `json:"left_fail_right_pass"`
		LeftPassRightFail     int     `json:"left_pass_right_fail"`
		BothFail              int     `json:"both_fail"`
		ExactMcNemarTwoSidedP float64 `json:"exact_mcnemar_two_sided_p"`
	} `json:"paired_comparisons"`
}

type promptCausalityHashes struct {
	SchemaVersion string `json:"schema_version"`
	Files         []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
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
	paired := make(map[string]bool, len(paths))
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
		key := result.TaskID + "/" + strconv.FormatUint(uint64(result.Replicate), 10) + "/" + arm
		if _, exists := paired[key]; exists {
			t.Fatalf("duplicate paired cell %s", key)
		}
		paired[key] = result.Metrics.StrictPass
	}
	for arm, actual := range aggregates {
		expected := summary.Arms[arm]
		if actual.trials != expected.Trials || actual.strict != expected.StrictPasses || actual.outcome != expected.OutcomeSuccesses || actual.provider != expected.ProviderCalls || actual.tools != expected.ToolCalls || actual.tokens != expected.TotalTokens {
			t.Fatalf("arm=%s actual=%+v expected=%+v", arm, actual, expected)
		}
	}
	if len(summary.PairedComparisons) != 3 {
		t.Fatalf("paired comparisons=%+v", summary.PairedComparisons)
	}
	seenPairs := map[string]bool{}
	for _, comparison := range summary.PairedComparisons {
		pairKey := comparison.Left + "->" + comparison.Right
		if seenPairs[pairKey] || aggregates[comparison.Left] == nil || aggregates[comparison.Right] == nil {
			t.Fatalf("invalid pair %s", pairKey)
		}
		seenPairs[pairKey] = true
		var bothPass, leftFailRightPass, leftPassRightFail, bothFail int
		for _, taskID := range summary.Tasks {
			for replicate := 0; replicate < summary.ReplicatesPerTask; replicate++ {
				prefix := taskID + "/" + strconv.Itoa(replicate) + "/"
				left, leftOK := paired[prefix+comparison.Left]
				right, rightOK := paired[prefix+comparison.Right]
				if !leftOK || !rightOK {
					t.Fatalf("missing pair cell %s", prefix)
				}
				switch {
				case left && right:
					bothPass++
				case !left && right:
					leftFailRightPass++
				case left && !right:
					leftPassRightFail++
				default:
					bothFail++
				}
			}
		}
		expectedP := exactMcNemarTwoSided(leftFailRightPass, leftPassRightFail)
		if comparison.BothPass != bothPass || comparison.LeftFailRightPass != leftFailRightPass || comparison.LeftPassRightFail != leftPassRightFail || comparison.BothFail != bothFail || math.Abs(comparison.ExactMcNemarTwoSidedP-expectedP) > 1e-12 {
			t.Fatalf("pair=%s got=%+v recomputed=[%d %d %d %d %.12f]", pairKey, comparison, bothPass, leftFailRightPass, leftPassRightFail, bothFail, expectedP)
		}
	}
	validatePromptCausalityHashes(t, root, readJSON)
}

func exactMcNemarTwoSided(leftFailRightPass, leftPassRightFail int) float64 {
	n := leftFailRightPass + leftPassRightFail
	if n == 0 {
		return 1
	}
	low := leftFailRightPass
	if leftPassRightFail < low {
		low = leftPassRightFail
	}
	numerator := 0
	for k := 0; k <= low; k++ {
		numerator += binomialCoefficient(n, k)
	}
	value := 2 * float64(numerator) / math.Pow(2, float64(n))
	if value > 1 {
		return 1
	}
	return value
}

func binomialCoefficient(n, k int) int {
	if k > n-k {
		k = n - k
	}
	result := 1
	for i := 1; i <= k; i++ {
		result = result * (n - k + i) / i
	}
	return result
}

func validatePromptCausalityHashes(t *testing.T, root string, readJSON func(string, any)) {
	t.Helper()
	var hashes promptCausalityHashes
	readJSON(filepath.Join(root, "hashes.json"), &hashes)
	if hashes.SchemaVersion != "codex-spark-prompt-causality-hashes/v1" {
		t.Fatalf("hash manifest=%+v", hashes)
	}
	expected := map[string]bool{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && filepath.Base(path) != "hashes.json" {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			expected[filepath.ToSlash(relative)] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range hashes.Files {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		if entry.Path == "" || filepath.IsAbs(entry.Path) || clean != entry.Path || clean == ".." || strings.HasPrefix(clean, "../") || seen[entry.Path] || !expected[entry.Path] || !validDigest(entry.SHA256) {
			t.Fatalf("invalid hash entry %+v", entry)
		}
		seen[entry.Path] = true
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		sum := sha256.Sum256(raw)
		if err != nil || entry.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Fatalf("hash mismatch %s: %v", entry.Path, err)
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("hash coverage got=%d expected=%d", len(seen), len(expected))
	}
	for path := range expected {
		if !seen[path] {
			t.Fatalf("unhashed evidence file %s", path)
		}
	}
}
