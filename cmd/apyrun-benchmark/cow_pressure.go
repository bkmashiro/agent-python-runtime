package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	goruntime "runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

const pressureInitialCapacity uint32 = 4
const pressureMaxGrowthStep uint32 = 64
const pressureMinimumHeadroom = 512 * 1024 * 1024

type cowPressureLimits struct {
	RuntimeBudgetBytes uint64 `json:"runtime_budget_bytes"`
	ReservedBytes      uint64 `json:"reserved_bytes"`
	AllocationBytes    uint64 `json:"allocation_bytes"`
	MaxSlots           uint32 `json:"max_slots"`
	Consumers          uint32 `json:"consumers"`
	DurationNS         uint64 `json:"duration_ns"`
	InitialCapacity    uint32 `json:"initial_capacity"`
	MaxGrowthStep      uint32 `json:"max_growth_step"`
	Workload           string `json:"workload"`
	WaitNS             uint64 `json:"wait_ns"`
	RefillWorkers      uint32 `json:"refill_workers"`
}

type cowPressureSnapshot struct {
	Phase            string                           `json:"phase"`
	Slots            uint32                           `json:"slots"`
	RuntimeInstances uint32                           `json:"runtime_instances"`
	ObservedNS       uint64                           `json:"observed_unix_ns"`
	Process          runtimeevidence.ProcessMetrics   `json:"process"`
	COWMappings      runtimeevidence.MappingMetrics   `json:"cow_mappings"`
	Pool             wazeroengine.PreparedPoolState   `json:"pool"`
	PreparedImage    wazeroengine.PreparedImageState  `json:"prepared_image"`
	GoRuntime        runtimeevidence.GoRuntimeMetrics `json:"go_runtime"`
	Cgroup           runtimeevidence.CgroupMetrics    `json:"cgroup"`
}

type cowPressurePhase struct {
	Name      string `json:"name"`
	Count     uint64 `json:"count"`
	Succeeded uint64 `json:"succeeded"`
	Failed    uint64 `json:"failed"`
	TotalNS   uint64 `json:"total_ns"`
	MaxNS     uint64 `json:"max_ns"`
}

type cowPressureLoad struct {
	StartedRequests    uint64             `json:"started_requests"`
	CompletedRequests  uint64             `json:"completed_requests"`
	FailedRequests     uint64             `json:"failed_requests"`
	TimedOutRequests   uint64             `json:"timed_out_requests"`
	DurationNS         uint64             `json:"duration_ns"`
	ReplenishDrainNS   uint64             `json:"replenish_drain_ns"`
	ReplenishStatus    string             `json:"replenish_status"`
	CPUUserNS          uint64             `json:"cpu_user_ns"`
	CPUSystemNS        uint64             `json:"cpu_system_ns"`
	CPUCoreUtilization float64            `json:"cpu_core_utilization"`
	GOMAXPROCS         int                `json:"gomaxprocs"`
	ThroughputPerSec   float64            `json:"throughput_per_second"`
	LatencyP50NS       uint64             `json:"latency_p50_ns"`
	LatencyP95NS       uint64             `json:"latency_p95_ns"`
	LatencyP99NS       uint64             `json:"latency_p99_ns"`
	LatencyMaxNS       uint64             `json:"latency_max_ns"`
	ReadyBefore        uint32             `json:"ready_before"`
	ReadyAfter         uint32             `json:"ready_after"`
	Phases             []cowPressurePhase `json:"phases"`
}

type cowPressureEvidence struct {
	SchemaVersion int                                 `json:"schema_version"`
	EvidenceKind  string                              `json:"evidence_kind"`
	EvidenceClass string                              `json:"evidence_class"`
	Artifact      runtimeevidence.ArtifactIdentity    `json:"artifact"`
	HostSource    runtimeevidence.HostSourceIdentity  `json:"host_source"`
	Environment   runtimeevidence.EnvironmentIdentity `json:"environment"`
	Strategy      runtimeevidence.StrategyIdentity    `json:"strategy"`
	Limits        cowPressureLimits                   `json:"limits"`
	StopReason    string                              `json:"stop_reason"`
	Spawn         []cowPressureSnapshot               `json:"spawn_snapshots"`
	LoadSamples   []cowPressureSnapshot               `json:"load_samples"`
	Load          cowPressureLoad                     `json:"load"`
	Limitations   []string                            `json:"limitations"`
}

