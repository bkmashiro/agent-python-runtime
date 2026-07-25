package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/eval/dataset"
	evalv1 "github.com/bkmashiro/agent-python-runtime/eval/v1"
)

type schemaManifest struct {
	SchemaVersion string            `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

type recordManifest struct {
	SchemaVersion         string `json:"schema_version"`
	ExperimentID          string `json:"experiment_id"`
	DatasetManifestDigest string `json:"dataset_manifest_digest"`
	PromptManifestDigest  string `json:"prompt_manifest_digest"`
	SchemaManifestDigest  string `json:"schema_manifest_digest"`
	Records               []struct {
		TrialID      string `json:"trial_id"`
		SpecDigest   string `json:"spec_digest"`
		RecordDigest string `json:"record_digest"`
		Path         string `json:"path"`
	} `json:"records"`
}

func readJSONArtifact(path string, target any) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, target) != nil {
		return nil, ErrHarness
	}
	return data, nil
}

func readStrictJSONArtifact(path string, target any) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrHarness
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return nil, ErrHarness
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrHarness
	}
	return data, nil
}

func exactArtifactDirectory(path string, expected int) bool {
	directoryInfo, statErr := os.Lstat(path)
	if statErr != nil || !directoryInfo.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != expected {
		return false
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			return false
		}
	}
	return true
}

// VerifyReplay revalidates a completed scripted run without invoking a provider.
func VerifyReplay(outputDir, schemaDir, datasetRoot, promptManifestPath string) (Summary, error) {
	validators, schemaFiles, err := loadContractValidators(schemaDir)
	if err != nil {
		return Summary{}, ErrHarness
	}
	data, err := dataset.Load(datasetRoot)
	if err != nil {
		return Summary{}, ErrHarness
	}
	promptManifestDigest, _, _, _, err := loadPromptBundle(promptManifestPath)
	if err != nil {
		return Summary{}, ErrHarness
	}
	var boundSchemas schemaManifest
	schemaManifestBytes, err := readStrictJSONArtifact(filepath.Join(outputDir, "schema-manifest.json"), &boundSchemas)
	boundDigest, _ := digestValue(boundSchemas.Files)
	currentDigest, _ := digestValue(schemaFiles)
	if err != nil || boundSchemas.SchemaVersion != "evaluation-schema-manifest/v1" || boundDigest != currentDigest {
		return Summary{}, fmt.Errorf("%w: replay schema manifest", ErrHarness)
	}
	schemaManifestDigest := digestBytes(schemaManifestBytes)
	var experiment any
	experimentBytes, err := readJSONArtifact(filepath.Join(outputDir, "experiment.json"), &experiment)
	experimentMap, ok := experiment.(map[string]any)
	if err != nil || !ok || validateArtifact(validators["experiment-v1"], experiment) != nil {
		return Summary{}, fmt.Errorf("%w: replay experiment", ErrHarness)
	}
	experimentID, _ := experimentMap["experiment_id"].(string)
	experimentDatasetDigest, _ := experimentMap["dataset_manifest_digest"].(string)
	experimentPromptDigest, _ := experimentMap["prompt_manifest_digest"].(string)
	var manifest recordManifest
	manifestBytes, err := readStrictJSONArtifact(filepath.Join(outputDir, "record-manifest.json"), &manifest)
	if err != nil || manifest.SchemaVersion != "evaluation-record-manifest/v1" || manifest.ExperimentID != experimentID || manifest.DatasetManifestDigest != data.ManifestDigest || manifest.DatasetManifestDigest != experimentDatasetDigest || manifest.PromptManifestDigest != experimentPromptDigest || manifest.PromptManifestDigest != promptManifestDigest || manifest.SchemaManifestDigest != schemaManifestDigest || len(manifest.Records) == 0 {
		return Summary{}, fmt.Errorf("%w: replay manifest", ErrHarness)
	}
	expectedTrials := make(map[string]bool)
	for _, scenarioID := range data.IDs("evaluation") {
		for _, condition := range []string{"direct-only", "python-only", "hybrid"} {
			expectedTrials[fmt.Sprintf("trial_%s_%s", scenarioID, condition)] = true
		}
	}
	if len(manifest.Records) != len(expectedTrials) {
		return Summary{}, fmt.Errorf("%w: replay planned trial count", ErrHarness)
	}
	if !exactArtifactDirectory(filepath.Join(outputDir, "records"), len(manifest.Records)) || !exactArtifactDirectory(filepath.Join(outputDir, "specs"), len(manifest.Records)) {
		return Summary{}, fmt.Errorf("%w: replay file set", ErrHarness)
	}
	observations := make([]trialObservation, 0, len(manifest.Records))
	expectedSafety := zeroSafety()
	seen := make(map[string]bool, len(manifest.Records))
	seenPaths := make(map[string]bool, len(manifest.Records))
	for _, entry := range manifest.Records {
		clean := filepath.Clean(entry.Path)
		if filepath.IsAbs(clean) || clean == ".." || len(clean) < 8 || clean[:8] != "records/" || seen[entry.TrialID] || !expectedTrials[entry.TrialID] || seenPaths[clean] || !validDigest(entry.SpecDigest) || !validDigest(entry.RecordDigest) {
			return Summary{}, fmt.Errorf("%w: replay record path", ErrHarness)
		}
		seen[entry.TrialID] = true
		seenPaths[clean] = true
		recordPath := filepath.Join(outputDir, clean)
		recordInfo, statErr := os.Lstat(recordPath)
		if statErr != nil || !recordInfo.Mode().IsRegular() {
			return Summary{}, fmt.Errorf("%w: replay record file %s", ErrHarness, entry.TrialID)
		}
		var record map[string]any
		recordBytes, readErr := readJSONArtifact(recordPath, &record)
		if readErr != nil || digestBytes(recordBytes) != entry.RecordDigest || validateArtifact(validators["trial-record-v1"], record) != nil || record["trial_id"] != entry.TrialID || record["trial_spec_digest"] != entry.SpecDigest {
			return Summary{}, fmt.Errorf("%w: replay record %s", ErrHarness, entry.TrialID)
		}
		specPath := filepath.Join(outputDir, "specs", filepath.Base(clean))
		specInfo, statErr := os.Lstat(specPath)
		if statErr != nil || !specInfo.Mode().IsRegular() {
			return Summary{}, fmt.Errorf("%w: replay spec file %s", ErrHarness, entry.TrialID)
		}
		var spec map[string]any
		specBytes, readErr := readJSONArtifact(specPath, &spec)
		if readErr != nil || digestBytes(specBytes) != entry.SpecDigest || validateArtifact(validators["trial-spec-v1"], spec) != nil || spec["trial_id"] != entry.TrialID || spec["experiment_id"] != experimentID || record["trial_id"] != spec["trial_id"] {
			return Summary{}, fmt.Errorf("%w: replay spec %s", ErrHarness, entry.TrialID)
		}
		scenarioID, scenarioOK := spec["scenario_id"].(string)
		condition, conditionOK := spec["condition"].(string)
		scenario, datasetOK := data.Scenarios[scenarioID]
		identity, identityOK := spec["identities"].(map[string]any)
		if !scenarioOK || !conditionOK || !datasetOK || !identityOK || fmt.Sprintf("trial_%s_%s", scenarioID, condition) != entry.TrialID || identity["scenario_digest"] != data.Entries[scenarioID].SHA256 || identity["fixture_state_digest"] != scenario.Fixture.InitialStateDigest {
			return Summary{}, fmt.Errorf("%w: replay scenario identity %s", ErrHarness, entry.TrialID)
		}
		scoring, scoringOK := record["scoring"].(map[string]any)
		metrics, metricsOK := record["metrics"].(map[string]any)
		safety, safetyOK := record["safety"].(map[string]any)
		if !scoringOK || !metricsOK || !safetyOK || record["exclusion"] != nil {
			return Summary{}, fmt.Errorf("%w: replay observation %s", ErrHarness, entry.TrialID)
		}
		for key, current := range expectedSafety {
			expectedSafety[key] = current.(int) + int(safety[key].(float64))
		}
		observations = append(observations, trialObservation{ScenarioID: scenarioID, Family: scenario.Family, Condition: condition, TaskSuccess: scoring["task_success"] == true, SchemaValid: scoring["output_schema_valid"] == true, Rounds: int(metrics["model_tool_round_trips"].(float64)), TotalTokens: int(metrics["input_tokens"].(float64) + metrics["output_tokens"].(float64) + metrics["intermediate_tokens"].(float64)), WorkflowRuns: int(metrics["workflow_runs"].(float64))})
		// Trial records bind the spec digest; scenario and condition live in the bound spec rather than being duplicated in the record schema.
	}
	if !exactArtifactDirectory(filepath.Join(outputDir, "records"), len(manifest.Records)) || !exactArtifactDirectory(filepath.Join(outputDir, "specs"), len(manifest.Records)) {
		return Summary{}, fmt.Errorf("%w: replay file set", ErrHarness)
	}
	var comparison map[string]any
	comparisonBytes, err := readJSONArtifact(filepath.Join(outputDir, "comparison.json"), &comparison)
	expectedAggregates := buildAggregates(observations, []string{"direct-only", "python-only", "hybrid"})
	expectedContrasts := buildContrasts(observations)
	expectedCompleteness := map[string]any{"planned_trials": len(expectedTrials), "observed_trials": len(expectedTrials), "included_trials": len(expectedTrials), "record_manifest_digest": digestBytes(manifestBytes), "missing_trial_ids": []string{}, "duplicate_trial_ids": []string{}, "unregistered_trial_ids": []string{}}
	provider, providerOK := experimentMap["provider"].(map[string]any)
	aggregatesDigest, _ := digestValue(comparison["aggregates"])
	expectedAggregatesDigest, _ := digestValue(expectedAggregates)
	contrastsDigest, _ := digestValue(comparison["paired_contrasts"])
	expectedContrastsDigest, _ := digestValue(expectedContrasts)
	safetyDigest, _ := digestValue(comparison["safety_totals"])
	expectedSafetyDigest, _ := digestValue(expectedSafety)
	completenessDigest, _ := digestValue(comparison["completeness"])
	expectedCompletenessDigest, _ := digestValue(expectedCompleteness)
	exclusions, exclusionsOK := comparison["exclusions"].([]any)
	if err != nil || !providerOK || provider["protocol"] != "scripted" || comparison["decision"] != "inconclusive" || !exclusionsOK || len(exclusions) != 0 || aggregatesDigest != expectedAggregatesDigest || contrastsDigest != expectedContrastsDigest || safetyDigest != expectedSafetyDigest || completenessDigest != expectedCompletenessDigest || validateArtifact(validators["comparison-v1"], comparison) != nil || comparison["experiment_id"] != experimentID || comparison["experiment_digest"] != digestBytes(experimentBytes) || comparison["trial_record_set_digest"] != digestBytes(manifestBytes) {
		return Summary{}, fmt.Errorf("%w: replay comparison", ErrHarness)
	}
	if err := evalv1.ValidateComparison(experimentBytes, comparisonBytes); err != nil {
		return Summary{}, fmt.Errorf("%w: replay semantic comparison", ErrHarness)
	}
	var summary Summary
	if _, err := readStrictJSONArtifact(filepath.Join(outputDir, "summary.json"), &summary); err != nil || summary.Trials != len(manifest.Records) || summary.RecordSetDigest != digestBytes(manifestBytes) || summary.ComparisonDecision != comparison["decision"] {
		return Summary{}, fmt.Errorf("%w: replay summary", ErrHarness)
	}
	return summary, nil
}
