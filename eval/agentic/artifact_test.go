package agentic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	adapter.preserveMissingInstructions = true
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), adapter, task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestValidateTrialResultV4BindsTreatmentEchoAndKeepsV1V2V3Readable(t *testing.T) {
	valid := validTrialResult(t)
	if valid.Version != "agentic-development-trial/v4" || valid.Metrics == nil || valid.TreatmentID != TreatmentBaselineV1 || valid.TreatmentDigest != BaselineTreatment().Digest {
		t.Fatalf("runner did not emit v4 treatment metrics: %+v", valid)
	}
	for _, exchange := range valid.Exchanges {
		if exchange.InstructionsEcho != InstructionsEchoUnavailable || exchange.ProtocolInvalid {
			t.Fatalf("unavailable echo evidence=%+v", exchange)
		}
	}
	if err := ValidateTrialResult(valid); err != nil {
		t.Fatal(err)
	}
	tampered := valid
	tampered.TreatmentDigest = "sha256:" + strings.Repeat("e", 64)
	if err := ValidateTrialResult(tampered); err == nil {
		t.Fatal("inconsistent treatment digest accepted")
	}
	tampered = valid
	tampered.Exchanges = append([]ExchangeEvidence(nil), valid.Exchanges...)
	tampered.Exchanges[0].InstructionsEcho = InstructionsEchoNotApplicable
	if err := ValidateTrialResult(tampered); err == nil {
		t.Fatal("v4 formal trial accepted not-applicable instructions echo")
	}
	tampered = valid
	tampered.Exchanges = append([]ExchangeEvidence(nil), valid.Exchanges...)
	tampered.Exchanges[0].InstructionsEcho = ""
	if err := ValidateTrialResult(tampered); err == nil {
		t.Fatal("v4 exchange without instructions echo state accepted")
	}
	tampered = valid
	metrics := *valid.Metrics
	metrics.OutcomeSuccess = !metrics.OutcomeSuccess
	tampered.Metrics = &metrics
	if err := ValidateTrialResult(tampered); err == nil {
		t.Fatal("inconsistent v4 metrics accepted")
	}
	legacyV3 := valid
	legacyV3.Version = "agentic-development-trial/v3"
	legacyV3.Exchanges = append([]ExchangeEvidence(nil), valid.Exchanges...)
	for index := range legacyV3.Exchanges {
		legacyV3.Exchanges[index].InstructionsEcho = ""
	}
	if err := ValidateTrialResult(legacyV3); err != nil {
		t.Fatalf("legacy v3 rejected: %v", err)
	}
	legacyV2 := legacyV3
	legacyV2.Version = "agentic-development-trial/v2"
	legacyV2.TreatmentID, legacyV2.TreatmentDigest = "", ""
	if err := ValidateTrialResult(legacyV2); err != nil {
		t.Fatalf("legacy v2 rejected: %v", err)
	}
	legacyV1 := legacyV2
	legacyV1.Version = "agentic-development-trial/v1"
	legacyV1.Metrics = nil
	if err := ValidateTrialResult(legacyV1); err != nil {
		t.Fatalf("legacy v1 rejected: %v", err)
	}
	legacyV1.Metrics = valid.Metrics
	if err := ValidateTrialResult(legacyV1); err == nil {
		t.Fatal("legacy v1 accepted later metrics")
	}
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

type failingPythonWorkflow struct{}

func (failingPythonWorkflow) Execute(context.Context, string, string, uint32) (PythonRunResult, error) {
	return PythonRunResult{}, errors.New("engine unavailable")
}
func (failingPythonWorkflow) Close(context.Context) error { return nil }

func TestProviderUsageOvershootProducesValidAbortArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	response := responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"c1","name":"pwd","arguments":"{}"}]}`, 5, 4_000)
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), &scriptedAdapter{responses: []provider.Response{response}}, task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "provider_output_limit_exceeded" || result.ProviderAttempts != 1 || result.ProviderCalls != 1 || result.Usage.OutputTokens != 4_000 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
	tampered := result
	tampered.ErrorCode = "provider_budget_exceeded"
	if ValidateTrialResult(tampered) == nil {
		t.Fatal("per-exchange output overshoot accepted as aggregate budget exhaustion")
	}
}

func TestIncompleteProviderResponseOvershootProducesProtocolArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	response := responseFixture(`{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`, 5, 4_000)
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), &scriptedAdapter{responses: []provider.Response{response}}, task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "provider_or_protocol_failure" || len(result.Exchanges) != 1 || !result.Exchanges[0].ProtocolInvalid || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
	tampered := result
	tampered.ErrorCode = "provider_output_limit_exceeded"
	if ValidateTrialResult(tampered) == nil {
		t.Fatal("protocol-invalid response accepted as output-limit failure")
	}
}

func TestProviderIdentityMismatchBindsInvalidEchoArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	response := responseFixture(`{"model":"wrong-model","status":"completed","output":[]}`, 5, 1)
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), &scriptedAdapter{responses: []provider.Response{response}}, task, ConditionDirect, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "provider_identity_mismatch" || len(result.Exchanges) != 1 || result.Exchanges[0].InstructionsEcho != InstructionsEchoInvalid || !result.Exchanges[0].ProtocolInvalid || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
	tampered := result
	tampered.Exchanges = append([]ExchangeEvidence(nil), result.Exchanges...)
	tampered.Exchanges[0].InstructionsEcho = InstructionsEchoUnavailable
	if ValidateTrialResult(tampered) == nil {
		t.Fatal("provider identity mismatch accepted without invalid echo evidence")
	}
}

func TestPythonEngineFailureProducesValidAttemptOnlyArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"py1","name":"run_python","arguments":"{\"code\":\"result = {}\"}"}]}`, 5, 5)}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
		GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	result, err := RunDevelopmentTrialWithIdentity(context.Background(), adapter, task, ConditionPython, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, func(*ToolRuntime) (PythonWorkflow, error) { return failingPythonWorkflow{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "python_engine_failure" || result.PythonAttempts != 1 || result.PythonRuns != 0 || len(result.PythonEvidence) != 0 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestMissingUsageProducesValidFailureArtifactWithoutInventedUsage(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	response := responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"c1","name":"pwd","arguments":"{}"}]}`, 1, 1)
	response.Usage = nil
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64),
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
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
		ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64), ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
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
