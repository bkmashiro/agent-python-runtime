// Measure fixed single-Run workloads at durable lifecycle boundaries.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

const benchmarkSeed = "phase-bench-seed"

type caseSpec struct {
	name   string
	code   string
	inputs json.RawMessage
	park   bool
	want   float64
}

type metadata struct {
	Type           string `json:"type"`
	Artifact       string `json:"artifact"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactBytes  int    `json:"artifact_bytes"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GoVersion      string `json:"go_version"`
	SourceRevision string `json:"source_revision,omitempty"`
	SourceModified *bool  `json:"source_modified,omitempty"`
	Preparation    string `json:"preparation"`
	Cases          string `json:"cases"`
	Iterations     int    `json:"iterations"`
	ToolDelayNS    int64  `json:"synthetic_tool_delay_ns"`
	ParkDelayNS    int64  `json:"synthetic_park_delay_ns"`
	RunnerSetupNS  int64  `json:"runner_setup_ns"`
	ExternalIO     bool   `json:"external_io"`
}

type sample struct {
	Type              string  `json:"type"`
	Case              string  `json:"case"`
	Iteration         int     `json:"iteration"`
	CreateNS          int64   `json:"create_ns"`
	FirstAttemptNS    int64   `json:"first_attempt_ns"`
	ParkIntervalNS    int64   `json:"park_interval_ns,omitempty"`
	DecideNS          int64   `json:"decide_ns,omitempty"`
	ReadmitAttemptNS  int64   `json:"readmit_attempt_ns,omitempty"`
	ToolServiceNS     int64   `json:"tool_service_ns"`
	ToolQueueNS       int64   `json:"tool_queue_ns"`
	ToolResumeNS      int64   `json:"tool_resume_ns"`
	ToolFailures      int64   `json:"tool_failures"`
	RuntimeOverheadNS int64   `json:"attempt_minus_tool_ns"`
	ToolDispatches    int64   `json:"tool_dispatches"`
	Result            float64 `json:"result"`
	TotalNS           int64   `json:"total_ns"`
}

type toolMetrics struct {
	calls     atomic.Int64
	queueNS   atomic.Int64
	serviceNS atomic.Int64
	resumeNS  atomic.Int64
	failures  atomic.Int64
}

func (m *toolMetrics) call(ctx context.Context, delay time.Duration, result any) (any, error) {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return result, nil
}

