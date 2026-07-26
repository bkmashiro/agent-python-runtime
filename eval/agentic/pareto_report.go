package agentic

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

type TaskHybridRegretReport struct {
	CohortDigest  string                         `json:"cohort_digest"`
	TaskID        string                         `json:"task_id"`
	Model         string                         `json:"model"`
	Replicate     uint32                         `json:"replicate"`
	TreatmentID   string                         `json:"treatment_id"`
	SelectedRoute HybridRoute                    `json:"selected_route,omitempty"`
	ReasonCode    HybridRouteReason              `json:"reason_code,omitempty"`
	Comparison    HybridCounterfactualComparison `json:"comparison"`
}

type ExperimentHybridRegretReport struct {
	Model       string             `json:"model"`
	TreatmentID string             `json:"treatment_id"`
	Aggregate   HybridRegretReport `json:"aggregate"`
}

type HybridRegretArtifact struct {
	Version         string                       `json:"version"`
	TreatmentDigest string                       `json:"treatment_digest"`
	Tasks           []TaskHybridRegretReport     `json:"tasks"`
	Report          ExperimentHybridRegretReport `json:"report"`
}

func BuildHybridRegretArtifact(treatment DevelopmentTreatment, tasks []TaskHybridRegretReport) (HybridRegretArtifact, error) {
	if !treatment.Implemented() {
		return HybridRegretArtifact{}, ErrCounterfactualComparison
	}
	report, err := AggregateTaskHybridRegret(tasks)
	if err != nil || report.TreatmentID != treatment.ID {
		return HybridRegretArtifact{}, ErrCounterfactualComparison
	}
	return HybridRegretArtifact{
		Version: "hybrid-regret-artifact/v1", TreatmentDigest: treatment.Digest,
		Tasks: append([]TaskHybridRegretReport(nil), tasks...), Report: report,
	}, nil
}

func WriteHybridRegretArtifact(path string, treatment DevelopmentTreatment, artifact HybridRegretArtifact) (string, error) {
	if path == "" || !treatment.Implemented() || artifact.Version != "hybrid-regret-artifact/v1" ||
		artifact.TreatmentDigest != treatment.Digest || artifact.Report.TreatmentID != treatment.ID {
		return "", ErrCounterfactualComparison
	}
	report, err := AggregateTaskHybridRegret(artifact.Tasks)
	if err != nil || report != artifact.Report {
		return "", ErrCounterfactualComparison
	}
	content, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", ErrCounterfactualComparison
	}
	content = append(content, '\n')
	parent := filepath.Dir(path)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrCounterfactualComparison
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remove = false
	return digest(content), nil
}

func AggregateTaskHybridRegret(reports []TaskHybridRegretReport) (ExperimentHybridRegretReport, error) {
	if len(reports) == 0 {
		return ExperimentHybridRegretReport{}, ErrCounterfactualComparison
	}
	model, treatmentID := reports[0].Model, reports[0].TreatmentID
	seen := make(map[string]struct{}, len(reports))
	comparisons := make([]HybridCounterfactualComparison, 0, len(reports))
	for _, report := range reports {
		if report.Model != model || report.TreatmentID != treatmentID || report.TaskID == "" || !validDigest(report.CohortDigest) {
			return ExperimentHybridRegretReport{}, ErrCounterfactualComparison
		}
		if (report.SelectedRoute == "") != (report.ReasonCode == "") ||
			(report.SelectedRoute != "" && ((report.SelectedRoute != HybridRouteDirect && report.SelectedRoute != HybridRoutePython) || !report.ReasonCode.valid())) {
			return ExperimentHybridRegretReport{}, ErrCounterfactualComparison
		}
		if _, exists := seen[report.CohortDigest]; exists {
			return ExperimentHybridRegretReport{}, ErrCounterfactualComparison
		}
		seen[report.CohortDigest] = struct{}{}
		comparisons = append(comparisons, report.Comparison)
	}
	aggregate, err := AggregateHybridRegret(comparisons)
	if err != nil {
		return ExperimentHybridRegretReport{}, err
	}
	return ExperimentHybridRegretReport{Model: model, TreatmentID: treatmentID, Aggregate: aggregate}, nil
}

