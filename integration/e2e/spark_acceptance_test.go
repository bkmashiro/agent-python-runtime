package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	agentfunction "github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func benchmarkTreatments(matrix string) []composableacceptance.Treatment {
	if matrix != "conformance" {
		return []composableacceptance.Treatment{composableacceptance.TreatmentAll}
	}
	return []composableacceptance.Treatment{
		composableacceptance.TreatmentFresh,
		composableacceptance.TreatmentStreaming,
		composableacceptance.TreatmentFanout,
		composableacceptance.TreatmentCacheOff,
		composableacceptance.TreatmentCacheOn,
		composableacceptance.TreatmentSingleFlightOff,
		composableacceptance.TreatmentSingleFlightOn,
		composableacceptance.TreatmentReevaluationOff,
		composableacceptance.TreatmentReevaluationOn,
		composableacceptance.TreatmentPrepared,
		composableacceptance.TreatmentCOW,
		composableacceptance.TreatmentAll,
		composableacceptance.TreatmentInvalidParent,
		composableacceptance.TreatmentInvalidChild,
		composableacceptance.TreatmentChangedObserve,
		composableacceptance.TreatmentBranchConflict,
		composableacceptance.TreatmentCacheCorruption,
		composableacceptance.TreatmentCancellation,
	}
}

func TestBenchmarkTreatmentsDefaultToAll(t *testing.T) {
	if got := benchmarkTreatments(""); len(got) != 1 || got[0] != composableacceptance.TreatmentAll {
		t.Fatalf("default benchmark treatments = %v, want [all]", got)
	}
	if got := benchmarkTreatments("conformance"); len(got) != 18 {
		t.Fatalf("conformance treatments = %d, want 18", len(got))
	}
}

func TestRealGuestSparkScenarioCoreTreatments(t *testing.T) {
	corpusPath := os.Getenv("PYSOLATE_SPARK_CORPUS")
	outputPath := os.Getenv("PYSOLATE_ACCEPTANCE_CORE_REPORT")
	if corpusPath == "" || outputPath == "" {
		t.Skip("PYSOLATE_SPARK_CORPUS and PYSOLATE_ACCEPTANCE_CORE_REPORT are required")
	}
	corpusRaw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	corpus, corpusSHA, err := composableacceptance.DecodeCorpus(corpusRaw)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := hashBytes(artifact)
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        os.Getenv("PYSOLATE_HOST_SOURCE_COMMIT"),
		GuestArtifactSHA256: artifactSHA, CorpusSHA256: corpusSHA, Model: corpus.Model,
	}
	for _, scenario := range corpus.Scenarios {
		if filter := os.Getenv("PYSOLATE_ACCEPTANCE_SCENARIO"); filter != "" && scenario.ID != filter {
			continue
		}
		scenarioSHA, err := composableacceptance.ScenarioIdentity(scenario)
		if err != nil {
			t.Fatal(err)
		}
		oracleSHA := composableacceptance.ArtifactIdentity(scenario.ExpectedArtifact)
		for _, treatment := range benchmarkTreatments(os.Getenv("PYSOLATE_ACCEPTANCE_MATRIX")) {
			if filter := os.Getenv("PYSOLATE_ACCEPTANCE_TREATMENT"); filter != "" && string(treatment) != filter {
				continue
			}
			t.Logf("direct treatment start scenario=%s treatment=%s", scenario.ID, treatment)
			row, ok := runScenarioCoreTreatment(t, artifact, artifactSHA, scenario, scenarioSHA, oracleSHA, treatment)
			if ok {
				assertTreatmentTrace(t, row)
				report.Rows = append(report.Rows, row)
			}
		}
	}
	composableacceptance.SortRows(report.Rows)
	for _, row := range report.Rows {
		single := report
		single.Rows = []composableacceptance.Row{row}
		if _, _, err := composableacceptance.EncodeReport(single); err != nil {
			t.Fatalf("invalid direct row scenario=%s treatment=%s row=%+v trace=%+v: %v", row.ScenarioID, row.Treatment, row, row.Trace, err)
		}
	}
	encoded, _, err := composableacceptance.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatal(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".core-report-*")
	if err != nil {
		t.Fatal(err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		t.Fatal(err)
	}
}

func TestTraceRecorderLifecycle(t *testing.T) {
	t.Setenv("PYSOLATE_HOST_SOURCE_COMMIT", strings.Repeat("1", 40))
	scenario := composableacceptance.Scenario{
		ID:               "trace-recorder-lifecycle",
		ExpectedArtifact: "ok",
	}
	scenarioSHA := hashBytes([]byte("trace-scenario-lifecycle"))
	oracleSHA := hashBytes([]byte("ok"))
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentFresh, started, 1)
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.create", composableacceptance.TraceEventOutcomeOK, nil, []byte("artifact"), nil, "", "", 1)
	bad := row
	bad.Trace = append([]composableacceptance.TraceEvent{}, recorder.events...)
	if err := assertTraceLifecycleValid(t, bad, true); err == nil {
		t.Fatalf("expected invalid trace without terminal to fail validation")
	}
	completeTraceRow(&row, started, recorder)
	if err := assertTraceLifecycleValid(t, row, true); err != nil {
		t.Fatalf("trace with terminal must validate: %v", err)
	}
}

func TestRepresentativeTreatmentTraceHelpers(t *testing.T) {
	t.Setenv("PYSOLATE_HOST_SOURCE_COMMIT", strings.Repeat("1", 40))
	scenario := composableacceptance.Scenario{
		ID:               "trace-helper-sample",
		ExpectedArtifact: "trace-helper-output",
	}
	scenarioSHA := hashBytes([]byte("trace-helper-scenario"))
	oracleSHA := hashBytes([]byte("trace-helper-output"))
	artifactSHA := hashBytes([]byte("trace-helper-artifact"))
	cases := []struct {
		name    string
		run     func(*testing.T) (composableacceptance.Row, bool)
		actions map[string]struct{}
	}{
		{
			name: "cache_on",
			run: func(t *testing.T) (composableacceptance.Row, bool) {
				return runScenarioCacheExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, true)
			},
			actions: map[string]struct{}{
				"run.start":      {},
				"cache.lookup":   {},
				"cache.compute":  {},
				"cache.store":    {},
				"oracle.compare": {},
				"run.terminal":   {},
			},
		},
		{
			name: "single_flight_on",
			run: func(t *testing.T) (composableacceptance.Row, bool) {
				return runScenarioSingleFlightExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, true)
			},
			actions: map[string]struct{}{
				"run.start":              {},
				"single_flight.leader":   {},
				"single_flight.follower": {},
				"single_flight.compute":  {},
				"oracle.compare":         {},
				"run.terminal":           {},
			},
		},
	}
	for _, c := range cases {
		tc := c
		t.Run(tc.name, func(t *testing.T) {
			row, ok := tc.run(t)
			if !ok {
				t.Fatalf("expected helper %s to run", tc.name)
			}
			if err := assertTraceLifecycleValid(t, row, true); err != nil {
				t.Fatalf("helper %s row validation: %v", tc.name, err)
			}
			actions := map[string]struct{}{}
			for _, event := range row.Trace {
				actions[event.Action] = struct{}{}
			}
			for action := range tc.actions {
				if _, ok := actions[action]; !ok {
					t.Fatalf("helper %s missing action %q in trace", tc.name, action)
				}
			}
		})
	}
}

func assertTreatmentTrace(t *testing.T, row composableacceptance.Row) {
	t.Helper()
	if err := assertTraceLifecycleValid(t, row, true); err != nil {
		t.Fatalf("scenario=%s treatment=%s invalid trace: %v", row.ScenarioID, row.Treatment, err)
	}
	if row.Status == "skipped" {
		return
	}
	actions := make(map[string]int, len(row.Trace))
	for _, event := range row.Trace {
		actions[event.Action]++
	}
	required := map[composableacceptance.Treatment][]string{
		composableacceptance.TreatmentFresh:           {"guest.create", "guest.run", "oracle.compare", "guest.close"},
		composableacceptance.TreatmentStreaming:       {"workspace.fork", "stream.begin", "stream.seal", "stream.end", "workspace.commit", "oracle.compare"},
		composableacceptance.TreatmentFanout:          {"fanout.child_start", "fanout.child_end", "fanout.select", "fanout.selected_root", "fanout.discard"},
		composableacceptance.TreatmentCacheOff:        {"cache.lookup", "cache.compute", "oracle.compare"},
		composableacceptance.TreatmentCacheOn:         {"cache.lookup", "cache.compute", "cache.store", "cache.hit", "oracle.compare"},
		composableacceptance.TreatmentSingleFlightOff: {"single_flight.leader", "single_flight.compute", "oracle.compare"},
		composableacceptance.TreatmentSingleFlightOn:  {"single_flight.leader", "single_flight.follower", "single_flight.compute", "oracle.compare"},
		composableacceptance.TreatmentReevaluationOff: {"wait.begin", "wait.release", "resume.disabled"},
		composableacceptance.TreatmentReevaluationOn:  {"wait.begin", "wait.release", "resume.reuse", "oracle.compare"},
		composableacceptance.TreatmentPrepared:        {"prepared.create", "prepared.consume", "guest.run", "oracle.compare"},
		composableacceptance.TreatmentCOW:             {"cow.map_private", "cow.discard", "guest.run", "oracle.compare"},
		composableacceptance.TreatmentAll:             {"stream.begin", "fanout.child_start", "cache.lookup", "single_flight.leader", "wait.begin", "resume.fresh", "oracle.compare"},
		composableacceptance.TreatmentInvalidParent:   {"validation.reject", "workspace.discard"},
		composableacceptance.TreatmentInvalidChild:    {"fanout.child_error", "workspace.discard"},
		composableacceptance.TreatmentChangedObserve:  {"observation.changed", "resume.fresh", "oracle.compare"},
		composableacceptance.TreatmentBranchConflict:  {"workspace.conflict", "workspace.discard"},
		composableacceptance.TreatmentCacheCorruption: {"cache.corrupt", "cache.detect", "cache.compute", "cache.hit"},
		composableacceptance.TreatmentCancellation:    {"cancellation.requested", "cancellation.observed", "cleanup.discard", "oracle.compare"},
	}
	for _, action := range required[row.Treatment] {
		if actions[action] == 0 {
			t.Fatalf("scenario=%s treatment=%s missing recorded action %q", row.ScenarioID, row.Treatment, action)
		}
	}
	if row.Treatment == composableacceptance.TreatmentCacheOff && actions["cache.hit"] != 0 {
		t.Fatalf("scenario=%s cache_off recorded a cache hit", row.ScenarioID)
	}
	if row.Treatment == composableacceptance.TreatmentSingleFlightOff && actions["single_flight.follower"] != 0 {
		t.Fatalf("scenario=%s single_flight_off recorded a follower", row.ScenarioID)
	}
}