type growablePreparedBenchmarkRunner interface {
	preparedBenchmarkRunner
	GrowPreparedCapacity(context.Context, uint32) error
	PreparedCapacity() uint32
	PreparedRefillWorkers() uint32
	PreparedPoolState() wazeroengine.PreparedPoolState
	PreparedImageState() wazeroengine.PreparedImageState
}

func (evidence cowPressureEvidence) Validate() error {
	if evidence.SchemaVersion != 6 || evidence.EvidenceKind != "cow-pressure" || evidence.EvidenceClass != "production-safe" ||
		evidence.HostSource.Modified || evidence.HostSource.Revision == "" || evidence.Artifact.SHA256 == "" ||
		evidence.Strategy.Requested != "cow-ready-single-use" || evidence.Strategy.Active != "cow-ready-single-use" || evidence.Strategy.Fallback {
		return errors.New("cow-pressure identity is incomplete or untruthful")
	}
	if evidence.Limits.RuntimeBudgetBytes+evidence.Limits.ReservedBytes != evidence.Limits.AllocationBytes ||
		evidence.Limits.InitialCapacity != pressureInitialCapacity || evidence.Limits.MaxGrowthStep != pressureMaxGrowthStep || evidence.Limits.RefillWorkers == 0 || evidence.Limits.RefillWorkers > 4 ||
		!validPressureWorkload(evidence.Limits.Workload, time.Duration(evidence.Limits.WaitNS)) || len(evidence.Spawn) == 0 || evidence.StopReason == "" {
		return errors.New("cow-pressure limits or spawn evidence is incomplete")
	}
	previousSlots := uint32(0)
	preparedImage := evidence.Spawn[0].PreparedImage
	if !validPreparedImageState(preparedImage) {
		return errors.New("cow-pressure prepared image census is invalid")
	}
	for index, snapshot := range evidence.Spawn {
		growth := snapshot.Slots - previousSlots
		if snapshot.Slots <= previousSlots || growth%pressureInitialCapacity != 0 || growth > pressureMaxGrowthStep ||
			(index == 0 && snapshot.Slots != pressureInitialCapacity) || snapshot.Phase != "spawn" || snapshot.RuntimeInstances != 1 ||
			snapshot.COWMappings.Name != "memfd:apyrun-cow-image" || snapshot.COWMappings.MappingCount != snapshot.Slots ||
			!validPressurePoolState(snapshot.Pool, snapshot.Slots) || snapshot.PreparedImage != preparedImage {
			return errors.New("cow-pressure single-runtime growth sequence or mapping identity drifted")
		}
		pss, err := measuredValue(snapshot.Process.PSSBytes, "spawn process PSS")
		if err != nil || pss > evidence.Limits.RuntimeBudgetBytes {
			return errors.New("cow-pressure spawn PSS is unavailable or exceeds budget")
		}
		previousSlots = snapshot.Slots
	}
	if len(evidence.LoadSamples) != 1 || evidence.Load.StartedRequests == 0 || evidence.Load.CompletedRequests == 0 ||
		evidence.Load.CompletedRequests+evidence.Load.FailedRequests != evidence.Load.StartedRequests ||
		evidence.Load.DurationNS == 0 || evidence.Load.ThroughputPerSec <= 0 || evidence.Load.LatencyP99NS == 0 ||
		evidence.Load.CPUCoreUtilization <= 0 || evidence.Load.GOMAXPROCS <= 0 || len(evidence.Load.Phases) == 0 {
		return errors.New("cow-pressure closed-loop load evidence is incomplete")
	}
	lastSlots := evidence.Spawn[len(evidence.Spawn)-1].Slots
	if evidence.Load.ReadyBefore != lastSlots ||
		(evidence.Load.ReplenishStatus == "complete" && evidence.Load.ReadyAfter != lastSlots) ||
		(evidence.Load.ReplenishStatus != "complete" && evidence.Load.ReplenishStatus != "timeout") {
		return errors.New("cow-pressure replenish outcome is inconsistent")
	}
	phaseNames := map[string]struct{}{}
	for _, phase := range evidence.Load.Phases {
		if phase.Name == "" || phase.Count == 0 || phase.Succeeded+phase.Failed != phase.Count {
			return errors.New("cow-pressure phase accounting is incomplete")
		}
		if _, exists := phaseNames[phase.Name]; exists {
			return errors.New("cow-pressure phase names are duplicated")
		}
		phaseNames[phase.Name] = struct{}{}
	}
	snapshot := evidence.LoadSamples[0]
	if snapshot.Phase != "load-final" || snapshot.Slots != lastSlots || snapshot.RuntimeInstances != 1 ||
		snapshot.COWMappings.Name != "memfd:apyrun-cow-image" || !validPressureLoadMappingCount(snapshot.COWMappings.MappingCount, lastSlots, evidence.Limits.Consumers) ||
		!validPressureFinalPoolState(snapshot.Pool, lastSlots, evidence.Load.ReplenishStatus) ||
		snapshot.Pool.Ready != evidence.Load.ReadyAfter || snapshot.PreparedImage != preparedImage {
		return errors.New("cow-pressure final mapping identity drifted")
	}
	return nil
}

