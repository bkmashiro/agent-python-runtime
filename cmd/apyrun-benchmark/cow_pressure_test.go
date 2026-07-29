package main

import (
	"encoding/json"
	"math"
	"os"
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
		SchemaVersion: 6, EvidenceKind: "cow-pressure", EvidenceClass: "production-safe",
		Artifact:    runtimeevidence.ArtifactIdentity{Filename: "guest.wasm", SHA256: strings.Repeat("a", 64), SizeBytes: 1, SourceCommit: strings.Repeat("b", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", ExecutionModel: "reactor"},
		HostSource:  runtimeevidence.HostSourceIdentity{Revision: strings.Repeat("c", 40)},
		Environment: runtimeevidence.EnvironmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.test", KernelRelease: "test", PageSizeBytes: 4096, CgroupVersion: "v2"},
		Strategy:    runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use"},
		Limits:      cowPressureLimits{RuntimeBudgetBytes: 1 << 30, ReservedBytes: 1 << 30, AllocationBytes: 2 << 30, MaxSlots: 4, Consumers: 1, DurationNS: uint64((5 * time.Second).Nanoseconds()), InitialCapacity: 4, MaxGrowthStep: 64, Workload: "cpu", RefillWorkers: 4},
		StopReason:  "max-slots", Spawn: []cowPressureSnapshot{spawn}, LoadSamples: []cowPressureSnapshot{loadSample},
		Load:        cowPressureLoad{StartedRequests: 1, CompletedRequests: 1, DurationNS: 1, ReplenishDrainNS: 1, ReplenishStatus: "complete", CPUUserNS: 1, CPUCoreUtilization: 1, GOMAXPROCS: 1, ThroughputPerSec: 1, LatencyP50NS: 1, LatencyP95NS: 1, LatencyP99NS: 1, LatencyMaxNS: 1, ReadyBefore: 4, ReadyAfter: 4, Phases: []cowPressurePhase{{Name: "execute", Count: 1, Succeeded: 1, TotalNS: 1, MaxNS: 1}}},
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
}

func TestValidateCOWPressureOptionsRequiresBoundedLinuxCOW(t *testing.T) {
	valid := benchmarkOptions{
		Kind: "cow-pressure", Class: "production-safe", Strategy: "cow-ready-single-use",
		ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "evidence.json",
		MemoryBudgetBytes: 32 * 1024 * 1024 * 1024, MemoryReserveBytes: 8 * 1024 * 1024 * 1024,
		MaxPressureSlots: 65536, ConsumerCount: 16, PressureDuration: 30 * time.Second, PressureWorkload: "cpu", PressureRefillWorkers: 4,
	}
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatal(err)
	}
	valid.PressureRefillWorkers = 16
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatalf("explicit 16-worker pressure sweep was rejected: %v", err)
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

func TestPressureLoadMappingCountAllowsOnlyBoundedRefillOverlap(t *testing.T) {
	if !validPressureLoadMappingCount(116, 100, 16) {
		t.Fatal("bounded served/refill mapping overlap was rejected")
	}
	if validPressureLoadMappingCount(117, 100, 16) {
		t.Fatal("mapping overlap exceeded consumer bound")
	}
}