func assertTraceLifecycleValid(t *testing.T, row composableacceptance.Row, requireTerminal bool) error {
	t.Helper()
	if len(row.Trace) == 0 {
		return errors.New("no trace events")
	}
	if row.Trace[0].Sequence != 1 || row.Trace[0].Action != "run.start" {
		return errors.New("trace missing run.start as first event")
	}
	for i, event := range row.Trace {
		if int(event.Sequence) != i+1 {
			return fmt.Errorf("trace sequence gap at index %d (%q)", i, event.Action)
		}
	}
	if requireTerminal {
		last := row.Trace[len(row.Trace)-1]
		if last.Action != "run.terminal" {
			return errors.New("trace missing run.terminal")
		}
	}
	if !requireTerminal {
		for _, event := range row.Trace {
			if event.Action == "run.terminal" {
				return errors.New("trace unexpectedly has terminal")
			}
		}
	}
	return nil
}

func runScenarioCoreTreatment(t *testing.T, artifact []byte, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, treatment composableacceptance.Treatment) (composableacceptance.Row, bool) {
	t.Helper()
	switch treatment {
	case composableacceptance.TreatmentFresh:
		config := runtimeconfig.DefaultRunConfig()
		return runScenarioGuestExecution(t, artifact, scenario, scenarioSHA, oracleSHA, treatment, config)
	case composableacceptance.TreatmentPrepared:
		config := runtimeconfig.DefaultRunConfig()
		config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true}
		return runScenarioGuestExecution(t, artifact, scenario, scenarioSHA, oracleSHA, treatment, config)
	case composableacceptance.TreatmentCOW:
		config := runtimeconfig.DefaultRunConfig()
		config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true, MemoryCOW: true}
		return runScenarioGuestExecution(t, artifact, scenario, scenarioSHA, oracleSHA, treatment, config)
	case composableacceptance.TreatmentStreaming:
		return runScenarioStreamingExecution(t, artifact, artifactSHA, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentCacheOff:
		return runScenarioCacheExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, false)
	case composableacceptance.TreatmentCacheOn:
		return runScenarioCacheExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, true)
	case composableacceptance.TreatmentSingleFlightOff:
		return runScenarioSingleFlightExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, false)
	case composableacceptance.TreatmentSingleFlightOn:
		return runScenarioSingleFlightExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA, true)
	case composableacceptance.TreatmentFanout:
		return runScenarioFanoutExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentReevaluationOff:
		return runScenarioReevaluationExecution(t, artifact, scenario, scenarioSHA, oracleSHA, false)
	case composableacceptance.TreatmentReevaluationOn:
		return runScenarioReevaluationExecution(t, artifact, scenario, scenarioSHA, oracleSHA, true)
	case composableacceptance.TreatmentAll:
		return runScenarioAllExecution(t, artifact, artifactSHA, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentInvalidParent:
		return runScenarioInvalidParentExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentInvalidChild:
		return runScenarioInvalidChildExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentChangedObserve:
		return runScenarioChangedObservationExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentBranchConflict:
		return runScenarioBranchConflictExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentCacheCorruption:
		return runScenarioCacheCorruptionExecution(t, artifactSHA, scenario, scenarioSHA, oracleSHA)
	case composableacceptance.TreatmentCancellation:
		return runScenarioCancellationExecution(t, artifact, scenario, scenarioSHA, oracleSHA)
	default:
		t.Fatalf("unsupported core treatment %q", treatment)
	}
	return composableacceptance.Row{}, false
}

func runScenarioGuestExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, treatment composableacceptance.Treatment, config runtimeconfig.RunConfig) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, treatment, started, 1)
	if treatment == composableacceptance.TreatmentPrepared {
		row.TerminalDisposition = "consumed_single_use"
	}
	if treatment == composableacceptance.TreatmentCOW {
		row.TerminalDisposition = "discarded_after_single_use"
	}
	manager, base := newComposableWorkspace(t)
	defer manager.Close()
	factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: base, WorkspaceOwner: "spark-" + scenario.ID}
	runner, err := factory.New(context.Background(), artifact, config)
	if err != nil {
		if treatment == composableacceptance.TreatmentCOW && runtime.GOOS != "linux" {
			row.Status = "skipped"
			row.GuestCreated = 0
			row.GuestDestroyed = 0
			row.TerminalDisposition = "platform_unavailable"
			completeTraceRow(&row, started, recorder)
			return row, true
		}
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.create", composableacceptance.TraceEventOutcomeOK, nil, artifact, nil, "", "", 1)
	if treatment == composableacceptance.TreatmentPrepared {
		preparedRunner := runner.(*wazeroengine.Engine)
		state := preparedRunner.PreparedState()
		stateJSON, err := json.Marshal(state)
		if err != nil {
			_ = runner.Close(context.Background())
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypePrepared, "prepared.create", composableacceptance.TraceEventOutcomeOK, nil, artifact, stateJSON, "", "", 1)
	}
	if treatment == composableacceptance.TreatmentCOW {
		cowRunner, ok := runner.(*wazeroengine.Engine)
		if !ok {
			_ = runner.Close(context.Background())
			t.Fatalf("scenario=%s treatment=%s expected wazero runner", scenario.ID, treatment)
		}
		probe := cowRunner.COWProbe()
		outcome := composableacceptance.TraceEventOutcomeMapped
		if !probe.COWSelected {
			outcome = composableacceptance.TraceEventOutcomeSkipped
		}
		probeJSON, err := json.Marshal(probe)
		if err != nil {
			_ = runner.Close(context.Background())
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeCOW, "cow.map_private", outcome, nil, nil, probeJSON, "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	request := scenarioRequest(t, scenario, scenarioSHA)
	response, err := runner.Run(context.Background(), request, "")
	if err != nil {
		_ = runner.Close(context.Background())
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeOK, nil, request, response, "", "", 1)
	output := responseStringResult(t, response)
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), []byte(output), "", "", 1)
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if treatment == composableacceptance.TreatmentCOW {
		recorder.append(composableacceptance.TraceEventTypeCOW, "cow.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
	}
	if output != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(output) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s outcome mismatch", scenario.ID, treatment)
	}
	if treatment == composableacceptance.TreatmentPrepared {
		prepared := runner.(*wazeroengine.Engine)
		recorder.append(composableacceptance.TraceEventTypePrepared, "prepared.consume", composableacceptance.TraceEventOutcomeConsumed, nil, []byte(prepared.Properties().ExecutionProfileID), nil, "", "", 1)
	}
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioStreamingExecution(t *testing.T, artifact []byte, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	_ = artifactSHA
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentStreaming, started, 1)
	manager, base := newComposableWorkspace(t)
	defer manager.Close()
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	prepares, err := streaming.BuildPrepareChunks(streaming.PrepareConfig{
		Inputs: json.RawMessage(`{}`),
		Chunks: []string{
			"scenario_identity = " + pythonStringLiteral(t, scenarioSHA) + "\n",
			"result = " + pythonStringLiteral(t, scenario.ExpectedArtifact) + "\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	factory := wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "streaming-spark-" + scenario.ID,
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
	runner, err := factory.New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	streamRunner, ok := runner.(streaming.StreamRunner)
	if !ok {
		_ = runner.Close(context.Background())
		row.Status = "skipped"
		row.TerminalDisposition = "streaming_unavailable"
		if err := attempt.Discard(); err != nil {
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
		completeTraceRow(&row, started, recorder)
		return row, true
	}
	type outcome struct {
		result streaming.RunResult
		err    error
	}
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.begin", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	prepareChannel := make(chan string, len(prepares))
	completed := make(chan outcome, 1)
	go func() {
		request := []byte(`{"run_id":"streaming-spark-` + scenario.ID + `","code":"result = stream_final['result']","inputs":{}}`)
		result, runErr := streaming.ExecuteStream(context.Background(), streamRunner, attempt, request, prepareChannel)
		completed <- outcome{result: result, err: runErr}
	}()
	for _, prepare := range prepares {
		prepareChannel <- prepare
		recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.prepare", composableacceptance.TraceEventOutcomeOK, nil, []byte(prepare), nil, "", "", 1)
	}
	close(prepareChannel)
	finished := <-completed
	if finished.err != nil {
		row.Status = "rejected"
		row.TerminalDisposition = "streaming_failed"
		recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.end", composableacceptance.TraceEventOutcomeError, nil, nil, nil, "", "", 1)
		_ = attempt.Discard()
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
		if err := runner.Close(context.Background()); err != nil {
			recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeError, nil, nil, nil, "", "", 1)
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
		t.Fatalf("scenario=%s treatment=%s stream error=%v", scenario.ID, composableacceptance.TreatmentStreaming, finished.err)
	}
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.seal", composableacceptance.TraceEventOutcomeSealed, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.end", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if publishedInfo, err := manager.Inspect(finished.result.PublishedWorkspace); err == nil {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.commit", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(publishedInfo.WorkspaceSHA256), "", "", 1)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if got := responseStringResult(t, finished.result.Response); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s outcome mismatch", scenario.ID, composableacceptance.TreatmentStreaming)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), []byte(finished.result.Response), "", "", 1)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioCacheExecution(t *testing.T, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, cacheOn bool) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentCacheOff, started, 0)
	if cacheOn {
		row.Treatment = composableacceptance.TreatmentCacheOn
	}
	storeDir := filepath.Join(t.TempDir(), "cache-"+scenario.ID)
	store, err := agentfunction.NewStore(storeDir, scenarioSHA, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := scenarioFunctionInvocation(scenario, scenarioSHA, artifactSHA)
	if err != nil {
		t.Fatal(err)
	}
	engine := agentfunction.Engine{Store: store, CacheEnabled: cacheOn}
	invocationIdentity, _, err := invocation.Identity()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	before := store.Stats()
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		calls.Add(1)
		return []byte(scenario.ExpectedArtifact), nil
	}
	first, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(first.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if first.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, first.Value, "", "", 1)
	}
	if !first.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), first.Value, "", "", 1)
	}
	second, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(second.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if second.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, second.Value, "", "", 1)
	}
	if !second.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), second.Value, "", "", 1)
	}
	if string(first.Value) != scenario.ExpectedArtifact || string(second.Value) != scenario.ExpectedArtifact {
		t.Fatalf("scenario=%s treatment=%s outcome mismatch", scenario.ID, row.Treatment)
	}
	if composableacceptance.ArtifactIdentity(string(first.Value)) != oracleSHA || composableacceptance.ArtifactIdentity(string(second.Value)) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s outcome identity mismatch", scenario.ID, row.Treatment)
	}
	stats := store.Stats()
	row.CacheHits = stats.Hits
	row.FlightFollowers = 0
	if cacheOn {
		if first.CacheHit || !second.CacheHit || calls.Load() != 1 || stats.Hits != 1 {
			t.Fatalf("scenario=%s treatment=%s cache-on behavior unexpected first=%+v second=%+v calls=%d stats=%+v", scenario.ID, row.Treatment, first, second, calls.Load(), stats)
		}
	} else {
		if first.CacheHit || second.CacheHit || calls.Load() != 2 || stats.Hits != 0 {
			t.Fatalf("scenario=%s treatment=%s cache-off behavior unexpected first=%+v second=%+v calls=%d stats=%+v", scenario.ID, row.Treatment, first, second, calls.Load(), stats)
		}
	}
	if before.Writes < stats.Writes {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.store", composableacceptance.TraceEventOutcomeOK, nil, nil, first.Value, "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), first.Value, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), second.Value, "", "", 1)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func cacheLookupOutcome(hit bool) composableacceptance.TraceEventOutcome {
	if hit {
		return composableacceptance.TraceEventOutcomeHit
	}
	return composableacceptance.TraceEventOutcomeMiss
}

