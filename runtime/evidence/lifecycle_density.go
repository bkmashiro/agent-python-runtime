package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidLifecycleDensityEvidence = errors.New("invalid lifecycle-density evidence")
	ErrArtifactIdentityMismatch        = errors.New("artifact identity mismatch")
)

type MetricStatus string

const (
	MetricMeasured          MetricStatus = "measured"
	MetricTimestampObserved MetricStatus = "timestamp_observed"
	MetricModelEstimated    MetricStatus = "model_estimated"
	MetricUnsupported       MetricStatus = "unsupported"
	MetricSkipped           MetricStatus = "skipped"
)

type UnavailableReason string

const (
	ReasonSourceUnavailable   UnavailableReason = "source_unavailable"
	ReasonPermissionDenied    UnavailableReason = "permission_denied"
	ReasonNotApplicable       UnavailableReason = "not_applicable"
	ReasonPlatformUnsupported UnavailableReason = "platform_unsupported"
	ReasonCollectionError     UnavailableReason = "collection_error"
	ReasonSafetyGuard         UnavailableReason = "safety_guard"
	ReasonWorkloadNotRun      UnavailableReason = "workload_not_run"
	ReasonNonisolatedScope    UnavailableReason = "nonisolated_scope"
	ReasonIsolationUnproven   UnavailableReason = "isolation_unproven"
)

type Metric struct {
	Status     MetricStatus      `json:"status"`
	Value      *uint64           `json:"value,omitempty"`
	ReasonCode UnavailableReason `json:"reason_code,omitempty"`
	Model      string            `json:"model,omitempty"`
}

func (metric Metric) Validate() error {
	switch metric.Status {
	case MetricMeasured, MetricTimestampObserved:
		if metric.Value == nil || metric.ReasonCode != "" || metric.Model != "" {
			return errors.New("measured/timestamp metric requires only a value")
		}
	case MetricModelEstimated:
		if metric.Value == nil || metric.ReasonCode != "" || !validModel(metric.Model) {
			return errors.New("model estimate requires a value and known model")
		}
	case MetricUnsupported, MetricSkipped:
		if metric.Value != nil || metric.Model != "" || !validUnavailableReason(metric.ReasonCode) {
			return errors.New("unavailable metric requires only a known reason")
		}
	default:
		return errors.New("unknown metric status")
	}
	return nil
}

func validModel(model string) bool {
	return model == "fixed_plus_per_slot" || model == "segmented_linear"
}

func validUnavailableReason(reason UnavailableReason) bool {
	switch reason {
	case ReasonSourceUnavailable, ReasonPermissionDenied, ReasonNotApplicable, ReasonPlatformUnsupported,
		ReasonCollectionError, ReasonSafetyGuard, ReasonWorkloadNotRun, ReasonNonisolatedScope, ReasonIsolationUnproven:
		return true
	default:
		return false
	}
}

type Histogram struct {
	Status        MetricStatus      `json:"status"`
	UpperBoundsNS []uint64          `json:"upper_bounds_ns,omitempty"`
	Counts        []uint64          `json:"counts,omitempty"`
	ReasonCode    UnavailableReason `json:"reason_code,omitempty"`
}

func (histogram Histogram) Validate() error {
	switch histogram.Status {
	case MetricMeasured:
		if histogram.ReasonCode != "" || len(histogram.UpperBoundsNS) == 0 || len(histogram.UpperBoundsNS) != len(histogram.Counts) {
			return errors.New("measured histogram requires matching nonempty bounds and counts")
		}
		for index := 1; index < len(histogram.UpperBoundsNS); index++ {
			if histogram.UpperBoundsNS[index] <= histogram.UpperBoundsNS[index-1] {
				return errors.New("histogram bounds must be strictly increasing")
			}
		}
	case MetricUnsupported, MetricSkipped:
		if len(histogram.UpperBoundsNS) != 0 || len(histogram.Counts) != 0 || !validUnavailableReason(histogram.ReasonCode) {
			return errors.New("unavailable histogram requires only a known reason")
		}
	default:
		return errors.New("histogram status must be measured, unsupported, or skipped")
	}
	return nil
}

type ArtifactIdentity struct {
	Filename        string `json:"filename"`
	SHA256          string `json:"sha256"`
	SizeBytes       uint64 `json:"size_bytes"`
	SourceCommit    string `json:"source_commit"`
	ArtifactProfile string `json:"artifact_profile"`
	Target          string `json:"target"`
	ExecutionModel  string `json:"execution_model"`
}

