package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func routingDatasetRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("failed to resolve routing dataset root")
	}
	return filepath.Join(filepath.Dir(filename), "routing", "v1")
}

func TestRoutingDatasetLoadAndDigestBinding(t *testing.T) {
	roo := routingDatasetRoot(t)
	dataset, err := LoadRoutingDataset(roo)
	if err != nil {
		t.Fatalf("load routing dataset: %v", err)
	}
	if got, want := len(dataset.Tasks), 6; got != want {
		t.Fatalf("task count = %d, want %d", got, want)
	}
	ids := make([]string, 0, len(dataset.Tasks))
	counts := map[string]int{}
	for _, task := range dataset.Tasks {
		ids = append(ids, task.ID)
		counts[dataset.Archetypes[task.ID]]++
	}
	if !reflect.DeepEqual(ids, []string{"rd-001", "rd-002", "rd-003", "rd-004", "rd-005", "rd-006"}) {
		t.Fatalf("task IDs = %v", ids)
	}
	if counts["direct_favored"] != 2 || counts["python_favored"] != 2 || counts["boundary"] != 2 {
		t.Fatalf("archetype counts = %v", counts)
	}
	if dataset.Plan.Status != "frozen_development" {
		t.Fatalf("plan status = %q", dataset.Plan.Status)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(roo, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if dataset.Plan.DatasetManifestDigest != digest(manifestBytes) {
		t.Fatalf("manifest binding mismatch: plan=%s digest=%s", dataset.Plan.DatasetManifestDigest, digest(manifestBytes))
	}
	packBytes, err := os.ReadFile(filepath.Join(roo, dataset.Manifest.Pack))
	if err != nil {
		t.Fatalf("read pack: %v", err)
	}
	if dataset.Plan.TaskPackDigest != digest(packBytes) {
		t.Fatalf("pack binding mismatch: plan=%s digest=%s", dataset.Plan.TaskPackDigest, digest(packBytes))
	}
	if dataset.Manifest.PackDigest != digest(packBytes) {
		t.Fatalf("pack digest mismatch: manifest=%s actual=%s", dataset.Manifest.PackDigest, digest(packBytes))
	}
	for _, entry := range dataset.Manifest.Tasks {
		raw, err := os.ReadFile(filepath.Join(roo, entry.Path))
		if err != nil {
			t.Fatalf("read task %s: %v", entry.ID, err)
		}
		if digest(raw) != entry.SHA256 {
			t.Fatalf("digest mismatch: task=%s manifest=%s actual=%s", entry.ID, entry.SHA256, digest(raw))
		}
		if strings.Contains(string(raw), "\"archetype\"") || strings.Contains(string(raw), "direct_favored") || strings.Contains(string(raw), "python_favored") || strings.Contains(string(raw), "boundary") {
			t.Fatalf("task %s leaks private metadata in model-visible JSON", entry.ID)
		}
	}

	for _, source := range dataset.Manifest.Sources[0].SourceFiles {
		raw, err := os.ReadFile(filepath.Join(roo, source.Path))
		if err != nil || digest(raw) != source.SHA256 {
			t.Fatalf("source binding failed for %s: %v", source.Path, err)
		}
		if strings.Contains(string(raw), "archetype") || strings.Contains(string(raw), "direct_favored") || strings.Contains(string(raw), "python_favored") || strings.Contains(string(raw), "boundary") {
			t.Fatalf("source record %s leaks private routing label", source.Path)
		}
	}

	// Ensure BFCL loader still works unchanged.
	if _, err := Load(datasetRoot(t)); err != nil {
		t.Fatalf("external BFCL dataset load failed: %v", err)
	}
}

func TestRoutingDatasetOracleReplayByteForByte(t *testing.T) {
	dataset, err := LoadRoutingDataset(routingDatasetRoot(t))
	if err != nil {
		t.Fatalf("load routing dataset: %v", err)
	}
	for _, task := range dataset.Tasks {
		t.Run(task.ID, func(t *testing.T) {
			replayStatefulOracleFromTask(t, task)
		})
	}
}

func replayStatefulOracleFromTask(t *testing.T, task Task) {
	t.Helper()
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil {
		t.Fatalf("decode oracle")
	}
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatalf("new tool runtime: %v", err)
	}
	for turnIndex, turn := range oracle.Turns {
		if err := runtime.SetTurn(turnIndex); err != nil {
			t.Fatal(err)
		}
		for callIndex, call := range turn {
			if _, err := runtime.InvokeDirect(context.Background(), "routing-diagnostic", fmt.Sprintf("%s-%d-%d", task.ID, turnIndex, callIndex), call.Name, call.Arguments); err != nil {
				t.Fatalf("replay %s: %v", call.Name, err)
			}
		}
	}
	score, err := ScoreStateful(task, runtime.Trace(), runtime.FileSystem())
	if err != nil {
		t.Fatalf("score stateful: %v", err)
	}
	if !score.Passed {
		t.Fatalf("not passed: %+v", score)
	}
}

func TestRoutingDatasetConditionExecutionSurfaces(t *testing.T) {
	dataset, err := LoadRoutingDataset(routingDatasetRoot(t))
	if err != nil {
		t.Fatalf("load routing dataset: %v", err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit:          strings.Repeat("a", 40),
		HostArtifactDigest:        "sha256:" + strings.Repeat("b", 64),
		DatasetManifestDigest:     "sha256:" + strings.Repeat("c", 64),
		ProviderCatalogDigest:     "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
		GuestArtifactDigest:       "sha256:" + strings.Repeat("e", 64),
		GuestProfile:              "core",
	}

	for _, task := range dataset.Tasks {
		t.Run(task.ID, func(t *testing.T) {
			t.Run("ConditionDirect", func(t *testing.T) {
				direct := adapterForStatefulOracle(t, task, func(name string) string { return name })
				limits := developmentTrialLimits(len(task.Interaction.Turns))
				directIdentity := identity
				directIdentity.GuestArtifactDigest = ""
				directIdentity.GuestProfile = ""
				result, err := RunDevelopmentDiagnosticTrialForModelWithIdentity(context.Background(), direct, task, ConditionDirect, developmentModel, 0, limits, directIdentity, nil)
				if err != nil {
					t.Fatalf("direct trial: %v", err)
				}
				if !result.Passed || result.StatefulScore == nil || !result.StatefulScore.Passed {
					t.Fatalf("direct trial did not pass: %+v", result)
				}
				if err := ValidateTrialResult(result); err != nil {
					t.Fatalf("direct result invalid: %v", err)
				}
			})

			t.Run("ConditionPython", func(t *testing.T) {
				responses := make([]provider.Response, 0, len(task.Interaction.Turns))
				for turn := range task.Interaction.Turns {
					arguments, marshalErr := json.Marshal(map[string]string{"code": "# deterministic oracle fixture"})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					body := fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"python-%d","name":"run_python","arguments":%q}]}`, turn, string(arguments))
					responses = append(responses, responseFixture(body, 10, 3))
				}
				pythonAdapter := &scriptedAdapter{responses: responses}
				factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
					var oracle StatefulOracle
					if decodeStrict(task.Oracle, &oracle) != nil {
						return nil, ErrDataset
					}
					return &oracleWorkflow{tools: tools, oracle: oracle}, nil
				}
				limits := developmentTrialLimits(len(task.Interaction.Turns))
				result, err := RunDevelopmentDiagnosticTrialForModelWithIdentity(context.Background(), pythonAdapter, task, ConditionPython, developmentModel, 0, limits, identity, factory)
				if err != nil {
					t.Fatalf("python trial: %v", err)
				}
				if !result.Passed || result.StatefulScore == nil || !result.StatefulScore.Passed {
					t.Fatalf("python trial did not pass: %+v", result)
				}
				if err := ValidateTrialResult(result); err != nil {
					t.Fatalf("python result invalid: %v", err)
				}
			})
		})
	}
}
