package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
		samples = append(samples, LifecycleDensitySample{
			SampleIndex:       uint32(index),
			RepeatIndex:       0,
			RequestedSlots:    count,
			ActiveConcurrency: 0,
			ObservedAtUnixNS:  metric(MetricTimestampObserved, uint64(index+1)),
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
				MemoryCurrentBytes:       metric(MetricMeasured, 2_000*n),
				MemoryPeakBytes:          metric(MetricMeasured, 2_100*n),
				MemorySwapCurrentBytes:   metric(MetricMeasured, 0),
				MemoryEventsHighTotal:    metric(MetricMeasured, 0),
				MemoryEventsOOMTotal:     metric(MetricMeasured, 0),
				MemoryEventsOOMKillTotal: metric(MetricMeasured, 0),
				PressureSomeTotalUS:      metric(MetricMeasured, n),
				PressureFullTotalUS:      metric(MetricMeasured, 0),
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
		},
		Samples: samples,
		Summary: DerivedSummary{
			SampleCount:                  len(samples),
			PeakProcessRSSBytes:          metric(MetricMeasured, 16_000),
			PeakCgroupMemoryCurrentBytes: metric(MetricMeasured, 32_000),
			PeakGoHeapLiveBytes:          metric(MetricMeasured, 1_600),
		},
		Limitations: []string{"fixture evidence only"},
	}, artifactBytes
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

func TestLifecycleDensityEvidenceRejectsDirtyFallbackMismatchAndFabricatedDerivedValues(t *testing.T) {
	base, artifactBytes := validLifecycleDensityEvidence()
	cases := map[string]func(*LifecycleDensityEvidence){
		"dirty Host": func(value *LifecycleDensityEvidence) { value.HostSource.Modified = true },
		"strategy fallback": func(value *LifecycleDensityEvidence) {
			value.Strategy.Active = "fresh-instance"
			value.Strategy.Fallback = true
		},
		"sample count":        func(value *LifecycleDensityEvidence) { value.Summary.SampleCount-- },
		"missing canonical N": func(value *LifecycleDensityEvidence) { value.Plan.SlotCounts = []uint32{1, 2, 4, 8} },
		"sample distribution": func(value *LifecycleDensityEvidence) { value.Samples[4].RequestedSlots = 8 },
		"pool accounting":     func(value *LifecycleDensityEvidence) { value.Samples[0].Pool.AccountedSlots = 2 },
		"cgroup identity drift": func(value *LifecycleDensityEvidence) {
			value.Samples[0].Cgroup.Version = "none"
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

func compileLifecycleDensitySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../benchmark/v1/lifecycle-density.schema.json"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://github.com/bkmashiro/agent-python-runtime/benchmark/v1/lifecycle-density.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
