package agentic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type publishedCodexSummary struct {
	SchemaVersion          string `json:"schema_version"`
	Status                 string `json:"status"`
	SourceRepositoryCommit string `json:"source_repository_commit"`
	Model                  string `json:"model"`
	Transport              string `json:"transport"`
	Combined               map[string]struct {
		Passed        int    `json:"passed"`
		Trials        int    `json:"trials"`
		ProviderCalls uint32 `json:"provider_calls"`
		TotalTokens   uint64 `json:"total_tokens"`
	} `json:"combined_condition_summary"`
	Artifacts []struct {
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
		Kind      string `json:"kind,omitempty"`
		Condition string `json:"condition,omitempty"`
		Replicate int    `json:"replicate"`
	} `json:"artifacts"`
	ProhibitedClaims []string `json:"prohibited_claims"`
}

func TestPublishedCodexSparkRoutingEvidence(t *testing.T) {
	root := filepath.Join("results", "codex-spark-routing-2026-08-11")
	raw, err := os.ReadFile(filepath.Join(root, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary publishedCodexSummary
	if json.Unmarshal(raw, &summary) != nil || summary.SchemaVersion != "codex-spark-routing-development-summary/v1" ||
		summary.Status != "development_only_not_decision_eligible" || summary.SourceRepositoryCommit != "2cc30b2b05b28ec88c05a1f9ddb82e45daf95fd3" ||
		summary.Model != codexSpark53DevelopmentModel || summary.Transport != provider.CodexCLIProtocol || len(summary.Artifacts) != 38 {
		t.Fatalf("invalid published summary: %+v", summary)
	}
	for _, claim := range []string{"decision_eligible", "computer_replacement_rate", "latency_reduction", "profile_qualified_placement"} {
		if !publishedContains(summary.ProhibitedClaims, claim) {
			t.Fatalf("missing prohibited claim %q", claim)
		}
	}
	type aggregate struct {
		passed, trials int
		providerCalls  uint32
		totalTokens    uint64
	}
	actual := map[string]*aggregate{"direct": {}, "python": {}, "hybrid": {}}
	seen := make(map[string]struct{}, len(summary.Artifacts))
	for _, artifact := range summary.Artifacts {
		clean := filepath.Clean(artifact.Path)
		if clean != artifact.Path || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe artifact path %q", artifact.Path)
		}
		if _, duplicate := seen[clean]; duplicate {
			t.Fatalf("duplicate artifact %q", clean)
		}
		seen[clean] = struct{}{}
		body, err := os.ReadFile(filepath.Join(root, clean))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		if got := "sha256:" + hex.EncodeToString(digest[:]); got != artifact.SHA256 {
			t.Fatalf("artifact digest %q: got %s want %s", clean, got, artifact.SHA256)
		}
		if artifact.Kind == "hybrid_regret" {
			continue
		}
		var result TrialResult
		if json.Unmarshal(body, &result) != nil || ValidateTrialResult(result) != nil || result.Version != "agentic-development-trial/v4" ||
			result.Model != codexSpark53DevelopmentModel || result.Replicate != uint32(artifact.Replicate) || string(result.Condition) != artifact.Condition ||
			result.Identity.RepositoryCommit != summary.SourceRepositoryCommit || strings.HasPrefix(result.ErrorCode, "provider_") {
			t.Fatalf("invalid trial artifact %q: %+v", clean, result)
		}
		bucket := actual[artifact.Condition]
		if bucket == nil {
			t.Fatalf("unknown condition %q", artifact.Condition)
		}
		bucket.trials++
		if result.Passed {
			bucket.passed++
		}
		bucket.providerCalls += result.ProviderCalls
		bucket.totalTokens += result.Usage.TotalTokens
	}
	for condition, want := range summary.Combined {
		got := actual[condition]
		if got == nil || got.passed != want.Passed || got.trials != want.Trials || got.providerCalls != want.ProviderCalls || got.totalTokens != want.TotalTokens {
			t.Fatalf("aggregate %s: got %+v want %+v", condition, got, want)
		}
	}
}

func publishedContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
