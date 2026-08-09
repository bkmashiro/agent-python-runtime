package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func metric(status MetricStatus, value uint64) Metric {
	return Metric{Status: status, Value: &value}
}

func unavailable(status MetricStatus, reason UnavailableReason) Metric {
	return Metric{Status: status, ReasonCode: reason}
}

func validLifecycleDensityEvidence() (LifecycleDensityEvidence, []byte) {
	artifactBytes := []byte("fixture-wasm")
	digest := sha256.Sum256(artifactBytes)
	slots := []uint32{1, 2, 4, 8, 16}
	samples := make([]LifecycleDensitySample, 0, len(slots))
	for index, count := range slots {
		n := uint64(count)
		processDigest := sha256.Sum256([]byte(fmt.Sprintf("process-%d", index)))
		samples = append(samples, LifecycleDensitySample{
			SampleIndex:           uint32(index),
			RepeatIndex:           0,
			RequestedSlots:        count,
			RuntimeShards:         1,
			ActiveConcurrency:     0,
			ProcessInstanceSHA256: hex.EncodeToString(processDigest[:]),
			ObservedAtUnixNS:      metric(MetricTimestampObserved, uint64(index+1)),
			Pool: PoolState{
				TargetCapacity: count,
				Ready:          count,
				AccountedSlots: count,
			},
			Phases: PhaseTimings{
				QueueNS:       metric(MetricMeasured, n),
				InstantiateNS: metric(MetricMeasured, n),
				InitializeNS:  metric(MetricMeasured, n),
				RuntimeInitNS: metric(MetricMeasured, n),
				PrepareNS:     metric(MetricMeasured, n),
				ExecuteNS:     unavailable(MetricSkipped, ReasonNotApplicable),
				CapabilityNS:  unavailable(MetricSkipped, ReasonWorkloadNotRun),
				TotalNS:       metric(MetricMeasured, n),
			},
			GoRuntime: GoRuntimeMetrics{
				HeapLiveBytes:  metric(MetricMeasured, 100*n),
				HeapGoalBytes:  metric(MetricMeasured, 200*n),
				GCCyclesTotal:  metric(MetricMeasured, n),
				GCPauseTotalNS: metric(MetricMeasured, 10*n),
				Goroutines:     metric(MetricMeasured, n),
				SchedulerLatency: Histogram{
					Status:        MetricMeasured,
					UpperBoundsNS: []uint64{1_000, 10_000},
					Counts:        []uint64{n, n},
				},
			},
			Process: ProcessMetrics{
				RSSBytes:          metric(MetricMeasured, 1_000*n),
				VirtualBytes:      metric(MetricMeasured, 2_000*n),
				PSSBytes:          metric(MetricMeasured, 900*n),
				PrivateCleanBytes: metric(MetricMeasured, 100*n),
				PrivateDirtyBytes: metric(MetricMeasured, 800*n),
				SwapBytes:         metric(MetricMeasured, 0),
				MinorFaults:       metric(MetricMeasured, 10*n),
				MajorFaults:       metric(MetricMeasured, 0),
				FDCount:           metric(MetricMeasured, 5+n),
				VMACount:          metric(MetricMeasured, 20+n),
			},
			Cgroup: CgroupMetrics{
				Version:                  "v2",
				Scope:                    "unverified",
				MembershipSHA256:         strings.Repeat("c", 64),
				MemoryCurrentBytes:       unavailable(MetricSkipped, ReasonIsolationUnproven),
				MemoryPeakBytes:          unavailable(MetricSkipped, ReasonIsolationUnproven),
				MemorySwapCurrentBytes:   unavailable(MetricSkipped, ReasonIsolationUnproven),
				MemoryEventsHighTotal:    unavailable(MetricSkipped, ReasonIsolationUnproven),
				MemoryEventsOOMTotal:     unavailable(MetricSkipped, ReasonIsolationUnproven),
				MemoryEventsOOMKillTotal: unavailable(MetricSkipped, ReasonIsolationUnproven),
				PressureSomeTotalUS:      unavailable(MetricSkipped, ReasonIsolationUnproven),
				PressureFullTotalUS:      unavailable(MetricSkipped, ReasonIsolationUnproven),
			},
		})
	}
	return LifecycleDensityEvidence{
		SchemaVersion: 1,
		EvidenceClass: "lifecycle-density",
		Artifact: ArtifactIdentity{
			Filename:        "guest.wasm",
			SHA256:          hex.EncodeToString(digest[:]),
			SizeBytes:       uint64(len(artifactBytes)),
			SourceCommit:    strings.Repeat("a", 40),
			ArtifactProfile: "base",
			Target:          "wasm32-wasip1",
			ExecutionModel:  "reactor",
		},
		HostSource: HostSourceIdentity{Revision: strings.Repeat("b", 40), Modified: false},
		Backend:    BackendIdentity{Name: "wazero", Version: "v1.11.0", ResetMode: "fresh-instance"},
		Environment: EnvironmentIdentity{
			GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.24.0",
			KernelRelease: "6.8.0", PageSizeBytes: 4096, CgroupVersion: "v2",
		},
		Strategy: StrategyIdentity{
			Requested: "single-use-preinitialized",
			Active:    "single-use-preinitialized",
			Fallback:  false,
		},
		Plan: SweepPlan{
			Workload:              "idle-ready",
			SlotCounts:            slots,
			RepeatsPerSlot:        1,
			FreshProcessPerSample: true,
			MaxProcessRSSBytes:    4 * 1024 * 1024 * 1024,
			ChildTimeoutNS:        120_000_000_000,
		},
		Samples: samples,
		Summary: DerivedSummary{
			SampleCount:                  len(samples),
			PeakProcessRSSBytes:          metric(MetricMeasured, 16_000),
			PeakCgroupMemoryCurrentBytes: unavailable(MetricSkipped, ReasonIsolationUnproven),
			PeakGoHeapLiveBytes:          metric(MetricMeasured, 1_600),
		},
		Limitations: []string{"fixture evidence only"},
	}, artifactBytes
}