type HostSourceIdentity struct {
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
}

type BackendIdentity struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	ResetMode string `json:"reset_mode"`
}

type EnvironmentIdentity struct {
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	GoVersion     string `json:"go_version"`
	KernelRelease string `json:"kernel_release"`
	PageSizeBytes uint64 `json:"page_size_bytes"`
	CgroupVersion string `json:"cgroup_version"`
}

type StrategyIdentity struct {
	Requested string `json:"requested"`
	Active    string `json:"active"`
	Fallback  bool   `json:"fallback"`
}

type SweepPlan struct {
	Workload              string   `json:"workload"`
	SlotCounts            []uint32 `json:"slot_counts"`
	RepeatsPerSlot        uint32   `json:"repeats_per_slot"`
	FreshProcessPerSample bool     `json:"fresh_process_per_sample"`
	MaxProcessRSSBytes    uint64   `json:"max_process_rss_bytes"`
	ChildTimeoutNS        uint64   `json:"child_timeout_ns"`
}

type PoolState struct {
	TargetCapacity uint32 `json:"target_capacity"`
	Initializing   uint32 `json:"initializing"`
	Ready          uint32 `json:"ready"`
	Leased         uint32 `json:"leased"`
	Unhealthy      uint32 `json:"unhealthy"`
	Retiring       uint32 `json:"retiring"`
	AccountedSlots uint32 `json:"accounted_slots"`
}

type PhaseTimings struct {
	QueueNS       Metric  `json:"queue_ns"`
	CompileNS     *Metric `json:"compile_ns,omitempty"`
	InstantiateNS Metric  `json:"instantiate_ns"`
	InitializeNS  Metric  `json:"initialize_ns"`
	RuntimeInitNS Metric  `json:"runtime_init_ns"`
	PrepareNS     Metric  `json:"prepare_ns"`
	ExecuteNS     Metric  `json:"execute_ns"`
	CapabilityNS  Metric  `json:"capability_ns"`
	TotalNS       Metric  `json:"total_ns"`
}

type GoRuntimeMetrics struct {
	HeapLiveBytes    Metric    `json:"heap_live_bytes"`
	HeapGoalBytes    Metric    `json:"heap_goal_bytes"`
	GCCyclesTotal    Metric    `json:"gc_cycles_total"`
	GCPauseTotalNS   Metric    `json:"gc_pause_total_ns"`
	Goroutines       Metric    `json:"goroutines"`
	SchedulerLatency Histogram `json:"scheduler_latency"`
}

type ProcessMetrics struct {
	RSSBytes          Metric `json:"rss_bytes"`
	VirtualBytes      Metric `json:"virtual_bytes"`
	PSSBytes          Metric `json:"pss_bytes"`
	PrivateCleanBytes Metric `json:"private_clean_bytes"`
	PrivateDirtyBytes Metric `json:"private_dirty_bytes"`
	SwapBytes         Metric `json:"swap_bytes"`
	MinorFaults       Metric `json:"minor_faults"`
	MajorFaults       Metric `json:"major_faults"`
	FDCount           Metric `json:"fd_count"`
	VMACount          Metric `json:"vma_count"`
}

type CgroupMetrics struct {
	Version                  string `json:"version"`
	Scope                    string `json:"scope"`
	MembershipSHA256         string `json:"membership_sha256,omitempty"`
	MemoryCurrentBytes       Metric `json:"memory_current_bytes"`
	MemoryPeakBytes          Metric `json:"memory_peak_bytes"`
	MemorySwapCurrentBytes   Metric `json:"memory_swap_current_bytes"`
	MemoryEventsHighTotal    Metric `json:"memory_events_high_total"`
	MemoryEventsOOMTotal     Metric `json:"memory_events_oom_total"`
	MemoryEventsOOMKillTotal Metric `json:"memory_events_oom_kill_total"`
	PressureSomeTotalUS      Metric `json:"pressure_some_total_us"`
	PressureFullTotalUS      Metric `json:"pressure_full_total_us"`
}

type LifecycleDensitySample struct {
	SampleIndex           uint32           `json:"sample_index"`
	RepeatIndex           uint32           `json:"repeat_index"`
	RequestedSlots        uint32           `json:"requested_slots"`
	RuntimeShards         uint32           `json:"runtime_shards"`
	ActiveConcurrency     uint32           `json:"active_concurrency"`
	ProcessInstanceSHA256 string           `json:"process_instance_sha256"`
	ObservedAtUnixNS      Metric           `json:"observed_at_unix_ns"`
	Pool                  PoolState        `json:"pool"`
	Phases                PhaseTimings     `json:"phases"`
	GoRuntime             GoRuntimeMetrics `json:"go_runtime"`
	Process               ProcessMetrics   `json:"process"`
	Cgroup                CgroupMetrics    `json:"cgroup"`
}