func validPreparedImageState(state wazeroengine.PreparedImageState) bool {
	return state.Available && state.VirtualBytes > 0 && state.AllocatedBytes <= state.VirtualBytes &&
		state.PageSizeBytes > 0 && state.VirtualBytes%state.PageSizeBytes == 0 &&
		state.ZeroPages+state.NonZeroPages == state.VirtualBytes/state.PageSizeBytes &&
		state.SparsePotentialBytes == state.ZeroPages*state.PageSizeBytes
}

func validPressurePoolState(state wazeroengine.PreparedPoolState, slots uint32) bool {
	return state.TargetCapacity == slots && state.MaximumCapacity >= slots && state.High == slots &&
		state.Floor <= state.Critical && state.Critical <= state.Low && state.Low <= state.High &&
		state.Ready == slots && state.SupplyAccounted == slots && state.Queued == 0 && state.Refilling == 0 &&
		state.Leased == 0 && state.Executing == 0 && state.Waiting == 0 && state.Retiring == 0 &&
		state.ConsecutiveFailures == 0 && state.TotalFailures == 0 && !state.BreakerOpen
}

func validPressureFinalPoolState(state wazeroengine.PreparedPoolState, slots uint32, replenishStatus string) bool {
	if replenishStatus == "complete" || replenishStatus == "timeout" && state.Ready == slots {
		return validPressurePoolState(state, slots)
	}
	return replenishStatus == "timeout" && state.TargetCapacity == slots && state.MaximumCapacity >= slots && state.High == slots &&
		state.Floor <= state.Critical && state.Critical <= state.Low && state.Low <= state.High &&
		state.Ready < slots && uint64(state.Ready)+uint64(state.Queued)+uint64(state.Refilling) == uint64(state.SupplyAccounted) &&
		state.SupplyAccounted == slots && state.Leased == 0 && state.Executing == 0 && state.Waiting == 0 && state.Retiring == 0 &&
		state.ConsecutiveFailures == 0 && state.TotalFailures == 0 && !state.BreakerOpen
}

func validPressureLoadMappingCount(mappings, slots, consumers uint32) bool {
	return uint64(mappings) <= uint64(slots)+uint64(consumers)
}

