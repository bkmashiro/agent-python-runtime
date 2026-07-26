package agentic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func validTrialResult(t *testing.T) TrialResult {
	t.Helper()
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
	}
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), adapterForStatefulOracle(t, task, func(name string) string { return name }), task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestValidateTrialResultBindsUsageConditionAndScores(t *testing.T) {
	valid := validTrialResult(t)
	if err := ValidateTrialResult(valid); err != nil {
		t.Fatal(err)
	}
	cases := []TrialResult{valid, valid, valid, valid}
	cases[0].ProviderCalls++
	cases[1].Usage.TotalTokens++
	cases[2].PythonRuns = 1
	cases[3].Passed = !cases[3].Passed
	for index, candidate := range cases {
		if err := ValidateTrialResult(candidate); err == nil {
			t.Fatalf("invalid case %d accepted", index)
		}
	}
}

func TestMissingUsageProducesValidFailureArtifactWithoutInventedUsage(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	response := responseFixture(`{"output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"}]}`, 1, 1)
	response.Usage = nil
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
	}
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), &scriptedAdapter{responses: []provider.Response{response}}, task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "usage_missing" || result.ProviderAttempts != 1 || result.ProviderCalls != 0 || result.Usage.TotalTokens != 0 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestExecutionIdentityRequiresGuestOnlyForPythonSurfaces(t *testing.T) {
	base := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
	}
	if !validExecutionIdentity(base, ConditionDirect) || validExecutionIdentity(base, ConditionPython) {
		t.Fatal("base identity condition boundary failed")
	}
	base.GuestArtifactDigest = "sha256:" + strings.Repeat("c", 64)
	base.GuestProfile = "core"
	if validExecutionIdentity(base, ConditionDirect) || !validExecutionIdentity(base, ConditionPython) || !validExecutionIdentity(base, ConditionHybrid) {
		t.Fatal("guest identity condition boundary failed")
	}
}

func TestWriteTrialArtifactIsExclusivePrivateAndDigestBound(t *testing.T) {
	result := validTrialResult(t)
	path := filepath.Join(t.TempDir(), "trial.json")
	artifactDigest, err := WriteTrialArtifact(path, result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteTrialArtifact(path, result); err == nil {
		t.Fatal("artifact overwrite accepted")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if artifactDigest != "sha256:"+hex.EncodeToString(sum[:]) || len(content) == 0 || content[len(content)-1] != '\n' {
		t.Fatalf("digest=%s bytes=%d", artifactDigest, len(content))
	}
}