type DerivedSummary struct {
	SampleCount                  int     `json:"sample_count"`
	PeakProcessRSSBytes          Metric  `json:"peak_process_rss_bytes"`
	PeakCgroupMemoryCurrentBytes Metric  `json:"peak_cgroup_memory_current_bytes"`
	PeakGoHeapLiveBytes          Metric  `json:"peak_go_heap_live_bytes"`
	EstimatedFixedBytes          *Metric `json:"estimated_fixed_bytes,omitempty"`
	EstimatedPerSlotBytes        *Metric `json:"estimated_per_slot_bytes,omitempty"`
}

type LifecycleDensityEvidence struct {
	SchemaVersion int                      `json:"schema_version"`
	EvidenceClass string                   `json:"evidence_class"`
	Artifact      ArtifactIdentity         `json:"artifact"`
	HostSource    HostSourceIdentity       `json:"host_source"`
	Backend       BackendIdentity          `json:"backend"`
	Environment   EnvironmentIdentity      `json:"environment"`
	Strategy      StrategyIdentity         `json:"strategy"`
	Plan          SweepPlan                `json:"plan"`
	Samples       []LifecycleDensitySample `json:"samples"`
	Summary       DerivedSummary           `json:"summary"`
	Limitations   []string                 `json:"limitations"`
}

func ValidateLifecycleDensityJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence LifecycleDensityEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidLifecycleDensityEvidence, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalidDensity("JSON contains trailing data")
	}
	return evidence.Validate()
}

func (evidence LifecycleDensityEvidence) Validate() error {
	if evidence.SchemaVersion != 1 || evidence.EvidenceClass != "lifecycle-density" {
		return invalidDensity("schema version or evidence class is unsupported")
	}
	if err := evidence.validateIdentity(); err != nil {
		return err
	}
	if err := evidence.validatePlan(); err != nil {
		return err
	}
	if len(evidence.Limitations) == 0 {
		return invalidDensity("at least one limitation is required")
	}
	for _, limitation := range evidence.Limitations {
		if strings.TrimSpace(limitation) == "" || limitation != strings.TrimSpace(limitation) {
			return invalidDensity("limitations must be nonempty and boundary-trimmed")
		}
	}
	expectedSamples := len(evidence.Plan.SlotCounts) * int(evidence.Plan.RepeatsPerSlot)
	if len(evidence.Samples) != expectedSamples || evidence.Summary.SampleCount != expectedSamples {
		return invalidDensity("sample count does not match the sweep plan")
	}
	processInstances := make(map[string]struct{}, len(evidence.Samples))
	for index := range evidence.Samples {
		expectedSlotIndex := index / int(evidence.Plan.RepeatsPerSlot)
		expectedRepeat := index % int(evidence.Plan.RepeatsPerSlot)
		sample := evidence.Samples[index]
		if sample.SampleIndex != uint32(index) || sample.RepeatIndex != uint32(expectedRepeat) ||
			sample.RequestedSlots != evidence.Plan.SlotCounts[expectedSlotIndex] {
			return invalidDensity("sample order or slot/repeat identity drifted")
		}
		if !lowerHex(sample.ProcessInstanceSHA256, 64) {
			return invalidDensity("sample process instance identity is missing")
		}
		if _, duplicate := processInstances[sample.ProcessInstanceSHA256]; duplicate {
			return invalidDensity("sample process instance identity was reused")
		}
		processInstances[sample.ProcessInstanceSHA256] = struct{}{}
		if err := evidence.validateSample(sample); err != nil {
			return fmt.Errorf("%w: sample %d: %v", ErrInvalidLifecycleDensityEvidence, index, err)
		}
	}
	if err := validateDerivedPeak("process RSS", evidence.Summary.PeakProcessRSSBytes, evidence.Samples, func(sample LifecycleDensitySample) Metric { return sample.Process.RSSBytes }); err != nil {
		return err
	}
	if err := validateDerivedPeak("cgroup memory.current", evidence.Summary.PeakCgroupMemoryCurrentBytes, evidence.Samples, func(sample LifecycleDensitySample) Metric { return sample.Cgroup.MemoryCurrentBytes }); err != nil {
		return err
	}
	if err := validateDerivedPeak("Go heap live", evidence.Summary.PeakGoHeapLiveBytes, evidence.Samples, func(sample LifecycleDensitySample) Metric { return sample.GoRuntime.HeapLiveBytes }); err != nil {
		return err
	}
	if (evidence.Summary.EstimatedFixedBytes == nil) != (evidence.Summary.EstimatedPerSlotBytes == nil) {
		return invalidDensity("capacity estimates must be present as a pair")
	}
	if evidence.Summary.EstimatedFixedBytes != nil {
		if err := validateModelMetric(*evidence.Summary.EstimatedFixedBytes); err != nil {
			return invalidDensity("invalid fixed-byte estimate")
		}
		if err := validateModelMetric(*evidence.Summary.EstimatedPerSlotBytes); err != nil {
			return invalidDensity("invalid per-slot estimate")
		}
		if evidence.Summary.EstimatedFixedBytes.Model != evidence.Summary.EstimatedPerSlotBytes.Model {
			return invalidDensity("capacity estimate models differ")
		}
	}
	return nil
}

