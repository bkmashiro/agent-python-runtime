package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const (
	gitChameleonNumPyManifestSchema = "pysolate.gitchameleon-numpy-derived-plm.v1"
	gitChameleonNumPyEvidenceSchema = "pysolate.gitchameleon-numpy-derived-plm.evidence.v1"
)

type gitChameleonNumPyManifest struct {
	SchemaVersion   string `json:"schema_version"`
	ProviderDelayMS int    `json:"provider_delay_ms"`
	TaskCount       int    `json:"task_count"`
	Dataset         struct {
		Name          string `json:"name"`
		Commit        string `json:"commit"`
		SHA256        string `json:"sha256"`
		ScannedRows   int    `json:"scanned_rows"`
		SelectionRule string `json:"selection_rule"`
	} `json:"dataset"`
	MockStream struct {
		Tokenizer            string `json:"tokenizer"`
		TokenizerVersion     string `json:"tokenizer_version"`
		BatchSize            int    `json:"batch_size"`
		ClockScope           string `json:"clock_scope"`
		RatesTokensPerSecond []int  `json:"rates_tokens_per_second"`
	} `json:"mock_stream"`
	Tasks []gitChameleonNumPyTask `json:"tasks"`
}

type gitChameleonNumPyTask struct {
	ExampleID          string                      `json:"example_id"`
	DatasetRow         int                         `json:"dataset_row"`
	TargetNumPyVersion string                      `json:"target_numpy_version"`
	API                string                      `json:"api"`
	Inputs             []gitChameleonNumPyInput    `json:"inputs"`
	Prefix             string                      `json:"prefix"`
	Suffix             string                      `json:"suffix"`
	SuffixTokens       int                         `json:"suffix_tokens"`
	SourceSHA256       string                      `json:"source_sha256"`
	Oracle             string                      `json:"oracle"`
	Cells              []gitChameleonNumPyTaskCell `json:"cells"`
}

type gitChameleonNumPyInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Body string `json:"body"`
}

type gitChameleonNumPyTaskCell struct {
	TokensPerSecond int `json:"tokens_per_second"`
	SourceWindowMS  int `json:"source_window_ms"`
}

type gitChameleonNumPyEvidence struct {
	plmPrefixEagerEvidence
	SourceTreeState      string `json:"source_tree_state"`
	ManifestSHA256       string `json:"manifest_sha256"`
	DatasetName          string `json:"dataset_name"`
	DatasetCommit        string `json:"dataset_commit"`
	DatasetSHA256        string `json:"dataset_sha256"`
	DatasetRow           int    `json:"dataset_row"`
	ExampleID            string `json:"example_id"`
	TargetNumPyVersion   string `json:"target_numpy_version"`
	API                  string `json:"api"`
	InputCount           int    `json:"input_count"`
	SuffixTokens         int    `json:"suffix_tokens"`
	TokensPerSecond      int    `json:"tokens_per_second"`
	SourceWindowMS       int    `json:"source_window_ms"`
	MockStreamTokenizer  string `json:"mock_stream_tokenizer"`
	MockTokenizerVersion string `json:"mock_stream_tokenizer_version"`
}