func TestPhase7PairedDensitySchemaCompiles(t *testing.T) {
	_ = compileLifecycleDensitySchema(t, "../../benchmark/v1/phase7-paired-density.schema.json")
}

func TestPhase7PairedDensitySchemaClosesIdentityObjects(t *testing.T) {
	content, err := os.ReadFile("../../benchmark/v1/phase7-paired-density.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	properties := document["properties"].(map[string]any)
	definitions := document["$defs"].(map[string]any)
	for _, field := range []string{"artifact", "host_source", "backend", "environment", "plan"} {
		property := properties[field].(map[string]any)
		reference, ok := property["$ref"].(string)
		if !ok || !strings.HasPrefix(reference, "#/$defs/") {
			t.Fatalf("%s is not schema-bound: %#v", field, property)
		}
		definition := definitions[strings.TrimPrefix(reference, "#/$defs/")].(map[string]any)
		allowsAdditional, closed := definition["additionalProperties"].(bool)
		required, _ := definition["required"].([]any)
		if !closed || allowsAdditional || len(required) == 0 {
			t.Fatalf("%s identity schema is not closed and required: %#v", field, definition)
		}
	}
}

func TestLifecycleDensityEvidenceAcceptsCanonicalSweepAndArtifactBinding(t *testing.T) {
	evidence, artifactBytes := validLifecycleDensityEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := evidence.ValidateArtifactBytes(artifactBytes); err != nil {
		t.Fatal(err)
	}
}

func validNumPyLifecycleDensityEvidence() (LifecycleDensityEvidence, []byte) {
	evidence, artifact := validLifecycleDensityEvidence()
	evidence.SchemaVersion = 2
	evidence.Artifact.ArtifactProfile = "numpy-core"
	evidence.Plan.Workload = "numpy-ready-idle"
	evidence.Strategy = StrategyIdentity{
		Requested: "single-use-preinitialized", Active: "single-use-preinitialized",
	}
	evidence.Warmup = &PreparedWarmupIdentity{
		Profile: "numpy-ready-v1", GenerationSHA256: strings.Repeat("e", 64),
	}
	for _, count := range []uint32{32, 64} {
		index := len(evidence.Samples)
		n := uint64(count)
		processDigest := sha256.Sum256([]byte(fmt.Sprintf("process-%d", index)))
		sample := evidence.Samples[0]
		sample.SampleIndex = uint32(index)
		sample.RequestedSlots = count
		sample.RuntimeShards = (count + 3) / 4
		sample.ProcessInstanceSHA256 = hex.EncodeToString(processDigest[:])
		sample.ObservedAtUnixNS = metric(MetricTimestampObserved, uint64(index+1))
		sample.Pool = PoolState{TargetCapacity: count, Ready: count, AccountedSlots: count}
		sample.Phases.QueueNS = metric(MetricMeasured, n)
		sample.Phases.InstantiateNS = metric(MetricMeasured, n)
		sample.Phases.InitializeNS = metric(MetricMeasured, n)
		sample.Phases.RuntimeInitNS = metric(MetricMeasured, n)
		sample.Phases.PrepareNS = metric(MetricMeasured, n)
		sample.Phases.TotalNS = metric(MetricMeasured, n)
		sample.GoRuntime.HeapLiveBytes = metric(MetricMeasured, 100*n)
		sample.GoRuntime.HeapGoalBytes = metric(MetricMeasured, 200*n)
		sample.GoRuntime.GCCyclesTotal = metric(MetricMeasured, n)
		sample.GoRuntime.GCPauseTotalNS = metric(MetricMeasured, 10*n)
		sample.GoRuntime.Goroutines = metric(MetricMeasured, n)
		sample.GoRuntime.SchedulerLatency = Histogram{Status: MetricMeasured, UpperBoundsNS: []uint64{1_000, 10_000}, Counts: []uint64{n, n}}
		sample.Process = ProcessMetrics{
			RSSBytes: metric(MetricMeasured, 1_000*n), VirtualBytes: metric(MetricMeasured, 2_000*n),
			PSSBytes: metric(MetricMeasured, 900*n), PrivateCleanBytes: metric(MetricMeasured, 100*n),
			PrivateDirtyBytes: metric(MetricMeasured, 800*n), SwapBytes: metric(MetricMeasured, 0),
			MinorFaults: metric(MetricMeasured, 10*n), MajorFaults: metric(MetricMeasured, 0),
			FDCount: metric(MetricMeasured, 5+n), VMACount: metric(MetricMeasured, 20+n),
		}
		evidence.Samples = append(evidence.Samples, sample)
		evidence.Plan.SlotCounts = append(evidence.Plan.SlotCounts, count)
	}
	for index := range evidence.Samples {
		warmup := metric(MetricMeasured, uint64(index+1))
		evidence.Samples[index].Phases.WarmupNS = &warmup
	}
	evidence.Summary.SampleCount = len(evidence.Samples)
	evidence.Summary.PeakProcessRSSBytes = metric(MetricMeasured, 64_000)
	evidence.Summary.PeakGoHeapLiveBytes = metric(MetricMeasured, 6_400)
	return evidence, artifact
}

func TestLifecycleDensityV2AcceptsNumPyReadyWarmupBinding(t *testing.T) {
	evidence, artifact := validNumPyLifecycleDensityEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := evidence.ValidateArtifactBytes(artifact); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if err := compileLifecycleDensitySchema(t).Validate(canonical); err != nil {
		t.Fatalf("schema v2 NumPy-ready evidence rejected: %v", err)
	}
}

func validNumPyLifecycleDensityV3BoundaryEvidence() (LifecycleDensityEvidence, []byte) {
	evidence, artifact := validNumPyLifecycleDensityEvidence()
	evidence.SchemaVersion = 3
	for index := range evidence.Samples {
		evidence.Samples[index].RuntimeShards = (evidence.Samples[index].RequestedSlots + 3) / 4
	}
	last := evidence.Samples[len(evidence.Samples)-1]
	evidence.Samples = evidence.Samples[:len(evidence.Samples)-1]
	evidence.Boundaries = []LifecycleDensityBoundary{{
		SampleIndex: last.SampleIndex, RepeatIndex: last.RepeatIndex, RequestedSlots: last.RequestedSlots,
		ProcessInstanceSHA256: last.ProcessInstanceSHA256, Status: "rss_guard",
		MaxObservedRSSBytes: evidence.Plan.MaxProcessRSSBytes + 1, GuardRSSBytes: evidence.Plan.MaxProcessRSSBytes,
	}}
	evidence.Summary.SampleCount = len(evidence.Samples)
	evidence.Summary.BoundaryCount = len(evidence.Boundaries)
	evidence.Summary.PeakProcessRSSBytes = metric(MetricMeasured, 32_000)
	evidence.Summary.PeakGoHeapLiveBytes = metric(MetricMeasured, 3_200)
	return evidence, artifact
}

func TestLifecycleDensityV3AcceptsNonCOWRSSGuardBoundaryAt64(t *testing.T) {
	evidence, artifact := validNumPyLifecycleDensityV3BoundaryEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatalf("valid v3 boundary evidence rejected: %v", err)
	}
	if err := evidence.ValidateArtifactBytes(artifact); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if err := compileLifecycleDensitySchema(t).Validate(canonical); err != nil {
		t.Fatalf("schema v3 boundary evidence rejected: %v", err)
	}
}

