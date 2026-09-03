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

const plmPrefixScalingSchema = "pysolate.plm-prefix-source-scaling.v1"

func TestPLMPrefixScalingCellsCoverDeclaredMatrix(t *testing.T) {
	cells := plmPrefixScalingCells()
	if len(cells) != 16 {
		t.Fatalf("cells=%d", len(cells))
	}
	seen := map[string]bool{}
	for _, cell := range cells {
		if seen[cell.id] {
			t.Fatalf("duplicate cell %q", cell.id)
		}
		seen[cell.id] = true
	}
	for _, id := range []string{"calls-1-window-0ms", "calls-2-window-100ms", "calls-4-window-200ms", "calls-8-window-400ms"} {
		if !seen[id] {
			t.Fatalf("missing cell %q", id)
		}
	}
}

func TestPLMPrefixScalingCellGeneratesIndependentFirstPrefixCalls(t *testing.T) {
	cell, err := plmPrefixScalingCell(8, 400)
	if err != nil {
		t.Fatal(err)
	}
	if cell.expectedCalls != 8 || cell.expectedPrefixCalls != 8 || cell.providerDelayDuration() != 200*time.Millisecond {
		t.Fatalf("cell=%+v", cell)
	}
	if len(cell.chunks) != 2 || cell.chunks[0].offset != 0 || cell.chunks[1].offset != 400*time.Millisecond {
		t.Fatalf("chunks=%+v", cell.chunks)
	}
	if got := strings.Count(cell.chunks[0].source, "sources.read("); got != 8 {
		t.Fatalf("prefix calls=%d source=%q", got, cell.chunks[0].source)
	}
	if strings.Contains(cell.chunks[1].source, "sources.read(") {
		t.Fatalf("suffix contains Host call: %q", cell.chunks[1].source)
	}
}

func TestPLMPrefixScalingCellRejectsUndeclaredDimensions(t *testing.T) {
	for _, item := range [][2]int{{0, 0}, {3, 100}, {1, 300}, {8, -1}} {
		if _, err := plmPrefixScalingCell(item[0], item[1]); err == nil {
			t.Fatalf("accepted calls=%d window=%d", item[0], item[1])
		}
	}
}

type plmPrefixScalingEvidence struct {
	plmPrefixEagerEvidence
	CallCount       int    `json:"call_count"`
	SourceWindowMS  int    `json:"source_window_ms"`
	SourceTreeState string `json:"source_tree_state"`
}

func plmPrefixScalingCells() []plmPrefixEagerCell {
	cells := make([]plmPrefixEagerCell, 0, 16)
	for _, calls := range []int{1, 2, 4, 8} {
		for _, windowMS := range []int{0, 100, 200, 400} {
			cell, err := plmPrefixScalingCell(calls, windowMS)
			if err != nil {
				panic(err)
			}
			cells = append(cells, cell)
		}
	}
	sort.Slice(cells, func(left, right int) bool { return cells[left].id < cells[right].id })
	return cells
}

func plmPrefixScalingCell(calls, windowMS int) (plmPrefixEagerCell, error) {
	if !containsScalingDimension([]int{1, 2, 4, 8}, calls) || !containsScalingDimension([]int{0, 100, 200, 400}, windowMS) {
		return plmPrefixEagerCell{}, fmt.Errorf("undeclared scaling cell calls=%d window_ms=%d", calls, windowMS)
	}
	var prefix strings.Builder
	values := make([]any, 0, calls+1)
	resultNames := make([]string, 0, calls+1)
	responses := make(map[string]string, calls)
	for index := 0; index < calls; index++ {
		path := fmt.Sprintf("input-%d", index)
		name := fmt.Sprintf("value%d", index)
		value := fmt.Sprintf("value-%d", index)
		fmt.Fprintf(&prefix, "%s = sources.read(%q)\n", name, path)
		responses[path] = value
		values = append(values, value)
		resultNames = append(resultNames, name)
	}
	values = append(values, "done")
	resultNames = append(resultNames, `"done"`)
	expected, err := json.Marshal(values)
	if err != nil {
		return plmPrefixEagerCell{}, err
	}
	suffix := fmt.Sprintf("result = [%s]\nprint(result)\n", strings.Join(resultNames, ", "))
	return plmPrefixEagerCell{
		id:                fmt.Sprintf("calls-%d-window-%dms", calls, windowMS),
		chunks:            []plmPrefixEagerChunk{{offset: 0, source: prefix.String()}, {offset: time.Duration(windowMS) * time.Millisecond, source: suffix}},
		providerResponses: responses, providerDelay: 200 * time.Millisecond,
		expectedCalls: uint32(calls), expectedPrefixCalls: uint32(calls), expectedResult: expected,
	}, nil
}