func loadGitChameleonNumPyManifest(t *testing.T) (gitChameleonNumPyManifest, []byte) {
	t.Helper()
	path := filepath.Join("testdata", "gitchameleon_numpy_subset_v1.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest gitChameleonNumPyManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, encoded
}

func TestGitChameleonNumPyDerivedManifestCoversDeclaredSubset(t *testing.T) {
	manifest, _ := loadGitChameleonNumPyManifest(t)
	if manifest.SchemaVersion != gitChameleonNumPyManifestSchema || manifest.TaskCount != 15 || len(manifest.Tasks) != 15 {
		t.Fatalf("manifest identity or task count drift: %+v", manifest)
	}
	if manifest.Dataset.Commit != "3a1b6045a6b2a276bd24d715589cb041f8eccb93" || manifest.Dataset.SHA256 != "978c7c581cad399989cb8399ec208ddd0edb6260ef576b3ce442aeaae455609e" {
		t.Fatalf("dataset identity drift: %+v", manifest.Dataset)
	}
	if manifest.ProviderDelayMS != 200 || fmt.Sprint(manifest.MockStream.RatesTokensPerSecond) != "[20 50 100 200]" {
		t.Fatalf("experiment dimensions drift: delay=%d rates=%v", manifest.ProviderDelayMS, manifest.MockStream.RatesTokensPerSecond)
	}
	oneInput, twoInputs := 0, 0
	for index, task := range manifest.Tasks {
		expectedID := strconv.Itoa(66 + index)
		if task.ExampleID != expectedID || task.DatasetRow != 67+index {
			t.Fatalf("subset ordering drift at %d: %+v", index, task)
		}
		if len(task.Cells) != 4 || task.SuffixTokens <= 0 || len(task.Inputs) < 1 || len(task.Inputs) > 2 {
			t.Fatalf("task dimensions drift: %+v", task)
		}
		if got := strings.Count(task.Prefix, "sources.read("); got != len(task.Inputs) || strings.Contains(task.Suffix, "sources.read(") {
			t.Fatalf("Host-input boundary drift example=%s", task.ExampleID)
		}
		if !strings.Contains(task.Suffix, fmt.Sprintf(`"example_id": "%s"`, task.ExampleID)) || !strings.Contains(task.Suffix, `"oracle": "passed"`) {
			t.Fatalf("result sentinel missing example=%s", task.ExampleID)
		}
		if testDigest(task.Prefix+task.Suffix) != task.SourceSHA256 {
			t.Fatalf("source identity drift example=%s", task.ExampleID)
		}
		if len(task.Inputs) == 1 {
			oneInput++
		} else {
			twoInputs++
		}
	}
	if oneInput != 10 || twoInputs != 5 {
		t.Fatalf("input arity distribution drift: one=%d two=%d", oneInput, twoInputs)
	}
}

func gitChameleonNumPyCell(task gitChameleonNumPyTask, taskCell gitChameleonNumPyTaskCell, providerDelayMS int) (plmPrefixEagerCell, error) {
	responses := make(map[string]string, len(task.Inputs))
	for _, input := range task.Inputs {
		if input.Path == "" || input.Body == "" {
			return plmPrefixEagerCell{}, fmt.Errorf("example %s has empty Host input", task.ExampleID)
		}
		if _, exists := responses[input.Path]; exists {
			return plmPrefixEagerCell{}, fmt.Errorf("example %s has duplicate Host path %q", task.ExampleID, input.Path)
		}
		responses[input.Path] = input.Body
	}
	if taskCell.TokensPerSecond <= 0 || taskCell.SourceWindowMS <= 0 || providerDelayMS <= 0 {
		return plmPrefixEagerCell{}, fmt.Errorf("example %s has invalid timing cell", task.ExampleID)
	}
	expected, err := json.Marshal(map[string]string{"example_id": task.ExampleID, "oracle": "passed"})
	if err != nil {
		return plmPrefixEagerCell{}, err
	}
	calls := uint32(len(task.Inputs))
	return plmPrefixEagerCell{
		id: fmt.Sprintf("example-%s-rate-%dtps", task.ExampleID, taskCell.TokensPerSecond),
		chunks: []plmPrefixEagerChunk{
			{offset: 0, source: task.Prefix},
			{offset: time.Duration(taskCell.SourceWindowMS) * time.Millisecond, source: task.Suffix},
		},
		providerResponses: responses,
		providerDelay:     time.Duration(providerDelayMS) * time.Millisecond,
		expectedCalls:     calls, expectedPrefixCalls: calls, expectedResult: expected,
	}, nil
}

func TestGitChameleonNumPyDerivedCellsPreserveManifestTiming(t *testing.T) {
	manifest, _ := loadGitChameleonNumPyManifest(t)
	for _, task := range manifest.Tasks {
		for _, taskCell := range task.Cells {
			cell, err := gitChameleonNumPyCell(task, taskCell, manifest.ProviderDelayMS)
			if err != nil {
				t.Fatal(err)
			}
			if len(cell.chunks) != 2 || cell.chunks[1].offset != time.Duration(taskCell.SourceWindowMS)*time.Millisecond || cell.providerDelayDuration() != 200*time.Millisecond {
				t.Fatalf("cell timing drift: %+v", cell)
			}
			if testDigest(cell.sourceText()) != task.SourceSHA256 {
				t.Fatalf("cell source drift example=%s rate=%d", task.ExampleID, taskCell.TokensPerSecond)
			}
		}
	}
}

func TestGitChameleonNumPyDerivedPLMFixture(t *testing.T) {
	outputDir := os.Getenv("PYSOLATE_GITCHAMELEON_NUMPY_OUTPUT_DIR")
	if outputDir == "" {
		t.Skip("set PYSOLATE_GITCHAMELEON_NUMPY_OUTPUT_DIR to run")
	}
	runs, err := scalingEnvInt("PYSOLATE_GITCHAMELEON_NUMPY_RUNS", 10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	orderOffset, err := scalingEnvInt("PYSOLATE_GITCHAMELEON_NUMPY_ORDER_OFFSET", 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	manifest, manifestBytes := loadGitChameleonNumPyManifest(t)
	if manifest.SchemaVersion != gitChameleonNumPyManifestSchema || len(manifest.Tasks) != manifest.TaskCount {
		t.Fatal("invalid NumPy-derived manifest")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 120 * time.Second
	capacity, err := newPLMPrefixPreparedCapacityForProfile(context.Background(), artifact, config, "numpy-core", []string{"json", "numpy"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := capacity.Close(context.Background()); closeErr != nil {
			t.Errorf("close pooled NumPy analyser capacity: %v", closeErr)
		}
	}()
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	manifestDigest := sha256.Sum256(manifestBytes)
	expectedSessions := uint32(0)
	treatments := []string{"serial_whole_file", "pysolate_pooled_prefix"}

	tasks := append([]gitChameleonNumPyTask(nil), manifest.Tasks...)
	sort.Slice(tasks, func(left, right int) bool {
		leftID, _ := strconv.Atoi(tasks[left].ExampleID)
		rightID, _ := strconv.Atoi(tasks[right].ExampleID)
		return leftID < rightID
	})
	for _, task := range tasks {
		cells := append([]gitChameleonNumPyTaskCell(nil), task.Cells...)
		sort.Slice(cells, func(left, right int) bool { return cells[left].TokensPerSecond < cells[right].TokensPerSecond })
		for _, taskCell := range cells {
			cell, cellErr := gitChameleonNumPyCell(task, taskCell, manifest.ProviderDelayMS)
			if cellErr != nil {
				t.Fatal(cellErr)
			}
			evidence := gitChameleonNumPyEvidence{
				plmPrefixEagerEvidence: plmPrefixEagerEvidence{
					SchemaVersion:      gitChameleonNumPyEvidenceSchema,
					CellID:             cell.id,
					SourceCommit:       os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_COMMIT"),
					SourceTree:         os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_TREE"),
					HostID:             os.Getenv("EVALUATION_HOST_ID"),
					ArtifactSHA256:     fmt.Sprintf("sha256:%x", artifactDigest[:]),
					Runs:               runs,
					ProviderDelayMS:    manifest.ProviderDelayMS,
					ChunkOffsetsMS:     cell.chunkOffsetsMS(),
					SourceSHA256:       task.SourceSHA256,
					EagerEstimateScope: "GitChameleon NumPy-derived source-tail overlap under a fixed mock token clock; no supplied EAGER runtime claim",
				},
				SourceTreeState:      os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_STATE"),
				ManifestSHA256:       fmt.Sprintf("sha256:%x", manifestDigest[:]),
				DatasetName:          manifest.Dataset.Name,
				DatasetCommit:        manifest.Dataset.Commit,
				DatasetSHA256:        manifest.Dataset.SHA256,
				DatasetRow:           task.DatasetRow,
				ExampleID:            task.ExampleID,
				TargetNumPyVersion:   task.TargetNumPyVersion,
				API:                  task.API,
				InputCount:           len(task.Inputs),
				SuffixTokens:         task.SuffixTokens,
				TokensPerSecond:      taskCell.TokensPerSecond,
				SourceWindowMS:       taskCell.SourceWindowMS,
				MockStreamTokenizer:  manifest.MockStream.Tokenizer,
				MockTokenizerVersion: manifest.MockStream.TokenizerVersion,
			}
			expectedResultSHA, hashErr := playback.CanonicalSHA256(cell.expectedResult)
			if hashErr != nil {
				t.Fatal(hashErr)
			}
			for trial := 0; trial < runs; trial++ {
				for order := 0; order < len(treatments); order++ {
					name := treatments[(trial+orderOffset+order)%len(treatments)]
					tracker := &plmPrefixEagerTracker{responses: cell.providerResponses, delay: cell.providerDelayDuration()}
					adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(tracker.handler)}
					plan := plmE2EPlan(t, cell.expectedCalls, adapter)
					runID := fmt.Sprintf("gitchameleon-numpy-%s-%d-%s", cell.id, trial, name)
					workspaceRoot := filepath.Join(t.TempDir(), runID)
					brokerFactory := func(context.Context) (*capability.Broker, error) {
						return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
					}
					var treatment semanticspeculation.ScheduledTreatment
					switch name {
					case "serial_whole_file":
						runConfig := config
						runConfig.ExecutionProfile = &capacity.profile
						runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
						treatment, err = semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
							Artifact: artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory,
							ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID,
						})
					case "pysolate_pooled_prefix":
						treatment = newPLMPrefixTreatment(capacity, plan, adapter, tracker, cell.expectedCalls, cell.expectedPrefixCalls, runID, workspaceRoot)
					}
					if err != nil {
						t.Fatal(err)
					}
					sample, runErr := runPLMPrefixEagerTreatment(context.Background(), trial, order, name, cell, tracker, treatment)
					if runErr != nil {
						t.Fatalf("example=%s rate=%d trial=%d treatment=%s: %v", task.ExampleID, taskCell.TokensPerSecond, trial, name, runErr)
					}
					if plm, ok := treatment.(*plmPrefixTreatment); ok {
						sample.SplitPhase, sample.PLMLifecycle = plm.split, plm.lifecycle
						sample.PrefixAnalysisNanos, sample.PrefixAdmissionNanos = plm.prefixAnalysisNanos, plm.prefixAdmissionNanos
						sample.PrefixAnalyzerInvocations = plm.prefixAnalyzerInvocations
						sample.AnalyzerProvision = plm.analyzerProvision
						sample.AnalyzerSessionPrepareNanos = plm.analyzerSessionPrepareNanos
						sample.FinalExecutionNanos = plm.finalExecutionNanos
						if sample.PrefixAnalyzerInvocations != 1 || sample.SplitPhase.Reused != cell.expectedCalls || sample.SplitPhase.MaximumConcurrent != cell.expectedCalls || sample.SplitPhase.JobsMaterialized != cell.expectedCalls {
							t.Fatalf("example=%s rate=%d invalid prefix lifecycle: %+v", task.ExampleID, taskCell.TokensPerSecond, sample)
						}
						if sample.AnalyzerSessionPrepareNanos == 0 || sample.PrefixAnalysisNanos == 0 || sample.PrefixAdmissionNanos == 0 || sample.FinalExecutionNanos == 0 {
							t.Fatalf("example=%s rate=%d missing prefix timing: %+v", task.ExampleID, taskCell.TokensPerSecond, sample)
						}
						if !sample.AnalyzerProvision.NeverServed || sample.AnalyzerProvision.BrokerAvailable || sample.AnalyzerProvision.WorkspaceMounted || (goruntime.GOOS == "linux" && !sample.AnalyzerProvision.COWHit) {
							t.Fatalf("example=%s rate=%d analyzer was not prepared: %+v", task.ExampleID, taskCell.TokensPerSecond, sample.AnalyzerProvision)
						}
					}
					if sample.Outcome.FinalProgramOutcome != "success" || sample.Outcome.LogicalCalls != cell.expectedCalls || sample.Outcome.PhysicalAttempts != cell.expectedCalls || sample.Outcome.ResultSHA256 != expectedResultSHA {
						t.Fatalf("example=%s rate=%d invalid outcome: %+v", task.ExampleID, taskCell.TokensPerSecond, sample.Outcome)
					}
					if name == "pysolate_pooled_prefix" && sample.ProviderMaxConcurrent != cell.expectedCalls {
						t.Fatalf("example=%s rate=%d Pysolate concurrency=%d", task.ExampleID, taskCell.TokensPerSecond, sample.ProviderMaxConcurrent)
					}
					if name == "serial_whole_file" && sample.ProviderMaxConcurrent != 1 {
						t.Fatalf("example=%s rate=%d serial concurrency=%d", task.ExampleID, taskCell.TokensPerSecond, sample.ProviderMaxConcurrent)
					}
					evidence.Samples = append(evidence.Samples, sample)
				}
			}
			expectedSessions += uint32(runs)
			evidence.AnalyzerCapacitySetupNanos = capacity.SetupNanos()
			evidence.AnalyzerCapacitySessionCount = capacity.SessionCount()
			evidence.AnalyzerCapacityLifecycle = capacity.LifecycleEvidence()
			encoded, encodeErr := json.MarshalIndent(evidence, "", "  ")
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			path := filepath.Join(outputDir, cell.id+".json")
			if writeErr := os.WriteFile(path, append(encoded, '\n'), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
	}
	lifecycle := capacity.LifecycleEvidence()
	if capacity.SessionCount() != expectedSessions || lifecycle.COWHits != expectedSessions || lifecycle.RuntimeInitCalls != 0 {
		t.Fatalf("NumPy analyser capacity was not pooled: sessions=%d expected=%d lifecycle=%+v", capacity.SessionCount(), expectedSessions, lifecycle)
	}
}