func validateCOWPressureOptions(options benchmarkOptions, goos string) error {
	if goos != "linux" || options.Kind != "cow-pressure" || options.Class != "production-safe" || options.Strategy != "cow-ready-single-use" {
		return errors.New("cow-pressure requires Linux production-safe cow-ready-single-use")
	}
	if options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath == "" {
		return errors.New("cow-pressure requires artifact, manifest, and output")
	}
	if options.MemoryBudgetBytes < 1024*1024*1024 || options.MemoryReserveBytes < 1024*1024*1024 ||
		options.MemoryBudgetBytes > 1<<50 || options.MemoryReserveBytes > 1<<50 ||
		math.MaxUint64-options.MemoryBudgetBytes < options.MemoryReserveBytes {
		return errors.New("cow-pressure memory budget or reserve is missing or outside bounds")
	}
	if options.MaxPressureSlots < uint(pressureInitialCapacity) || options.MaxPressureSlots > 65536 || options.MaxPressureSlots%uint(pressureInitialCapacity) != 0 {
		return errors.New("cow-pressure max slots must be a multiple of four between 4 and 65536")
	}
	if options.ConsumerCount == 0 || options.ConsumerCount > 256 || options.PressureDuration < 5*time.Second || options.PressureDuration > 10*time.Minute {
		return errors.New("cow-pressure consumers or duration is outside bounds")
	}
	if options.PressureRefillWorkers != 1 && options.PressureRefillWorkers != 2 && options.PressureRefillWorkers != 4 &&
		options.PressureRefillWorkers != 8 && options.PressureRefillWorkers != 12 && options.PressureRefillWorkers != 16 {
		return errors.New("cow-pressure refill workers must be 1, 2, 4, 8, 12, or 16")
	}
	if !validPressureWorkload(options.PressureWorkload, options.PressureWait) {
		return errors.New("cow-pressure workload or wait is outside bounds")
	}
	return nil
}

func validPressureWorkload(workload string, wait time.Duration) bool {
	switch workload {
	case "cpu":
		return wait == 0
	case "wasi-timer-wait":
		return wait >= time.Millisecond && wait <= 10*time.Second
	default:
		return false
	}
}