func TestLifecycleDensityV3RejectsOversizedSampleIndexWithoutPanic(t *testing.T) {
	evidence, _ := validNumPyLifecycleDensityV3BoundaryEvidence()
	evidence.Samples[0].SampleIndex = ^uint32(0)
	if err := evidence.Validate(); err == nil {
		t.Fatal("oversized sample index was accepted")
	}
}

func TestLifecycleDensityV3RejectsInvalidBoundaryOutcomes(t *testing.T) {
	base, _ := validNumPyLifecycleDensityV3BoundaryEvidence()
	for name, mutate := range map[string]func(*LifecycleDensityEvidence){
		"boundary below 64": func(value *LifecycleDensityEvidence) {
			value.Boundaries[0].SampleIndex = 5
			value.Boundaries[0].RequestedSlots = 32
		},
		"COW boundary": func(value *LifecycleDensityEvidence) {
			value.Strategy = StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use"}
		},
		"peak at guard": func(value *LifecycleDensityEvidence) {
			value.Boundaries[0].MaxObservedRSSBytes = value.Plan.MaxProcessRSSBytes
		},
		"wrong guard":  func(value *LifecycleDensityEvidence) { value.Boundaries[0].GuardRSSBytes++ },
		"wrong status": func(value *LifecycleDensityEvidence) { value.Boundaries[0].Status = "oom" },
		"duplicate outcome": func(value *LifecycleDensityEvidence) {
			value.Samples = append(value.Samples, value.Samples[len(value.Samples)-1])
			value.Samples[len(value.Samples)-1].SampleIndex = value.Boundaries[0].SampleIndex
			value.Samples[len(value.Samples)-1].RequestedSlots = 64
			value.Summary.SampleCount++
		},
		"missing outcome": func(value *LifecycleDensityEvidence) {
			value.Boundaries = nil
			value.Summary.BoundaryCount = 0
		},
		"v2 boundary": func(value *LifecycleDensityEvidence) { value.SchemaVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := deepCopy(t, base)
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidLifecycleDensityEvidence) {
				t.Fatalf("invalid boundary evidence accepted: %v", err)
			}
		})
	}
}

