package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

const pressureShardCapacity uint32 = 4
const pressureMinimumHeadroom = 512 * 1024 * 1024

type cowPressureLimits struct {
	RuntimeBudgetBytes uint64 `json:"runtime_budget_bytes"`
	ReservedBytes      uint64 `json:"reserved_bytes"`
	AllocationBytes    uint64 `json:"allocation_bytes"`
	MaxSlots           uint32 `json:"max_slots"`
	Consumers          uint32 `json:"consumers"`
	DurationNS         uint64 `json:"duration_ns"`
	ShardCapacity      uint32 `json:"shard_capacity"`
}

type cowPressureSnapshot struct {
	Phase       string                         `json:"phase"`
	Slots       uint32                         `json:"slots"`
	Shards      uint32                         `json:"shards"`
	ObservedNS  uint64                         `json:"observed_unix_ns"`
	Process     runtimeevidence.ProcessMetrics `json:"process"`
	COWMappings runtimeevidence.MappingMetrics `json:"cow_mappings"`
}

type cowPressureLoad struct {
	StartedRequests   uint64  `json:"started_requests"`
	CompletedRequests uint64  `json:"completed_requests"`
	FailedRequests    uint64  `json:"failed_requests"`
	TimedOutRequests  uint64  `json:"timed_out_requests"`
	DurationNS        uint64  `json:"duration_ns"`
	ThroughputPerSec  float64 `json:"throughput_per_second"`
	LatencyP50NS      uint64  `json:"latency_p50_ns"`
	LatencyP95NS      uint64  `json:"latency_p95_ns"`
	LatencyP99NS      uint64  `json:"latency_p99_ns"`
	LatencyMaxNS      uint64  `json:"latency_max_ns"`
	ReadyBefore       uint32  `json:"ready_before"`
	ReadyAfter        uint32  `json:"ready_after"`
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

func (evidence cowPressureEvidence) Validate() error {
	if evidence.SchemaVersion != 1 || evidence.EvidenceKind != "cow-pressure" || evidence.EvidenceClass != "production-safe" ||
		evidence.HostSource.Modified || evidence.HostSource.Revision == "" || evidence.Artifact.SHA256 == "" ||
		evidence.Strategy.Requested != "cow-ready-single-use" || evidence.Strategy.Active != "cow-ready-single-use" || evidence.Strategy.Fallback {
		return errors.New("cow-pressure identity is incomplete or untruthful")
	}
	if evidence.Limits.RuntimeBudgetBytes+evidence.Limits.ReservedBytes != evidence.Limits.AllocationBytes ||
		evidence.Limits.ShardCapacity != pressureShardCapacity || len(evidence.Spawn) == 0 || evidence.StopReason == "" {
		return errors.New("cow-pressure limits or spawn evidence is incomplete")
	}
	for index, snapshot := range evidence.Spawn {
		wantSlots := uint32(index+1) * pressureShardCapacity
		if snapshot.Phase != "spawn" || snapshot.Slots != wantSlots || snapshot.Shards != uint32(index+1) ||
			snapshot.COWMappings.Name != "memfd:apyrun-cow-image" || snapshot.COWMappings.MappingCount != wantSlots {
			return errors.New("cow-pressure spawn sequence or mapping identity drifted")
		}
		pss, err := measuredValue(snapshot.Process.PSSBytes, "spawn process PSS")
		if err != nil || pss > evidence.Limits.RuntimeBudgetBytes {
			return errors.New("cow-pressure spawn PSS is unavailable or exceeds budget")
		}
	}
	if len(evidence.LoadSamples) == 0 || evidence.Load.StartedRequests == 0 || evidence.Load.CompletedRequests == 0 ||
		evidence.Load.CompletedRequests+evidence.Load.FailedRequests != evidence.Load.StartedRequests ||
		evidence.Load.DurationNS == 0 || evidence.Load.ThroughputPerSec <= 0 || evidence.Load.LatencyP99NS == 0 {
		return errors.New("cow-pressure closed-loop load evidence is incomplete")
	}
	lastSlots := evidence.Spawn[len(evidence.Spawn)-1].Slots
	if evidence.Load.ReadyBefore != lastSlots || evidence.Load.ReadyAfter > lastSlots {
		return errors.New("cow-pressure ready pool accounting drifted")
	}
	for _, snapshot := range evidence.LoadSamples {
		if snapshot.Slots != lastSlots || snapshot.COWMappings.Name != "memfd:apyrun-cow-image" ||
			!validPressureLoadMappingCount(snapshot.COWMappings.MappingCount, lastSlots, evidence.Limits.Consumers) {
			return errors.New("cow-pressure load mapping identity drifted")
		}
	}
	return nil
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
	if options.MaxPressureSlots < uint(pressureShardCapacity) || options.MaxPressureSlots > 4096 || options.MaxPressureSlots%uint(pressureShardCapacity) != 0 {
		return errors.New("cow-pressure max slots must be a multiple of four between 4 and 4096")
	}
	if options.ConsumerCount == 0 || options.ConsumerCount > 256 || options.PressureDuration < 5*time.Second || options.PressureDuration > 10*time.Minute {
		return errors.New("cow-pressure consumers or duration is outside bounds")
	}
	return nil
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
	runners := make([]preparedBenchmarkRunner, 0, options.MaxPressureSlots/uint(pressureShardCapacity))
	defer func() {
		for _, runner := range runners {
			_ = runner.Close(context.Background())
		}
	}()

	spawn := make([]cowPressureSnapshot, 0, cap(runners))
	currentPSS, err := measuredValue(initial.Process.PSSBytes, "initial process PSS")
	if err != nil {
		return err
	}
	headroom := uint64(pressureMinimumHeadroom)
	stopReason := "max-slots"
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 2 * time.Minute
	for uint32(len(runners))*pressureShardCapacity < uint32(options.MaxPressureSlots) {
		if currentPSS > options.MemoryBudgetBytes || options.MemoryBudgetBytes-currentPSS < headroom {
			stopReason = "admission-headroom"
			break
		}
		neutral, err := (wazeroengine.Factory{PreparedCapacity: pressureShardCapacity, Strategy: enginecontract.StrategyCOWReadySingleUse}).New(context.Background(), wasm, config)
		if err != nil {
			return fmt.Errorf("spawn COW pressure shard %d: %w", len(runners), err)
		}
		runner, ok := neutral.(preparedBenchmarkRunner)
		if !ok {
			_ = neutral.Close(context.Background())
			return errors.New("COW pressure diagnostics are unavailable")
		}
		if runner.PreparedReady() != int(pressureShardCapacity) {
			_ = runner.Close(context.Background())
			return errors.New("COW pressure shard did not become fully ready")
		}
		runners = append(runners, runner)
		slots := uint32(len(runners)) * pressureShardCapacity
		snapshot, err := collectCOWPressureSnapshot(collector, "spawn", slots, uint32(len(runners)))
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
		delta := nextPSS - currentPSS
		if delta > headroom/2 && delta <= math.MaxUint64/2 {
			headroom = delta * 2
		}
		currentPSS = nextPSS
	}
	if len(runners) == 0 {
		return errors.New("COW pressure admitted no shards")
	}

	readyBefore := pressureReadyCount(runners)
	loadSamples := make([]cowPressureSnapshot, 0, int(options.PressureDuration/time.Second)+1)
	var sampleMu sync.Mutex
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), options.PressureDuration)
	defer cancelLoad()
	var started, completed, failed, timedOut atomic.Uint64
	var sequence atomic.Uint64
	latencies := make([]uint64, 0, 4096)
	var latencyMu sync.Mutex
	var workers sync.WaitGroup
	loadStarted := time.Now()
	for worker := uint(0); worker < options.ConsumerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for loadCtx.Err() == nil {
				id := sequence.Add(1)
				runner := runners[(id-1)%uint64(len(runners))]
				request, err := makeRequest(fmt.Sprintf("pressure-%d", id), "result = inputs['value'] + 1", map[string]any{"value": id})
				if err != nil {
					failed.Add(1)
					continue
				}
				started.Add(1)
				requestCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				began := time.Now()
				response, runErr := runner.Run(requestCtx, request, "")
				cancel()
				elapsed := uint64(time.Since(began).Nanoseconds())
				if runErr != nil || !json.Valid(response) {
					failed.Add(1)
					if errors.Is(runErr, context.DeadlineExceeded) {
						timedOut.Add(1)
					}
					continue
				}
				completed.Add(1)
				latencyMu.Lock()
				latencies = append(latencies, elapsed)
				latencyMu.Unlock()
			}
		}()
	}
	samplerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-loadCtx.Done():
				samplerDone <- nil
				return
			case <-ticker.C:
				snapshot, err := collectCOWPressureSnapshot(collector, "load", uint32(len(runners))*pressureShardCapacity, uint32(len(runners)))
				if err != nil {
					samplerDone <- err
					return
				}
				sampleMu.Lock()
				loadSamples = append(loadSamples, snapshot)
				sampleMu.Unlock()
			}
		}
	}()
	workers.Wait()
	if err := <-samplerDone; err != nil {
		return err
	}
	loadElapsed := time.Since(loadStarted)
	finalSnapshot, err := collectCOWPressureSnapshot(collector, "load-final", uint32(len(runners))*pressureShardCapacity, uint32(len(runners)))
	if err != nil {
		return err
	}
	loadSamples = append(loadSamples, finalSnapshot)
	readyAfter := pressureReadyCount(runners)

	latencyMu.Lock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	latencyCopy := append([]uint64(nil), latencies...)
	latencyMu.Unlock()
	load := cowPressureLoad{
		StartedRequests: started.Load(), CompletedRequests: completed.Load(), FailedRequests: failed.Load(), TimedOutRequests: timedOut.Load(),
		DurationNS: uint64(loadElapsed.Nanoseconds()), ReadyBefore: readyBefore, ReadyAfter: readyAfter,
	}
	if loadElapsed > 0 {
		load.ThroughputPerSec = float64(load.CompletedRequests) / loadElapsed.Seconds()
	}
	if len(latencyCopy) > 0 {
		load.LatencyP50NS = pressurePercentile(latencyCopy, 50)
		load.LatencyP95NS = pressurePercentile(latencyCopy, 95)
		load.LatencyP99NS = pressurePercentile(latencyCopy, 99)
		load.LatencyMaxNS = latencyCopy[len(latencyCopy)-1]
	}

	evidence := cowPressureEvidence{
		SchemaVersion: 1, EvidenceKind: "cow-pressure", EvidenceClass: "production-safe",
		Artifact:   runtimeevidence.ArtifactIdentity{Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size), SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile, Target: artifact.Target, ExecutionModel: artifact.Execution},
		HostSource: runtimeevidence.HostSourceIdentity{Revision: host.Revision, Modified: host.Modified}, Environment: initial.Environment,
		Strategy:   runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use", Fallback: false},
		Limits:     cowPressureLimits{RuntimeBudgetBytes: options.MemoryBudgetBytes, ReservedBytes: options.MemoryReserveBytes, AllocationBytes: options.MemoryBudgetBytes + options.MemoryReserveBytes, MaxSlots: uint32(options.MaxPressureSlots), Consumers: uint32(options.ConsumerCount), DurationNS: uint64(options.PressureDuration.Nanoseconds()), ShardCapacity: pressureShardCapacity},
		StopReason: stopReason, Spawn: spawn, LoadSamples: loadSamples, Load: load,
		Limitations: []string{
			"Admission uses process PSS with a conservative dynamic headroom; it is not a kernel memory reservation.",
			"Each four-slot shard owns a distinct wazero runtime, compiled module, and sealed baseline in this unoptimized implementation.",
			"Closed-loop consumers use a small deterministic Python request and do not model provider latency or open-loop arrivals.",
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
	fmt.Printf("{\"output\":%q,\"slots\":%d,\"completed\":%d}\n", options.OutputPath, len(runners)*int(pressureShardCapacity), load.CompletedRequests)
	return nil
}

func collectCOWPressureSnapshot(collector runtimeevidence.LinuxCollector, phase string, slots, shards uint32) (cowPressureSnapshot, error) {
	process, err := collector.Collect()
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	mappings, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
	if err != nil {
		return cowPressureSnapshot{}, err
	}
	return cowPressureSnapshot{Phase: phase, Slots: slots, Shards: shards, ObservedNS: uint64(time.Now().UnixNano()), Process: process.Process, COWMappings: mappings}, nil
}

func measuredValue(metric runtimeevidence.Metric, name string) (uint64, error) {
	if metric.Status != runtimeevidence.MetricMeasured || metric.Value == nil {
		return 0, fmt.Errorf("%s is unavailable", name)
	}
	return *metric.Value, nil
}

func pressureReadyCount(runners []preparedBenchmarkRunner) uint32 {
	var total uint32
	for _, runner := range runners {
		total += uint32(runner.PreparedReady())
	}
	return total
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