func runCOWPressureMain(options benchmarkOptions, goos string) error {
	if err := validateCOWPressureOptions(options, goos); err != nil {
		return err
	}
	artifact, wasm, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return err
	}
	if artifact.ArtifactProfile != "base" {
		return errors.New("cow-pressure is restricted to the qualified base artifact")
	}
	host, err := currentHostSource()
	if err != nil {
		return err
	}
	collector := runtimeevidence.DefaultLinuxCollector()
	initial, err := collector.Collect()
	if err != nil {
		return err
	}
	currentPSS, err := measuredValue(initial.Process.PSSBytes, "initial process PSS")
	if err != nil {
		return err
	}

	lifecycle := &lifecycleCollector{}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 2 * time.Minute
	neutral, err := (wazeroengine.Factory{
		PreparedCapacity:      pressureInitialCapacity,
		PreparedMaxCapacity:   uint32(options.MaxPressureSlots),
		PreparedRefillWorkers: uint32(options.PressureRefillWorkers),
		Strategy:              enginecontract.StrategyCOWReadySingleUse,
		Observer:              lifecycle.observe,
	}).New(context.Background(), wasm, config)
	if err != nil {
		return fmt.Errorf("initialize single COW runtime: %w", err)
	}
	defer neutral.Close(context.Background())
	runner, ok := neutral.(growablePreparedBenchmarkRunner)
	if !ok {
		return errors.New("growable COW pressure diagnostics are unavailable")
	}

	spawn := make([]cowPressureSnapshot, 0, options.MaxPressureSlots/uint(pressureMaxGrowthStep)+6)
	headroom := uint64(pressureMinimumHeadroom)
	stopReason := "max-slots"
	slots := pressureInitialCapacity
	for {
		snapshot, err := collectCOWPressureSnapshot(collector, runner, "spawn", slots)
		if err != nil {
			return err
		}
		if snapshot.COWMappings.MappingCount != slots {
			return fmt.Errorf("COW pressure mapping count=%d, want slots=%d", snapshot.COWMappings.MappingCount, slots)
		}
		spawn = append(spawn, snapshot)
		nextPSS, err := measuredValue(snapshot.Process.PSSBytes, "spawn process PSS")
		if err != nil {
			return err
		}
		if nextPSS > options.MemoryBudgetBytes {
			return fmt.Errorf("COW pressure admission exceeded runtime budget: PSS=%d budget=%d", nextPSS, options.MemoryBudgetBytes)
		}
		if nextPSS >= currentPSS {
			delta := nextPSS - currentPSS
			if delta > headroom/2 && delta <= math.MaxUint64/2 {
				headroom = delta * 2
			}
		}
		currentPSS = nextPSS
		if slots == uint32(options.MaxPressureSlots) {
			break
		}
		if currentPSS > options.MemoryBudgetBytes || options.MemoryBudgetBytes-currentPSS < headroom {
			stopReason = "admission-headroom"
			break
		}
		growth := slots
		if growth > pressureMaxGrowthStep {
			growth = pressureMaxGrowthStep
		}
		remaining := uint32(options.MaxPressureSlots) - slots
		if growth > remaining {
			growth = remaining
		}
		if err := runner.GrowPreparedCapacity(context.Background(), growth); err != nil {
			return fmt.Errorf("grow single COW runtime to %d slots: %w", slots+growth, err)
		}
		slots += growth
	}
	if runner.PreparedReady() != int(slots) || runner.PreparedCapacity() != slots {
		return errors.New("single COW runtime did not become fully ready")
	}

	lifecycle.drain()
	readyBefore := uint32(runner.PreparedReady())
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), options.PressureDuration)
	defer cancelLoad()
	var started, completed, failed, timedOut atomic.Uint64
	var sequence atomic.Uint64
	latencies := make([]uint64, 0, 4096)
	var latencyMu sync.Mutex
	var failureMu sync.Mutex
	var firstFailure string
	recordFailure := func(reason string) {
		failureMu.Lock()
		if firstFailure == "" {
			firstFailure = reason
			cancelLoad()
		}
		failureMu.Unlock()
	}
	var workers sync.WaitGroup
	cpuStarted, err := collectPressureCPU()
	if err != nil {
		return err
	}
	loadStarted := time.Now()
	for worker := uint(0); worker < options.ConsumerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for loadCtx.Err() == nil {
				id := sequence.Add(1)
				code := "result = inputs['value'] + 1"
				inputs := map[string]any{"value": id}
				if options.PressureWorkload == "wasi-timer-wait" {
					code = "import time\ntime.sleep(inputs['wait_seconds'])\nresult = inputs['value'] + 1"
					inputs["wait_seconds"] = options.PressureWait.Seconds()
				}
				request, err := makeRequest(fmt.Sprintf("pressure-%d", id), code, inputs)
				if err != nil {
					failed.Add(1)
					continue
				}
				started.Add(1)
				requestCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				began := time.Now()
				response, runErr := runner.Run(requestCtx, request, "")
				cancel()
				elapsedDuration := time.Since(began)
				elapsed := uint64(elapsedDuration.Nanoseconds())
				failureReason := pressureResponseFailure(response, options.PressureWorkload, options.PressureWait, elapsedDuration)
				if runErr != nil || failureReason != "" {
					failed.Add(1)
					if errors.Is(runErr, context.DeadlineExceeded) {
						timedOut.Add(1)
					}
					if runErr != nil {
						failureReason = "runner error: " + runErr.Error()
					}
					recordFailure(failureReason)
					continue
				}
				completed.Add(1)
				latencyMu.Lock()
				latencies = append(latencies, elapsed)
				latencyMu.Unlock()
			}
		}()
	}
	workers.Wait()
	if failed.Load() > 0 || timedOut.Load() > 0 {
		return fmt.Errorf("cow-pressure workload failed closed: failed=%d timed_out=%d first_failure=%s", failed.Load(), timedOut.Load(), firstFailure)
	}
	loadElapsed := time.Since(loadStarted)
	cpuFinished, err := collectPressureCPU()
	if err != nil {
		return err
	}

	replenishStarted := time.Now()
	replenishContext, cancelReplenish := context.WithTimeout(context.Background(), config.Timeout)
	defer cancelReplenish()
	replenishStatus := "complete"
	if err := waitForPreparedReady(replenishContext, runner, slots); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		replenishStatus = "timeout"
	}
	replenishElapsed := time.Since(replenishStarted)
	finalSnapshot, err := collectCOWPressureSnapshot(collector, runner, "load-final", slots)
	if err != nil {
		return err
	}
	readyAfter := finalSnapshot.Pool.Ready

	latencyMu.Lock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	latencyCopy := append([]uint64(nil), latencies...)
	latencyMu.Unlock()
	load := cowPressureLoad{
		StartedRequests: started.Load(), CompletedRequests: completed.Load(), FailedRequests: failed.Load(), TimedOutRequests: timedOut.Load(),
		DurationNS: uint64(loadElapsed.Nanoseconds()), ReplenishDrainNS: uint64(replenishElapsed.Nanoseconds()), ReplenishStatus: replenishStatus,
		CPUUserNS: cpuFinished.userNS - cpuStarted.userNS, CPUSystemNS: cpuFinished.systemNS - cpuStarted.systemNS,
		GOMAXPROCS:  goruntime.GOMAXPROCS(0),
		ReadyBefore: readyBefore, ReadyAfter: readyAfter, Phases: aggregatePressurePhases(lifecycle.drain()),
	}
	if loadElapsed > 0 {
		load.ThroughputPerSec = float64(load.CompletedRequests) / loadElapsed.Seconds()
		load.CPUCoreUtilization = float64(load.CPUUserNS+load.CPUSystemNS) / float64(load.DurationNS)
	}
	if len(latencyCopy) > 0 {
		load.LatencyP50NS = pressurePercentile(latencyCopy, 50)
		load.LatencyP95NS = pressurePercentile(latencyCopy, 95)
		load.LatencyP99NS = pressurePercentile(latencyCopy, 99)
		load.LatencyMaxNS = latencyCopy[len(latencyCopy)-1]
	}

	evidence := cowPressureEvidence{
		SchemaVersion: 6, EvidenceKind: "cow-pressure", EvidenceClass: "production-safe",
		Artifact:   runtimeevidence.ArtifactIdentity{Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size), SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile, Target: artifact.Target, ExecutionModel: artifact.Execution},
		HostSource: runtimeevidence.HostSourceIdentity{Revision: host.Revision, Modified: host.Modified}, Environment: initial.Environment,
		Strategy:   runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use", Fallback: false},
		Limits:     cowPressureLimits{RuntimeBudgetBytes: options.MemoryBudgetBytes, ReservedBytes: options.MemoryReserveBytes, AllocationBytes: options.MemoryBudgetBytes + options.MemoryReserveBytes, MaxSlots: uint32(options.MaxPressureSlots), Consumers: uint32(options.ConsumerCount), DurationNS: uint64(options.PressureDuration.Nanoseconds()), InitialCapacity: pressureInitialCapacity, MaxGrowthStep: pressureMaxGrowthStep, Workload: options.PressureWorkload, WaitNS: uint64(options.PressureWait.Nanoseconds()), RefillWorkers: runner.PreparedRefillWorkers()},
		StopReason: stopReason, Spawn: spawn, LoadSamples: []cowPressureSnapshot{finalSnapshot}, Load: load,
		Limitations: []string{
			"Admission uses process PSS with a conservative dynamic headroom; it is not a kernel memory reservation.",
			"One bounded wazero runtime, compiled module, and sealed baseline owns every admitted COW slot.",
			"Full smaps collection is excluded from the timed load and runs only after request and refill drain, so in-load physical peaks are not sampled.",
			"Raw cgroup counters retain their observed scope; shared job-level values are not process attribution or per-slot memory.",
			"Closed-loop consumers use a small deterministic Python request and do not model provider latency or open-loop arrivals.",
			"The configured pressure duration is the request-issuance window; load.duration_ns and throughput include bounded in-flight request drain but exclude replenish_drain_ns.",
			"Served slots remain single-use and are replenished; no served-slot restore is claimed.",
		},
	}
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate cow-pressure evidence: %w", err)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return err
	}
	fmt.Printf("{\"output\":%q,\"slots\":%d,\"completed\":%d,\"replenish_status\":%q}\n", options.OutputPath, slots, load.CompletedRequests, load.ReplenishStatus)
	return nil
}