func TestLifecycleDensityV2RejectsWarmupIdentityDrift(t *testing.T) {
	base, _ := validNumPyLifecycleDensityEvidence()
	for name, mutate := range map[string]func(*LifecycleDensityEvidence){
		"missing warmup": func(value *LifecycleDensityEvidence) { value.Warmup = nil },
		"wrong profile":  func(value *LifecycleDensityEvidence) { value.Warmup.Profile = "request-shell-v1" },
		"bad generation": func(value *LifecycleDensityEvidence) { value.Warmup.GenerationSHA256 = "bad" },
		"base artifact":  func(value *LifecycleDensityEvidence) { value.Artifact.ArtifactProfile = "base" },
		"wrong workload": func(value *LifecycleDensityEvidence) { value.Plan.Workload = "idle-ready" },
		"incomplete canonical sweep": func(value *LifecycleDensityEvidence) {
			value.Plan.SlotCounts = value.Plan.SlotCounts[:len(value.Plan.SlotCounts)-1]
		},
		"fresh strategy": func(value *LifecycleDensityEvidence) {
			value.Strategy = StrategyIdentity{Requested: "fresh-instance", Active: "fresh-instance"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			copyWarmup := *base.Warmup
			value.Warmup = &copyWarmup
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid NumPy-ready lifecycle density evidence was accepted")
			}
		})
	}
}

