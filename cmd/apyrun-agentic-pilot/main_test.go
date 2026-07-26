package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func TestPilotSummaryCarriesTreatmentScopeBounds(t *testing.T) {
	summary := pilotSummary{Bounds: executionBounds{TrialCount: 1, MaxProviderAttempts: 4, MaxPythonRuns: 4, MaxTotalTokens: 84_000}}
	encoded, err := json.Marshal(summary)
	if err != nil || !strings.Contains(string(encoded), `"max_provider_attempts":4`) || !strings.Contains(string(encoded), `"max_python_runs":4`) {
		t.Fatalf("encoded=%s err=%v", encoded, err)
	}
}

func TestPilotSummaryAggregatesOutcomeAndStrictSeparately(t *testing.T) {
	summary := pilotSummary{}
	results := []agentic.TrialResult{
		{Metrics: &agentic.TrialMetrics{OutcomeSuccess: true, StrictPass: false}},
		{Metrics: &agentic.TrialMetrics{OutcomeSuccess: true, StrictPass: true}},
		{Metrics: &agentic.TrialMetrics{OutcomeSuccess: false, StrictPass: false}},
	}
	for _, result := range results {
		if err := summary.recordTrialMetrics(result); err != nil {
			t.Fatal(err)
		}
	}
	if summary.OutcomeSuccessfulTrials != 2 || summary.StrictPassedTrials != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	if err := summary.recordTrialMetrics(agentic.TrialResult{}); err == nil {
		t.Fatal("missing metrics accepted")
	}
}