func (m *toolMetrics) ObserveTool(observation durable.ToolObservation) {
	if observation.Operation != durable.ToolCall {
		return
	}
	m.calls.Add(1)
	m.queueNS.Add(observation.QueueDuration.Nanoseconds())
	m.serviceNS.Add(observation.ServiceDuration.Nanoseconds())
	m.resumeNS.Add(observation.ResumeDuration.Nanoseconds())
	if observation.Outcome != durable.ToolSucceeded {
		m.failures.Add(1)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	caseName := flag.String("case", "all", "comma-separated read-finish, tool-chain, park-readmit, numpy-local, read-numpy, or all")
	iterations := flag.Int("iterations", 5, "samples per case")
	preparationName := flag.String("preparation", "copy", "fresh, copy, or cow")
	toolDelay := flag.Duration("tool-delay", 50*time.Millisecond, "synthetic Host tool service delay")
	parkDelay := flag.Duration("park-delay", 200*time.Millisecond, "synthetic time outside a Guest before a decision")
	externalIO := flag.Bool("external-io", false, "classify synthetic Host reads as ExternalIO")
	flag.Parse()
	if *iterations < 1 || *iterations > 1000 || *toolDelay < 0 || *parkDelay < 0 {
		return errors.New("invalid benchmark bounds")
	}
	selected, err := selectCases(*caseName)
	if err != nil {
		return err
	}
	preparation, err := parsePreparation(*preparationName)
	if err != nil {
		return err
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(wasm)
	root, err := os.MkdirTemp("", "pysolate-phase-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	store, err := durable.Open(filepath.Join(root, "runs.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	var metrics toolMetrics
	scheduling := durable.Inline
	if *externalIO {
		scheduling = durable.ExternalIO
	}
	tools := []durable.Tool{
		{Name: "remote_read", Version: "bench-v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Observer: &metrics, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			return metrics.call(ctx, *toolDelay, map[string]any{"id": "item-1", "value": 41})
		}},
		{Name: "remote_detail", Version: "bench-v1", Recovery: durable.RetrySafe, Scheduling: scheduling, Observer: &metrics, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			return metrics.call(ctx, *toolDelay, map[string]any{"value": 42})
		}},
		{Name: "approval", Version: "bench-v1", Recovery: durable.WaitMode, Wait: func(context.Context, json.RawMessage) (durable.WaitSpec, error) {
			return durable.WaitSpec{Kind: "approval", Request: json.RawMessage(`{"change":"item-1"}`)}, nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	setupStarted := time.Now()
	var runner *durable.Runner
	if preparation == nil {
		runner, err = durable.NewRunner(ctx, store, wasm, "phase-bench-v1", tools)
	} else {
		runner, err = durable.NewRunner(ctx, store, wasm, "phase-bench-v1", tools, *preparation)
	}
	setupNS := time.Since(setupStarted).Nanoseconds()
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	executor, err := durable.NewExecutor(runner, durable.Limits{MaxActive: 1, MaxQueued: 0})
	if err != nil {
		return err
	}
	defer executor.Close(context.Background())

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	revision, modified := buildRevision()
	if err := encoder.Encode(metadata{
		Type: "metadata", Artifact: *guest, ArtifactSHA256: hex.EncodeToString(digest[:]), ArtifactBytes: len(wasm),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), SourceRevision: revision, SourceModified: modified,
		Preparation: *preparationName, Cases: *caseName, Iterations: *iterations, ToolDelayNS: toolDelay.Nanoseconds(), ParkDelayNS: parkDelay.Nanoseconds(), RunnerSetupNS: setupNS, ExternalIO: *externalIO,
	}); err != nil {
		return err
	}
	for _, spec := range selected {
		for iteration := 0; iteration < *iterations; iteration++ {
			row, err := runSample(ctx, runner, executor, &metrics, spec, iteration, *parkDelay)
			if err != nil {
				return fmt.Errorf("%s iteration %d: %w", spec.name, iteration, err)
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
	}
	return nil
}

func runSample(ctx context.Context, runner *durable.Runner, executor *durable.Executor, metrics *toolMetrics, spec caseSpec, iteration int, parkDelay time.Duration) (sample, error) {
	row := sample{Type: "sample", Case: spec.name, Iteration: iteration}
	totalStarted := time.Now()
	callsBefore := metrics.calls.Load()
	queueBefore := metrics.queueNS.Load()
	serviceBefore := metrics.serviceNS.Load()
	resumeBefore := metrics.resumeNS.Load()
	failuresBefore := metrics.failures.Load()
	runID := fmt.Sprintf("%s-%d", spec.name, iteration)
	createStarted := time.Now()
	_, err := runner.Create(ctx, durable.Definition{
		ID: runID, Code: spec.code, Inputs: spec.inputs, Seed: benchmarkSeed,
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "phase-bench-v1",
	})
	row.CreateNS = time.Since(createStarted).Nanoseconds()
	if err != nil {
		return row, err
	}
	attemptStarted := time.Now()
	attempt, err := executor.Admit(ctx, runID)
	if err != nil {
		return row, err
	}
	result, err := attempt.Result(ctx)
	row.FirstAttemptNS = time.Since(attemptStarted).Nanoseconds()
	if err != nil {
		return row, err
	}
	if spec.park {
		if result.State != durable.AttemptParked || result.Park == nil || result.Park.Kind != durable.ParkWait {
			return row, fmt.Errorf("expected wait park, got state=%q park=%+v", result.State, result.Park)
		}
		parkStarted := time.Now()
		if err := sleepContext(ctx, parkDelay); err != nil {
			return row, err
		}
		decideStarted := time.Now()
		if err := runner.Decide(ctx, result.Park.WaitID, durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
			return row, err
		}
		row.DecideNS = time.Since(decideStarted).Nanoseconds()
		row.ParkIntervalNS = time.Since(parkStarted).Nanoseconds()
		readmitStarted := time.Now()
		attempt, err = executor.Admit(ctx, runID)
		if err != nil {
			return row, err
		}
		result, err = attempt.Result(ctx)
		row.ReadmitAttemptNS = time.Since(readmitStarted).Nanoseconds()
		if err != nil {
			return row, err
		}
	}
	if result.State != durable.AttemptCompleted {
		return row, fmt.Errorf("expected completion, got %q", result.State)
	}
	if err := json.Unmarshal(result.Output.Value, &row.Result); err != nil {
		return row, fmt.Errorf("decode result %q: %w", result.Output.Value, err)
	}
	if row.Result != spec.want {
		return row, fmt.Errorf("result=%v want=%v", row.Result, spec.want)
	}
	row.ToolDispatches = metrics.calls.Load() - callsBefore
	row.ToolQueueNS = metrics.queueNS.Load() - queueBefore
	row.ToolServiceNS = metrics.serviceNS.Load() - serviceBefore
	row.ToolResumeNS = metrics.resumeNS.Load() - resumeBefore
	row.ToolFailures = metrics.failures.Load() - failuresBefore
	row.RuntimeOverheadNS = row.FirstAttemptNS + row.ReadmitAttemptNS - row.ToolServiceNS
	row.TotalNS = time.Since(totalStarted).Nanoseconds()
	return row, nil
}

func benchmarkCases() []caseSpec {
	return []caseSpec{
		{name: "read-finish", code: `record = remote_read(key=inputs["key"])
result = record["value"] + 1`, inputs: json.RawMessage(`{"key":"item-1"}`), want: 42},
		{name: "tool-chain", code: `first = remote_read(key=inputs["key"])
second = remote_detail(id=first["id"])
result = second["value"]`, inputs: json.RawMessage(`{"key":"item-1"}`), want: 42},
		{name: "park-readmit", code: `record = remote_read(key=inputs["key"])
approved = approval(change=record)
result = record["value"] if approved else None`, inputs: json.RawMessage(`{"key":"item-1"}`), park: true, want: 41},
		{name: "numpy-local", code: `import numpy as np
values = np.arange(inputs["size"], dtype=np.float64)
result = float((values * values).sum())`, inputs: json.RawMessage(`{"size":10000}`), want: 333283335000},
		{name: "read-numpy", code: `import numpy as np
record = remote_read(key=inputs["key"])
values = np.arange(inputs["size"], dtype=np.float64)
result = float(values.sum()) + record["value"]`, inputs: json.RawMessage(`{"key":"item-1","size":10000}`), want: 49995041},
	}
}

func selectCases(name string) ([]caseSpec, error) {
	cases := benchmarkCases()
	if name == "all" {
		return cases, nil
	}
	byName := make(map[string]caseSpec, len(cases))
	for _, spec := range cases {
		byName[spec.name] = spec
	}
	var selected []caseSpec
	seen := map[string]bool{}
	for _, part := range strings.Split(name, ",") {
		part = strings.TrimSpace(part)
		spec, ok := byName[part]
		if !ok || part == "" {
			return nil, fmt.Errorf("unknown case %q", part)
		}
		if seen[part] {
			return nil, fmt.Errorf("duplicate case %q", part)
		}
		seen[part] = true
		selected = append(selected, spec)
	}
	return selected, nil
}

func parsePreparation(name string) (*durable.Preparation, error) {
	switch name {
	case "fresh":
		return nil, nil
	case "copy":
		return &durable.Preparation{Seed: benchmarkSeed}, nil
	case "cow":
		return &durable.Preparation{Seed: benchmarkSeed, COW: true}, nil
	default:
		return nil, fmt.Errorf("unknown preparation %q", name)
	}
}

func buildRevision() (string, *bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", nil
	}
	var revision string
	var modified *bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			value := setting.Value == "true"
			modified = &value
		}
	}
	return revision, modified
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