func (evidence LifecycleDensityEvidence) ValidateArtifactBytes(artifact []byte) error {
	digest := sha256.Sum256(artifact)
	if uint64(len(artifact)) != evidence.Artifact.SizeBytes || hex.EncodeToString(digest[:]) != evidence.Artifact.SHA256 {
		return ErrArtifactIdentityMismatch
	}
	return nil
}

func (evidence LifecycleDensityEvidence) validateIdentity() error {
	artifact := evidence.Artifact
	if artifact.Filename == "" || !lowerHex(artifact.SHA256, 64) || artifact.SizeBytes == 0 ||
		!lowerHex(artifact.SourceCommit, 40) || (artifact.ArtifactProfile != "base" && artifact.ArtifactProfile != "numpy-core") ||
		artifact.Target != "wasm32-wasip1" || artifact.ExecutionModel != "reactor" {
		return invalidDensity("artifact identity is incomplete or unsupported")
	}
	if !lowerHex(evidence.HostSource.Revision, 40) || evidence.HostSource.Modified {
		return invalidDensity("Host source must be an exact clean revision")
	}
	if evidence.Backend.Name == "" || evidence.Backend.Version == "" || evidence.Backend.ResetMode != "fresh-instance" {
		return invalidDensity("backend identity is incomplete or reset claim widened")
	}
	environment := evidence.Environment
	if environment.GOOS == "" || environment.GOARCH == "" || environment.GoVersion == "" || environment.KernelRelease == "" ||
		environment.PageSizeBytes == 0 || (environment.CgroupVersion != "v2" && environment.CgroupVersion != "none") {
		return invalidDensity("environment identity is incomplete")
	}
	if !validStrategy(evidence.Strategy.Requested) || evidence.Strategy.Active != evidence.Strategy.Requested || evidence.Strategy.Fallback {
		return invalidDensity("requested strategy was not proven active without fallback")
	}
	if evidence.Strategy.Active == "single-use-preinitialized-shared-cache" {
		cacheBoundary := false
		noProductionApproval := false
		for _, limitation := range evidence.Limitations {
			cacheBoundary = cacheBoundary || (strings.Contains(limitation, "first shard") && strings.Contains(limitation, "separate wazero runtime"))
			noProductionApproval = noProductionApproval || strings.Contains(limitation, "does not approve")
		}
		if !cacheBoundary || !noProductionApproval {
			return invalidDensity("shared compilation cache strategy lacks its ownership or no-production limitation")
		}
	}
	return nil
}

func (evidence LifecycleDensityEvidence) validatePlan() error {
	if evidence.Plan.Workload != "idle-ready" && evidence.Plan.Workload != "execute" && evidence.Plan.Workload != "capability" {
		return invalidDensity("unsupported workload")
	}
	canonical := []uint32{1, 2, 4, 8, 16, 32, 64}
	if len(evidence.Plan.SlotCounts) < 5 || len(evidence.Plan.SlotCounts) > len(canonical) {
		return invalidDensity("slot sweep must contain canonical 1..16 and only guarded 32/64 extensions")
	}
	for index, count := range evidence.Plan.SlotCounts {
		if count != canonical[index] {
			return invalidDensity("slot sweep is not canonical")
		}
	}
	if evidence.Plan.RepeatsPerSlot == 0 || evidence.Plan.RepeatsPerSlot > 1000 || !evidence.Plan.FreshProcessPerSample {
		return invalidDensity("repeat count or fresh-process isolation is invalid")
	}
	if evidence.Plan.MaxProcessRSSBytes == 0 || evidence.Plan.MaxProcessRSSBytes > 1<<50 ||
		evidence.Plan.ChildTimeoutNS == 0 || evidence.Plan.ChildTimeoutNS > 86_400_000_000_000 {
		return invalidDensity("child RSS or timeout guard is missing or outside its hard bound")
	}
	return nil
}