func TestAbortPilotIncludesProviderOutputLimitViolation(t *testing.T) {
	if !abortPilot("provider_output_limit_exceeded") {
		t.Fatal("provider output limit violation did not fail closed")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestPilotRejectsBaselineTreatmentOverrideBeforeExecution(t *testing.T) {
	root := repositoryRoot(t)
	dataset := filepath.Join(root, "eval", "agentic", "v1")
	planPath := filepath.Join(dataset, "development-pilot-plan.json")
	baseArgs := []string{
		"--diagnostic-task", "bfcl-v4-stateful-local-tools-multi_turn_base_12",
		"--diagnostic-condition", "python", "--dataset", dataset, "--plan", planPath,
		"--activation", "unused", "--guest", "unused", "--out", filepath.Join(t.TempDir(), "out"),
		"--repository-commit", strings.Repeat("a", 40),
	}
	for _, file := range []string{"baseline-v1.json"} {
		args := append(append([]string(nil), baseArgs...), "--treatment", filepath.Join(dataset, "treatments", file))
		if err := run(context.Background(), args, dependencies{}); !errors.Is(err, agentic.ErrDevelopmentTreatment) {
			t.Fatalf("file=%s err=%v", file, err)
		}
	}
}

func TestRunRejectsGuestDigestBeforeAdapterOrOutput(t *testing.T) {
	root := repositoryRoot(t)
	dataset := filepath.Join(root, "eval", "agentic", "v1")
	planPath := filepath.Join(dataset, "development-pilot-plan.json")
	plan, _, err := agentic.LoadDevelopmentPilotPlan(planPath, dataset)
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "host")
	guest := filepath.Join(t.TempDir(), "guest.wasm")
	if os.WriteFile(host, []byte("host-binary"), 0o700) != nil || os.WriteFile(guest, []byte("guest-one"), 0o600) != nil {
		t.Fatal("write artifacts")
	}
	hostDigest, _ := fileDigest(host, 1024)
	activationDocument := map[string]any{
		"schema_version": "agentic-pilot-activation/v1", "status": "approved", "execution_mode": "pilot", "plan_digest": plan.Digest,
		"repository_commit": strings.Repeat("a", 40), "host_artifact_digest": hostDigest,
		"dataset_manifest_digest": plan.DatasetManifestDigest,
		"provider_catalog_digest": "sha256:" + strings.Repeat("d", 64), "provider_catalog_observed_at": "2026-07-26T11:00:00Z",
		"guest_artifacts": map[string]any{"core": "sha256:" + strings.Repeat("c", 64)},
		"approved_by":     "owner", "approved_at": "2026-07-26T12:00:00Z",
	}
	activationBytes, _ := json.Marshal(activationDocument)
	activation := filepath.Join(t.TempDir(), "activation.json")
	if os.WriteFile(activation, activationBytes, 0o600) != nil {
		t.Fatal("write activation")
	}
	adapterCalled := false
	deps := dependencies{
		executablePath: func() (string, error) { return host, nil },
		newAdapter:     func() (provider.Adapter, error) { adapterCalled = true; return nil, errors.New("must not be called") },
	}
	out := filepath.Join(t.TempDir(), "out")
	err = run(context.Background(), []string{
		"--dataset", dataset, "--plan", planPath, "--activation", activation, "--guest", guest,
		"--out", out, "--repository-commit", strings.Repeat("a", 40),
	}, deps)
	if !errors.Is(err, agentic.ErrPilotActivation) || adapterCalled {
		t.Fatalf("err=%v adapter_called=%v", err, adapterCalled)
	}
	if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output created before gate: %v", statErr)
	}

	guestDigest, _ := fileDigest(guest, 1024)
	activationDocument["guest_artifacts"] = map[string]any{"core": guestDigest}
	activationBytes, _ = json.Marshal(activationDocument)
	validActivation := filepath.Join(t.TempDir(), "activation.json")
	if os.WriteFile(validActivation, activationBytes, 0o600) != nil {
		t.Fatal("write valid activation")
	}
	t.Setenv("LINKAPI_API_KEY", "   ")
	adapterCalled = false
	out = filepath.Join(t.TempDir(), "out")
	err = run(context.Background(), []string{
		"--dataset", dataset, "--plan", planPath, "--activation", validActivation, "--guest", guest,
		"--out", out, "--repository-commit", strings.Repeat("a", 40),
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "credential") || adapterCalled {
		t.Fatalf("err=%v adapter_called=%v", err, adapterCalled)
	}
	if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output created before credential gate: %v", statErr)
	}

	t.Setenv("LINKAPI_API_KEY", "test-only")
	adapterCalled = false
	out = filepath.Join(t.TempDir(), "out")
	err = run(context.Background(), []string{
		"--canary", "--dataset", dataset, "--plan", planPath, "--activation", validActivation, "--guest", guest,
		"--out", out, "--repository-commit", strings.Repeat("a", 40),
	}, deps)
	if !errors.Is(err, agentic.ErrPilotActivation) || adapterCalled {
		t.Fatalf("mode mismatch err=%v adapter_called=%v", err, adapterCalled)
	}
	if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output created before mode gate: %v", statErr)
	}
}