func TestValidateLifecycleDensityJSONRejectsDuplicateKeys(t *testing.T) {
	evidence, _ := validNumPyLifecycleDensityEvidence()
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	duplicated := bytes.Replace(encoded, []byte(`"schema_version":2`), []byte(`"schema_version":2,"schema_version":2`), 1)
	if err := ValidateLifecycleDensityJSON(duplicated); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate JSON key was accepted: %v", err)
	}
}

func TestLifecycleDensityEvidenceAcceptsExplicitSharedCompilationCacheStrategy(t *testing.T) {
	evidence, _ := validLifecycleDensityEvidence()
	evidence.Strategy.Requested = "single-use-preinitialized-shared-cache"
	evidence.Strategy.Active = "single-use-preinitialized-shared-cache"
	evidence.Limitations = append(evidence.Limitations,
		"The first shard populates one borrowed cache and each shard retains a separate wazero runtime.",
		"This experimental strategy does not approve production use.",
	)
	for index := range evidence.Samples {
		compile := metric(MetricMeasured, uint64(index+1))
		evidence.Samples[index].Phases.CompileNS = &compile
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("shared compilation cache strategy rejected: %v", err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if err := compileLifecycleDensitySchema(t).Validate(canonical); err != nil {
		t.Fatalf("shared compilation cache evidence rejected by schema: %v", err)
	}
}

func TestLifecycleDensityEvidenceRejectsSharedCacheWithoutCompileEvidence(t *testing.T) {
	evidence, _ := validLifecycleDensityEvidence()
	evidence.Strategy.Requested = "single-use-preinitialized-shared-cache"
	evidence.Strategy.Active = "single-use-preinitialized-shared-cache"
	evidence.Limitations = append(evidence.Limitations,
		"The first shard populates one borrowed cache and each shard retains a separate wazero runtime.",
		"This experimental strategy does not approve production use.",
	)
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "compile") {
		t.Fatalf("shared cache without compile evidence was accepted: %v", err)
	}
}

func TestLifecycleDensityEvidenceRejectsSharedCacheWithoutExperimentalLimitations(t *testing.T) {
	evidence, _ := validLifecycleDensityEvidence()
	evidence.Strategy.Requested = "single-use-preinitialized-shared-cache"
	evidence.Strategy.Active = "single-use-preinitialized-shared-cache"
	for index := range evidence.Samples {
		compile := metric(MetricMeasured, uint64(index+1))
		evidence.Samples[index].Phases.CompileNS = &compile
	}
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "limitation") {
		t.Fatalf("shared cache without experimental limitations was accepted: %v", err)
	}
}