func (evidence LifecycleDensityEvidence) validateSample(sample LifecycleDensitySample) error {
	if sample.RuntimeShards == 0 || sample.RuntimeShards > sample.RequestedSlots {
		return errors.New("runtime shard count is outside the requested slot bound")
	}
	if sample.ActiveConcurrency > sample.RequestedSlots {
		return errors.New("active concurrency exceeds requested slots")
	}
	if err := validateMetricStatus(sample.ObservedAtUnixNS, MetricTimestampObserved); err != nil {
		return errors.New("observation timestamp is not timestamp-observed")
	}
	poolCounts := []uint32{
		sample.Pool.TargetCapacity,
		sample.Pool.Initializing,
		sample.Pool.Ready,
		sample.Pool.Leased,
		sample.Pool.Unhealthy,
		sample.Pool.Retiring,
		sample.Pool.AccountedSlots,
	}
	for _, count := range poolCounts {
		if count > 64 {
			return errors.New("pool state exceeds the hard slot bound")
		}
	}
	accounted := uint64(sample.Pool.Initializing) + uint64(sample.Pool.Ready) + uint64(sample.Pool.Leased) +
		uint64(sample.Pool.Unhealthy) + uint64(sample.Pool.Retiring)
	if uint64(sample.Pool.AccountedSlots) != accounted || accounted > uint64(sample.Pool.TargetCapacity) {
		return errors.New("pool state accounting is inconsistent")
	}
	for _, named := range []struct {
		name   string
		metric Metric
	}{
		{"queue", sample.Phases.QueueNS}, {"instantiate", sample.Phases.InstantiateNS},
		{"initialize", sample.Phases.InitializeNS}, {"runtime_init", sample.Phases.RuntimeInitNS},
		{"prepare", sample.Phases.PrepareNS}, {"execute", sample.Phases.ExecuteNS},
		{"capability", sample.Phases.CapabilityNS}, {"total", sample.Phases.TotalNS},
		{"Go heap live", sample.GoRuntime.HeapLiveBytes}, {"Go heap goal", sample.GoRuntime.HeapGoalBytes},
		{"Go GC cycles", sample.GoRuntime.GCCyclesTotal}, {"Go GC pause", sample.GoRuntime.GCPauseTotalNS},
		{"Go goroutines", sample.GoRuntime.Goroutines},
		{"process RSS", sample.Process.RSSBytes}, {"process virtual", sample.Process.VirtualBytes},
		{"process PSS", sample.Process.PSSBytes}, {"process private clean", sample.Process.PrivateCleanBytes},
		{"process private dirty", sample.Process.PrivateDirtyBytes}, {"process swap", sample.Process.SwapBytes},
		{"minor faults", sample.Process.MinorFaults}, {"major faults", sample.Process.MajorFaults},
		{"FD count", sample.Process.FDCount}, {"VMA count", sample.Process.VMACount},
		{"cgroup current", sample.Cgroup.MemoryCurrentBytes}, {"cgroup peak", sample.Cgroup.MemoryPeakBytes},
		{"cgroup swap", sample.Cgroup.MemorySwapCurrentBytes}, {"cgroup high events", sample.Cgroup.MemoryEventsHighTotal},
		{"cgroup OOM events", sample.Cgroup.MemoryEventsOOMTotal}, {"cgroup OOM kill events", sample.Cgroup.MemoryEventsOOMKillTotal},
		{"pressure some", sample.Cgroup.PressureSomeTotalUS}, {"pressure full", sample.Cgroup.PressureFullTotalUS},
	} {
		if err := validateRawMetric(named.metric); err != nil {
			return fmt.Errorf("%s metric: %w", named.name, err)
		}
	}
	if sample.Phases.CompileNS != nil {
		if err := validateRawMetric(*sample.Phases.CompileNS); err != nil {
			return fmt.Errorf("compile metric: %w", err)
		}
	}
	if evidence.Strategy.Active == "single-use-preinitialized-shared-cache" &&
		(sample.Phases.CompileNS == nil || sample.Phases.CompileNS.Status != MetricMeasured ||
			sample.Phases.CompileNS.Value == nil || *sample.Phases.CompileNS.Value == 0) {
		return errors.New("shared compilation cache strategy requires positive measured compile evidence")
	}
	if err := sample.GoRuntime.SchedulerLatency.Validate(); err != nil {
		return fmt.Errorf("scheduler latency: %w", err)
	}
	if sample.Cgroup.Version != evidence.Environment.CgroupVersion {
		return errors.New("sample cgroup version differs from environment identity")
	}
	allCgroupMetrics := []Metric{
		sample.Cgroup.MemoryCurrentBytes, sample.Cgroup.MemoryPeakBytes, sample.Cgroup.MemorySwapCurrentBytes,
		sample.Cgroup.MemoryEventsHighTotal, sample.Cgroup.MemoryEventsOOMTotal, sample.Cgroup.MemoryEventsOOMKillTotal,
		sample.Cgroup.PressureSomeTotalUS, sample.Cgroup.PressureFullTotalUS,
	}
	if sample.Cgroup.Version == "none" {
		if sample.Cgroup.Scope != "unverified" || sample.Cgroup.MembershipSHA256 != "" {
			return errors.New("cgroup=none carries scoped identity")
		}
		for _, metric := range allCgroupMetrics {
			if metric.Status != MetricUnsupported || metric.ReasonCode != ReasonNotApplicable {
				return errors.New("cgroup=none metrics must be unsupported as not applicable")
			}
		}
		return nil
	}
	if !lowerHex(sample.Cgroup.MembershipSHA256, 64) {
		return errors.New("cgroup v2 membership identity is missing")
	}
	var expectedReason UnavailableReason
	switch sample.Cgroup.Scope {
	case "shared":
		expectedReason = ReasonNonisolatedScope
	case "unverified":
		expectedReason = ReasonIsolationUnproven
	default:
		return errors.New("lifecycle-density v1 does not accept cgroup isolation claims")
	}
	for _, metric := range allCgroupMetrics {
		if metric.Status != MetricSkipped || metric.ReasonCode != expectedReason {
			return errors.New("cgroup v2 metrics lack the required fail-closed scope reason")
		}
	}
	return nil
}