func runScenarioSingleFlightExecution(t *testing.T, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, singleFlight bool) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentSingleFlightOff, started, 0)
	if singleFlight {
		row.Treatment = composableacceptance.TreatmentSingleFlightOn
	}
	invocation, err := scenarioFunctionInvocation(scenario, scenarioSHA, artifactSHA)
	if err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(t.TempDir(), "single-flight-"+scenario.ID)
	store, err := agentfunction.NewStore(storeDir, scenarioSHA, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	engine := agentfunction.Engine{Store: store, CacheEnabled: false}
	var flights *agentfunction.FlightGroup
	if singleFlight {
		flights = agentfunction.NewFlightGroup()
		engine.Flights = flights
	}
	var (
		first struct {
			result agentfunction.Result
			err    error
		}
		second struct {
			result agentfunction.Result
			err    error
		}
	)
	var calls atomic.Int32
	waiterStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	computeReady := make(chan struct{}, 2)
	compute := func(_ context.Context, _ *agentfunction.Guard) ([]byte, error) {
		select {
		case computeReady <- struct{}{}:
		default:
		}
		<-release
		calls.Add(1)
		return []byte(scenario.ExpectedArtifact), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		first.result, first.err = engine.Execute(context.Background(), invocation, compute)
	}()
	if singleFlight {
		<-computeReady
		wg.Add(1)
		go func() {
			waiterStarted <- struct{}{}
			defer wg.Done()
			second.result, second.err = engine.Execute(context.Background(), invocation, compute)
		}()
		<-waiterStarted
		done := make(chan struct{})
		go func() {
			for {
				if flights.Stats().Waiters == 1 {
					close(done)
					return
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("scenario=%s single-flight on failed to establish waiter", scenario.ID)
		}
		close(release)
	} else {
		wg.Add(1)
		go func() {
			second.result, second.err = engine.Execute(context.Background(), invocation, compute)
			wg.Done()
		}()
		for range 2 {
			<-computeReady
		}
		close(release)
	}
	wg.Wait()
	if first.err != nil {
		t.Fatalf("scenario=%s treatment=%s first=%v", scenario.ID, row.Treatment, first.err)
	}
	if second.err != nil {
		t.Fatalf("scenario=%s treatment=%s second=%v", scenario.ID, row.Treatment, second.err)
	}
	if string(first.result.Value) != scenario.ExpectedArtifact || string(second.result.Value) != scenario.ExpectedArtifact {
		t.Fatalf("scenario=%s treatment=%s outcome mismatch", scenario.ID, row.Treatment)
	}
	if composableacceptance.ArtifactIdentity(string(first.result.Value)) != oracleSHA || composableacceptance.ArtifactIdentity(string(second.result.Value)) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s outcome identity mismatch", scenario.ID, row.Treatment)
	}
	if singleFlight {
		row.FlightFollowers = flights.Stats().Waiters
		if calls.Load() != 1 || flights.Stats().Leaders != 1 || row.FlightFollowers != 1 {
			t.Fatalf("scenario=%s treatment=%s calls=%d stats=%+v", scenario.ID, row.Treatment, calls.Load(), flights.Stats())
		}
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.leader", composableacceptance.TraceEventOutcomeLeader, nil, []byte(scenarioSHA), []byte(first.result.Value), "", "", 1)
		if second.result.Shared {
			recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.follower", composableacceptance.TraceEventOutcomeFollower, nil, []byte(scenarioSHA), []byte(second.result.Value), "", "", 1)
		}
	} else {
		if calls.Load() != 2 {
			t.Fatalf("scenario=%s treatment=%s calls=%d", scenario.ID, row.Treatment, calls.Load())
		}
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.leader", composableacceptance.TraceEventOutcomeLeader, nil, []byte(scenarioSHA), []byte(first.result.Value), "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.leader", composableacceptance.TraceEventOutcomeLeader, nil, []byte(scenarioSHA), []byte(second.result.Value), "", "", 1)
	}
	for i := int64(0); i < int64(calls.Load()); i++ {
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), []byte(scenario.ExpectedArtifact), "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), first.result.Value, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), second.result.Value, "", "", 1)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioFanoutExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentFanout, started, 2)

	manager, base := newComposableWorkspace(t)
	parentLineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	parentInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.ChildAnalyses) != 2 || scenario.SelectedChild < 0 || scenario.SelectedChild >= len(scenario.ChildAnalyses) {
		t.Fatalf("scenario=%s has invalid fanout selection", scenario.ID)
	}
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			factory := wazeroengine.Factory{
				WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "fanout-child-" + safeIdentifier(descriptor.ChildID),
			}
			return factory.New(ctx, artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request, err := json.Marshal(map[string]any{
				"run_id": "spark-fanout-child",
				"code": "from pathlib import Path\n" +
					"Path('/workspace/" + safeIdentifier(descriptor.ChildID) + ".txt').write_text(" + pythonStringLiteral(t, scenario.ExpectedArtifact) + ")\n" +
					"result = " + pythonStringLiteral(t, scenario.ExpectedArtifact),
				"inputs": map[string]any{},
			})
			if err != nil {
				return subagent.ChildProgram{}, err
			}
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: uint32(len(scenario.ChildAnalyses)), MaxDepth: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, child := range scenario.ChildAnalyses {
		descriptor := scenarioFanoutDescriptor(index, child, scenarioSHA, parentLineage)
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeOK, nil, []byte(descriptor.ChildID), nil, "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_start", composableacceptance.TraceEventOutcomeStarted, nil, []byte(descriptor.ChildID), nil, "", "", 1)
		if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
			recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_end", composableacceptance.TraceEventOutcomeRejected, nil, []byte(descriptor.ChildID), nil, "", "", 1)
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_end", composableacceptance.TraceEventOutcomeOK, nil, []byte(descriptor.ChildID), nil, "", "", 1)
	}
	selected := fmt.Sprintf("child-%d", scenario.SelectedChild)
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeSelected, nil, []byte(selected), nil, "", "", 1)
	joined, err := orchestrator.Seal(context.Background(), selected)
	if err != nil {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeRejected, nil, []byte(selected), nil, "", "", 1)
		t.Fatal(err)
	}
	if joined.SelectedChildID != selected {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeRejected, nil, []byte(selected), []byte(joined.SelectedChildID), "", "", 1)
		t.Fatalf("scenario=%s fanout selected=%s got=%s", scenario.ID, selected, joined.SelectedChildID)
	}
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.selected_root", composableacceptance.TraceEventOutcomeOK, nil, []byte(joined.SelectedRoot.IdentitySHA256), nil, "", "", 1)
	if !rootContainsWithSHA(t, manager, joined.SelectedRoot, safeIdentifier(selected)+".txt", oracleSHA) {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.selected_root", composableacceptance.TraceEventOutcomeRejected, nil, nil, nil, "", "", 1)
		t.Fatalf("scenario=%s fanout selected branch missing expected output", scenario.ID)
	}
	for _, discarded := range joined.DiscardedRefs {
		if _, err := manager.Inspect(discarded); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.discard", composableacceptance.TraceEventOutcomeRejected, nil, []byte(discarded), nil, "", "", 1)
			t.Fatalf("scenario=%s fanout sibling ref=%s present: %v", scenario.ID, discarded, err)
		}
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(discarded), nil, "", "", 1)
	}
	row.SelectedRootSHA256 = joined.SelectedRoot.IdentitySHA256
	row.ChangedBytes = joined.ChangedBytes
	row.MaterializedBytes = joined.MaterializedBytes
	row.GuestCreated = uint64(2)
	row.GuestDestroyed = uint64(2)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioReevaluationExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, resumeEnabled bool) (composableacceptance.Row, bool) {
	t.Helper()
	treatment := composableacceptance.TreatmentReevaluationOff
	guestCount := uint64(1)
	if resumeEnabled {
		treatment = composableacceptance.TreatmentReevaluationOn
		guestCount = 2
	}
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, treatment, started, guestCount)
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	factory := &scenarioReevaluationWorkflowGuestFactory{
		t:                   t,
		artifact:            artifact,
		manager:             manager,
		base:                base,
		baseWorkspaceSHA256: baseInfo.WorkspaceSHA256,
	}

	observedID := safeIdentifier("obs-" + scenario.Observation)
	waitID := safeIdentifier("wait-" + scenario.WaitBoundary)
	transformationID := suffixedIdentifier("transform-"+scenario.RepeatedTransformation, "-1")
	transformAgainID := suffixedIdentifier("transform-"+scenario.RepeatedTransformation, "-2")
	artifactID := suffixedIdentifier("artifact-"+scenario.ID, "-final")
	var transformCalls atomic.Int32
	transform := func(ctx context.Context, guest workflow.Guest, _ map[string][]byte) ([]byte, error) {
		call := transformCalls.Add(1)
		switch call {
		case 1, 2:
			return runWorkflowCodeAsResult(t, ctx, guest, scenario.RepeatedTransformation)
		default:
			return nil, errors.New("unexpected repeated transformation invocation")
		}
	}
	transformArtifact := func(ctx context.Context, guest workflow.Guest, _ map[string][]byte) ([]byte, error) {
		return runWorkflowCodeAsResult(t, ctx, guest, scenario.ExpectedArtifact)
	}
	graph := workflow.Graph{
		SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: safeIdentifier("spark-reeval-" + scenario.ID),
		Nodes: []workflow.Node{
			{ID: observedID, Kind: workflow.Observation, VersionSHA256: hashBytes([]byte("observe:" + scenario.Observation)), RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
				return workflow.ObservedValue{
					Value:           []byte(scenario.Observation),
					FreshnessSHA256: hashBytes([]byte("fresh:" + scenario.Observation)),
					PolicySHA256:    hashBytes([]byte("policy:" + scenario.Observation)),
				}, nil
			}},
			{ID: waitID, Kind: workflow.Wait, VersionSHA256: hashBytes([]byte("wait:" + scenario.WaitBoundary)), Dependencies: []string{observedID}},
			{ID: transformationID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("transform:" + scenario.RepeatedTransformation + "-1")), Dependencies: []string{observedID}, Compute: transform},
			{ID: transformAgainID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("transform:" + scenario.RepeatedTransformation + "-2")), Dependencies: []string{transformationID}, Compute: transform},
			{ID: artifactID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("artifact:" + scenarioSHA)), Dependencies: []string{transformAgainID}, Compute: transformArtifact},
			{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: hashBytes([]byte("terminal")), Dependencies: []string{artifactID}},
		},
	}
	evaluator, err := workflow.New(workflow.Config{
		Graph: graph, Guests: factory, ResumeEnabled: resumeEnabled,
		ImmutableRootSHA256: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "guest.workflow", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.begin", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenario.WaitBoundary), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.initial", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	suspended, err := evaluator.Start(context.Background(), []byte(`{"scenario":"`+scenario.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "guest.workflow", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), []byte(suspended.State.WaitNodeID), "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.begin", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.WaitBoundary), []byte(suspended.State.WaitNodeID), "", "", 1)
	if suspended.Disposition != workflow.Suspended {
		t.Fatalf("scenario=%s reevaluation start=%+v", scenario.ID, suspended)
	}
	if suspended.State.WaitNodeID != waitID {
		t.Fatalf("scenario=%s reevaluation wait=%s got=%s", scenario.ID, waitID, suspended.State.WaitNodeID)
	}
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.initial", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), []byte(scenario.Observation), "", "", 1)
	if !resumeEnabled {
		row.Status = "rejected"
		row.TerminalDisposition = "mechanism_disabled"
		row.GuestCreated = uint64(factory.created)
		row.GuestDestroyed = uint64(factory.closed)
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.release", composableacceptance.TraceEventOutcomeRejected, nil, nil, []byte("disabled"), "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.disabled", composableacceptance.TraceEventOutcomeRejected, nil, nil, []byte("disabled"), "", "", 1)
		if _, err := evaluator.Resume(context.Background(), suspended.State); !errors.Is(err, workflow.ErrResumeDisabled) {
			t.Fatalf("scenario=%s reevaluation off resume=%v", scenario.ID, err)
		}
		completeTraceRow(&row, started, recorder)
		return row, true
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "guest.workflow", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.release", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	resumed, err := evaluator.Resume(context.Background(), suspended.State)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "guest.workflow", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), []byte(resumed.State.WaitNodeID), "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.release", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(resumed.State.WaitNodeID), "", "", 1)
	if resumed.Disposition != workflow.Completed {
		t.Fatalf("scenario=%s reevaluation resume=%+v", scenario.ID, resumed)
	}
	if transformCalls.Load() != 2 {
		t.Fatalf("scenario=%s reevaluation transform-calls=%d", scenario.ID, transformCalls.Load())
	}
	if resumed.Metrics.Refreshed == 0 {
		t.Fatalf("scenario=%s reevaluation did not refresh observation", scenario.ID)
	}
	if got := string(resumed.Output); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s reevaluation output mismatch", scenario.ID)
	}
	if resumed.Metrics.Invalidated > 0 {
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.reuse", composableacceptance.TraceEventOutcomeRejected, nil, nil, nil, "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.fresh", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(resumed.Output), "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.reuse", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(resumed.Output), "", "", 1)
	}
	row.GuestCreated = uint64(factory.created)
	row.GuestDestroyed = uint64(factory.closed)
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), resumed.Output, "", "", 1)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioAllExecution(t *testing.T, artifact []byte, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentAll, started, 1)
	manager, base := newComposableWorkspace(t)

	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	parentLineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenario.ChildAnalyses) != 2 || scenario.SelectedChild < 0 || scenario.SelectedChild >= len(scenario.ChildAnalyses) {
		t.Fatalf("scenario=%s all has invalid fanout selection", scenario.ID)
	}
	parentAttempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
	factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: parentAttempt.Ref(), WorkspaceOwner: "all-spark-parent-" + scenario.ID}
	parentRunner, err := factory.New(context.Background(), artifact, config)
	if err != nil {
		if errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
			row.Status = "skipped"
			row.GuestCreated = 0
			row.GuestDestroyed = 0
			row.TerminalDisposition = "streaming_unavailable"
			completeTraceRow(&row, started, recorder)
			return row, true
		}
		t.Fatal(err)
	}
	streamRunner, ok := parentRunner.(streaming.StreamRunner)
	if !ok {
		_ = parentRunner.Close(context.Background())
		row.Status = "skipped"
		row.GuestCreated = 0
		row.GuestDestroyed = 0
		row.TerminalDisposition = "streaming_unavailable"
		if err := parentAttempt.Discard(); err != nil {
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
		completeTraceRow(&row, started, recorder)
		return row, true
	}

	prepares, err := streaming.BuildPrepareChunks(streaming.PrepareConfig{
		Inputs: json.RawMessage(`{}`),
		Chunks: []string{
			"scenario_identity = " + pythonStringLiteral(t, scenarioSHA) + "\n",
			"result = " + pythonStringLiteral(t, scenario.ExpectedArtifact) + "\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var childGuests atomic.Int32
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(_ context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			childGuests.Add(1)
			factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "all-spark-child-" + safeIdentifier(descriptor.ChildID)}
			return factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request, err := json.Marshal(map[string]any{
				"run_id": "spark-all-child",
				"code": "from pathlib import Path\n" +
					"Path('/workspace/" + safeIdentifier(descriptor.ChildID) + ".txt').write_text(" + pythonStringLiteral(t, scenario.ExpectedArtifact) + ")\n" +
					"result = " + pythonStringLiteral(t, scenario.ExpectedArtifact),
				"inputs": map[string]any{},
			})
			if err != nil {
				return subagent.ChildProgram{}, err
			}
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: uint32(len(scenario.ChildAnalyses)), MaxDepth: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	type streamOutcome struct {
		result streaming.RunResult
		err    error
	}
	prepareChannel := make(chan string, len(prepares))
	completed := make(chan streamOutcome, 1)
	go func() {
		request := []byte(`{"run_id":"all-spark-parent","code":"result = stream_final['result']","inputs":{}}`)
		result, runErr := streaming.ExecuteStream(context.Background(), streamRunner, parentAttempt, request, prepareChannel)
		completed <- streamOutcome{result: result, err: runErr}
	}()
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.begin", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	for _, prepare := range prepares {
		prepareChannel <- prepare
		recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.prepare", composableacceptance.TraceEventOutcomeOK, nil, []byte(prepare), nil, "", "", 1)
	}
	close(prepareChannel)
	finished := <-completed
	if finished.err != nil {
		row.Status = "rejected"
		row.TerminalDisposition = "streaming_failed"
		recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.end", composableacceptance.TraceEventOutcomeError, nil, nil, nil, "", "", 1)
		_ = parentAttempt.Discard()
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
		if err := parentRunner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeError, nil, nil, nil, "", "", 1)
		t.Fatalf("scenario=%s treatment=%s stream error=%v", scenario.ID, composableacceptance.TreatmentAll, finished.err)
	}
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.seal", composableacceptance.TraceEventOutcomeSealed, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeStreaming, "stream.end", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if publishedInfo, err := manager.Inspect(finished.result.PublishedWorkspace); err == nil {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.commit", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(publishedInfo.WorkspaceSHA256), "", "", 1)
	}
	if err := parentRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if got := responseStringResult(t, finished.result.Response); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s stream outcome mismatch", scenario.ID, composableacceptance.TreatmentAll)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), []byte(finished.result.Response), "", "", 1)

	selected := fmt.Sprintf("child-%d", scenario.SelectedChild)
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeStarted, nil, []byte(selected), nil, "", "", 1)
	for index, child := range scenario.ChildAnalyses {
		descriptor := scenarioFanoutDescriptor(index, child, scenarioSHA, parentLineage)
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeOK, nil, []byte(descriptor.ChildID), nil, "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_start", composableacceptance.TraceEventOutcomeStarted, nil, []byte(descriptor.ChildID), nil, "", "", 1)
		if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
			recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_end", composableacceptance.TraceEventOutcomeRejected, nil, []byte(descriptor.ChildID), nil, "", "", 1)
			t.Fatal(err)
		}
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_end", composableacceptance.TraceEventOutcomeOK, nil, []byte(descriptor.ChildID), nil, "", "", 1)
	}
	joined, err := orchestrator.Seal(context.Background(), selected)
	if err != nil {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeRejected, nil, []byte(selected), nil, "", "", 1)
		t.Fatal(err)
	}
	if joined.SelectedChildID != selected {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeRejected, nil, []byte(selected), []byte(joined.SelectedChildID), "", "", 1)
		t.Fatalf("scenario=%s treatment=%s selected=%s got=%s", scenario.ID, composableacceptance.TreatmentAll, selected, joined.SelectedChildID)
	}
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.select", composableacceptance.TraceEventOutcomeSelected, nil, []byte(joined.SelectedChildID), nil, "", "", 1)
	selectedRootPath := safeIdentifier(selected) + ".txt"
	if !rootContainsWithSHA(t, manager, joined.SelectedRoot, selectedRootPath, oracleSHA) {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.selected_root", composableacceptance.TraceEventOutcomeRejected, nil, nil, nil, "", "", 1)
		t.Fatalf("scenario=%s treatment=%s selected root missing artifact", scenario.ID, composableacceptance.TreatmentAll)
	}
	for _, discarded := range joined.DiscardedRefs {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(discarded), nil, "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.selected_root", composableacceptance.TraceEventOutcomeOK, nil, []byte(joined.SelectedRoot.IdentitySHA256), nil, "", "", 1)

	invocation, err := scenarioFunctionInvocation(scenario, scenarioSHA, artifactSHA)
	if err != nil {
		t.Fatal(err)
	}
	invocation.ImmutableRootSHA256 = []string{joined.SelectedRoot.WorkspaceSHA256}
	invocationIdentity, _, err := invocation.Identity()
	if err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(t.TempDir(), "all-cache-"+scenario.ID)
	store, err := agentfunction.NewStore(storeDir, scenarioSHA, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cache := agentfunction.Engine{
		Store: store, CacheEnabled: true, Flights: agentfunction.NewFlightGroup(),
	}
	var calls atomic.Int32
	release := make(chan struct{})
	computeReady := make(chan struct{}, 1)
	compute := func(_ context.Context, _ *agentfunction.Guard) ([]byte, error) {
		select {
		case computeReady <- struct{}{}:
		default:
		}
		<-release
		calls.Add(1)
		return []byte(scenario.ExpectedArtifact), nil
	}
	var first struct {
		result agentfunction.Result
		err    error
	}
	var second struct {
		result agentfunction.Result
		err    error
	}
	var cacheWait sync.WaitGroup
	cacheWait.Add(1)
	go func() {
		defer cacheWait.Done()
		first.result, first.err = cache.Execute(context.Background(), invocation, compute)
	}()
	<-computeReady
	cacheWait.Add(1)
	go func() {
		defer cacheWait.Done()
		second.result, second.err = cache.Execute(context.Background(), invocation, compute)
	}()
	followerReady := make(chan struct{})
	go func() {
		for {
			if cache.Flights.Stats().Waiters == 1 {
				close(followerReady)
				return
			}
		}
	}()
	select {
	case <-followerReady:
	case <-time.After(2 * time.Second):
		t.Fatalf("scenario=%s treatment=%s failed to establish single-flight follower", scenario.ID, composableacceptance.TreatmentAll)
	}
	close(release)
	cacheWait.Wait()
	if first.err != nil {
		t.Fatalf("scenario=%s treatment=%s first cache err=%v", scenario.ID, composableacceptance.TreatmentAll, first.err)
	}
	if second.err != nil {
		t.Fatalf("scenario=%s treatment=%s second cache err=%v", scenario.ID, composableacceptance.TreatmentAll, second.err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(first.result.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if first.result.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, first.result.Value, "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), first.result.Value, "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(second.result.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if second.result.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, second.result.Value, "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), second.result.Value, "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.leader", composableacceptance.TraceEventOutcomeLeader, nil, []byte(invocationIdentity), first.result.Value, "", "", 1)
	if second.result.Shared {
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.follower", composableacceptance.TraceEventOutcomeFollower, nil, []byte(invocationIdentity), second.result.Value, "", "", 1)
	}
	if string(first.result.Value) != scenario.ExpectedArtifact || string(second.result.Value) != scenario.ExpectedArtifact {
		t.Fatalf("scenario=%s treatment=%s cache outcome mismatch", scenario.ID, composableacceptance.TreatmentAll)
	}
	if calls.Load() != 1 {
		t.Fatalf("scenario=%s treatment=%s cache calls=%d", scenario.ID, composableacceptance.TreatmentAll, calls.Load())
	}
	replay, err := cache.Execute(context.Background(), invocation, compute)
	if err != nil || !replay.CacheHit || string(replay.Value) != scenario.ExpectedArtifact {
		t.Fatalf("scenario=%s treatment=%s cache replay=%+v err=%v", scenario.ID, composableacceptance.TreatmentAll, replay, err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(replay.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, replay.Value, "", "", 1)
	for i := 0; i < int(calls.Load()); i++ {
		recorder.append(composableacceptance.TraceEventTypeSingleFlight, "single_flight.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), first.result.Value, "", "", 1)
	}
	row.CacheHits = cache.Store.Stats().Hits
	row.FlightFollowers = cache.Flights.Stats().Waiters
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), first.result.Value, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), second.result.Value, "", "", 1)

	if callStats := cache.Flights.Stats(); callStats.Waiters != 1 {
		t.Fatalf("scenario=%s treatment=%s flight stats=%+v", scenario.ID, composableacceptance.TreatmentAll, callStats)
	}

	workflowFactory := &scenarioReevaluationWorkflowGuestFactory{
		t:                   t,
		artifact:            artifact,
		manager:             manager,
		base:                joined.SelectedRoot.Ref(),
		baseWorkspaceSHA256: joined.SelectedRoot.WorkspaceSHA256,
	}
	observationID := suffixedIdentifier("observe-"+scenario.Observation, "-all")
	transformID := suffixedIdentifier("transform-"+scenario.RepeatedTransformation, "-a1")
	artifactID := suffixedIdentifier("artifact-"+scenario.ID, "-all")
	waitID := suffixedIdentifier("wait-"+scenario.WaitBoundary, "-all")
	terminalID := suffixedIdentifier("terminal-"+scenario.ID, "-all")
	graph := workflow.Graph{
		SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: safeIdentifier("all-workflow-" + scenario.ID),
		Nodes: []workflow.Node{
			{ID: observationID, Kind: workflow.Observation, VersionSHA256: hashBytes([]byte("observe:" + scenario.Observation)), RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
				return workflow.ObservedValue{Value: []byte(scenario.Observation), FreshnessSHA256: hashBytes([]byte("fresh:" + scenario.Observation)), PolicySHA256: hashBytes([]byte("policy:" + scenario.Observation))}, nil
			}},
			{ID: waitID, Kind: workflow.Wait, VersionSHA256: hashBytes([]byte("wait-all:" + scenario.WaitBoundary)), Dependencies: []string{observationID}},
			{ID: transformID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("transform-all:" + scenario.RepeatedTransformation)), Dependencies: []string{observationID}, Compute: func(ctx context.Context, guest workflow.Guest, values map[string][]byte) ([]byte, error) {
				return runWorkflowCodeAsResult(t, ctx, guest, scenario.RepeatedTransformation)
			}},
			{ID: artifactID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("artifact-all:" + scenarioSHA)), Dependencies: []string{transformID}, Compute: func(ctx context.Context, guest workflow.Guest, values map[string][]byte) ([]byte, error) {
				return runWorkflowCodeAsResult(t, ctx, guest, scenario.ExpectedArtifact)
			}},
			{ID: terminalID, Kind: workflow.Terminal, VersionSHA256: hashBytes([]byte("terminal-all:" + scenario.ID)), Dependencies: []string{artifactID}},
		},
	}
	workflowEvaluator, err := workflow.New(workflow.Config{
		Graph: graph, Guests: workflowFactory, ResumeEnabled: true,
		ImmutableRootSHA256: []string{joined.SelectedRoot.WorkspaceSHA256},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.begin", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	suspended, err := workflowEvaluator.Start(context.Background(), []byte(`{"scenario":"`+scenario.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.initial", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.release", composableacceptance.TraceEventOutcomeStarted, nil, []byte(suspended.State.WaitNodeID), nil, "", "", 1)
	if suspended.Disposition != workflow.Suspended {
		t.Fatalf("scenario=%s treatment=%s reeval start=%+v", scenario.ID, composableacceptance.TreatmentAll, suspended)
	}
	if suspended.State.WaitNodeID != waitID {
		t.Fatalf("scenario=%s treatment=%s reeval wait=%s got=%s", scenario.ID, composableacceptance.TreatmentAll, waitID, suspended.State.WaitNodeID)
	}
	resumed, err := workflowEvaluator.Resume(context.Background(), suspended.State)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.release", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(resumed.State.WaitNodeID), "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.changed", composableacceptance.TraceEventOutcomeOK, nil, nil, []byte(scenario.Observation), "", "", 1)
	if resumed.Disposition != workflow.Completed {
		t.Fatalf("scenario=%s treatment=%s reeval resume=%+v", scenario.ID, composableacceptance.TreatmentAll, resumed)
	}
	if got := string(resumed.Output); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s treatment=%s reeval output mismatch", scenario.ID, composableacceptance.TreatmentAll)
	}
	if resumed.Metrics.Invalidated > 0 || resumed.Metrics.Recomputed > 0 {
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.fresh", composableacceptance.TraceEventOutcomeOK, nil, nil, resumed.Output, "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.reuse", composableacceptance.TraceEventOutcomeOK, nil, nil, resumed.Output, "", "", 1)
	}
	if resumed.Metrics.Lookups == 0 {
		t.Fatalf("scenario=%s treatment=%s reeval metrics %+v", scenario.ID, composableacceptance.TreatmentAll, resumed.Metrics)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), resumed.Output, "", "", 1)
	row.SelectedRootSHA256 = joined.SelectedRoot.IdentitySHA256
	row.ChangedBytes = joined.ChangedBytes
	row.MaterializedBytes = joined.MaterializedBytes
	row.GuestCreated = uint64(1) + uint64(childGuests.Load()) + uint64(workflowFactory.created)
	row.GuestDestroyed = row.GuestCreated
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioInvalidParentExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentInvalidParent, started, 0)
	row.TerminalDisposition = "parent_invalid"
	row.Status = "rejected"
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	var childGuests atomic.Int32
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(_ context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			childGuests.Add(1)
			factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "invalid-parent-child-" + safeIdentifier(descriptor.ChildID)}
			return factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request, err := json.Marshal(map[string]any{
				"run_id": "spark-invalid-parent-child",
				"code":   "result = 'private'",
				"inputs": map[string]any{},
			})
			if err != nil {
				return subagent.ChildProgram{}, err
			}
			_ = descriptor
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: lineage, MaxFanout: uint32(len(scenario.ChildAnalyses)), MaxDepth: 1,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		if err := orchestrator.Stage(context.Background(), scenarioFanoutDescriptor(index, scenario.ChildAnalyses[index], scenarioSHA, lineage)); err != nil {
			t.Fatal(err)
		}
	}
	refs := orchestrator.PrivateRefs()
	if len(refs) != 2 {
		t.Fatalf("scenario=%s invalid_parent private refs=%d", scenario.ID, len(refs))
	}
	if err := orchestrator.Abort(context.Background(), subagent.ParentInvalid); err != nil {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "validation.reject", composableacceptance.TraceEventOutcomeError, nil, nil, []byte(err.Error()), "", "", 1)
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "validation.reject", composableacceptance.TraceEventOutcomeRejected, nil, []byte(string(subagent.ParentInvalid)), nil, "", "", 1)
	for _, ref := range refs {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(ref), nil, "", "", 1)
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeRejected, nil, []byte(ref), nil, "", "", 1)
			t.Fatalf("scenario=%s invalid_parent private ref=%s retained: %v", scenario.ID, ref, err)
		}
	}
	final, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	if final.WorkspaceSHA256 != baseInfo.WorkspaceSHA256 {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.conflict", composableacceptance.TraceEventOutcomeConflict, nil, []byte(baseInfo.WorkspaceSHA256), []byte(final.WorkspaceSHA256), "", "", 1)
		t.Fatalf("scenario=%s invalid_parent mutated base", scenario.ID)
	}
	row.GuestCreated = uint64(childGuests.Load())
	row.GuestDestroyed = row.GuestCreated
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioInvalidChildExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentInvalidChild, started, uint64(2))
	row.Status = "rejected"
	row.TerminalDisposition = "child_execution_failed"
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	failedChild := 0
	if scenario.SelectedChild == 0 {
		failedChild = 1
	}
	selectedChild := fmt.Sprintf("child-%d", scenario.SelectedChild)
	selectedCode := `result = ` + pythonStringLiteral(t, scenario.ExpectedArtifact)
	var childGuests atomic.Int32
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(_ context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			childGuests.Add(1)
			factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "invalid-child-" + safeIdentifier(descriptor.ChildID)}
			return factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			code := selectedCode
			if descriptor.ChildID == fmt.Sprintf("child-%d", failedChild) {
				code = "raise RuntimeError(\"child rejected\")"
			}
			request, err := json.Marshal(map[string]any{
				"run_id": "invalid-child",
				"code":   code,
				"inputs": map[string]any{},
			})
			if err != nil {
				return subagent.ChildProgram{}, err
			}
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: lineage, MaxFanout: uint32(len(scenario.ChildAnalyses)), MaxDepth: 1,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		if err := orchestrator.Stage(context.Background(), scenarioFanoutDescriptor(index, scenario.ChildAnalyses[index], scenarioSHA, lineage)); err != nil {
			t.Fatal(err)
		}
	}
	var sealErr error
	_, sealErr = orchestrator.Seal(context.Background(), selectedChild)
	if sealErr == nil {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_error", composableacceptance.TraceEventOutcomeSkipped, nil, []byte(selectedChild), nil, "", "", 1)
		t.Fatalf("scenario=%s invalid_child expected child execution failure got=nil", scenario.ID)
	}
	if !errors.Is(sealErr, subagent.ErrChildExecution) {
		recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_error", composableacceptance.TraceEventOutcomeConflict, nil, []byte(hashBytes([]byte(selectedChild))), []byte(hashBytes([]byte(sealErr.Error()))), "", "", 1)
		t.Fatalf("scenario=%s invalid_child expected child execution failure got=%v", scenario.ID, sealErr)
	}
	recorder.append(composableacceptance.TraceEventTypeFanout, "fanout.child_error", composableacceptance.TraceEventOutcomeRejected, nil, []byte(selectedChild), []byte(subagent.ErrChildExecution.Error()), "", "", 1)
	for _, ref := range orchestrator.PrivateRefs() {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(ref), nil, "", "", 1)
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("invalid_child private ref=%s: %v", ref, err)
		}
	}
	final, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	if final.WorkspaceSHA256 != baseInfo.WorkspaceSHA256 {
		t.Fatalf("scenario=%s invalid_child mutated base", scenario.ID)
	}
	row.GuestCreated = uint64(childGuests.Load())
	row.GuestDestroyed = row.GuestCreated
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioChangedObservationExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentChangedObserve, started, 1)
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	factory := &scenarioReevaluationWorkflowGuestFactory{
		t: t, artifact: artifact, manager: manager, base: base, baseWorkspaceSHA256: baseInfo.WorkspaceSHA256,
	}
	observation := scenario.Observation
	independentID := suffixedIdentifier("independent-"+scenario.ID, "-1")
	observationID := suffixedIdentifier("observe-"+scenario.Observation, "-obs")
	transformID := suffixedIdentifier("transform-"+scenario.RepeatedTransformation, "-obs")
	waitID := suffixedIdentifier("wait-"+scenario.WaitBoundary, "-obs")
	artifactID := suffixedIdentifier("artifact-"+scenario.ID, "-obs")
	terminalID := suffixedIdentifier("terminal-"+scenario.ID, "-obs")
	graph := workflow.Graph{
		SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: safeIdentifier("changed-observation-" + scenario.ID),
		Nodes: []workflow.Node{
			{ID: independentID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("independent:" + scenario.ID)), Compute: func(context.Context, workflow.Guest, map[string][]byte) ([]byte, error) {
				return []byte("stable"), nil
			}},
			{ID: observationID, Kind: workflow.Observation, VersionSHA256: hashBytes([]byte("observe:" + scenario.Observation)), RefreshOnResume: true, Dependencies: []string{independentID}, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
				return workflow.ObservedValue{Value: []byte(observation), FreshnessSHA256: hashBytes([]byte("fresh:" + observation)), PolicySHA256: hashBytes([]byte("policy:" + scenario.ID))}, nil
			}},
			{ID: waitID, Kind: workflow.Wait, VersionSHA256: hashBytes([]byte("wait:" + scenario.WaitBoundary)), Dependencies: []string{observationID}},
			{ID: transformID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("transform:" + scenario.RepeatedTransformation)), Dependencies: []string{observationID}, Compute: func(ctx context.Context, guest workflow.Guest, values map[string][]byte) ([]byte, error) {
				return runWorkflowCodeAsResult(t, ctx, guest, scenario.RepeatedTransformation)
			}},
			{ID: artifactID, Kind: workflow.Compute, VersionSHA256: hashBytes([]byte("artifact:" + scenarioSHA)), Dependencies: []string{transformID, independentID}, Compute: func(ctx context.Context, guest workflow.Guest, values map[string][]byte) ([]byte, error) {
				return runWorkflowCodeAsResult(t, ctx, guest, scenario.ExpectedArtifact)
			}},
			{ID: terminalID, Kind: workflow.Terminal, VersionSHA256: hashBytes([]byte("terminal:" + scenario.ID)), Dependencies: []string{artifactID}},
		},
	}
	evaluator, err := workflow.New(workflow.Config{
		Graph: graph, Guests: factory, ResumeEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.begin", composableacceptance.TraceEventOutcomeStarted, nil, []byte(waitID), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.initial", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	suspended, err := evaluator.Start(context.Background(), []byte(`{"scenario":"`+scenario.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "wait.begin", composableacceptance.TraceEventOutcomeOK, nil, []byte(waitID), []byte(suspended.State.WaitNodeID), "", "", 1)
	if suspended.Disposition != workflow.Suspended || suspended.State.WaitNodeID != waitID {
		t.Fatalf("scenario=%s changed_observation start=%+v", scenario.ID, suspended)
	}
	observation = scenario.RepeatedTransformation
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.initial", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.Observation), []byte(observation), "", "", 1)
	resumed, err := evaluator.Resume(context.Background(), suspended.State)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeObservation, "observation.changed", composableacceptance.TraceEventOutcomeOK, nil, []byte(observation), []byte(scenario.RepeatedTransformation), "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeWaitResume, "resume.fresh", composableacceptance.TraceEventOutcomeOK, nil, nil, resumed.Output, "", "", 1)
	if resumed.Disposition != workflow.Completed {
		t.Fatalf("scenario=%s changed_observation resume=%+v", scenario.ID, resumed)
	}
	if resumed.Metrics.Invalidated == 0 {
		t.Fatalf("scenario=%s changed_observation invalidated=%d", scenario.ID, resumed.Metrics.Invalidated)
	}
	if got := string(resumed.Output); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s changed_observation output mismatch", scenario.ID)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), resumed.Output, "", "", 1)
	row.GuestCreated = uint64(factory.created)
	row.GuestDestroyed = uint64(factory.closed)
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioBranchConflictExecution(t *testing.T, _ []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentBranchConflict, started, 0)
	row.Status = "rejected"
	row.TerminalDisposition = "expected_base_conflict"
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	root, err := branch.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	wrongParent := hashBytes([]byte("branch-conflict:" + scenario.ID + ":" + scenarioSHA))
	if _, err := manager.SelectRoot(wrongParent, []workspace.Root{root}, root.IdentitySHA256); !errors.Is(err, workspace.ErrWorkspaceConflict) {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.commit", composableacceptance.TraceEventOutcomeConflict, nil, []byte(wrongParent), nil, "", "", 1)
		t.Fatalf("scenario=%s expected root conflict err=%v", scenario.ID, err)
	} else {
		recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.conflict", composableacceptance.TraceEventOutcomeConflict, nil, []byte(wrongParent), []byte(baseInfo.WorkspaceSHA256), "", "", 1)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, nil, nil, "", "", 1)
	if err := manager.Destroy(root.Ref()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(root.IdentitySHA256), nil, "", "", 1)
	if _, err := manager.Inspect(base); err != nil {
		t.Fatal(err)
	}
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioCacheCorruptionExecution(t *testing.T, artifactSHA string, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentCacheCorruption, started, 0)
	storeDir := filepath.Join(t.TempDir(), "cache-corruption-"+scenario.ID)
	store, err := agentfunction.NewStore(storeDir, scenarioSHA, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := scenarioFunctionInvocation(scenario, scenarioSHA, artifactSHA)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := invocation.Identity()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	engine := agentfunction.Engine{Store: store, CacheEnabled: true}
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		calls.Add(1)
		return []byte(scenario.ExpectedArtifact), nil
	}
	first, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	invocationIdentity, _, err := invocation.Identity()
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(first.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if first.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, first.Value, "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), first.Value, "", "", 1)
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.write", composableacceptance.TraceEventOutcomeOK, nil, []byte(first.Value), []byte(scenario.ExpectedArtifact), "", "", 1)
	}
	if got := string(first.Value); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s cache_corruption first mismatch", scenario.ID)
	}
	corruptedPath := filepath.Join(store.Directory(), key+".json")
	if err := os.WriteFile(corruptedPath, []byte("invalid cache record"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.corrupt", composableacceptance.TraceEventOutcomeError, nil, []byte(key), nil, "", "", 1)
	second, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.detect", composableacceptance.TraceEventOutcomeRecovered, nil, []byte(invocationIdentity), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(second.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	if second.CacheHit {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, second.Value, "", "", 1)
	} else {
		recorder.append(composableacceptance.TraceEventTypeCache, "cache.compute", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenarioSHA), second.Value, "", "", 1)
	}
	if got := string(second.Value); got != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(got) != oracleSHA {
		t.Fatalf("scenario=%s cache_corruption replay mismatch", scenario.ID)
	}
	if calls.Load() != 2 {
		t.Fatalf("scenario=%s cache_corruption calls=%d", scenario.ID, calls.Load())
	}
	stats := store.Stats()
	if stats.Corruptions != 1 {
		t.Fatalf("scenario=%s cache_corruption corruptions=%d", scenario.ID, stats.Corruptions)
	}
	replay, err := engine.Execute(context.Background(), invocation, compute)
	if err != nil || !replay.CacheHit {
		t.Fatalf("scenario=%s cache_corruption replay cache_hit=%v err=%v", scenario.ID, replay.CacheHit, err)
	}
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.lookup", cacheLookupOutcome(replay.CacheHit), nil, []byte(invocationIdentity), nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeCache, "cache.hit", composableacceptance.TraceEventOutcomeHit, nil, nil, replay.Value, "", "", 1)
	if calls.Load() != 2 {
		t.Fatalf("scenario=%s cache_corruption callcount=%d", scenario.ID, calls.Load())
	}
	row.CacheHits = store.Stats().Hits
	completeTraceRow(&row, started, recorder)
	return row, true
}

func runScenarioCancellationExecution(t *testing.T, artifact []byte, scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string) (composableacceptance.Row, bool) {
	t.Helper()
	started := time.Now()
	row, recorder := scenarioRow(scenario, scenarioSHA, oracleSHA, composableacceptance.TreatmentCancellation, started, 2)
	row.TerminalDisposition = "cancelled_recovered"
	manager, base := newComposableWorkspace(t)

	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "cancellation-spark-" + scenario.ID}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.create", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	cancelCtx, cancel := context.WithCancel(context.Background())
	longRequest := []byte(`{"run_id":"spark-cancel-long","code":"import time\nwhile True:\n    time.sleep(0.25)","inputs":{}}`)
	type cancellationOutcome struct {
		err error
	}
	complete := make(chan cancellationOutcome, 1)
	go func() {
		_, runErr := runner.Run(cancelCtx, longRequest, "")
		complete <- cancellationOutcome{err: runErr}
	}()
	time.Sleep(20 * time.Millisecond)
	recorder.append(composableacceptance.TraceEventTypeCancellation, "cancellation.requested", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	cancel()
	recorder.append(composableacceptance.TraceEventTypeCancellation, "cancellation.observed", composableacceptance.TraceEventOutcomeCancelled, nil, nil, nil, "", "", 1)
	res := <-complete
	if res.err == nil || !errors.Is(res.err, context.Canceled) {
		t.Fatalf("scenario=%s cancellation expected context canceled err=%v", scenario.ID, res.err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeError, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeCancellation, "cleanup.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(fmt.Sprintf("%v", attempt.Ref())), nil, "", "", 1)
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if err := attempt.Discard(); err != nil {
		t.Fatal(err)
	}

	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.fork", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	recoveryAttempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	recoveryFactory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: recoveryAttempt.Ref(), WorkspaceOwner: "cancellation-recovery-" + scenario.ID}
	recoveryRunner, err := recoveryFactory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.create", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeStarted, nil, nil, nil, "", "", 1)
	response, err := recoveryRunner.Run(context.Background(), scenarioRequest(t, scenario, scenarioSHA), "")
	if err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.run", composableacceptance.TraceEventOutcomeOK, nil, nil, response, "", "", 1)
	if recovered := responseStringResult(t, response); recovered != scenario.ExpectedArtifact || composableacceptance.ArtifactIdentity(recovered) != oracleSHA {
		t.Fatalf("scenario=%s cancellation recovery mismatch", scenario.ID)
	}
	recorder.append(composableacceptance.TraceEventTypeOracle, "oracle.compare", composableacceptance.TraceEventOutcomeOK, nil, []byte(scenario.ExpectedArtifact), []byte(response), "", "", 1)
	if err := recoveryRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeGuestLifecycle, "guest.close", composableacceptance.TraceEventOutcomeOK, nil, nil, nil, "", "", 1)
	if err := recoveryAttempt.Discard(); err != nil {
		t.Fatal(err)
	}
	recorder.append(composableacceptance.TraceEventTypeWorkspace, "workspace.discard", composableacceptance.TraceEventOutcomeDiscarded, nil, []byte(fmt.Sprintf("%v", recoveryAttempt.Ref())), nil, "", "", 1)
	completeTraceRow(&row, started, recorder)
	return row, true
}

type scenarioReevaluationWorkflowGuestFactory struct {
	t                   *testing.T
	artifact            []byte
	manager             *workspace.Manager
	base                workspace.Ref
	baseWorkspaceSHA256 string
	created             uint64
	closed              uint64
}

func (factory *scenarioReevaluationWorkflowGuestFactory) NewGuest(ctx context.Context) (workflow.Guest, error) {
	branch, err := factory.manager.ForkBranch(factory.base, factory.baseWorkspaceSHA256)
	if err != nil {
		return nil, err
	}
	factory.created++
	runner, err := (wazeroengine.Factory{
		WorkspaceManager: factory.manager, WorkspaceRef: branch.Ref(), WorkspaceOwner: "reeval-workflow",
	}).New(ctx, factory.artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		_ = branch.Discard()
		return nil, err
	}
	return &scenarioReevaluationWorkflowGuest{
		factory: factory,
		runner:  runner,
		branch:  branch,
	}, nil
}

type scenarioReevaluationWorkflowGuest struct {
	factory *scenarioReevaluationWorkflowGuestFactory
	runner  engine.Runner
	branch  *workspace.Branch
}

func (guest *scenarioReevaluationWorkflowGuest) Run(ctx context.Context, code string) ([]byte, error) {
	request, err := json.Marshal(map[string]any{
		"run_id": "reeval-workflow",
		"code":   code,
		"inputs": map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	return guest.runner.Run(ctx, request, "")
}

func (guest *scenarioReevaluationWorkflowGuest) Close(ctx context.Context) error {
	guest.factory.closed++
	return errors.Join(guest.runner.Close(ctx), guest.branch.Discard())
}

func runWorkflowCodeAsResult(t *testing.T, ctx context.Context, guest workflow.Guest, value string) ([]byte, error) {
	t.Helper()
	evaluatorGuest, ok := guest.(*scenarioReevaluationWorkflowGuest)
	if !ok {
		return nil, errors.New("unexpected reevaluation workflow guest type")
	}
	response, err := evaluatorGuest.Run(ctx, "result = "+pythonStringLiteral(t, value))
	if err != nil {
		return nil, err
	}
	return []byte(responseStringResult(t, response)), nil
}

func scenarioFanoutDescriptor(index int, analysis, scenarioSHA, parentLineage string) subagent.Descriptor {
	return subagent.Descriptor{
		SchemaVersion:          subagent.DescriptorSchemaVersion,
		ChildID:                fmt.Sprintf("child-%d", index),
		ParentStreamEpoch:      "spark-parent-stream",
		ParentLineageSHA256:    parentLineage,
		SourceOccurrence:       fmt.Sprintf("scenario-child:%d", index),
		SourceSHA256:           hashBytes([]byte(analysis)),
		InputsSHA256:           scenarioSHA,
		ArtifactSHA256:         hashBytes([]byte("spark-fanout-artifact")),
		ExecutionProfileSHA256: hashBytes([]byte("spark-fanout-profile")),
		ChildPlanSHA256:        hashBytes([]byte(fmt.Sprintf("%s:%d", scenarioSHA, index))),
		PrivacyPartition:       "spark-private",
		Depth:                  1,
	}
}

func rootContainsWithSHA(t *testing.T, manager *workspace.Manager, root workspace.Root, path string, expectedSHA string) bool {
	t.Helper()
	branch, err := manager.ForkBranch(root.Ref(), root.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = branch.Discard() }()
	lease, err := manager.Acquire(branch.Ref(), "verify-fanout")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := lease.Snapshot()
	_ = lease.Release()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snapshot.Entries {
		if entry.Path == path && entry.SHA256 == expectedSHA {
			return true
		}
	}
	return false
}

func safeIdentifier(value string) string {
	var builder strings.Builder
	for _, char := range value {
		c := byte(char)
		switch {
		case c >= 'a' && c <= 'z':
			builder.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			builder.WriteByte(c)
		case c >= '0' && c <= '9':
			builder.WriteByte(c)
		case c == '-' || c == '_' || c == '.':
			builder.WriteByte(c)
		default:
			builder.WriteByte('-')
		}
	}
	identifier := builder.String()
	if identifier == "" || len(strings.Trim(identifier, "-")) == 0 {
		identifier = "node"
	}
	if c := identifier[0]; !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') {
		identifier = "n" + identifier
	}
	if len(identifier) > 128 {
		identifier = identifier[:128]
	}
	return identifier
}

func suffixedIdentifier(base string, suffix string) string {
	identifier := safeIdentifier(base)
	if len(identifier)+len(suffix) > 128 {
		identifier = identifier[:128-len(suffix)]
	}
	return identifier + suffix
}

func scenarioRequest(t *testing.T, scenario composableacceptance.Scenario, scenarioSHA string) []byte {
	t.Helper()
	return plainRequest(t, "scenario_identity = "+pythonStringLiteral(t, scenarioSHA)+"\nresult = "+pythonStringLiteral(t, scenario.ExpectedArtifact))
}

func scenarioFunctionInvocation(scenario composableacceptance.Scenario, scenarioSHA, artifactSHA string) (agentfunction.Invocation, error) {
	inputs, err := json.Marshal(map[string]string{
		"expected_artifact_sha256": hashBytes([]byte(scenario.ExpectedArtifact)),
		"scenario_id":              scenario.ID,
		"scenario_sha256":          scenarioSHA,
	})
	if err != nil {
		return agentfunction.Invocation{}, err
	}
	return agentfunction.Invocation{
		SchemaVersion:               agentfunction.InvocationSchemaVersion,
		Admission:                   agentfunction.Cacheable,
		ProjectSHA256:               scenarioSHA,
		FunctionSourceSHA256:        hashBytes([]byte("function-source:" + scenario.ID)),
		ArtifactSHA256:              artifactSHA,
		ExecutionProfileSHA256:      hashBytes([]byte("execution-profile:" + scenario.ID)),
		ImportClosureSHA256:         hashBytes([]byte("import-closure:" + scenario.ID)),
		CanonicalInputs:             inputs,
		ImmutableRootSHA256:         []string{scenarioSHA},
		DeterministicSettingsSHA256: hashBytes([]byte("deterministic-settings:" + scenario.ID)),
		OutputSchemaSHA256:          hashBytes([]byte("output-schema:" + scenario.ID)),
		PrivacyPartition:            "spark-" + scenario.ID,
		PolicyEpochSHA256:           hashBytes([]byte("policy-epoch:" + scenario.ID)),
	}, nil
}

func pythonStringLiteral(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func responseStringResult(t *testing.T, response []byte) string {
	t.Helper()
	var envelope struct {
		Status string `json:"status"`
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
		t.Fatalf("invalid response err=%v envelope=%+v", err, envelope)
	}
	return envelope.Result
}

type traceRecorder struct {
	started time.Time
	events  []composableacceptance.TraceEvent
}

func newTraceRecorder(started time.Time) *traceRecorder {
	return &traceRecorder{started: started}
}

func uint32Pointer(value uint32) *uint32 {
	return &value
}

func (recorder *traceRecorder) append(
	category composableacceptance.TraceEventType,
	action string,
	outcome composableacceptance.TraceEventOutcome,
	parent *uint32,
	input, output []byte,
	checkpointSHA256 string,
	checkpointStatus string,
	count uint64,
) {
	sequence := uint32(len(recorder.events) + 1)
	elapsed := float64(time.Since(recorder.started).Milliseconds())
	if sequence == 1 {
		elapsed = 0
	}
	event := composableacceptance.TraceEvent{
		Sequence:              sequence,
		Type:                  category,
		Action:                action,
		Outcome:               outcome,
		CheckpointSHA256:      checkpointSHA256,
		CheckpointStatus:      checkpointStatus,
		Count:                 count,
		RelativeElapsedMillis: elapsed,
	}
	if len(input) > 0 {
		event.InputSHA256 = hashBytes(input)
	}
	if len(output) > 0 {
		event.OutputSHA256 = hashBytes(output)
	}
	if parent != nil {
		event.ParentSequence = parent
	} else if sequence > 1 {
		event.ParentSequence = uint32Pointer(1)
	}
	recorder.events = append(recorder.events, event)
}

func completeTraceRow(row *composableacceptance.Row, started time.Time, recorder *traceRecorder) {
	if len(recorder.events) == 0 {
		recorder.append(composableacceptance.TraceEventTypeRunStart, "run.start", composableacceptance.TraceEventOutcomeStarted, nil, []byte(row.ScenarioSHA256), nil, "", "", 1)
	}
	terminalDisposition := row.TerminalDisposition
	if terminalDisposition == "" {
		terminalDisposition = "closed"
		row.TerminalDisposition = terminalDisposition
	}
	terminalOutcome := composableacceptance.TraceEventOutcomeOK
	switch row.Status {
	case "rejected":
		terminalOutcome = composableacceptance.TraceEventOutcomeRejected
	case "skipped":
		terminalOutcome = composableacceptance.TraceEventOutcomeSkipped
	}
	recorder.append(composableacceptance.TraceEventTypeRunTerminal, "run.terminal", terminalOutcome, nil, nil, nil, "", "", 1)
	terminal := &recorder.events[len(recorder.events)-1]
	terminal.TerminalDisposition = terminalDisposition
	if terminal.ParentSequence == nil {
		terminal.ParentSequence = uint32Pointer(1)
	}
	if terminal.RelativeElapsedMillis > float64(time.Since(started).Milliseconds()) {
		terminal.RelativeElapsedMillis = float64(time.Since(started).Milliseconds())
	}
	row.Trace = recorder.events
	row.RelativeElapsedMillis = terminal.RelativeElapsedMillis
	row.TerminalDisposition = terminalDisposition
	row.EvidenceComplete = true
}

func scenarioRow(scenario composableacceptance.Scenario, scenarioSHA, oracleSHA string, treatment composableacceptance.Treatment, started time.Time, guestCount uint64) (composableacceptance.Row, *traceRecorder) {
	row := composableacceptance.Row{
		ScenarioID: scenario.ID, ScenarioSHA256: scenarioSHA, Treatment: treatment,
		Status: "passed", OracleSHA256: oracleSHA, GuestCreated: guestCount, GuestDestroyed: guestCount,
		EvidenceScope: "direct_replay", ConformanceSHA256: composableacceptance.ArtifactIdentity("TestRealGuestSparkScenarioCoreTreatments@" + os.Getenv("PYSOLATE_HOST_SOURCE_COMMIT")),
		TerminalDisposition: "closed", EvidenceComplete: true,
	}
	recorder := newTraceRecorder(started)
	recorder.append(composableacceptance.TraceEventTypeRunStart, "run.start", composableacceptance.TraceEventOutcomeStarted, nil, []byte(scenarioSHA), nil, "", "", 1)
	return row, recorder
}