func CompareHybridTrialCohort(task Task, treatment DevelopmentTreatment, direct, python, hybrid TrialResult) (TaskHybridRegretReport, error) {
	if !treatment.Implemented() || direct.Condition != ConditionDirect || python.Condition != ConditionPython || hybrid.Condition != ConditionHybrid ||
		ValidateTrialResult(direct) != nil || ValidateTrialResult(python) != nil || ValidateTrialResult(hybrid) != nil {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	trials := []TrialResult{direct, python, hybrid}
	taskBytes, err := json.Marshal(task)
	if err != nil {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	taskDigest := digest(taskBytes)
	for _, trial := range trials {
		if trial.TaskID != task.ID || trial.TaskDigest != taskDigest || trial.SourceRecordDigest != task.Source.RecordSHA256 ||
			trial.Model != direct.Model || trial.Replicate != direct.Replicate || trial.TreatmentID != treatment.ID || trial.TreatmentDigest != treatment.Digest {
			return TaskHybridRegretReport{}, ErrCounterfactualComparison
		}
	}
	if !sharedCounterfactualIdentity(direct.Identity, python.Identity, hybrid.Identity) {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	expected, err := expectedCohortSurfaces(task, treatment)
	if err != nil {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	for _, trial := range trials {
		want := expected[trial.Condition]
		if trial.PromptDigest != want.prompt || trial.SurfaceDigest != want.surface {
			return TaskHybridRegretReport{}, ErrCounterfactualComparison
		}
	}
	if direct.Metrics == nil || python.Metrics == nil || hybrid.Metrics == nil ||
		direct.Usage.TotalTokens > math.MaxInt64 || python.Usage.TotalTokens > math.MaxInt64 || hybrid.Usage.TotalTokens > math.MaxInt64 {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	comparison, err := CompareHybridCounterfactuals(routeOutcomeFromTrial(direct), routeOutcomeFromTrial(python), routeOutcomeFromTrial(hybrid))
	if err != nil {
		return TaskHybridRegretReport{}, err
	}
	identity := map[string]any{
		"version": "hybrid-counterfactual-cohort/v1", "task_id": task.ID, "task_digest": taskDigest,
		"model": direct.Model, "replicate": direct.Replicate, "treatment_id": treatment.ID, "treatment_digest": treatment.Digest,
		"repository_commit": direct.Identity.RepositoryCommit, "host_artifact_digest": direct.Identity.HostArtifactDigest,
		"guest_artifact_digest": python.Identity.GuestArtifactDigest, "guest_profile": python.Identity.GuestProfile,
		"dataset_manifest_digest": direct.Identity.DatasetManifestDigest, "provider_catalog_digest": direct.Identity.ProviderCatalogDigest,
		"provider_catalog_observed_at": direct.Identity.ProviderCatalogObservedAt,
		"direct_prompt_digest":         direct.PromptDigest, "direct_surface_digest": direct.SurfaceDigest,
		"python_prompt_digest": python.PromptDigest, "python_surface_digest": python.SurfaceDigest,
		"hybrid_prompt_digest": hybrid.PromptDigest, "hybrid_surface_digest": hybrid.SurfaceDigest,
	}
	identityBytes, err := json.Marshal(identity)
	if err != nil {
		return TaskHybridRegretReport{}, ErrCounterfactualComparison
	}
	report := TaskHybridRegretReport{
		CohortDigest: digest(identityBytes), TaskID: task.ID, Model: direct.Model, Replicate: direct.Replicate,
		TreatmentID: treatment.ID, Comparison: comparison,
	}
	if hybrid.Route != nil {
		report.SelectedRoute = hybrid.Route.Route
		report.ReasonCode = hybrid.Route.ReasonCode
	}
	return report, nil
}

type expectedCohortSurface struct {
	prompt  string
	surface string
}

func expectedCohortSurfaces(task Task, treatment DevelopmentTreatment) (map[Condition]expectedCohortSurface, error) {
	runtime, err := NewToolRuntime(task)
	if err != nil {
		return nil, err
	}
	result := make(map[Condition]expectedCohortSurface, 3)
	for _, condition := range []Condition{ConditionDirect, ConditionPython} {
		continueWithinTurn := task.Track == "stateful_local_tools" && condition != ConditionPython
		surface, _, prompt, err := buildConditionSurface(runtime, condition, continueWithinTurn)
		if err != nil {
			return nil, err
		}
		surfaceDigest, err := digestJSON(surface)
		if err != nil {
			return nil, err
		}
		result[condition] = expectedCohortSurface{prompt: digest([]byte(prompt)), surface: surfaceDigest}
	}
	if treatment.UsesTwoStageRouter() {
		_, _, promptDigest, surfaceDigest, err := buildHybridRouterContract(task)
		if err != nil {
			return nil, err
		}
		result[ConditionHybrid] = expectedCohortSurface{prompt: promptDigest, surface: surfaceDigest}
	} else {
		surface, _, prompt, err := buildConditionSurface(runtime, ConditionHybrid, task.Track == "stateful_local_tools")
		if err != nil {
			return nil, err
		}
		surfaceDigest, err := digestJSON(surface)
		if err != nil {
			return nil, err
		}
		result[ConditionHybrid] = expectedCohortSurface{prompt: digest([]byte(prompt)), surface: surfaceDigest}
	}
	return result, nil
}

func sharedCounterfactualIdentity(direct, python, hybrid ExecutionIdentity) bool {
	if direct.GuestArtifactDigest != "" || direct.GuestProfile != "" || python.GuestArtifactDigest == "" || python.GuestProfile == "" ||
		python.GuestArtifactDigest != hybrid.GuestArtifactDigest || python.GuestProfile != hybrid.GuestProfile {
		return false
	}
	return direct.RepositoryCommit == python.RepositoryCommit && direct.RepositoryCommit == hybrid.RepositoryCommit &&
		direct.HostArtifactDigest == python.HostArtifactDigest && direct.HostArtifactDigest == hybrid.HostArtifactDigest &&
		direct.DatasetManifestDigest == python.DatasetManifestDigest && direct.DatasetManifestDigest == hybrid.DatasetManifestDigest &&
		direct.ProviderCatalogDigest == python.ProviderCatalogDigest && direct.ProviderCatalogDigest == hybrid.ProviderCatalogDigest &&
		direct.ProviderCatalogObservedAt == python.ProviderCatalogObservedAt && direct.ProviderCatalogObservedAt == hybrid.ProviderCatalogObservedAt
}

func routeOutcomeFromTrial(result TrialResult) RouteOutcome {
	return RouteOutcome{
		OutcomeSuccess: result.Metrics.OutcomeSuccess,
		StrictPass:     result.Metrics.StrictPass,
		ProviderCalls:  result.ProviderCalls,
		TotalTokens:    int64(result.Usage.TotalTokens),
	}
}
