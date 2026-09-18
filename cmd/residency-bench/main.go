package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"golang.org/x/sys/unix"
)

const (
	rowSchemaVersion      = "pysolate.residency-bench.row.v1"
	metadataSchemaVersion = "pysolate.residency-bench.metadata.v1"
	maxPressureMiB        = 256
	maxWorkloadMiB        = 1024
	pageSize              = 4096
)

type arm string

const (
	armNatural  arm = "natural"
	armFixed    arm = "fixed"
	armPressure arm = "pressure"
)

var arms = []arm{armNatural, armFixed, armPressure}

type options struct {
	guest             string
	output            string
	blocks            int
	seed              int64
	workloadMiB       int
	waitMs            int
	pressureMiB       int
	cold              time.Duration
	pageout           time.Duration
	pressureThreshold float64
	smoke             bool
	worker            bool
	workerArm         arm
	workerBlock       int
	workerSeed        int64
}

type artifactBundle struct {
	wasm    []byte
	profile runtimeconfig.ExecutionProfile
}

type metadataRecord struct {
	RecordType        string   `json:"record_type"`
	SchemaVersion     string   `json:"schema_version"`
	Command           string   `json:"command"`
	Guest             string   `json:"guest"`
	Platform          string   `json:"platform"`
	GoVersion         string   `json:"go_version"`
	RuntimeCommit     string   `json:"runtime_commit,omitempty"`
	ArtifactSHA256    string   `json:"artifact_sha256"`
	ManifestSHA256    string   `json:"manifest_sha256"`
	ProfileID         string   `json:"profile_id"`
	Blocks            int      `json:"blocks"`
	Seed              int64    `json:"seed"`
	WorkloadMiB       int      `json:"workload_mib"`
	WaitMs            int      `json:"wait_ms"`
	PressureMiB       int      `json:"pressure_mib"`
	ColdAfterNS       int64    `json:"cold_after_ns"`
	PageOutAfterNS    int64    `json:"pageout_after_ns"`
	PressureThreshold float64  `json:"pressure_threshold"`
	ArmOrder          []string `json:"arm_order"`
}

type trialSpec struct {
	ID        string
	Block     int
	BlockSeed int64
	Arm       arm
}

type trialRow struct {
	RecordType     string           `json:"record_type"`
	SchemaVersion  string           `json:"schema_version"`
	TrialID        string           `json:"trial_id"`
	Block          int              `json:"block"`
	BlockSeed      int64            `json:"block_seed"`
	Arm            string           `json:"arm"`
	Outcome        string           `json:"outcome"`
	Error          *string          `json:"error"`
	SetupNS        *uint64          `json:"setup_ns"`
	RunNS          *uint64          `json:"run_ns"`
	WakeToResultNS *uint64          `json:"wake_to_result_ns"`
	CleanupNS      *uint64          `json:"cleanup_ns"`
	EndToEndNS     *uint64          `json:"end_to_end_ns"`
	WaitSamples    []resourceSample `json:"wait_samples"`
	AfterRunFaults *faultCounts     `json:"after_run_faults"`
	Correctness    *correctness     `json:"correctness"`
	ColdIO         *coldEvidence    `json:"cold_io"`
	Pressure       *pressureResult  `json:"pressure"`
	WorkerExitCode *int             `json:"worker_exit_code"`
}

type correctness struct {
	WaitOK          *bool  `json:"wait_ok"`
	Exact           *bool  `json:"exact"`
	ExpectedSum     *int64 `json:"expected_sum"`
	ActualSum       *int64 `json:"actual_sum"`
	ExpectedCompute *int64 `json:"expected_compute"`
	ActualCompute   *int64 `json:"actual_compute"`
	Offset          *int64 `json:"offset"`
}

type coldEvidence = wazeroengine.ColdIOEvidence

type pressureResult struct {
	RequestedMiB uint64  `json:"requested_mib"`
	MappedBytes  *uint64 `json:"mapped_bytes"`
	Error        *string `json:"error"`
}

type resourceSample struct {
	AtMonoNS        uint64         `json:"at_mono_ns"`
	PSSBytes        *uint64        `json:"pss_bytes"`
	RSSBytes        *uint64        `json:"rss_bytes"`
	SwapPSSBytes    *uint64        `json:"swap_pss_bytes"`
	MinorFaults     *uint64        `json:"minor_faults"`
	MajorFaults     *uint64        `json:"major_faults"`
	CgroupAncestors []cgroupSample `json:"cgroup_ancestors"`
}