func validateRawMetric(metric Metric) error {
	if err := metric.Validate(); err != nil {
		return err
	}
	if metric.Status == MetricTimestampObserved || metric.Status == MetricModelEstimated {
		return errors.New("raw metric cannot be timestamp-observed or model-estimated")
	}
	return nil
}

func validateModelMetric(metric Metric) error {
	if err := metric.Validate(); err != nil {
		return err
	}
	if metric.Status != MetricModelEstimated {
		return errors.New("capacity estimate must be model-estimated")
	}
	return nil
}

func validateMetricStatus(metric Metric, status MetricStatus) error {
	if err := metric.Validate(); err != nil {
		return err
	}
	if metric.Status != status {
		return errors.New("unexpected metric status")
	}
	return nil
}

func validateDerivedPeak(name string, summary Metric, samples []LifecycleDensitySample, selectMetric func(LifecycleDensitySample) Metric) error {
	if err := summary.Validate(); err != nil {
		return invalidDensity(name + " summary is invalid")
	}
	allMeasured := true
	var peak uint64
	var unavailableStatus MetricStatus
	var unavailableReason UnavailableReason
	for index, sample := range samples {
		metric := selectMetric(sample)
		if metric.Status == MetricMeasured {
			if !allMeasured {
				return invalidDensity(name + " availability differs across samples")
			}
			if *metric.Value > peak {
				peak = *metric.Value
			}
			continue
		}
		if index > 0 && (allMeasured || metric.Status != unavailableStatus || metric.ReasonCode != unavailableReason) {
			return invalidDensity(name + " availability differs across samples")
		}
		allMeasured = false
		unavailableStatus = metric.Status
		unavailableReason = metric.ReasonCode
	}
	if allMeasured {
		if summary.Status != MetricMeasured || summary.Value == nil || *summary.Value != peak {
			return invalidDensity(name + " derived peak does not match raw samples")
		}
		return nil
	}
	if summary.Status != unavailableStatus || summary.ReasonCode != unavailableReason || summary.Value != nil {
		return invalidDensity(name + " unavailable summary differs from raw samples")
	}
	return nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validStrategy(strategy string) bool {
	return strategy == "fresh-instance" || strategy == "single-use-preinitialized" ||
		strategy == "single-use-preinitialized-shared-cache"
}

func invalidDensity(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidLifecycleDensityEvidence, message)
}