func TestLifecycleDensityEvidenceRejectsDirtyFallbackMismatchAndFabricatedDerivedValues(t *testing.T) {
	base, artifactBytes := validLifecycleDensityEvidence()
	cases := map[string]func(*LifecycleDensityEvidence){
		"dirty Host": func(value *LifecycleDensityEvidence) { value.HostSource.Modified = true },
		"strategy fallback": func(value *LifecycleDensityEvidence) {
			value.Strategy.Active = "fresh-instance"
			value.Strategy.Fallback = true
		},
		"sample count":          func(value *LifecycleDensityEvidence) { value.Summary.SampleCount-- },
		"missing canonical N":   func(value *LifecycleDensityEvidence) { value.Plan.SlotCounts = []uint32{1, 2, 4, 8} },
		"sample distribution":   func(value *LifecycleDensityEvidence) { value.Samples[4].RequestedSlots = 8 },
		"missing runtime shard": func(value *LifecycleDensityEvidence) { value.Samples[0].RuntimeShards = 0 },
		"missing RSS guard":     func(value *LifecycleDensityEvidence) { value.Plan.MaxProcessRSSBytes = 0 },
		"missing child timeout": func(value *LifecycleDensityEvidence) { value.Plan.ChildTimeoutNS = 0 },
		"reused process instance": func(value *LifecycleDensityEvidence) {
			value.Samples[1].ProcessInstanceSHA256 = value.Samples[0].ProcessInstanceSHA256
		},
		"pool accounting": func(value *LifecycleDensityEvidence) { value.Samples[0].Pool.AccountedSlots = 2 },
		"cgroup identity drift": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Cgroup.Version = "none"
		},
		"shared cgroup reason mismatch": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Cgroup.Scope = "shared"
		},
		"process-dedicated cgroup claim in v1": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Cgroup.Scope = "process-dedicated"
		},
		"mixed metric availability": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Process.RSSBytes = unavailable(MetricUnsupported, ReasonPlatformUnsupported)
		},
		"pool counter overflow": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Pool.TargetCapacity = 64
			value.Samples[0].Pool.Initializing = ^uint32(0)
			value.Samples[0].Pool.Ready = 2
			value.Samples[0].Pool.AccountedSlots = 1
		},
		"fabricated RSS peak": func(value *LifecycleDensityEvidence) { *value.Summary.PeakProcessRSSBytes.Value++ },
		"model estimate in raw metric": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Process.RSSBytes = Metric{Status: MetricModelEstimated, Value: ptr(uint64(1000)), Model: "fixed_plus_per_slot"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := deepCopy(t, base)
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidLifecycleDensityEvidence) {
				t.Fatalf("expected invalid evidence, got %v", err)
			}
		})
	}
	if err := base.ValidateArtifactBytes(append(artifactBytes, 'x')); !errors.Is(err, ErrArtifactIdentityMismatch) {
		t.Fatalf("tampered artifact was not rejected: %v", err)
	}
}

func TestMetricStatusCarriesValueModelOrReasonExclusively(t *testing.T) {
	valid := []Metric{
		metric(MetricMeasured, 0),
		metric(MetricTimestampObserved, 1),
		{Status: MetricModelEstimated, Value: ptr(uint64(2)), Model: "fixed_plus_per_slot"},
		unavailable(MetricUnsupported, ReasonPlatformUnsupported),
		unavailable(MetricSkipped, ReasonSafetyGuard),
	}
	for _, candidate := range valid {
		if err := candidate.Validate(); err != nil {
			t.Fatalf("valid metric %#v rejected: %v", candidate, err)
		}
	}
	invalid := []Metric{
		{},
		{Status: MetricMeasured},
		{Status: MetricMeasured, Value: ptr(uint64(1)), ReasonCode: ReasonNotApplicable},
		{Status: MetricModelEstimated, Value: ptr(uint64(1))},
		{Status: MetricUnsupported, Value: ptr(uint64(1)), ReasonCode: ReasonPlatformUnsupported},
		{Status: MetricSkipped, ReasonCode: UnavailableReason("later")},
	}
	for _, candidate := range invalid {
		if err := candidate.Validate(); err == nil {
			t.Fatalf("invalid metric %#v accepted", candidate)
		}
	}
}