type faultCounts struct {
	MinorFaults *uint64 `json:"minor_faults"`
	MajorFaults *uint64 `json:"major_faults"`
}

type cgroupSample struct {
	Path         string  `json:"path"`
	CurrentBytes *uint64 `json:"memory_current_bytes"`
	HighBytes    *uint64 `json:"memory_high_bytes"`
	MaxBytes     *uint64 `json:"memory_max_bytes"`
	SwapCurrent  *uint64 `json:"swap_current_bytes"`
	SwapMax      *uint64 `json:"swap_max_bytes"`
}

// A cancelled Run can return before its Host handler has stopped sampling.
type waitObservations struct {
	mu      sync.Mutex
	samples []resourceSample
}

func (o *waitObservations) add(sample resourceSample) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.samples = append(o.samples, sample)
}
func (o *waitObservations) snapshot() []resourceSample {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]resourceSample(nil), o.samples...)
}

type processStats struct {
	PSSBytes     *uint64
	RSSBytes     *uint64
	SwapPSSBytes *uint64
	MinorFaults  *uint64
	MajorFaults  *uint64
}

func main() {
	opts := parseOptions()
	if opts.worker {
		row := runWorker(opts)
		_ = json.NewEncoder(os.Stdout).Encode(row)
		if row.Outcome == "worker_error" {
			os.Exit(0)
		}
		return
	}
	if err := runParent(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func parseOptions() options {
	var opts options
	flag.StringVar(&opts.guest, "guest", "", "numpy-core artifact directory or wasm path")
	flag.StringVar(&opts.output, "output", "", "JSONL output path")
	flag.IntVar(&opts.blocks, "blocks", 5, "randomized blocks; each block schedules all three arms")
	flag.Int64Var(&opts.seed, "seed", 1, "randomization seed")
	flag.IntVar(&opts.workloadMiB, "workloadMiB", 64, "NumPy working-set size in MiB")
	flag.IntVar(&opts.waitMs, "waitMs", 1000, "bounded Host wait in milliseconds (default: 1s)")
	flag.IntVar(&opts.pressureMiB, "pressureMiB", 0, "bounded anonymous-mmap pressure companion in MiB; shared by every arm")
	flag.DurationVar(&opts.cold, "cold", 10*time.Millisecond, "fixed/pressure cold threshold")
	flag.DurationVar(&opts.pageout, "pageout", 20*time.Millisecond, "fixed/pressure pageout threshold; zero disables pageout")
	flag.Float64Var(&opts.pressureThreshold, "pressureThreshold", 0.8, "pressure-arm ancestor cgroup occupancy threshold")
	flag.BoolVar(&opts.smoke, "smoke", false, "schedule one randomized block")
	flag.BoolVar(&opts.worker, "worker", false, "internal single-trial worker")
	var armName string
	flag.StringVar(&armName, "arm", "", "internal worker arm")
	flag.IntVar(&opts.workerBlock, "block", 0, "internal worker block")
	flag.Int64Var(&opts.workerSeed, "blockSeed", 0, "internal worker block seed")
	flag.Parse()
	opts.workerArm = arm(armName)
	if opts.smoke {
		opts.blocks = 1
	}
	if opts.guest == "" || (opts.output == "" && !opts.worker) || opts.blocks <= 0 || opts.workloadMiB <= 0 || opts.workloadMiB > maxWorkloadMiB || opts.waitMs < 1000 || opts.pressureMiB < 0 || opts.pressureMiB >= maxPressureMiB || opts.cold <= 0 || opts.pageout < 0 || opts.pressureThreshold <= 0 || opts.pressureThreshold > 1 {
		failUsage("invalid benchmark flags")
	}
	if opts.worker && (opts.workerBlock < 0 || !validArm(opts.workerArm)) {
		failUsage("invalid worker flags")
	}
	return opts
}

func runParent(opts options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	bundle, err := loadArtifactBundle(opts.guest)
	if err != nil {
		return fmt.Errorf("load guest: %w", err)
	}
	file, err := os.OpenFile(opts.output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	metadata, err := makeMetadata(opts, bundle)
	if err != nil {
		return err
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("serialize metadata: %w", err)
	}
	if _, err := file.Write(append(metadataBytes, '\n')); err != nil {
		return err
	}
	// The parent has finished artifact verification. Do not retain the large
	// Guest image while it serially schedules child trials.
	bundle.wasm = nil
	bundle.profile = runtimeconfig.ExecutionProfile{}
	debug.FreeOSMemory()
	trials := makeSchedule(opts.blocks, opts.seed)
	hadFailure := false
	for _, spec := range trials {
		row := runIsolatedWorker(opts, spec)
		if err := writeJSONLine(file, row); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		if row.Outcome != "success" {
			hadFailure = true
		}
	}
	if hadFailure {
		return errors.New("one or more scheduled trials failed; see JSONL rows")
	}
	return nil
}

func makeSchedule(blocks int, seed int64) []trialSpec {
	trials := make([]trialSpec, 0, blocks*len(arms))
	for block := 0; block < blocks; block++ {
		blockSeed := seed + int64(block)
		order := append([]arm(nil), arms...)
		rand.New(rand.NewSource(blockSeed)).Shuffle(len(order), func(i, j int) {
			order[i], order[j] = order[j], order[i]
		})
		for index, selected := range order {
			id := fmt.Sprintf("b%03d-%s-%02d", block, selected, index)
			trials = append(trials, trialSpec{ID: id, Block: block, BlockSeed: blockSeed, Arm: selected})
		}
	}
	return trials
}

func runIsolatedWorker(opts options, spec trialSpec) trialRow {
	row := trialRow{RecordType: "trial", SchemaVersion: rowSchemaVersion, TrialID: spec.ID, Block: spec.Block, BlockSeed: spec.BlockSeed, Arm: string(spec.Arm), Outcome: "worker_crash"}
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout(opts))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		row.Error = stringPtr(err.Error())
		return row
	}
	args := []string{
		"-worker", "-guest", opts.guest, "-arm", string(spec.Arm), "-block", strconv.Itoa(spec.Block), "-blockSeed", strconv.FormatInt(spec.BlockSeed, 10),
		"-workloadMiB", strconv.Itoa(opts.workloadMiB), "-waitMs", strconv.Itoa(opts.waitMs), "-pressureMiB", strconv.Itoa(opts.pressureMiB),
		"-cold", opts.cold.String(), "-pageout", opts.pageout.String(), "-pressureThreshold", strconv.FormatFloat(opts.pressureThreshold, 'g', -1, 64),
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		row.Outcome = "timeout"
		row.Error = stringPtr("worker deadline exceeded")
		return row
	}
	if err != nil {
		row.Outcome = "worker_crash"
		row.Error = stringPtr(err.Error())
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			row.WorkerExitCode = &code
		}
		return row
	}
	var child trialRow
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &child); err != nil {
		row.Error = stringPtr("worker returned invalid JSON: " + err.Error())
		return row
	}
	child.TrialID = spec.ID
	child.Block = spec.Block
	child.BlockSeed = spec.BlockSeed
	child.Arm = string(spec.Arm)
	return child
}

