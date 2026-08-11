package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/placement"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

var errScriptedCanary = errors.New("placement scripted canary failed")
var safeID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

const (
	guestArtifactSHA = "sha256:4078dbcec0307e5636c86b84523b8349a557db115bfac7569ff5d003b08ceadb"
	guestManifestSHA = "sha256:b6baa6f5adb27263ef586faed897cde42c1815b4ce7c415333696800b3bbb6a6"
	computerHarness  = "sha256:c4732ffde8ed5ff329360f7248f4686807733f4c19cbf7002c98b2b5f793f61a"
)

type cellArtifact struct {
	SchemaVersion string                `json:"schema_version"`
	Trial         placement.TrialResult `json:"trial"`
	Score         placement.ScoreResult `json:"score"`
}

type summaryCell struct {
	TaskID string `json:"task_id"`
	Arm    string `json:"arm"`
	Pass   bool   `json:"pass"`
	Path   string `json:"path"`
}

type canarySummary struct {
	SchemaVersion string        `json:"schema_version"`
	SourceCommit  string        `json:"source_commit"`
	PlanSHA256    string        `json:"plan_sha256"`
	CellCount     int           `json:"cell_count"`
	PassCount     int           `json:"pass_count"`
	Cells         []summaryCell `json:"cells"`
}