func collectCOWPressureSnapshot(collector runtimeevidence.LinuxCollector, runner growablePreparedBenchmarkRunner, phase string, slots uint32) (cowPressureSnapshot, error) {
	process, err := collector.Collect()
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	mappings, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	goMetrics, err := runtimeevidence.CollectGoRuntimeMetrics()
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	cgroup, err := collector.CollectOperationalCgroup()
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	return cowPressureSnapshot{
		Phase: phase, Slots: slots, RuntimeInstances: 1, ObservedNS: uint64(time.Now().UnixNano()),
		Process: process.Process, COWMappings: mappings, Pool: runner.PreparedPoolState(), PreparedImage: runner.PreparedImageState(), GoRuntime: goMetrics, Cgroup: cgroup,
	}, nil
}

func waitForPreparedReady(ctx context.Context, runner growablePreparedBenchmarkRunner, slots uint32) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if runner.PreparedReady() == int(slots) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %d replenished COW slots: %w", slots, ctx.Err())
		case <-ticker.C:
		}
	}
}

func aggregatePressurePhases(observations []wazeroengine.Observation) []cowPressurePhase {
	byName := map[string]*cowPressurePhase{}
	for _, observation := range observations {
		phase := byName[observation.Phase]
		if phase == nil {
			phase = &cowPressurePhase{Name: observation.Phase}
			byName[observation.Phase] = phase
		}
		phase.Count++
		if observation.Success {
			phase.Succeeded++
		} else {
			phase.Failed++
		}
		duration := uint64(observation.Duration.Nanoseconds())
		phase.TotalNS += duration
		if duration > phase.MaxNS {
			phase.MaxNS = duration
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]cowPressurePhase, 0, len(names))
	for _, name := range names {
		result = append(result, *byName[name])
	}
	return result
}

func measuredValue(metric runtimeevidence.Metric, name string) (uint64, error) {
	if metric.Status != runtimeevidence.MetricMeasured || metric.Value == nil {
		return 0, fmt.Errorf("%s is unavailable", name)
	}
	return *metric.Value, nil
}

func pressureResponseFailure(raw []byte, workload string, requestedWait, elapsed time.Duration) string {
	var response struct {
		Status string         `json:"status"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "invalid guest response JSON"
	}
	if response.Status != "ok" {
		errorType, _ := response.Error["type"].(string)
		message, _ := response.Error["message"].(string)
		if len(message) > 256 {
			message = message[:256]
		}
		return fmt.Sprintf("guest status=%q error_type=%q message=%q", response.Status, errorType, message)
	}
	if workload == "wasi-timer-wait" && elapsed+time.Millisecond < requestedWait {
		return fmt.Sprintf("timer returned early: elapsed=%s requested=%s", elapsed, requestedWait)
	}
	return ""
}

func pressurePercentile(sorted []uint64, percentile uint64) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (uint64(len(sorted))*percentile + 99) / 100
	if index == 0 {
		index = 1
	}
	return sorted[index-1]
}