func workerTimeout(opts options) time.Duration {
	base := time.Duration(opts.waitMs)*time.Millisecond + 30*time.Second
	if base < 45*time.Second {
		base = 45 * time.Second
	}
	return base
}

func runWorker(opts options) trialRow {
	spec := trialSpec{ID: fmt.Sprintf("b%03d-%s", opts.workerBlock, opts.workerArm), Block: opts.workerBlock, BlockSeed: opts.workerSeed, Arm: opts.workerArm}
	row := trialRow{RecordType: "trial", SchemaVersion: rowSchemaVersion, TrialID: spec.ID, Block: spec.Block, BlockSeed: spec.BlockSeed, Arm: string(spec.Arm), Outcome: "worker_error"}
	if !validArm(spec.Arm) {
		row.Error = stringPtr("invalid arm")
		return row
	}
	started := time.Now()
	bundle, err := loadArtifactBundle(opts.guest)
	if err != nil {
		row.Error = stringPtr("load guest: " + err.Error())
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	if err := validateOptions(opts); err != nil {
		row.Error = stringPtr(err.Error())
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	origin := started
	waitReleased := atomic.Int64{}
	var waitSamples waitObservations
	plan, err := waitPlan(opts.waitMs, origin, &waitReleased, &waitSamples)
	if err != nil {
		row.Error = stringPtr("wait plan: " + err.Error())
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	config, err := runConfig(bundle.profile, spec.Arm, opts)
	if err != nil {
		row.Error = stringPtr("run config: " + err.Error())
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	var lastBroker *capability.Broker
	warmupID := spec.ID + "-warmup"
	nextRunID := warmupID
	brokerFactory := func(context.Context) (*capability.Broker, error) {
		broker, err := capability.NewBroker(capability.Config{RunIdentity: nextRunID, Plan: plan})
		if err == nil {
			lastBroker = broker
		}
		return broker, err
	}
	engine, err := wazeroengine.NewWithBrokerFactory(context.Background(), bundle.wasm, config, brokerFactory)
	if err != nil {
		row.Error = stringPtr("create engine: " + err.Error())
		row.SetupNS = durationPtr(time.Since(started))
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	warmupRequest := makeRequest(warmupID, "import numpy as np\nresult = 0", nil)
	warmupResponse, runErr := engine.Run(context.Background(), warmupRequest, plan.PythonPrelude())
	if runErr != nil {
		_ = engine.Close(context.Background())
		row.Error = stringPtr("warmup: " + runErr.Error())
		row.SetupNS = durationPtr(time.Since(started))
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	if err := validateWarmupResponse(warmupRequest, warmupResponse, lastBroker, warmupID); err != nil {
		_ = engine.Close(context.Background())
		row.Error = stringPtr("warmup validation: " + err.Error())
		row.SetupNS = durationPtr(time.Since(started))
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	nextRunID = spec.ID
	row.SetupNS = durationPtr(time.Since(started))

	pressure, err := startPressure(opts.pressureMiB)
	if err != nil {
		_ = engine.Close(context.Background())
		row.Error = stringPtr("pressure companion: " + err.Error())
		row.Pressure = &pressureResult{RequestedMiB: uint64(opts.pressureMiB), Error: stringPtr(err.Error())}
		row.EndToEndNS = durationPtr(time.Since(started))
		return row
	}
	row.Pressure = &pressureResult{RequestedMiB: uint64(opts.pressureMiB), MappedBytes: pressure.mappedBytesPtr()}
	runStarted := time.Now()
	request := makeRequest(spec.ID, workloadSource(opts.workloadMiB), map[string]any{"seed": spec.BlockSeed, "workload_mib": opts.workloadMiB})
	response, runErr := engine.Run(context.Background(), request, plan.PythonPrelude())
	runElapsed := time.Since(runStarted)
	runFinishedNS := uint64(maxInt64(0, time.Since(origin).Nanoseconds()))
	afterRunStats := readProcessStats()
	row.WaitSamples = waitSamples.snapshot()
	row.AfterRunFaults = &faultCounts{MinorFaults: afterRunStats.MinorFaults, MajorFaults: afterRunStats.MajorFaults}
	if released := waitReleased.Load(); released > 0 {
		wake := uint64(maxInt64(0, int64(runFinishedNS)-released))
		row.WakeToResultNS = &wake
	}
	cleanupStarted := time.Now()
	pressureErr := pressure.stop()
	evidence := engine.ColdIOEvidence()
	closeErr := engine.Close(context.Background())
	cleanupElapsed := time.Since(cleanupStarted)
	row.RunNS = durationPtr(runElapsed)
	row.CleanupNS = durationPtr(cleanupElapsed)
	row.EndToEndNS = durationPtr(time.Since(started))
	row.ColdIO = &evidence
	if pressureErr != nil {
		row.Error = stringPtr("pressure cleanup: " + pressureErr.Error())
		return row
	}
	if runErr != nil {
		row.Error = stringPtr(runErr.Error())
		if closeErr != nil {
			row.Error = stringPtr(runErr.Error() + "; close: " + closeErr.Error())
		}
		return row
	}
	if closeErr != nil {
		row.Error = stringPtr("close: " + closeErr.Error())
		return row
	}
	parsed, err := parseGuestResponse(response, opts.workloadMiB, spec.BlockSeed)
	if err != nil {
		row.Error = stringPtr(err.Error())
		return row
	}
	row.Correctness = parsed
	if parsed.Exact != nil && *parsed.Exact {
		row.Outcome = "success"
	} else {
		row.Outcome = "incorrect"
	}
	return row
}

func runConfig(profile runtimeconfig.ExecutionProfile, selected arm, opts options) (runtimeconfig.RunConfig, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = workerTimeout(opts)
	config.ExecutionProfile = &profile
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	config.Mechanisms.ColdIOContinuation = true
	policy := runtimeconfig.ColdIOPolicy{Strategy: strategyForArm(selected)}
	if selected != armNatural {
		policy.ColdAfter = opts.cold
		policy.PageOutAfter = opts.pageout
	}
	if selected == armPressure {
		policy.PressureThreshold = opts.pressureThreshold
	}
	config.ColdIO = &policy
	return config, config.Validate()
}

func strategyForArm(selected arm) runtimeconfig.ColdIOStrategy {
	switch selected {
	case armNatural:
		return runtimeconfig.ColdIONatural
	case armFixed:
		return runtimeconfig.ColdIOFixed
	case armPressure:
		return runtimeconfig.ColdIOPressure
	default:
		return ""
	}
}

func waitPlan(waitMs int, origin time.Time, released *atomic.Int64, samples *waitObservations) (*capability.Plan, error) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"wait":"bounded"}`))
	if err != nil {
		return nil, err
	}
	spec := capability.Spec{
		Name: "residency.wait", Version: "pysolate.residency.wait.v1", Description: "Bounded Host wait for residency benchmark.",
		EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "residency-bench.wait.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "testio", Method: "wait", Arguments: []string{}, ResultField: "ok"},
	}
	handler := capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (result json.RawMessage, err error) {
		record := func() {
			if sample := sampleResource(origin); sample != nil {
				samples.add(*sample)
			}
		}
		record() // The working set and the trial's shared pressure are live now.
		defer func() {
			if err == nil {
				record() // Successful wait-end, before Guest resumes.
				released.Store(time.Since(origin).Nanoseconds())
			}
		}()
		timer := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
		defer timer.Stop()
		ticker := time.NewTicker(50 * time.Millisecond) // 20 Hz
		defer ticker.Stop()
		for {
			select {
			case <-timer.C:
				return json.RawMessage(`{"ok":true}`), nil
			case <-ticker.C:
				record()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	})
	if err := registry.Register(spec, grant, handler); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
}

func workloadSource(workloadMiB int) string {
	return fmt.Sprintf(`import numpy as np
n = %d * 1024 * 1024 // 8
offset = int(inputs["seed"] %% 97)
data = np.arange(n, dtype=np.int64)
computed = int(np.sum(data * 3 + offset))
wait_ok = testio.wait()
actual_sum = int(np.sum(data))
result = {"wait_ok": bool(wait_ok), "sum": actual_sum, "computed": computed, "offset": offset}`, workloadMiB)
}

func makeRequest(runID, source string, inputs map[string]any) []byte {
	if inputs == nil {
		inputs = map[string]any{}
	}
	request, _ := json.Marshal(map[string]any{"run_id": runID, "code": source, "inputs": inputs, "compatibility": map[string]any{"profile": "numpy-core", "imports": []string{"numpy"}}})
	return request
}

func validateWarmupResponse(requestBytes, response []byte, broker *capability.Broker, expectedRunID string) error {
	request, err := runtimeconfig.DecodeRunRequest(requestBytes)
	if err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if request.RunID != expectedRunID {
		return fmt.Errorf("request RunID %q does not match expected %q", request.RunID, expectedRunID)
	}
	if broker == nil || broker.RunIdentity() != expectedRunID {
		return fmt.Errorf("broker RunIdentity %q does not match %q", broker.RunIdentity(), expectedRunID)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
	if err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if decoded.Status != runtimeconfig.RunResponseOK {
		return fmt.Errorf("status=%q", decoded.Status)
	}
	var result int
	if err := json.Unmarshal(decoded.Result, &result); err != nil || result != 0 {
		return fmt.Errorf("warmup result is not integer zero: %s", string(decoded.Result))
	}
	return nil
}

func parseGuestResponse(response []byte, workloadMiB int, seed int64) (*correctness, error) {
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return nil, err
	}
	if envelope.Status != "ok" {
		return nil, fmt.Errorf("guest response status %q: %s", envelope.Status, string(envelope.Error))
	}
	var result struct {
		WaitOK   bool  `json:"wait_ok"`
		Sum      int64 `json:"sum"`
		Computed int64 `json:"computed"`
		Offset   int64 `json:"offset"`
	}
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		return nil, err
	}
	n := int64(workloadMiB) * 1024 * 1024 / 8
	sum := n * (n - 1) / 2
	offset := seed % 97
	if offset < 0 {
		offset += 97
	}
	expectedCompute := 3*sum + offset*n
	exact := result.WaitOK && result.Sum == sum && result.Computed == expectedCompute && result.Offset == offset
	return &correctness{WaitOK: boolPtr(result.WaitOK), Exact: boolPtr(exact), ExpectedSum: int64Ptr(sum), ActualSum: int64Ptr(result.Sum), ExpectedCompute: int64Ptr(expectedCompute), ActualCompute: int64Ptr(result.Computed), Offset: int64Ptr(result.Offset)}, nil
}

func startPressure(mebibytes int) (*pressureCompanion, error) {
	if mebibytes == 0 {
		return &pressureCompanion{}, nil
	}
	if mebibytes < 0 || mebibytes >= maxPressureMiB {
		return nil, fmt.Errorf("pressureMiB must be between zero and %d MiB (exclusive)", maxPressureMiB)
	}
	size := mebibytes * 1024 * 1024
	mapping, err := unix.Mmap(-1, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return nil, err
	}
	companion := &pressureCompanion{mapping: mapping, stopCh: make(chan struct{}), doneCh: make(chan struct{}), mapped: uint64(len(mapping))}
	companion.touch()
	go companion.touchLoop()
	return companion, nil
}

type pressureCompanion struct {
	mapping []byte
	stopCh  chan struct{}
	doneCh  chan struct{}
	mapped  uint64
	once    sync.Once
	err     error
}

func (pressure *pressureCompanion) touch() {
	for index := 0; index < len(pressure.mapping); index += pageSize {
		pressure.mapping[index]++
	}
}

func (pressure *pressureCompanion) touchLoop() {
	defer close(pressure.doneCh)
	for {
		pressure.touch()
		select {
		case <-pressure.stopCh:
			return
		default:
		}
	}
}

func (pressure *pressureCompanion) stop() error {
	if pressure == nil || pressure.mapping == nil {
		return nil
	}
	pressure.once.Do(func() {
		close(pressure.stopCh)
		<-pressure.doneCh
		pressure.err = unix.Munmap(pressure.mapping)
		pressure.mapping = nil
	})
	return pressure.err
}

func (pressure *pressureCompanion) mappedBytesPtr() *uint64 {
	if pressure == nil || pressure.mapped == 0 {
		return nil
	}
	value := pressure.mapped
	return &value
}

func sampleResource(origin time.Time) *resourceSample {
	stats := readProcessStats()
	return &resourceSample{AtMonoNS: uint64(time.Since(origin)), PSSBytes: stats.PSSBytes, RSSBytes: stats.RSSBytes, SwapPSSBytes: stats.SwapPSSBytes, MinorFaults: stats.MinorFaults, MajorFaults: stats.MajorFaults, CgroupAncestors: readCgroupAncestors()}
}

func readProcessStats() processStats {
	var stats processStats
	if raw, err := os.ReadFile("/proc/self/stat"); err == nil {
		if closeIndex := bytes.LastIndex(raw, []byte(")")); closeIndex >= 0 && closeIndex+2 < len(raw) {
			fields := strings.Fields(string(raw[closeIndex+2:]))
			if value, err := parseUintAt(fields, 7); err == nil {
				stats.MinorFaults = &value
			}
			if value, err := parseUintAt(fields, 9); err == nil {
				stats.MajorFaults = &value
			}
			if value, err := parseUintAt(fields, 21); err == nil {
				pages := uint64(os.Getpagesize())
				rss := value * pages
				stats.RSSBytes = &rss
			}
		}
	}
	if raw, err := os.ReadFile("/proc/self/smaps_rollup"); err == nil {
		var pss, swapPss *uint64
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			value *= 1024
			switch fields[0] {
			case "Pss:":
				pss = &value
			case "SwapPss:":
				swapPss = &value
			}
		}
		stats.PSSBytes, stats.SwapPSSBytes = pss, swapPss
	}
	return stats
}

func readCgroupAncestors() []cgroupSample {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil
	}
	var relative string
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			relative = parts[2]
			break
		}
	}
	if relative == "" {
		return nil
	}
	mount := cgroup2Mountpoint()
	if mount == "" {
		mount = "/sys/fs/cgroup"
	}
	path := filepath.Join(mount, relative)
	var result []cgroupSample
	for {
		result = append(result, readOneCgroup(path))
		if filepath.Clean(path) == filepath.Clean(mount) {
			break
		}
		next := filepath.Dir(path)
		if next == path {
			break
		}
		path = next
	}
	return result
}

func cgroup2Mountpoint() string {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for index, field := range fields {
			if field == "-" {
				separator = index
				break
			}
		}
		if separator >= 0 && separator+1 < len(fields) && fields[separator+1] == "cgroup2" && len(fields) > 4 {
			return strings.ReplaceAll(fields[4], `\040`, " ")
		}
	}
	return ""
}

func readOneCgroup(path string) cgroupSample {
	return cgroupSample{Path: path, CurrentBytes: readCgroupValue(filepath.Join(path, "memory.current")), HighBytes: readCgroupValue(filepath.Join(path, "memory.high")), MaxBytes: readCgroupValue(filepath.Join(path, "memory.max")), SwapCurrent: readCgroupValue(filepath.Join(path, "memory.swap.current")), SwapMax: readCgroupValue(filepath.Join(path, "memory.swap.max"))}
}

func readCgroupValue(path string) *uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "max" {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func makeMetadata(opts options, bundle artifactBundle) (metadataRecord, error) {
	for _, selected := range arms {
		if _, err := runConfig(bundle.profile, selected, opts); err != nil {
			return metadataRecord{}, err
		}
	}
	return metadataRecord{
		RecordType: "metadata", SchemaVersion: metadataSchemaVersion, Command: "cmd/residency-bench",
		Guest: opts.guest, Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(),
		RuntimeCommit: buildCommit(), ArtifactSHA256: bundle.profile.ArtifactSHA256(), ManifestSHA256: bundle.profile.ManifestSHA256(), ProfileID: bundle.profile.ID(),
		Blocks: opts.blocks, Seed: opts.seed, WorkloadMiB: opts.workloadMiB, WaitMs: opts.waitMs, PressureMiB: opts.pressureMiB,
		ColdAfterNS: opts.cold.Nanoseconds(), PageOutAfterNS: opts.pageout.Nanoseconds(), PressureThreshold: opts.pressureThreshold,
		ArmOrder: []string{string(armNatural), string(armFixed), string(armPressure)},
	}, nil
}

func loadArtifactBundle(root string) (artifactBundle, error) {
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return artifactBundle{}, err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return artifactBundle{}, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return artifactBundle{}, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return artifactBundle{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return artifactBundle{}, err
	}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return artifactBundle{}, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	if err != nil {
		return artifactBundle{}, err
	}
	return artifactBundle{wasm: wasm, profile: profile}, nil
}

func writeJSONLine(file *os.File, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = file.Write(append(encoded, '\n'))
	return err
}

func validateOptions(opts options) error {
	if opts.blocks <= 0 || opts.workloadMiB <= 0 || opts.workloadMiB > maxWorkloadMiB || opts.waitMs < 1000 || opts.pressureMiB < 0 || opts.pressureMiB >= maxPressureMiB || opts.cold <= 0 || opts.pageout < 0 || opts.pressureThreshold <= 0 || opts.pressureThreshold > 1 {
		return errors.New("invalid benchmark flags")
	}
	if opts.pageout != 0 && opts.pageout <= opts.cold {
		return errors.New("pageout must follow cold")
	}
	return nil
}

func validArm(selected arm) bool {
	return selected == armNatural || selected == armFixed || selected == armPressure
}
func stringPtr(value string) *string { return &value }
func boolPtr(value bool) *bool       { return &value }
func int64Ptr(value int64) *int64    { return &value }
func durationPtr(value time.Duration) *uint64 {
	result := uint64(maxInt64(0, value.Nanoseconds()))
	return &result
}
func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
func parseUintAt(fields []string, index int) (uint64, error) {
	if index < 0 || index >= len(fields) {
		return 0, errors.New("field unavailable")
	}
	return strconv.ParseUint(fields[index], 10, 64)
}
func buildCommit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return ""
}
func failUsage(message string) { fmt.Fprintln(os.Stderr, message); flag.PrintDefaults(); os.Exit(2) }