type computerResult struct {
	Identity struct {
		HarnessSHA256 string `json:"harness_sha256"`
	} `json:"identity"`
	Execution struct {
		Status   string          `json:"status"`
		ExitCode int             `json:"exitCode"`
		Value    json.RawMessage `json:"value"`
	} `json:"execution"`
	OutputFiles map[string]string `json:"output_files"`
	ToolTrace   struct {
		Calls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Matched   bool            `json:"matched"`
			Sequence  int             `json:"sequence"`
		} `json:"calls"`
		Complete      bool `json:"complete"`
		Cursor        int  `json:"cursor"`
		ExpectedCount int  `json:"expected_count"`
	} `json:"tool_trace"`
	Lifecycle struct {
		WallNS int64 `json:"wall_ns"`
	} `json:"lifecycle"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	corpusRoot := flag.String("corpus", "eval/agentic/placement/v1", "placement corpus root")
	planPath := flag.String("plan", "eval/agentic/results/placement-development-canary-prereg-2026-08-11/plan.json", "pre-registered canary plan")
	guestDir := flag.String("guest-dir", "", "verified Guest artifact directory")
	computerCheckout := flag.String("computer-checkout", "", "pinned Cloudflare Computer checkout")
	computerAdapter := flag.String("computer-adapter", "tools/cloudflare_computer_adapter.py", "Computer adapter")
	pythonPath := flag.String("python", "python3", "Python executable")
	outputDir := flag.String("out", "", "new output directory")
	sourceCommit := flag.String("source-commit", "", "40-character runner source commit")
	flag.Parse()
	if *guestDir == "" || *computerCheckout == "" || *outputDir == "" || len(*sourceCommit) != 40 {
		return errScriptedCanary
	}
	plan, err := placement.LoadDevelopmentCanaryPlan(*corpusRoot, *planPath)
	if err != nil {
		return err
	}
	corpus, err := placement.Load(*corpusRoot)
	if err != nil {
		return err
	}
	bundle, err := placement.LoadGuestBundle(*guestDir, placement.AgentStdlibWorkspaceImports(), placement.GuestIdentityExpectation{
		ArtifactSHA256: guestArtifactSHA, ManifestSHA256: guestManifestSHA,
	})
	if err != nil {
		return err
	}
	if err := os.Mkdir(*outputDir, 0o700); err != nil {
		return err
	}
	planBytes, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	summary := canarySummary{SchemaVersion: "placement-scripted-canary-summary/v1", SourceCommit: *sourceCommit, PlanSHA256: digest(planBytes)}
	tasks := make(map[string]placement.Task, len(corpus.Tasks))
	for _, task := range corpus.Tasks {
		tasks[task.ID] = task
	}
	for _, selected := range plan.Tasks {
		task, ok := tasks[selected.ID]
		if !ok {
			return errScriptedCanary
		}
		compiled, err := placement.CompileScriptedCanaryCase(task)
		if err != nil {
			return fmt.Errorf("%w: compile %s", err, task.ID)
		}
		for _, arm := range plan.Arms {
			trial, err := executeCell(context.Background(), task, selected.SHA256, arm, *sourceCommit, compiled, bundle, *computerCheckout, *computerAdapter, *pythonPath)
			if err != nil {
				return fmt.Errorf("%w: %s/%s: %v", errScriptedCanary, task.ID, arm, err)
			}
			if err := placement.ValidateTrialResult(trial); err != nil {
				return err
			}
			score, err := placement.Score(task, trial)
			if err != nil {
				return err
			}
			name := trialFilename(task.ID, arm)
			if name == "" {
				return errScriptedCanary
			}
			if err := writeNewJSON(filepath.Join(*outputDir, name), cellArtifact{SchemaVersion: "placement-scripted-cell/v1", Trial: trial, Score: score}); err != nil {
				return err
			}
			summary.CellCount++
			if score.Pass {
				summary.PassCount++
			}
			summary.Cells = append(summary.Cells, summaryCell{TaskID: task.ID, Arm: arm, Pass: score.Pass, Path: name})
		}
	}
	if summary.CellCount != int(plan.Budgets.PlannedCells) || summary.PassCount != summary.CellCount {
		return errScriptedCanary
	}
	return writeNewJSON(filepath.Join(*outputDir, "summary.json"), summary)
}

func executeCell(ctx context.Context, task placement.Task, taskDigest, arm, sourceCommit string, compiled placement.ScriptedCanaryCase, bundle placement.GuestBundle, checkout, adapter, python string) (placement.TrialResult, error) {
	runtimeIdentity := map[string]string{"direct": digest([]byte("placement-direct-scripted-v1")), "pysolate": guestArtifactSHA, "computer": computerHarness}[arm]
	treatment := map[string]string{"direct": digest([]byte("placement-direct-scripted-v1")), "pysolate": digest([]byte(compiled.PythonSource)), "computer": digest([]byte(compiled.JavaScriptSource))}[arm]
	if task.Admission[arm].Status == "rejected" {
		return rejectedTrial(task, arm, taskDigest, sourceCommit, runtimeIdentity), nil
	}
	trial := placement.TrialResult{
		SchemaVersion: "placement-trial-result/v1", TrialID: task.ID + "--" + arm + "--scripted--r1",
		TaskID: task.ID, TaskSHA256: taskDigest, SourceCommit: sourceCommit, TreatmentSHA256: treatment,
		RuntimeIdentitySHA256: runtimeIdentity, Arm: arm, Mode: "scripted", Replicate: 1,
		Admission: placement.ObservedAdmission{Status: "admitted", Reason: task.Admission[arm].Reason, BeforeProvider: true},
		Execution: placement.ExecutionEvidence{Status: "completed"}, ObservedFinalState: append(json.RawMessage(nil), compiled.ObservedFinalState...),
	}
	var effects []placement.SemanticCall
	var lifecycle placement.LifecycleEvidence
	var evidence map[string]string
	var err error
	switch arm {
	case "direct":
		effects, evidence, err = runDirect(ctx, task, compiled)
	case "pysolate":
		effects, lifecycle, evidence, err = runPysolate(ctx, task, compiled, bundle)
	case "computer":
		effects, lifecycle, evidence, trial.ObservedFinalState, err = runComputer(ctx, task, compiled, checkout, adapter, python)
	default:
		return placement.TrialResult{}, errScriptedCanary
	}
	if err != nil {
		return placement.TrialResult{}, err
	}
	trial.ObservedEffects, trial.Lifecycle, trial.Evidence = effects, lifecycle, evidence
	return trial, nil
}

func rejectedTrial(task placement.Task, arm, taskDigest, sourceCommit, runtimeIdentity string) placement.TrialResult {
	return placement.TrialResult{
		SchemaVersion: "placement-trial-result/v1", TrialID: task.ID + "--" + arm + "--scripted--r1",
		TaskID: task.ID, TaskSHA256: taskDigest, SourceCommit: sourceCommit,
		TreatmentSHA256: digest([]byte("placement-preregistered-rejection-v1")), RuntimeIdentitySHA256: runtimeIdentity,
		Arm: arm, Mode: "scripted", Replicate: 1,
		Admission: placement.ObservedAdmission{Status: "rejected", Reason: task.Admission[arm].Reason, BeforeProvider: true},
		Execution: placement.ExecutionEvidence{Status: "not_started"}, Evidence: map[string]string{"admission": digest([]byte("frozen_pre_provider"))},
	}
}

func runDirect(ctx context.Context, task placement.Task, compiled placement.ScriptedCanaryCase) ([]placement.SemanticCall, map[string]string, error) {
	runtime, err := agentic.NewScriptedToolRuntime(compiled.Tools, compiled.Calls)
	if err != nil {
		return nil, nil, err
	}
	var last json.RawMessage
	for index, call := range compiled.Calls {
		last, err = runtime.InvokeDirect(ctx, task.ID+"-direct-"+fmt.Sprint(index), fmt.Sprintf("call-%d", index), call.Name, call.Arguments)
		if err != nil {
			return nil, nil, err
		}
	}
	if !runtime.Complete() || (len(compiled.ExpectedResult) > 0 && !canonicalEqual(last, compiled.ExpectedResult)) {
		return nil, nil, errScriptedCanary
	}
	return semanticTrace(runtime.Trace()), map[string]string{"trace": digest(mustJSON(runtime.Trace()))}, nil
}

func runPysolate(ctx context.Context, task placement.Task, compiled placement.ScriptedCanaryCase, bundle placement.GuestBundle) ([]placement.SemanticCall, placement.LifecycleEvidence, map[string]string, error) {
	tools, err := agentic.NewScriptedToolRuntime(compiled.Tools, compiled.Calls)
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, err
	}
	profile := bundle.Profile
	executor, err := agentic.NewWASIPythonExecutor(ctx, bundle.WASM, runtimeconfig.RunConfig{
		Timeout: time.Duration(task.Limits.TimeoutMillis) * time.Millisecond, MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20,
		MemoryLimitPages: 8192, ExecutionProfile: &profile,
	}, tools)
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, err
	}
	defer executor.Close(context.Background())
	imports, err := runtimeconfig.InferStaticImportRoots(compiled.PythonSource)
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, err
	}
	started := time.Now()
	result, err := executor.ExecuteProfileQualified(ctx, task.ID+"-pysolate-scripted", compiled.PythonSource, "base", imports, task.Limits.MaxToolCalls)
	wall := time.Since(started)
	if err != nil || !result.Success || !tools.Complete() {
		return nil, placement.LifecycleEvidence{}, nil, errScriptedCanary
	}
	if len(compiled.ExpectedResult) > 0 && result.ResultDigest != digest(compiled.ExpectedResult) {
		return nil, placement.LifecycleEvidence{}, nil, errScriptedCanary
	}
	return semanticTrace(tools.Trace()), placement.LifecycleEvidence{StartCount: 1, WallTimeMillis: uint64(wall.Milliseconds())}, map[string]string{
		"request": result.RequestDigest, "response": result.ResponseDigest, "result": result.ResultDigest,
	}, nil
}

func runComputer(ctx context.Context, task placement.Task, compiled placement.ScriptedCanaryCase, checkout, adapter, python string) ([]placement.SemanticCall, placement.LifecycleEvidence, map[string]string, json.RawMessage, error) {
	temporary, err := os.MkdirTemp("", "placement-computer-scripted-")
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, nil, err
	}
	defer os.RemoveAll(temporary)
	calls := make([]map[string]any, 0, len(compiled.Calls))
	for _, call := range compiled.Calls {
		var arguments, result any
		if json.Unmarshal(call.Arguments, &arguments) != nil || json.Unmarshal(call.Result, &result) != nil {
			return nil, placement.LifecycleEvidence{}, nil, nil, errScriptedCanary
		}
		calls = append(calls, map[string]any{"name": call.Name, "arguments": arguments, "result": result})
	}
	request := map[string]any{
		"schema_version": "cloudflare-computer-local-trial/v1", "workspace_id": workspaceID(task.ID),
		"files": compiled.InitialFiles, "source": compiled.JavaScriptSource, "input": map[string]any{}, "output_files": compiled.OutputFiles,
		"tool_fixture": map[string]any{"schema_version": "placement-computer-tool-fixture/v1", "calls": calls},
	}
	requestPath, resultPath := filepath.Join(temporary, "request.json"), filepath.Join(temporary, "result.json")
	if err := os.WriteFile(requestPath, mustJSON(request), 0o600); err != nil {
		return nil, placement.LifecycleEvidence{}, nil, nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, nil, err
	}
	command := exec.CommandContext(ctx, python, adapter, "run", "--checkout", checkout, "--request", requestPath, "--result", resultPath, "--port", fmt.Sprint(port))
	if output, err := command.CombinedOutput(); err != nil {
		return nil, placement.LifecycleEvidence{}, nil, nil, fmt.Errorf("Computer adapter: %w output_sha256=%s", err, digest(output))
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, placement.LifecycleEvidence{}, nil, nil, err
	}
	var result computerResult
	if decodeJSON(data, &result) != nil || result.Identity.HarnessSHA256 != computerHarness || result.Execution.Status != "completed" ||
		result.Execution.ExitCode != 0 || !result.ToolTrace.Complete || result.ToolTrace.ExpectedCount != len(compiled.Calls) || result.ToolTrace.Cursor != len(compiled.Calls) {
		return nil, placement.LifecycleEvidence{}, nil, nil, errScriptedCanary
	}
	effects := make([]placement.SemanticCall, len(result.ToolTrace.Calls))
	for index, call := range result.ToolTrace.Calls {
		if !call.Matched || call.Sequence != index {
			return nil, placement.LifecycleEvidence{}, nil, nil, errScriptedCanary
		}
		effects[index] = placement.SemanticCall{Name: call.Name, Arguments: call.Arguments}
	}
	observed := append(json.RawMessage(nil), compiled.ObservedFinalState...)
	if len(compiled.OutputFiles) > 0 {
		files := cloneFiles(compiled.InitialFiles)
		for _, path := range compiled.OutputFiles {
			content, ok := result.OutputFiles[path]
			if !ok {
				return nil, placement.LifecycleEvidence{}, nil, nil, errScriptedCanary
			}
			files[path] = content
		}
		observed = mustJSON(map[string]any{"kind": "exact_files", "files": files})
	}
	if len(compiled.ExpectedResult) > 0 && !canonicalEqual(result.Execution.Value, compiled.ExpectedResult) {
		return nil, placement.LifecycleEvidence{}, nil, nil, errScriptedCanary
	}
	return effects, placement.LifecycleEvidence{StartCount: 1, WallTimeMillis: uint64(result.Lifecycle.WallNS / int64(time.Millisecond))}, map[string]string{
		"adapter_result": digest(data), "tool_trace": digest(mustJSON(result.ToolTrace)),
	}, observed, nil
}

func semanticTrace(trace []agentic.ScriptedObservedCall) []placement.SemanticCall {
	result := make([]placement.SemanticCall, len(trace))
	for index, call := range trace {
		result[index] = placement.SemanticCall{Name: call.Name, Arguments: call.Arguments}
	}
	return result
}

func trialFilename(taskID, arm string) string {
	if !safeID.MatchString(taskID) || (arm != "direct" && arm != "pysolate" && arm != "computer") {
		return ""
	}
	return taskID + "--" + arm + "--scripted--r1.json"
}

func workspaceID(taskID string) string {
	value := strings.ToLower(strings.ReplaceAll(taskID, "_", "-"))
	if len(value) > 55 {
		value = value[:55]
	}
	return "psc-" + strings.Trim(value, "-")
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func cloneFiles(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for path, content := range input {
		result[path] = content
	}
	return result
}

func canonicalEqual(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return bytes.Equal(mustJSON(a), mustJSON(b))
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) == nil {
		return errScriptedCanary
	}
	return nil
}

func decodeJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) == nil {
		return errScriptedCanary
	}
	return nil
}

func writeNewJSON(path string, value any) error {
	if filepath.Base(path) == "." {
		return errScriptedCanary
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
