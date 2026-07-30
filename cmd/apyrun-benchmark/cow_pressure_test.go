package main

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCOWPressureSchemaCompiles(t *testing.T) {
	_ = compileCOWPressureSchema(t)
}

func compileCOWPressureSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	content, err := os.ReadFile("../../benchmark/v1/cow-pressure.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://github.com/bkmashiro/agent-python-runtime/benchmark/v1/cow-pressure.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestCanonicalCOWPressureEvidenceValidatesSchemaAndSemantics(t *testing.T) {
	one := uint64(1)
	metric := runtimeevidence.Metric{Status: runtimeevidence.MetricMeasured, Value: &one}
	unavailable := runtimeevidence.Metric{Status: runtimeevidence.MetricSkipped, ReasonCode: runtimeevidence.ReasonIsolationUnproven}
	process := runtimeevidence.ProcessMetrics{
		RSSBytes: metric, VirtualBytes: metric, PSSBytes: metric, PrivateCleanBytes: metric, PrivateDirtyBytes: metric,
		SwapBytes: metric, MinorFaults: metric, MajorFaults: metric, FDCount: metric, VMACount: metric, PageTableBytes: &metric,
	}
	mappings := runtimeevidence.MappingMetrics{
		Name: "memfd:apyrun-cow-image", MappingCount: 4, VirtualBytes: metric, RSSBytes: metric, PSSBytes: metric,
		SharedCleanBytes: metric, SharedDirtyBytes: metric, PrivateCleanBytes: metric, PrivateDirtyBytes: metric,
		ReferencedBytes: metric, AnonymousBytes: metric,
	}
	pool := wazeroengine.PreparedPoolState{TargetCapacity: 4, MaximumCapacity: 4, Floor: 1, Critical: 1, Low: 2, High: 4, Ready: 4, SupplyAccounted: 4}
	goMetrics := runtimeevidence.GoRuntimeMetrics{
		HeapLiveBytes: metric, HeapGoalBytes: metric, GCCyclesTotal: metric, GCPauseTotalNS: metric, Goroutines: metric,
		SchedulerLatency: runtimeevidence.Histogram{Status: runtimeevidence.MetricMeasured, UpperBoundsNS: []uint64{1}, Counts: []uint64{1}},
	}
	cgroup := runtimeevidence.CgroupMetrics{
		Version: "v2", Scope: "unverified", MembershipSHA256: strings.Repeat("d", 64),
		MemoryCurrentBytes: unavailable, MemoryPeakBytes: unavailable, MemorySwapCurrentBytes: unavailable,
		MemoryEventsHighTotal: unavailable, MemoryEventsOOMTotal: unavailable, MemoryEventsOOMKillTotal: unavailable,
		PressureSomeTotalUS: unavailable, PressureFullTotalUS: unavailable,
	}
	image := wazeroengine.PreparedImageState{Available: true, VirtualBytes: 65536, AllocatedBytes: 65536, PageSizeBytes: 4096, ZeroPages: 8, NonZeroPages: 8, SparsePotentialBytes: 32768}
	spawn := cowPressureSnapshot{Phase: "spawn", Slots: 4, RuntimeInstances: 1, ObservedNS: 1, Process: process, COWMappings: mappings, Pool: pool, PreparedImage: image, GoRuntime: goMetrics, Cgroup: cgroup}
	loadSample := spawn
	loadSample.Phase = "load-final"
	evidence := cowPressureEvidence{
		SchemaVersion: 10, EvidenceKind: "cow-pressure", EvidenceClass: "production-safe",
		Artifact:    runtimeevidence.ArtifactIdentity{Filename: "guest.wasm", SHA256: strings.Repeat("a", 64), SizeBytes: 1, SourceCommit: strings.Repeat("b", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", ExecutionModel: "reactor"},
		HostSource:  runtimeevidence.HostSourceIdentity{Revision: strings.Repeat("c", 40)},
		Environment: runtimeevidence.EnvironmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.test", KernelRelease: "test", PageSizeBytes: 4096, CgroupVersion: "v2"},
		Strategy:    runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use"},
		Limits:      cowPressureLimits{RuntimeBudgetBytes: 1 << 30, ReservedBytes: 1 << 30, AllocationBytes: 2 << 30, MaxSlots: 4, Consumers: 1, DurationNS: uint64((5 * time.Second).Nanoseconds()), InitialCapacity: 4, MaxGrowthStep: 64, Workload: "cpu", RefillPolicy: "fixed", RefillWorkers: 4, BurstFactor: 1},
		StopReason:  "max-slots", Spawn: []cowPressureSnapshot{spawn}, LoadSamples: []cowPressureSnapshot{loadSample},
		Load:        cowPressureLoad{StartedRequests: 1, CompletedRequests: 1, DurationNS: 1, ReplenishDrainNS: 1, ReplenishStatus: "complete", CPUUserNS: 1, CPUCoreUtilization: 1, GOMAXPROCS: 1, ThroughputPerSec: 1, LatencyP50NS: 1, LatencyP95NS: 1, LatencyP99NS: 1, LatencyMaxNS: 1, ReadyBefore: 4, ReadyAfter: 4, Phases: []cowPressurePhase{{Name: "execute", Count: 1, Succeeded: 1, TotalNS: 1, MaxNS: 1}}, RequestClasses: []cowPressureRequestClass{{Name: "tiny-cpu", Started: 1, Completed: 1}}},
		Limitations: []string{"one", "two", "three", "four"},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if err := compileCOWPressureSchema(t).Validate(document); err != nil {
		t.Fatal(err)
	}
	timeout := evidence
	timeout.Load.ReplenishStatus = "timeout"
	timeout.Load.ReadyAfter = 3
	timeoutSample := loadSample
	timeoutSample.Pool.Ready = 3
	timeoutSample.Pool.Queued = 1
	timeout.LoadSamples = []cowPressureSnapshot{timeoutSample}
	if err := timeout.Validate(); err != nil {
		t.Fatalf("bounded replenish timeout was rejected: %v", err)
	}
	sixteenWorkers := evidence
	sixteenWorkers.Limits.MaxSlots = 16
	sixteenWorkers.Limits.RefillWorkers = 16
	sixteenWorkers.StopReason = "admission-headroom"
	if err := sixteenWorkers.Validate(); err != nil {
		t.Fatalf("explicit 16-worker evidence was rejected: %v", err)
	}
	automaticWorkers := evidence
	automaticWorkers.Limits.RefillPolicy = "adaptive"
	if err := automaticWorkers.Validate(); err != nil {
		t.Fatalf("adaptive refill evidence was rejected: %v", err)
	}
	invalidWorkers := evidence
	invalidWorkers.Limits.RefillWorkers = 3
	if err := invalidWorkers.Validate(); err == nil {
		t.Fatal("invalid refill-worker evidence was accepted")
	}
	invalidPolicy := evidence
	invalidPolicy.Limits.RefillPolicy = "adaptive"
	invalidPolicy.Limits.RefillWorkers = 2
	if err := invalidPolicy.Validate(); err == nil {
		t.Fatal("adaptive evidence with the wrong worker bound was accepted")
	}
	encodedTimeout, err := json.Marshal(timeout)
	if err != nil {
		t.Fatal(err)
	}
	var timeoutDocument any
	if err := json.Unmarshal(encodedTimeout, &timeoutDocument); err != nil {
		t.Fatal(err)
	}
	if err := compileCOWPressureSchema(t).Validate(timeoutDocument); err != nil {
		t.Fatal(err)
	}
	dirty := evidence
	dirty.Limits.Workload = "dirty-hold"
	dirty.Limits.WaitNS = uint64((2 * time.Second).Nanoseconds())
	dirty.Limits.DirtyBytes = 1 << 20
	active := loadSample
	active.Phase = "load-active"
	active.COWMappings.MappingCount = 5
	active.Pool.Leased = 1
	active.Pool.Executing = 1
	dirty.LoadSamples = []cowPressureSnapshot{active, active, active, loadSample}
	if err := dirty.Validate(); err != nil {
		t.Fatalf("dirty active samples were rejected: %v", err)
	}
	encodedDirty, err := json.Marshal(dirty)
	if err != nil {
		t.Fatal(err)
	}
	var dirtyDocument any
	if err := json.Unmarshal(encodedDirty, &dirtyDocument); err != nil {
		t.Fatal(err)
	}
	if err := compileCOWPressureSchema(t).Validate(dirtyDocument); err != nil {
		t.Fatal(err)
	}
	burst := dirty
	burst.Limits.Workload = "mixed-v1"
	burst.Limits.WaitNS = 0
	burst.Limits.DirtyBytes = 0
	burst.Limits.DurationNS = uint64((10 * time.Second).Nanoseconds())
	burst.Limits.BurstFactor = 2
	burst.Load.Burst = &cowPressureBurst{Factor: 2, BaselineConsumers: 1, PeakConsumers: 2, StartOffsetNS: 5, PreWindowDurationNS: 5, BurstWindowDurationNS: 5, PreCompleted: 1, BurstCompleted: 1, PreThroughputPerSec: 1, BurstThroughputPerSec: 1}
	if err := burst.Validate(); err != nil {
		t.Fatalf("bounded burst evidence was rejected: %v", err)
	}
	encodedBurst, err := json.Marshal(burst)
	if err != nil {
		t.Fatal(err)
	}
	var burstDocument any
	if err := json.Unmarshal(encodedBurst, &burstDocument); err != nil {
		t.Fatal(err)
	}
	if err := compileCOWPressureSchema(t).Validate(burstDocument); err != nil {
		t.Fatal(err)
	}
}

func TestPressureRequestSpecCanonicalDistributions(t *testing.T) {
	mixed := benchmarkOptions{PressureWorkload: "mixed-v1"}
	mixedCounts := map[string]int{}
	for id := uint64(1); id <= 20; id++ {
		mixedCounts[pressureRequestSpecFor(mixed, id).Class]++
	}
	wantMixed := map[string]int{"tiny-cpu": 12, "wait-50ms": 5, "dirty-4m-500ms": 2, "dirty-16m-2s": 1}
	if !reflect.DeepEqual(mixedCounts, wantMixed) {
		t.Fatalf("mixed counts=%v want=%v", mixedCounts, wantMixed)
	}
	heavy := benchmarkOptions{PressureWorkload: "heavy-tail-v1"}
	heavyCounts := map[string]int{}
	for id := uint64(1); id <= 20; id++ {
		heavyCounts[pressureRequestSpecFor(heavy, id).Class]++
	}
	wantHeavy := map[string]int{"tiny-cpu": 19, "tail-2s": 1}
	if !reflect.DeepEqual(heavyCounts, wantHeavy) {
		t.Fatalf("heavy-tail counts=%v want=%v", heavyCounts, wantHeavy)
	}
}

func TestPressurePreparedImageIdentityExcludesAllocatedBlockCensus(t *testing.T) {
	baseline := wazeroengine.PreparedImageState{Available: true, VirtualBytes: 128 << 20, AllocatedBytes: 4 << 20, PageSizeBytes: 4096, ZeroPages: 30000, NonZeroPages: 2768, SparsePotentialBytes: 120 << 20}
	candidate := baseline
	candidate.AllocatedBytes += 2 << 20
	if !samePressurePreparedImageIdentity(candidate, baseline) {
		t.Fatal("allocated-block census drift changed prepared image identity")
	}
	candidate.ZeroPages++
	if samePressurePreparedImageIdentity(candidate, baseline) {
		t.Fatal("prepared image shape drift retained identity")
	}
}

func TestStableCOWPressureFinalSnapshotRequiresSettledCompleteState(t *testing.T) {
	completePool := wazeroengine.PreparedPoolState{TargetCapacity: 4, MaximumCapacity: 4, Floor: 1, Critical: 1, Low: 2, High: 4, Ready: 4, SupplyAccounted: 4}
	snapshot := cowPressureSnapshot{COWMappings: runtimeevidence.MappingMetrics{Name: "memfd:apyrun-cow-image", MappingCount: 4}, Pool: completePool}
	if !validStableCOWPressureFinalSnapshot(snapshot, 4, "complete", 2) {
		t.Fatal("settled complete snapshot was rejected")
	}
	snapshot.COWMappings.MappingCount = 3
	if validStableCOWPressureFinalSnapshot(snapshot, 4, "complete", 2) {
		t.Fatal("complete snapshot with a stale mapping census was accepted")
	}
	snapshot.Pool.Ready = 3
	snapshot.Pool.Queued = 1
	if !validStableCOWPressureFinalSnapshot(snapshot, 4, "timeout", 2) {
		t.Fatal("bounded timeout snapshot with in-progress refill was rejected")
	}
}

func TestValidateCOWPressureOptionsRequiresBoundedLinuxCOW(t *testing.T) {
	valid := benchmarkOptions{
		Kind: "cow-pressure", Class: "production-safe", Strategy: "cow-ready-single-use",
		ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "evidence.json",
		MemoryBudgetBytes: 32 * 1024 * 1024 * 1024, MemoryReserveBytes: 8 * 1024 * 1024 * 1024,
		MaxPressureSlots: 65536, ConsumerCount: 16, PressureDuration: 30 * time.Second, PressureWorkload: "cpu", PressureRefillWorkers: 4, PressureBurstFactor: 1,
	}
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatal(err)
	}
	valid.PressureRefillWorkers = 16
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatalf("explicit 16-worker pressure sweep was rejected: %v", err)
	}
	valid.PressureRefillWorkers = 0
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatalf("automatic refill policy was rejected: %v", err)
	}
	valid.PressureRefillWorkers = 4
	for name, mutate := range map[string]func(*benchmarkOptions){
		"wrong strategy":     func(value *benchmarkOptions) { value.Strategy = "fresh" },
		"missing budget":     func(value *benchmarkOptions) { value.MemoryBudgetBytes = 0 },
		"missing reserve":    func(value *benchmarkOptions) { value.MemoryReserveBytes = 0 },
		"nonmultiple slots":  func(value *benchmarkOptions) { value.MaxPressureSlots = 5 },
		"too many slots":     func(value *benchmarkOptions) { value.MaxPressureSlots = 65540 },
		"too many consumers": func(value *benchmarkOptions) { value.ConsumerCount = 257 },
		"invalid workers":    func(value *benchmarkOptions) { value.PressureRefillWorkers = 3 },
		"unknown workload":   func(value *benchmarkOptions) { value.PressureWorkload = "io" },
		"cpu with wait":      func(value *benchmarkOptions) { value.PressureWait = time.Second },
		"short duration":     func(value *benchmarkOptions) { value.PressureDuration = time.Second },
		"overflow total": func(value *benchmarkOptions) {
			value.MemoryBudgetBytes = math.MaxUint64
			value.MemoryReserveBytes = 1024 * 1024 * 1024
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := validateCOWPressureOptions(candidate, "linux"); err == nil {
				t.Fatal("invalid cow-pressure options accepted")
			}
		})
	}
	if err := validateCOWPressureOptions(valid, "darwin"); err == nil {
		t.Fatal("non-Linux cow-pressure accepted")
	}
	waiting := valid
	waiting.PressureWorkload = "wasi-timer-wait"
	waiting.PressureWait = 100 * time.Millisecond
	if err := validateCOWPressureOptions(waiting, "linux"); err != nil {
		t.Fatalf("bounded wait workload rejected: %v", err)
	}
	dirty := valid
	dirty.PressureWorkload = "dirty-hold"
	dirty.PressureWait = 2 * time.Second
	dirty.PressureDirtyBytes = 16 << 20
	if err := validateCOWPressureOptions(dirty, "linux"); err != nil {
		t.Fatalf("bounded dirty workload was rejected: %v", err)
	}
	burst := valid
	burst.PressureWorkload = "mixed-v1"
	burst.PressureBurstFactor = 8
	if err := validateCOWPressureOptions(burst, "linux"); err != nil {
		t.Fatalf("bounded correlated burst was rejected: %v", err)
	}
	dirty.PressureDirtyBytes = 0
	if err := validateCOWPressureOptions(dirty, "linux"); err == nil {
		t.Fatal("dirty workload without a working set was accepted")
	}
}