func TestSelectExecutionScopeUsesFixedRepresentativeCanary(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := selectExecutionScope(plan, dataset, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != "canary" || len(scope.TaskIDs) != 2 ||
		scope.TaskIDs[0] != "bfcl-v4-stateful-local-tools-multi_turn_base_12" ||
		scope.TaskIDs[1] != "bfcl-v4-stateless-function-calling-parallel_multiple_112" {
		t.Fatalf("scope=%+v", scope)
	}
	if scope.Bounds.TrialCount != 6 || scope.Bounds.MaxProviderAttempts != 30 || scope.Bounds.MaxPythonRuns != 8 {
		t.Fatalf("bounds=%+v", scope.Bounds)
	}
	if len(scope.Conditions) != 3 || len(scope.Replicates) != 1 || scope.Replicates[0] != 0 {
		t.Fatalf("scope=%+v", scope)
	}
}

func TestSelectExecutionScopeUsesOneAuthorizedDiagnosticTask(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	taskID := "bfcl-v4-stateful-local-tools-multi_turn_base_12"
	scope, err := selectExecutionScope(plan, dataset, false, taskID, "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != "canary" || len(scope.TaskIDs) != 1 || scope.TaskIDs[0] != taskID ||
		scope.Bounds.TrialCount != 3 || scope.Bounds.MaxProviderAttempts != 27 || scope.Bounds.MaxInputTokens != 540000 ||
		scope.Bounds.MaxOutputTokens != 27648 || scope.Bounds.MaxTotalTokens != 567648 ||
		scope.Bounds.MaxToolCalls != 96 || scope.Bounds.MaxPythonRuns != 6 {
		t.Fatalf("scope=%+v", scope)
	}
	if _, err := selectExecutionScope(plan, dataset, true, taskID, ""); !errors.Is(err, agentic.ErrPilotPlan) {
		t.Fatalf("combined canary and diagnostic task err=%v", err)
	}
	if _, err := selectExecutionScope(plan, dataset, false, "missing-task", ""); !errors.Is(err, agentic.ErrPilotPlan) {
		t.Fatalf("unknown diagnostic task err=%v", err)
	}
}

func TestApplyTreatmentBoundsAddsOneRepairOnlyToPythonCapableTrials(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	taskID := "bfcl-v4-stateful-local-tools-multi_turn_base_12"
	treatment, err := agentic.LoadDevelopmentTreatment(filepath.Join(datasetRoot, "treatments", "python-safe-repair-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		condition    agentic.Condition
		wantPython   uint32
		wantProvider uint32
	}{
		{agentic.ConditionDirect, 0, 12},
		{agentic.ConditionPython, 4, 4},
		{agentic.ConditionHybrid, 4, 13},
	} {
		scope, err := selectExecutionScope(plan, dataset, false, taskID, test.condition)
		if err != nil {
			t.Fatal(err)
		}
		scope, err = applyTreatmentBounds(plan, scope, treatment)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Bounds.MaxPythonRuns != test.wantPython || scope.Bounds.MaxProviderAttempts != test.wantProvider {
			t.Fatalf("condition=%s bounds=%+v", test.condition, scope.Bounds)
		}
	}
}

func TestApplyTreatmentBoundsAddsOneRouterCallOnlyToHybridTrials(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	treatment, err := agentic.LoadDevelopmentTreatment(filepath.Join(datasetRoot, "treatments", "hybrid-two-stage-router-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	taskID := "bfcl-v4-stateful-local-tools-multi_turn_base_12"
	for _, test := range []struct {
		condition    agentic.Condition
		wantProvider uint32
	}{
		{agentic.ConditionDirect, 12},
		{agentic.ConditionPython, 3},
		{agentic.ConditionHybrid, 13},
	} {
		scope, err := selectExecutionScope(plan, dataset, false, taskID, test.condition)
		if err != nil {
			t.Fatal(err)
		}
		scope, err = applyTreatmentBounds(plan, scope, treatment)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Bounds.MaxProviderAttempts != test.wantProvider {
			t.Fatalf("condition=%s bounds=%+v", test.condition, scope.Bounds)
		}
	}
}

func TestApplyTreatmentBoundsComposesRouterAndOneRepairForV2(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	treatment, err := agentic.LoadDevelopmentTreatment(filepath.Join(datasetRoot, "treatments", "hybrid-two-stage-safe-repair-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	taskID := "bfcl-v4-stateful-local-tools-multi_turn_base_12"
	for _, test := range []struct {
		condition    agentic.Condition
		wantProvider uint32
		wantPython   uint32
	}{
		{agentic.ConditionDirect, 12, 0},
		{agentic.ConditionPython, 4, 4},
		{agentic.ConditionHybrid, 14, 4},
	} {
		scope, err := selectExecutionScope(plan, dataset, false, taskID, test.condition)
		if err != nil {
			t.Fatal(err)
		}
		scope, err = applyTreatmentBounds(plan, scope, treatment)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Bounds.MaxProviderAttempts != test.wantProvider || scope.Bounds.MaxPythonRuns != test.wantPython {
			t.Fatalf("condition=%s bounds=%+v", test.condition, scope.Bounds)
		}
	}
}

func TestSelectExecutionScopeSupportsOneDiagnosticCondition(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	taskID := "bfcl-v4-stateful-local-tools-multi_turn_base_12"
	scope, err := selectExecutionScope(plan, dataset, false, taskID, agentic.ConditionPython)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != "canary" || len(scope.TaskIDs) != 1 || len(scope.Conditions) != 1 || scope.Conditions[0] != agentic.ConditionPython ||
		scope.Bounds.TrialCount != 1 || scope.Bounds.MaxProviderAttempts != 3 || scope.Bounds.MaxInputTokens != 60000 ||
		scope.Bounds.MaxOutputTokens != 3072 || scope.Bounds.MaxTotalTokens != 63072 ||
		scope.Bounds.MaxToolCalls != 32 || scope.Bounds.MaxPythonRuns != 3 {
		t.Fatalf("scope=%+v", scope)
	}
	if _, err := selectExecutionScope(plan, dataset, false, "", agentic.ConditionPython); !errors.Is(err, agentic.ErrPilotPlan) {
		t.Fatalf("condition without task err=%v", err)
	}
	if _, err := selectExecutionScope(plan, dataset, true, "", agentic.ConditionPython); !errors.Is(err, agentic.ErrPilotPlan) {
		t.Fatalf("condition with representative canary err=%v", err)
	}
}

func TestSelectExecutionScopePreservesCompletePilot(t *testing.T) {
	root := repositoryRoot(t)
	datasetRoot := filepath.Join(root, "eval", "agentic", "v1")
	plan, dataset, err := agentic.LoadDevelopmentPilotPlan(filepath.Join(datasetRoot, "development-pilot-plan.json"), datasetRoot)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := selectExecutionScope(plan, dataset, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if scope.Mode != "pilot" || len(scope.TaskIDs) != len(plan.TaskIDs) || scope.Bounds.TrialCount != plan.GlobalBounds.TrialCount || scope.Bounds.MaxProviderAttempts != plan.GlobalBounds.MaxProviderAttempts {
		t.Fatalf("scope=%+v plan=%+v", scope, plan.GlobalBounds)
	}
}

func TestWriteRawDebugUsesPrivateExclusiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trial-debug.json")
	result := agentic.TrialResult{
		TrialID: "dev_test", TaskID: "task", Condition: agentic.ConditionPython,
		RawDebug: &agentic.TrialRawDebug{
			DeveloperPrompt:   "private prompt",
			ProviderExchanges: []agentic.RawProviderExchange{{Request: json.RawMessage(`{"private":"request"}`), Response: json.RawMessage(`{"private":"response"}`), StatusCode: 200}},
		},
	}
	if err := writeRawDebug(path, result); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("info=%v err=%v", info, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "private prompt") || !strings.Contains(string(body), "private\": \"response") {
		t.Fatalf("body=%s err=%v", body, err)
	}
	if err := writeRawDebug(path, result); err == nil {
		t.Fatal("raw debug overwrite was accepted")
	}
	formal, _ := json.Marshal(result)
	if strings.Contains(string(formal), "private prompt") || strings.Contains(string(formal), "private\":\"request") {
		t.Fatalf("formal result leaked raw debug: %s", formal)
	}
}

func TestAbortPilotSeparatesModelApplicationErrorsFromInfrastructureFailures(t *testing.T) {
	if abortPilot("host_application_error") {
		t.Fatal("model-caused Host application error aborted pilot")
	}
	if !abortPilot("direct_host_call_failed") {
		t.Fatal("unclassified Host/runtime failure did not abort pilot")
	}
}

func TestAbortPilotRejectsProviderIdentityMismatch(t *testing.T) {
	if !abortPilot("provider_identity_mismatch") {
		t.Fatal("provider identity mismatch did not abort pilot")
	}
}