func TestLifecycleDensityJSONSchemaAcceptsCanonicalAndRejectsInvalidClaims(t *testing.T) {
	evidence, _ := validLifecycleDensityEvidence()
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	schema := compileLifecycleDensitySchema(t)
	if err := schema.Validate(canonical); err != nil {
		t.Fatalf("canonical evidence rejected: %v", err)
	}
	cases := map[string]func(map[string]any){
		"fallback": func(object map[string]any) {
			object["strategy"].(map[string]any)["fallback"] = true
		},
		"dirty Host": func(object map[string]any) {
			object["host_source"].(map[string]any)["modified"] = true
		},
		"raw model estimate": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["process"].(map[string]any)["rss_bytes"] = map[string]any{
				"status": "model_estimated", "value": 1, "model": "fixed_plus_per_slot",
			}
		},
		"unavailable value": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["process"].(map[string]any)["rss_bytes"] = map[string]any{
				"status": "unsupported", "reason_code": "platform_unsupported", "value": 0,
			}
		},
		"noncanonical slot plan": func(object map[string]any) {
			object["plan"].(map[string]any)["slot_counts"] = []any{float64(1), float64(2), float64(4), float64(8)}
		},
		"unknown authority field": func(object map[string]any) {
			object["credentials"] = map[string]any{"token": "forbidden"}
		},
		"runtime shards exceed requested slots": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["runtime_shards"] = float64(2)
		},
		"process-dedicated cgroup claim": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["cgroup"].(map[string]any)["scope"] = "process-dedicated"
		},
		"legacy baseline boolean": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["cgroup"].(map[string]any)["cumulative_baseline"] = true
		},
		"unverified measured cgroup": func(object map[string]any) {
			object["samples"].([]any)[0].(map[string]any)["cgroup"].(map[string]any)["memory_current_bytes"] = map[string]any{"status": "measured", "value": float64(1)}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var candidate map[string]any
			if err := json.Unmarshal(encoded, &candidate); err != nil {
				t.Fatal(err)
			}
			mutate(candidate)
			if err := schema.Validate(candidate); err == nil {
				t.Fatal("schema accepted an invalid lifecycle-density claim")
			}
		})
	}
}

func TestValidateLifecycleDensityJSONRejectsCrossFieldAndHistogramDrift(t *testing.T) {
	base, _ := validLifecycleDensityEvidence()
	canonical, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLifecycleDensityJSON(canonical); err != nil {
		t.Fatalf("canonical semantic JSON was rejected: %v", err)
	}
	if err := ValidateLifecycleDensityJSON(append(canonical, []byte(" {}")...)); !errors.Is(err, ErrInvalidLifecycleDensityEvidence) {
		t.Fatalf("trailing JSON was accepted: %v", err)
	}
	var unknown map[string]any
	if err := json.Unmarshal(canonical, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["credentials"] = "forbidden"
	encodedUnknown, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLifecycleDensityJSON(encodedUnknown); !errors.Is(err, ErrInvalidLifecycleDensityEvidence) {
		t.Fatalf("unknown field was accepted: %v", err)
	}
	cases := map[string]func(*LifecycleDensityEvidence){
		"runtime shards exceed requested slots": func(value *LifecycleDensityEvidence) {
			value.Samples[0].RuntimeShards = 2
		},
		"histogram lengths differ": func(value *LifecycleDensityEvidence) {
			value.Samples[0].GoRuntime.SchedulerLatency.Counts = []uint64{1}
		},
		"sample order drifts": func(value *LifecycleDensityEvidence) {
			value.Samples[0].SampleIndex = 1
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := deepCopy(t, base)
			mutate(&candidate)
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateLifecycleDensityJSON(encoded); !errors.Is(err, ErrInvalidLifecycleDensityEvidence) {
				t.Fatalf("semantic JSON validator accepted drift: %v", err)
			}
		})
	}
}

func ptr[T any](value T) *T { return &value }

func deepCopy(t *testing.T, value LifecycleDensityEvidence) LifecycleDensityEvidence {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy LifecycleDensityEvidence
	if err := json.Unmarshal(encoded, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func compileLifecycleDensitySchema(t *testing.T, relativePath ...string) *jsonschema.Schema {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test path")
	}
	selected := "../../benchmark/v1/lifecycle-density.schema.json"
	if len(relativePath) > 1 {
		t.Fatal("at most one schema path may be selected")
	}
	if len(relativePath) == 1 {
		selected = relativePath[0]
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), selected))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	schemaURL := "file://" + path
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
