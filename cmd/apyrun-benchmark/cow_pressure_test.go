package main

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

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
	process := runtimeevidence.ProcessMetrics{
		RSSBytes: metric, VirtualBytes: metric, PSSBytes: metric, PrivateCleanBytes: metric, PrivateDirtyBytes: metric,
		SwapBytes: metric, MinorFaults: metric, MajorFaults: metric, FDCount: metric, VMACount: metric,
	}
	mappings := runtimeevidence.MappingMetrics{
		Name: "memfd:apyrun-cow-image", MappingCount: 4, VirtualBytes: metric, RSSBytes: metric, PSSBytes: metric,
		SharedCleanBytes: metric, SharedDirtyBytes: metric, PrivateCleanBytes: metric, PrivateDirtyBytes: metric,
		ReferencedBytes: metric, AnonymousBytes: metric,
	}
	spawn := cowPressureSnapshot{Phase: "spawn", Slots: 4, Shards: 1, ObservedNS: 1, Process: process, COWMappings: mappings}
	loadSample := spawn
	loadSample.Phase = "load-final"
	evidence := cowPressureEvidence{
		SchemaVersion: 1, EvidenceKind: "cow-pressure", EvidenceClass: "production-safe",
		Artifact:    runtimeevidence.ArtifactIdentity{Filename: "guest.wasm", SHA256: strings.Repeat("a", 64), SizeBytes: 1, SourceCommit: strings.Repeat("b", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", ExecutionModel: "reactor"},
		HostSource:  runtimeevidence.HostSourceIdentity{Revision: strings.Repeat("c", 40)},
		Environment: runtimeevidence.EnvironmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.test", KernelRelease: "test", PageSizeBytes: 4096, CgroupVersion: "v2"},
		Strategy:    runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use"},
		Limits:      cowPressureLimits{RuntimeBudgetBytes: 1 << 30, ReservedBytes: 1 << 30, AllocationBytes: 2 << 30, MaxSlots: 4, Consumers: 1, DurationNS: uint64((5 * time.Second).Nanoseconds()), ShardCapacity: 4},
		StopReason:  "max-slots", Spawn: []cowPressureSnapshot{spawn}, LoadSamples: []cowPressureSnapshot{loadSample},
		Load:        cowPressureLoad{StartedRequests: 1, CompletedRequests: 1, DurationNS: 1, ThroughputPerSec: 1, LatencyP50NS: 1, LatencyP95NS: 1, LatencyP99NS: 1, LatencyMaxNS: 1, ReadyBefore: 4, ReadyAfter: 4},
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
}

func TestValidateCOWPressureOptionsRequiresBoundedLinuxCOW(t *testing.T) {
	valid := benchmarkOptions{
		Kind: "cow-pressure", Class: "production-safe", Strategy: "cow-ready-single-use",
		ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "evidence.json",
		MemoryBudgetBytes: 32 * 1024 * 1024 * 1024, MemoryReserveBytes: 8 * 1024 * 1024 * 1024,
		MaxPressureSlots: 4096, ConsumerCount: 16, PressureDuration: 30 * time.Second,
	}
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*benchmarkOptions){
		"wrong strategy":     func(value *benchmarkOptions) { value.Strategy = "fresh" },
		"missing budget":     func(value *benchmarkOptions) { value.MemoryBudgetBytes = 0 },
		"missing reserve":    func(value *benchmarkOptions) { value.MemoryReserveBytes = 0 },
		"nonmultiple slots":  func(value *benchmarkOptions) { value.MaxPressureSlots = 5 },
		"too many consumers": func(value *benchmarkOptions) { value.ConsumerCount = 257 },
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

func TestPressureLoadMappingCountAllowsOnlyBoundedRefillOverlap(t *testing.T) {
	if !validPressureLoadMappingCount(116, 100, 16) {
		t.Fatal("bounded served/refill mapping overlap was rejected")
	}
	if validPressureLoadMappingCount(117, 100, 16) {
		t.Fatal("mapping overlap exceeded consumer bound")
	}
}
