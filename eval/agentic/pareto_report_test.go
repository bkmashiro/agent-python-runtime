package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func TestCompareHybridTrialCohortBindsIdentityAndDerivesRegret(t *testing.T) {
	task, treatment, direct, python, hybrid := routingCohortFixture(t)
	report, err := CompareHybridTrialCohort(task, treatment, direct, python, hybrid)
	if err != nil {
		t.Fatal(err)
	}
	if report.CohortDigest == "" || report.TaskID != task.ID || report.SelectedRoute != HybridRouteDirect || report.ReasonCode != HybridReasonKnownArguments {
		t.Fatalf("report=%+v", report)
	}
	if report.Comparison.OutcomeRegret != 0 || report.Comparison.StrictRegret != 0 {
		t.Fatalf("comparison=%+v", report.Comparison)
	}
	aggregate, err := AggregateTaskHybridRegret([]TaskHybridRegretReport{report})
	if err != nil || aggregate.Aggregate.Tasks != 1 {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	if _, err := AggregateTaskHybridRegret([]TaskHybridRegretReport{report, report}); err == nil {
		t.Fatal("duplicate cohort accepted")
	}
	mixedReport := report
	mixedReport.Model = "gpt-4.1"
	mixedReport.CohortDigest = "sha256:" + strings.Repeat("1", 64)
	if _, err := AggregateTaskHybridRegret([]TaskHybridRegretReport{report, mixedReport}); err == nil {
		t.Fatal("mixed report model accepted")
	}
	artifact, err := BuildHybridRegretArtifact(treatment, []TaskHybridRegretReport{report})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "regret.json")
	if artifactDigest, err := WriteHybridRegretArtifact(path, treatment, artifact); err != nil || !validDigest(artifactDigest) {
		t.Fatalf("artifact digest=%q err=%v", artifactDigest, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode=%v", info.Mode().Perm())
	}
	if _, err := WriteHybridRegretArtifact(path, treatment, artifact); err == nil {
		t.Fatal("artifact overwrite accepted")
	}

	mixedModel := python
	mixedModel.Model = "gpt-4.1"
	if _, err := CompareHybridTrialCohort(task, treatment, direct, mixedModel, hybrid); err == nil {
		t.Fatal("mixed model cohort accepted")
	}
	mixedPrompt := direct
	mixedPrompt.PromptDigest = "sha256:" + strings.Repeat("0", 64)
	if _, err := CompareHybridTrialCohort(task, treatment, mixedPrompt, python, hybrid); err == nil {
		t.Fatal("mixed prompt cohort accepted")
	}
	mixedCommit := hybrid
	mixedCommit.Identity.RepositoryCommit = strings.Repeat("f", 40)
	if _, err := CompareHybridTrialCohort(task, treatment, direct, python, mixedCommit); err == nil {
		t.Fatal("mixed code cohort accepted")
	}
}

func routingCohortFixture(t *testing.T) (Task, DevelopmentTreatment, TrialResult, TrialResult, TrialResult) {
	t.Helper()
	dataset, err := LoadRoutingDataset(routingDatasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	task := dataset.Tasks[0]
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-router-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	shared := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("b", 64),
		DatasetManifestDigest: dataset.Plan.DatasetManifestDigest, ProviderCatalogDigest: "sha256:" + strings.Repeat("c", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	directIdentity := shared
	guestIdentity := shared
	guestIdentity.GuestArtifactDigest = "sha256:" + strings.Repeat("d", 64)
	guestIdentity.GuestProfile = "core"
	limits := developmentTrialLimits(1)

	directAdapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	direct, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), directAdapter, task, ConditionDirect, developmentModel, 0, limits, directIdentity, treatment, nil)
	if err != nil {
		t.Fatal(err)
	}

	arguments, _ := json.Marshal(map[string]string{"code": "# deterministic oracle fixture"})
	pythonBody := fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"python-0","name":"run_python","arguments":%q}]}`, string(arguments))
	pythonAdapter := &scriptedAdapter{responses: []provider.Response{responseFixture(pythonBody, 10, 3)}}
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		return &oracleWorkflow{tools: tools, oracle: oracle}, nil
	}
	python, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), pythonAdapter, task, ConditionPython, developmentModel, 0, limits, guestIdentity, treatment, factory)
	if err != nil {
		t.Fatal(err)
	}

	routeBody := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-0","name":"select_execution_surface","arguments":"{\"surface\":\"direct\",\"reason_code\":\"known_arguments\"}"}]}`
	hybridResponses := append([]provider.Response{responseFixture(routeBody, 8, 2)}, adapterForStatefulOracle(t, task, func(name string) string { return name }).responses...)
	hybridAdapter := &scriptedAdapter{responses: hybridResponses}
	hybrid, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), hybridAdapter, task, ConditionHybrid, developmentModel, 0, limits, guestIdentity, treatment, factory)
	if err != nil {
		t.Fatal(err)
	}
	return task, treatment, direct, python, hybrid
}