func containsScalingDimension(values []int, candidate int) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func TestPLMPrefixSourceScalingFixture(t *testing.T) {
	output := os.Getenv("PYSOLATE_PLM_PREFIX_SCALING_OUTPUT")
	if output == "" {
		t.Skip("set PYSOLATE_PLM_PREFIX_SCALING_OUTPUT to run")
	}
	calls, err := scalingEnvInt("PYSOLATE_PLM_PREFIX_SCALING_CALLS", 1, 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	windowMS, err := scalingEnvInt("PYSOLATE_PLM_PREFIX_SCALING_WINDOW_MS", 0, 0, 400)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := plmPrefixScalingCell(calls, windowMS)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := scalingEnvInt("PYSOLATE_PLM_PREFIX_SCALING_RUNS", 10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	orderOffset, err := scalingEnvInt("PYSOLATE_PLM_PREFIX_SCALING_ORDER_OFFSET", 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	capacity, err := newPLMPrefixPreparedCapacity(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := capacity.Close(context.Background()); closeErr != nil {
			t.Errorf("close pooled analyser capacity: %v", closeErr)
		}
	}()
	artifactDigest := sha256.Sum256(artifact)
	evidence := plmPrefixScalingEvidence{
		plmPrefixEagerEvidence: plmPrefixEagerEvidence{
			SchemaVersion: plmPrefixScalingSchema, CellID: cell.id,
			SourceCommit: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_COMMIT"), SourceTree: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_TREE"),
			HostID: os.Getenv("EVALUATION_HOST_ID"), ArtifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]), Runs: runs,
			ProviderDelayMS: int(cell.providerDelayDuration() / time.Millisecond), ChunkOffsetsMS: cell.chunkOffsetsMS(), SourceSHA256: testDigest(cell.sourceText()),
			EagerEstimateScope: "Controlled Pysolate source-tail overlap; no supplied EAGER runtime claim",
		},
		CallCount: calls, SourceWindowMS: windowMS, SourceTreeState: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_STATE"),
	}
	expectedResultSHA, err := playback.CanonicalSHA256(cell.expectedResult)
	if err != nil {
		t.Fatal(err)
	}
	treatments := []string{"serial_whole_file", "pysolate_pooled_prefix"}
	for trial := 0; trial < runs; trial++ {
		for order := 0; order < len(treatments); order++ {
			name := treatments[(trial+orderOffset+order)%len(treatments)]
			tracker := &plmPrefixEagerTracker{responses: cell.providerResponses, delay: cell.providerDelayDuration()}
			adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(tracker.handler)}
			plan := plmE2EPlan(t, uint32(calls), adapter)
			runID := fmt.Sprintf("plm-prefix-scaling-%s-%d-%s", cell.id, trial, name)
			workspaceRoot := filepath.Join(t.TempDir(), runID)
			brokerFactory := func(context.Context) (*capability.Broker, error) {
				return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
			}
			var treatment semanticspeculation.ScheduledTreatment
			switch name {
			case "serial_whole_file":
				runConfig := config
				runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
				treatment, err = semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
					Artifact: artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory,
					ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID,
				})
			case "pysolate_pooled_prefix":
				treatment = newPLMPrefixTreatment(capacity, plan, adapter, tracker, uint32(calls), uint32(calls), runID, workspaceRoot)
			}
			if err != nil {
				t.Fatal(err)
			}
			sample, runErr := runPLMPrefixEagerTreatment(context.Background(), trial, order, name, cell, tracker, treatment)
			if runErr != nil {
				t.Fatalf("trial=%d treatment=%s: %v", trial, name, runErr)
			}
			if plm, ok := treatment.(*plmPrefixTreatment); ok {
				sample.SplitPhase, sample.PLMLifecycle = plm.split, plm.lifecycle
				sample.PrefixAnalysisNanos, sample.PrefixAdmissionNanos = plm.prefixAnalysisNanos, plm.prefixAdmissionNanos
				sample.PrefixAnalyzerInvocations = plm.prefixAnalyzerInvocations
				sample.AnalyzerProvision = plm.analyzerProvision
				sample.AnalyzerSessionPrepareNanos = plm.analyzerSessionPrepareNanos
				sample.FinalExecutionNanos = plm.finalExecutionNanos
				if sample.PrefixAnalyzerInvocations != 1 || sample.SplitPhase.Reused != uint32(calls) || sample.SplitPhase.MaximumConcurrent != uint32(calls) || sample.SplitPhase.JobsMaterialized != uint32(calls) {
					t.Fatalf("invalid prefix lifecycle: %+v", sample)
				}
				if sample.AnalyzerSessionPrepareNanos == 0 || sample.PrefixAnalysisNanos == 0 || sample.PrefixAdmissionNanos == 0 || sample.FinalExecutionNanos == 0 {
					t.Fatalf("prefix timing was not recorded: %+v", sample)
				}
				if !sample.AnalyzerProvision.NeverServed || sample.AnalyzerProvision.BrokerAvailable || sample.AnalyzerProvision.WorkspaceMounted || (goruntime.GOOS == "linux" && !sample.AnalyzerProvision.COWHit) {
					t.Fatalf("prefix analyser was not prepared: %+v", sample.AnalyzerProvision)
				}
			}
			if sample.Outcome.FinalProgramOutcome != "success" || sample.Outcome.LogicalCalls != uint32(calls) || sample.Outcome.PhysicalAttempts != uint32(calls) || sample.Outcome.ResultSHA256 != expectedResultSHA {
				t.Fatalf("invalid outcome: %+v", sample.Outcome)
			}
			if name == "pysolate_pooled_prefix" && sample.ProviderMaxConcurrent != uint32(calls) {
				t.Fatalf("Pysolate provider concurrency=%d calls=%d", sample.ProviderMaxConcurrent, calls)
			}
			if name == "serial_whole_file" && sample.ProviderMaxConcurrent != 1 {
				t.Fatalf("serial provider concurrency=%d", sample.ProviderMaxConcurrent)
			}
			evidence.Samples = append(evidence.Samples, sample)
		}
	}
	evidence.AnalyzerCapacitySetupNanos = capacity.SetupNanos()
	evidence.AnalyzerCapacitySessionCount = capacity.SessionCount()
	evidence.AnalyzerCapacityLifecycle = capacity.LifecycleEvidence()
	if evidence.AnalyzerCapacitySessionCount != uint32(runs) || evidence.AnalyzerCapacityLifecycle.COWHits != uint32(runs) || evidence.AnalyzerCapacityLifecycle.RuntimeInitCalls != 0 {
		t.Fatalf("analyser capacity was not pooled: sessions=%d lifecycle=%+v", evidence.AnalyzerCapacitySessionCount, evidence.AnalyzerCapacityLifecycle)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func scalingEnvInt(name string, fallback, minimum, maximum int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be in [%d,%d]", name, minimum, maximum)
	}
	return value, nil
}