func TestPressureResponseFailureFailsClosed(t *testing.T) {
	if reason := pressureResponseFailure([]byte(`{"status":"ok"}`), "cpu", 0, 10*time.Millisecond); reason != "" {
		t.Fatalf("valid CPU response was rejected: %s", reason)
	}
	for name, raw := range map[string][]byte{
		"invalid json": []byte(`{`),
		"guest error":  []byte(`{"status":"error","error":{"type":"RuntimeError","message":"timer unavailable"}}`),
		"no status":    []byte(`{"result":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			if reason := pressureResponseFailure(raw, "cpu", 0, 10*time.Millisecond); reason == "" {
				t.Fatal("invalid response was accepted")
			}
		})
	}
	if reason := pressureResponseFailure([]byte(`{"status":"ok"}`), "wasi-timer-wait", 100*time.Millisecond, 40*time.Millisecond); reason == "" {
		t.Fatal("timer response that returned before its requested wait was accepted")
	}
	if reason := pressureResponseFailure([]byte(`{"status":"ok"}`), "wasi-timer-wait", 100*time.Millisecond, 100*time.Millisecond); reason != "" {
		t.Fatalf("completed timer response was rejected: %s", reason)
	}
}

func TestPressurePercentileUsesNearestRank(t *testing.T) {
	values := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if pressurePercentile(values, 50) != 5 || pressurePercentile(values, 95) != 10 || pressurePercentile(values, 99) != 10 {
		t.Fatal("pressure percentile drifted")
	}
	if pressurePercentile(nil, 99) != 0 {
		t.Fatal("empty pressure percentile is not zero")
	}
}

func TestAggregatePressurePhasesIsDeterministicAndComplete(t *testing.T) {
	phases := aggregatePressurePhases([]wazeroengine.Observation{
		{Phase: "pool_wait", Duration: 2 * time.Millisecond, Success: true},
		{Phase: "execute", Duration: 3 * time.Millisecond, Success: false},
		{Phase: "execute", Duration: time.Millisecond, Success: true},
	})
	if len(phases) != 2 || phases[0].Name != "execute" || phases[1].Name != "pool_wait" {
		t.Fatalf("phase ordering drifted: %#v", phases)
	}
	if phases[0].Count != 2 || phases[0].Succeeded != 1 || phases[0].Failed != 1 || phases[0].TotalNS != uint64((4*time.Millisecond).Nanoseconds()) || phases[0].MaxNS != uint64((3*time.Millisecond).Nanoseconds()) {
		t.Fatalf("execute aggregation drifted: %#v", phases[0])
	}
}

func TestPressureActivePoolAllowsAccountedRetirementChurn(t *testing.T) {
	state := wazeroengine.PreparedPoolState{TargetCapacity: 256, MaximumCapacity: 256, Floor: 1, Critical: 64, Low: 128, High: 256, Ready: 217, Queued: 31, Refilling: 8, Leased: 32, Executing: 28, Retiring: 4, SupplyAccounted: 256}
	if !validPressureActivePoolState(state, 256, 32) {
		t.Fatal("accounted single-use retirement churn was rejected")
	}
	state.Waiting = 32
	if !validPressureActivePoolState(state, 256, 64) || validPressureActivePoolState(state, 256, 63) {
		t.Fatal("bounded saturation wait accounting was not enforced")
	}
	state.Waiting = 0
	state.Retiring = 3
	if validPressureActivePoolState(state, 256, 32) {
		t.Fatal("leased/executing/retiring drift was accepted")
	}
}

func TestPressureLoadMappingCountAllowsOnlyBoundedRefillOverlap(t *testing.T) {
	if !validPressureLoadMappingCount(116, 100, 16) {
		t.Fatal("bounded served/refill mapping overlap was rejected")
	}
	if validPressureLoadMappingCount(117, 100, 16) {
		t.Fatal("mapping overlap exceeded consumer bound")
	}
}
