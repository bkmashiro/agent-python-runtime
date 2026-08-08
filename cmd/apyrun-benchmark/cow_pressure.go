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
	runtimescheduler "github.com/bkmashiro/agent-python-runtime/runtime/scheduler"
)

const pressureInitialCapacity uint32 = 4
const pressureMaxGrowthStep uint32 = 64
const pressureMinimumHeadroom = 512 * 1024 * 1024
const pressureActiveSampleCount = 3
const pressureAdaptiveRefillWorkers uint32 = 12
const pressureMaximumOfferedRequests uint64 = 1 << 20
const pressureMaximumLatencySamples = 250_000
const pressureRequestTimeout = 30 * time.Second

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
	DirtyBytes         uint64 `json:"dirty_bytes"`
	RefillPolicy       string `json:"refill_policy"`
	RefillWorkers      uint32 `json:"refill_workers"`
	BurstFactor        uint32 `json:"burst_factor"`
	WarmupProfile      string `json:"warmup_profile"`
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

type cowPressureRequestClass struct {
	Name      string `json:"name"`
	Started   uint64 `json:"started"`
	Completed uint64 `json:"completed"`
	Failed    uint64 `json:"failed"`
}

type cowPressureArrival struct {
	Mode             string `json:"mode"`
	WindowNS         uint64 `json:"window_ns"`
	RatePerSecond    uint32 `json:"rate_per_second"`
	QueueCapacity    uint32 `json:"queue_capacity"`
	OfferedRequests  uint64 `json:"offered_requests"`
	AcceptedRequests uint64 `json:"accepted_requests"`
	RejectedRequests uint64 `json:"rejected_requests"`
}

type cowPressureRequestSpec struct {
	Class            string
	Code             string
	Inputs           map[string]any
	Wait             time.Duration
	RequireNumPy     bool
	ExpectedPrepared int64
	ExpectedSum      int64
}

type cowPressureBurst struct {
	Factor                uint32  `json:"factor"`
	BaselineConsumers     uint32  `json:"baseline_consumers"`
	PeakConsumers         uint32  `json:"peak_consumers"`
	StartOffsetNS         uint64  `json:"start_offset_ns"`
	PreWindowDurationNS   uint64  `json:"pre_window_duration_ns"`
	BurstWindowDurationNS uint64  `json:"burst_window_duration_ns"`
	PreCompleted          uint64  `json:"pre_completed"`
	BurstCompleted        uint64  `json:"burst_completed"`
	PreThroughputPerSec   float64 `json:"pre_throughput_per_second"`
	BurstThroughputPerSec float64 `json:"burst_throughput_per_second"`
}

type cowPressureBurstWindow struct {
	preCompleted  uint64
	endCompleted  uint64
	preDuration   time.Duration
	burstDuration time.Duration
}

type cowPressureLoad struct {
	Arrival            cowPressureArrival        `json:"arrival"`
	ResultOracle       string                    `json:"result_oracle"`
	ValidatedResults   uint64                    `json:"validated_results"`
	StartedRequests    uint64                    `json:"started_requests"`
	CompletedRequests  uint64                    `json:"completed_requests"`
	FailedRequests     uint64                    `json:"failed_requests"`
	TimedOutRequests   uint64                    `json:"timed_out_requests"`
	DurationNS         uint64                    `json:"duration_ns"`
	ReplenishDrainNS   uint64                    `json:"replenish_drain_ns"`
	ReplenishStatus    string                    `json:"replenish_status"`
	CPUUserNS          uint64                    `json:"cpu_user_ns"`
	CPUSystemNS        uint64                    `json:"cpu_system_ns"`
	CPUCoreUtilization float64                   `json:"cpu_core_utilization"`
	GOMAXPROCS         int                       `json:"gomaxprocs"`
	ThroughputPerSec   float64                   `json:"throughput_per_second"`
	LatencyP50NS       uint64                    `json:"latency_p50_ns"`
	LatencyP95NS       uint64                    `json:"latency_p95_ns"`
	LatencyP99NS       uint64                    `json:"latency_p99_ns"`
	LatencyMaxNS       uint64                    `json:"latency_max_ns"`
	LatencyTotalNS     uint64                    `json:"latency_total_ns"`
	LatencyMeanNS      uint64                    `json:"latency_mean_ns"`
	LatencySamplesNS   []uint64                  `json:"latency_samples_ns"`
	ReadyBefore        uint32                    `json:"ready_before"`
	ReadyAfter         uint32                    `json:"ready_after"`
	Phases             []cowPressurePhase        `json:"phases"`
	RequestClasses     []cowPressureRequestClass `json:"request_classes"`
	Burst              *cowPressureBurst         `json:"burst,omitempty"`
}

type cowPressureActiveSamples struct {
	Snapshots []cowPressureSnapshot
	Err       error
}

func collectPressureActiveSamples(ctx context.Context, started time.Time, window time.Duration, collect func() (cowPressureSnapshot, error)) cowPressureActiveSamples {
	samples := make([]cowPressureSnapshot, 0, pressureActiveSampleCount)
	for index := 1; index <= pressureActiveSampleCount; index++ {
		target := started.Add(window * time.Duration(index) / time.Duration(pressureActiveSampleCount+1))
		wait := time.Until(target)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return cowPressureActiveSamples{Err: ctx.Err()}
			case <-timer.C:
			}
		} else if err := ctx.Err(); err != nil {
			return cowPressureActiveSamples{Err: err}
		}
		if err := ctx.Err(); err != nil {
			return cowPressureActiveSamples{Err: err}
		}
		snapshot, err := collect()
		if err != nil {
			return cowPressureActiveSamples{Err: err}
		}
		samples = append(samples, snapshot)
	}
	return cowPressureActiveSamples{Snapshots: samples}
}

func executeAcceptedPressureJobs(ctx context.Context, jobs <-chan struct{}, sequence *atomic.Uint64, execute func(uint64)) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case _, ok := <-jobs:
			if !ok {
				return
			}
			if ctx.Err() != nil {
				return
			}
			execute(sequence.Add(1))
		}
	}
}

func newPressureRequestContext(loadCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(loadCtx, pressureRequestTimeout)
}

func waitPressureBurstStart(ctx context.Context, target time.Time) bool {
	wait := time.Until(target)
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type cowPressureEvidence struct {
	SchemaVersion int                                       `json:"schema_version"`
	EvidenceKind  string                                    `json:"evidence_kind"`
	EvidenceClass string                                    `json:"evidence_class"`
	Artifact      runtimeevidence.ArtifactIdentity          `json:"artifact"`
	HostSource    runtimeevidence.HostSourceIdentity        `json:"host_source"`
	Environment   runtimeevidence.EnvironmentIdentity       `json:"environment"`
	Strategy      runtimeevidence.StrategyIdentity          `json:"strategy"`
	Policy        runtimescheduler.EffectivePolicyTelemetry `json:"policy"`
	Limits        cowPressureLimits                         `json:"limits"`
	StopReason    string                                    `json:"stop_reason"`
	Spawn         []cowPressureSnapshot                     `json:"spawn_snapshots"`
	LoadSamples   []cowPressureSnapshot                     `json:"load_samples"`
	Load          cowPressureLoad                           `json:"load"`
	Limitations   []string                                  `json:"limitations"`
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
	if evidence.SchemaVersion != 11 || evidence.EvidenceKind != "cow-pressure" ||
		evidence.HostSource.Modified || evidence.HostSource.Revision == "" || evidence.Artifact.SHA256 == "" ||
		evidence.Strategy.Requested != "cow-ready-single-use" || evidence.Strategy.Active != "cow-ready-single-use" || evidence.Strategy.Fallback {
		return errors.New("cow-pressure identity is incomplete or untruthful")
	}
	if evidence.Limits.BurstFactor != 1 && evidence.Limits.BurstFactor != 2 && evidence.Limits.BurstFactor != 4 && evidence.Limits.BurstFactor != 8 {
		return errors.New("cow-pressure burst factor is invalid")
	}
	if evidence.Limits.BurstFactor == 1 {
		if evidence.Load.Burst != nil {
			return errors.New("cow-pressure non-burst load contains burst evidence")
		}
	} else if evidence.Load.Burst == nil || evidence.Load.Burst.Factor != evidence.Limits.BurstFactor ||
		evidence.Load.Burst.BaselineConsumers != evidence.Limits.Consumers || evidence.Load.Burst.PeakConsumers != evidence.Limits.Consumers*evidence.Limits.BurstFactor ||
		evidence.Load.Burst.StartOffsetNS == 0 || evidence.Load.Burst.PreWindowDurationNS == 0 || evidence.Load.Burst.BurstWindowDurationNS == 0 ||
		evidence.Load.Burst.PreCompleted == 0 || evidence.Load.Burst.BurstCompleted == 0 || evidence.Load.Burst.PreThroughputPerSec <= 0 || evidence.Load.Burst.BurstThroughputPerSec <= 0 {
		return errors.New("cow-pressure burst evidence is incomplete")
	}
	if evidence.Limits.RuntimeBudgetBytes+evidence.Limits.ReservedBytes != evidence.Limits.AllocationBytes ||
		evidence.Limits.InitialCapacity != pressureInitialCapacity || evidence.Limits.MaxGrowthStep != pressureMaxGrowthStep || !validPressureRefillEvidence(evidence.Limits.RefillPolicy, evidence.Limits.RefillWorkers, evidence.Limits.MaxSlots) ||
		!validPressureWorkload(evidence.Limits.Workload, time.Duration(evidence.Limits.WaitNS), evidence.Limits.DirtyBytes) || len(evidence.Spawn) == 0 || evidence.StopReason == "" {
		return errors.New("cow-pressure limits or spawn evidence is incomplete")
	}
	compiledPolicy, err := runtimescheduler.CompileProductionPolicy(runtimescheduler.ProductionPolicy{MaxMemoryBytes: evidence.Limits.RuntimeBudgetBytes, MaxCPU: evidence.Policy.MaxCPU, Greed: evidence.Policy.Greed})
	if err != nil || compiledPolicy.Telemetry() != evidence.Policy {
		return errors.New("cow-pressure effective policy telemetry drifted")
	}
	if uint64(evidence.Limits.Consumers)*uint64(evidence.Limits.BurstFactor) > uint64(evidence.Policy.MaxActive) {
		return errors.New("cow-pressure consumers exceed the effective policy")
	}
	previousSlots := uint32(0)
	preparedImage := evidence.Spawn[0].PreparedImage
	if !validPreparedImageState(preparedImage) {
		return errors.New("cow-pressure prepared image census is invalid")
	}
	if !validPressureProfileBinding(evidence, preparedImage) {
		return errors.New("cow-pressure evidence class, artifact, workload, or warmup profile drifted")
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
	expectedLoadSamples := 1
	if evidence.Limits.Workload == "dirty-hold" || evidence.Limits.Workload == "mixed-v1" || evidence.Limits.Workload == "numpy-mixed-v1" {
		expectedLoadSamples += pressureActiveSampleCount
	}
	if len(evidence.LoadSamples) != expectedLoadSamples || evidence.Load.StartedRequests == 0 || evidence.Load.CompletedRequests == 0 ||
		evidence.Load.CompletedRequests+evidence.Load.FailedRequests != evidence.Load.StartedRequests ||
		evidence.Load.DurationNS == 0 || evidence.Load.ThroughputPerSec <= 0 ||
		evidence.Load.CPUCoreUtilization <= 0 || evidence.Load.GOMAXPROCS <= 0 || len(evidence.Load.Phases) == 0 {
		return errors.New("cow-pressure load evidence is incomplete")
	}
	if err := validatePressureLatencyEvidence(evidence.Load); err != nil {
		return err
	}
	if evidence.Load.ResultOracle != pressureResultOracle(evidence.Limits.Workload) || evidence.Load.ValidatedResults != evidence.Load.CompletedRequests {
		return errors.New("cow-pressure result oracle evidence drifted")
	}
	if err := validatePressureArrivalEvidence(evidence.Load.Arrival, evidence.Load.StartedRequests); err != nil {
		return err
	}
	classNames := map[string]struct{}{}
	expectedClassCounts := expectedPressureRequestClassCounts(evidence.Limits.Workload, evidence.Load.StartedRequests)
	var classStarted, classCompleted, classFailed uint64
	for _, class := range evidence.Load.RequestClasses {
		if !validPressureRequestClassName(evidence.Limits.Workload, class.Name) || class.Started == 0 || class.Completed+class.Failed != class.Started {
			return errors.New("cow-pressure request-class accounting is incomplete")
		}
		if expected, ok := expectedClassCounts[class.Name]; !ok || class.Started != expected {
			return errors.New("cow-pressure request-class distribution drifted")
		}
		if _, exists := classNames[class.Name]; exists {
			return errors.New("cow-pressure request-class names are duplicated")
		}
		classNames[class.Name] = struct{}{}
		classStarted += class.Started
		classCompleted += class.Completed
		classFailed += class.Failed
	}
	if len(classNames) != len(expectedClassCounts) {
		return errors.New("cow-pressure request-class distribution drifted")
	}
	if classStarted != evidence.Load.StartedRequests || classCompleted != evidence.Load.CompletedRequests || classFailed != evidence.Load.FailedRequests {
		return errors.New("cow-pressure request-class totals drifted")
	}
	lastSlots := evidence.Spawn[len(evidence.Spawn)-1].Slots
	switch evidence.StopReason {
	case "max-slots":
		if lastSlots != evidence.Limits.MaxSlots {
			return errors.New("cow-pressure max-slots stop reason does not match final capacity")
		}
	case "admission-headroom":
		if lastSlots >= evidence.Limits.MaxSlots {
			return errors.New("cow-pressure admission-headroom stop reason does not match final capacity")
		}
	default:
		return errors.New("cow-pressure stop reason is invalid")
	}
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
	for _, active := range evidence.LoadSamples[:len(evidence.LoadSamples)-1] {
		if active.Phase != "load-active" || active.Slots != lastSlots || active.RuntimeInstances != 1 ||
			active.COWMappings.Name != "memfd:apyrun-cow-image" || !validPressureLoadMappingCount(active.COWMappings.MappingCount, lastSlots, evidence.Limits.Consumers*evidence.Limits.BurstFactor) ||
			!validPressureActivePoolState(active.Pool, lastSlots, evidence.Limits.Consumers*evidence.Limits.BurstFactor) || !samePressurePreparedImageIdentity(active.PreparedImage, preparedImage) {
			return fmt.Errorf("cow-pressure active-load identity drifted: phase=%q runtime_instances=%d mapping_name=%q mappings=%d mappings_valid=%t pool_valid=%t image_identity_equal=%t image_allocated=%d baseline_allocated=%d slots=%d target=%d max=%d floor=%d critical=%d low=%d high=%d ready=%d leased=%d executing=%d waiting=%d queued=%d refilling=%d retiring=%d accounted=%d consecutive_failures=%d failures=%d breaker=%t", active.Phase, active.RuntimeInstances, active.COWMappings.Name, active.COWMappings.MappingCount, validPressureLoadMappingCount(active.COWMappings.MappingCount, lastSlots, evidence.Limits.Consumers*evidence.Limits.BurstFactor), validPressureActivePoolState(active.Pool, lastSlots, evidence.Limits.Consumers*evidence.Limits.BurstFactor), samePressurePreparedImageIdentity(active.PreparedImage, preparedImage), active.PreparedImage.AllocatedBytes, preparedImage.AllocatedBytes, lastSlots, active.Pool.TargetCapacity, active.Pool.MaximumCapacity, active.Pool.Floor, active.Pool.Critical, active.Pool.Low, active.Pool.High, active.Pool.Ready, active.Pool.Leased, active.Pool.Executing, active.Pool.Waiting, active.Pool.Queued, active.Pool.Refilling, active.Pool.Retiring, active.Pool.SupplyAccounted, active.Pool.ConsecutiveFailures, active.Pool.TotalFailures, active.Pool.BreakerOpen)
		}
	}
	snapshot := evidence.LoadSamples[len(evidence.LoadSamples)-1]
	if snapshot.Phase != "load-final" || snapshot.Slots != lastSlots || snapshot.RuntimeInstances != 1 ||
		snapshot.COWMappings.Name != "memfd:apyrun-cow-image" || !validPressureLoadMappingCount(snapshot.COWMappings.MappingCount, lastSlots, evidence.Limits.Consumers) ||
		!validPressureFinalPoolState(snapshot.Pool, lastSlots, evidence.Load.ReplenishStatus) ||
		snapshot.Pool.Ready != evidence.Load.ReadyAfter || !samePressurePreparedImageIdentity(snapshot.PreparedImage, preparedImage) {
		return errors.New("cow-pressure final mapping identity drifted")
	}
	return nil
}

func validPressureProfileBinding(evidence cowPressureEvidence, prepared wazeroengine.PreparedImageState) bool {
	switch evidence.Artifact.ArtifactProfile {
	case "base":
		return evidence.EvidenceClass == "production-safe" && evidence.Limits.WarmupProfile == "" &&
			prepared.WarmupProfile == "" && prepared.WarmupGenerationSHA256 == "" &&
			evidence.Limits.Workload != "numpy-v1" && evidence.Limits.Workload != "numpy-mixed-v1"
	case "numpy-core":
		return evidence.EvidenceClass == "profile-candidate" && evidence.Limits.WarmupProfile == wazeroengine.COWWarmupNumPyReadyV1 &&
			prepared.WarmupProfile == wazeroengine.COWWarmupNumPyReadyV1 && len(prepared.WarmupGenerationSHA256) == 64 &&
			(evidence.Limits.Workload == "numpy-v1" || evidence.Limits.Workload == "numpy-mixed-v1")
	default:
		return false
	}
}

func validPressureRequestClassName(workload, name string) bool {
	allowed := map[string]map[string]struct{}{
		"cpu":             {"tiny-cpu": {}},
		"wasi-timer-wait": {"timer-wait": {}},
		"dirty-hold":      {"dirty-hold": {}},
		"mixed-v1":        {"tiny-cpu": {}, "wait-50ms": {}, "dirty-4m-500ms": {}, "dirty-16m-2s": {}},
		"heavy-tail-v1":   {"tiny-cpu": {}, "tail-2s": {}},
		"numpy-v1":        {"numpy-tiny": {}},
		"numpy-mixed-v1":  {"numpy-tiny": {}, "numpy-cpu": {}, "numpy-dirty-4m-500ms": {}, "numpy-dirty-16m-2s": {}},
	}
	_, ok := allowed[workload][name]
	return ok
}

func pressureResultOracle(workload string) string {
	if workload == "numpy-v1" || workload == "numpy-mixed-v1" {
		return "numpy-exact-v1"
	}
	return "status-ok-v1"
}

func validatePressureLatencyEvidence(load cowPressureLoad) error {
	if load.CompletedRequests == 0 || load.CompletedRequests > pressureMaximumLatencySamples || uint64(len(load.LatencySamplesNS)) != load.CompletedRequests {
		return errors.New("cow-pressure latency sample count drifted")
	}
	var total uint64
	for index, latency := range load.LatencySamplesNS {
		if latency == 0 || (index > 0 && latency < load.LatencySamplesNS[index-1]) || total > math.MaxUint64-latency {
			return errors.New("cow-pressure latency samples are invalid")
		}
		total += latency
	}
	last := load.LatencySamplesNS[len(load.LatencySamplesNS)-1]
	if load.LatencyTotalNS != total || load.LatencyMeanNS != total/load.CompletedRequests ||
		load.LatencyP50NS != pressurePercentile(load.LatencySamplesNS, 50) ||
		load.LatencyP95NS != pressurePercentile(load.LatencySamplesNS, 95) ||
		load.LatencyP99NS != pressurePercentile(load.LatencySamplesNS, 99) ||
		load.LatencyMaxNS != last {
		return errors.New("cow-pressure derived latency evidence drifted")
	}
	return nil
}

func expectedPressureRequestClassCounts(workload string, started uint64) map[string]uint64 {
	counts := make(map[string]uint64)
	add := func(name string, count uint64) {
		if count > 0 {
			counts[name] = count
		}
	}
	switch workload {
	case "cpu":
		add("tiny-cpu", started)
	case "wasi-timer-wait":
		add("timer-wait", started)
	case "dirty-hold":
		add("dirty-hold", started)
	case "mixed-v1":
		cycles, remainder := started/20, started%20
		add("tiny-cpu", cycles*12+minUint64(remainder, 12))
		waitRemainder := uint64(0)
		if remainder > 12 {
			waitRemainder = minUint64(remainder-12, 5)
		}
		add("wait-50ms", cycles*5+waitRemainder)
		add("dirty-4m-500ms", cycles*2+boolUint64(remainder > 17)+boolUint64(remainder > 18))
		add("dirty-16m-2s", cycles+boolUint64(remainder > 19))
	case "heavy-tail-v1":
		cycles, remainder := started/20, started%20
		add("tiny-cpu", cycles*19+minUint64(remainder, 19))
		add("tail-2s", cycles+boolUint64(remainder > 19))
	case "numpy-v1":
		add("numpy-tiny", started)
	case "numpy-mixed-v1":
		cycles, remainder := started/20, started%20
		add("numpy-tiny", cycles*12+minUint64(remainder, 12))
		cpuRemainder := uint64(0)
		if remainder > 12 {
			cpuRemainder = minUint64(remainder-12, 5)
		}
		add("numpy-cpu", cycles*5+cpuRemainder)
		add("numpy-dirty-4m-500ms", cycles*2+boolUint64(remainder > 17)+boolUint64(remainder > 18))
		add("numpy-dirty-16m-2s", cycles+boolUint64(remainder > 19))
	}
	return counts
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func boolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
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

func validPressureActivePoolState(state wazeroengine.PreparedPoolState, slots, peakConsumers uint32) bool {
	return state.TargetCapacity == slots && state.MaximumCapacity >= slots && state.High == slots &&
		state.Floor <= state.Critical && state.Critical <= state.Low && state.Low <= state.High &&
		state.Ready <= slots && uint64(state.Ready)+uint64(state.Queued)+uint64(state.Refilling) == uint64(state.SupplyAccounted) &&
		state.SupplyAccounted == slots && state.Leased > 0 && state.Executing > 0 && state.Executing+state.Retiring == state.Leased &&
		state.Leased+state.Waiting <= peakConsumers && state.ConsecutiveFailures == 0 && state.TotalFailures == 0 && !state.BreakerOpen
}

func samePressurePreparedImageIdentity(candidate, baseline wazeroengine.PreparedImageState) bool {
	return candidate.Available && baseline.Available && candidate.AllocatedBytes <= candidate.VirtualBytes && baseline.AllocatedBytes <= baseline.VirtualBytes &&
		candidate.VirtualBytes == baseline.VirtualBytes && candidate.PageSizeBytes == baseline.PageSizeBytes &&
		candidate.ZeroPages == baseline.ZeroPages && candidate.NonZeroPages == baseline.NonZeroPages &&
		candidate.SparsePotentialBytes == baseline.SparsePotentialBytes && candidate.WarmupProfile == baseline.WarmupProfile &&
		candidate.WarmupGenerationSHA256 == baseline.WarmupGenerationSHA256
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
	if goos != "linux" || options.Kind != "cow-pressure" || options.Strategy != "cow-ready-single-use" {
		return errors.New("cow-pressure requires Linux cow-ready-single-use")
	}
	numpyWorkload := options.PressureWorkload == "numpy-v1" || options.PressureWorkload == "numpy-mixed-v1"
	if numpyWorkload {
		if options.Class != "profile-candidate" || options.COWWarmupProfile != wazeroengine.COWWarmupNumPyReadyV1 {
			return errors.New("NumPy pressure requires profile-candidate and numpy-ready-v1")
		}
	} else if options.Class != "production-safe" || options.COWWarmupProfile != "" {
		return errors.New("base pressure requires production-safe without a warmup profile")
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
	if !validPressureRefillWorkers(uint32(options.PressureRefillWorkers)) {
		return errors.New("cow-pressure refill workers must be automatic (0), 1, 2, 4, 8, 12, or 16")
	}
	if options.PressureBurstFactor != 1 && options.PressureBurstFactor != 2 && options.PressureBurstFactor != 4 && options.PressureBurstFactor != 8 {
		return errors.New("cow-pressure burst factor must be 1, 2, 4, or 8")
	}
	if options.ConsumerCount > 256/options.PressureBurstFactor ||
		(options.PressureBurstFactor > 1 && (options.PressureWorkload != "mixed-v1" || options.PressureDuration < 10*time.Second)) {
		return errors.New("cow-pressure burst requires mixed-v1, at least ten seconds, and at most 256 peak consumers")
	}
	if !validPressureWorkload(options.PressureWorkload, options.PressureWait, options.PressureDirtyBytes) {
		return errors.New("cow-pressure workload or wait is outside bounds")
	}
	if uint64(options.PressureMaxCPU) > math.MaxUint32 || options.PressureMaxCPU == 0 || options.PressureGreed > 100 {
		return errors.New("cow-pressure maximum CPU or greed is outside bounds")
	}
	compiledPolicy, err := runtimescheduler.CompileProductionPolicy(runtimescheduler.ProductionPolicy{MaxMemoryBytes: options.MemoryBudgetBytes, MaxCPU: uint32(options.PressureMaxCPU), Greed: uint8(options.PressureGreed)})
	if err != nil || uint64(options.ConsumerCount)*uint64(options.PressureBurstFactor) > uint64(compiledPolicy.MaxActive) {
		return errors.New("cow-pressure consumers exceed the compiled production policy")
	}
	mode := canonicalPressureArrivalMode(options.PressureArrivalMode)
	switch mode {
	case "closed-loop":
		if options.PressureArrivalRate != 0 || options.PressureQueueCapacity != 0 {
			return errors.New("closed-loop pressure cannot define an arrival rate or queue")
		}
	case "open-loop-fixed-v1":
		if options.PressureQueueCapacity == 0 || options.PressureQueueCapacity > 65536 || options.PressureBurstFactor != 1 {
			return errors.New("fixed open-loop pressure requires a bounded queue and forbids correlated burst")
		}
		offsets, err := fixedOpenLoopArrivalOffsets(options.PressureDuration, options.PressureArrivalRate)
		if err != nil {
			return err
		}
		if len(offsets) > pressureMaximumLatencySamples {
			return errors.New("fixed open-loop pressure exceeds the latency sample bound")
		}
	default:
		return errors.New("cow-pressure arrival mode is unknown")
	}
	return nil
}

func canonicalPressureArrivalMode(mode string) string {
	if mode == "" {
		return "closed-loop"
	}
	return mode
}

func fixedOpenLoopArrivalOffsets(duration time.Duration, rate uint) ([]time.Duration, error) {
	if duration <= 0 || rate == 0 || rate > 4096 || uint64(duration) > math.MaxUint64/uint64(rate) {
		return nil, errors.New("fixed open-loop duration or rate is outside bounds")
	}
	count := uint64(duration) * uint64(rate) / uint64(time.Second)
	if count == 0 || count > pressureMaximumOfferedRequests {
		return nil, errors.New("fixed open-loop offered request count is outside bounds")
	}
	offsets := make([]time.Duration, count)
	for index := uint64(0); index < count; index++ {
		offsets[index] = time.Duration(index * uint64(time.Second) / uint64(rate))
	}
	return offsets, nil
}

func validatePressureArrivalEvidence(arrival cowPressureArrival, started uint64) error {
	if arrival.Mode == "closed-loop" {
		if arrival.WindowNS != 0 || arrival.RatePerSecond != 0 || arrival.QueueCapacity != 0 || arrival.RejectedRequests != 0 ||
			arrival.OfferedRequests == 0 || arrival.OfferedRequests != arrival.AcceptedRequests || arrival.AcceptedRequests != started {
			return errors.New("closed-loop arrival accounting is inconsistent")
		}
		return nil
	}
	if arrival.Mode != "open-loop-fixed-v1" ||
		arrival.WindowNS < uint64((5*time.Second).Nanoseconds()) || arrival.WindowNS > uint64((10*time.Minute).Nanoseconds()) ||
		arrival.RatePerSecond == 0 || arrival.RatePerSecond > 4096 ||
		arrival.QueueCapacity == 0 || arrival.QueueCapacity > 65536 || arrival.OfferedRequests == 0 ||
		arrival.OfferedRequests != arrival.AcceptedRequests+arrival.RejectedRequests || arrival.AcceptedRequests != started {
		return errors.New("fixed open-loop arrival accounting is inconsistent")
	}
	offsets, err := fixedOpenLoopArrivalOffsets(time.Duration(arrival.WindowNS), uint(arrival.RatePerSecond))
	if err != nil || arrival.OfferedRequests != uint64(len(offsets)) {
		return errors.New("fixed open-loop offered count drifted from the arrival tape")
	}
	return nil
}

func pressureRequestSpecFor(options benchmarkOptions, id uint64) cowPressureRequestSpec {
	cpu := func() cowPressureRequestSpec {
		return cowPressureRequestSpec{Class: "tiny-cpu", Code: "result = inputs['value'] + 1", Inputs: map[string]any{"value": id}}
	}
	wait := func(class string, duration time.Duration) cowPressureRequestSpec {
		return cowPressureRequestSpec{Class: class, Code: "import time\ntime.sleep(inputs['wait_seconds'])\nresult = inputs['value'] + 1", Inputs: map[string]any{"value": id, "wait_seconds": duration.Seconds()}, Wait: duration}
	}
	dirty := func(class string, bytes uint64, duration time.Duration) cowPressureRequestSpec {
		return cowPressureRequestSpec{Class: class, Code: "import time\nbuf = bytearray(inputs['dirty_bytes'])\nfor offset in range(0, len(buf), 4096):\n    buf[offset] = (offset // 4096) & 255\ntime.sleep(inputs['wait_seconds'])\nresult = inputs['value'] + 1", Inputs: map[string]any{"value": id, "dirty_bytes": bytes, "wait_seconds": duration.Seconds()}, Wait: duration}
	}
	numpy := func(class string, elements uint64) cowPressureRequestSpec {
		return cowPressureRequestSpec{
			Class:  class,
			Code:   `result = {"prepared": prepared, "numpy_version": np.__version__, "sum": int(np.arange(inputs["elements"], dtype=np.int64).sum())}`,
			Inputs: map[string]any{"elements": elements}, RequireNumPy: true, ExpectedPrepared: 41,
			ExpectedSum: int64(elements * (elements - 1) / 2),
		}
	}
	numpyDirty := func(class string, bytes uint64, duration time.Duration) cowPressureRequestSpec {
		pages := (bytes + 4095) / 4096
		cycles, remainder := pages/256, pages%256
		expected := cycles*32640 + remainder*(remainder-1)/2
		return cowPressureRequestSpec{
			Class:  class,
			Code:   "import time\narr = np.zeros(inputs['dirty_bytes'], dtype=np.uint8)\nfor offset in range(0, len(arr), 4096):\n    arr[offset] = (offset // 4096) & 255\ntime.sleep(inputs['wait_seconds'])\nresult = {\"prepared\": prepared, \"numpy_version\": np.__version__, \"sum\": int(arr[::4096].sum())}",
			Inputs: map[string]any{"dirty_bytes": bytes, "wait_seconds": duration.Seconds()}, Wait: duration,
			RequireNumPy: true, ExpectedPrepared: 41, ExpectedSum: int64(expected),
		}
	}
	switch options.PressureWorkload {
	case "wasi-timer-wait":
		return wait("timer-wait", options.PressureWait)
	case "dirty-hold":
		return dirty("dirty-hold", options.PressureDirtyBytes, options.PressureWait)
	case "mixed-v1":
		position := (id - 1) % 20
		switch {
		case position < 12:
			return cpu()
		case position < 17:
			return wait("wait-50ms", 50*time.Millisecond)
		case position < 19:
			return dirty("dirty-4m-500ms", 4<<20, 500*time.Millisecond)
		default:
			return dirty("dirty-16m-2s", 16<<20, 2*time.Second)
		}
	case "heavy-tail-v1":
		if (id-1)%20 == 19 {
			return wait("tail-2s", 2*time.Second)
		}
	case "numpy-v1":
		return numpy("numpy-tiny", 1000)
	case "numpy-mixed-v1":
		position := (id - 1) % 20
		switch {
		case position < 12:
			return numpy("numpy-tiny", 1000)
		case position < 17:
			return numpy("numpy-cpu", 250000)
		case position < 19:
			return numpyDirty("numpy-dirty-4m-500ms", 4<<20, 500*time.Millisecond)
		default:
			return numpyDirty("numpy-dirty-16m-2s", 16<<20, 2*time.Second)
		}
	}
	return cpu()
}

func validPressureRefillWorkers(workers uint32) bool {
	switch workers {
	case 0, 1, 2, 4, 8, 12, 16:
		return true
	default:
		return false
	}
}

func validPressureRefillEvidence(policy string, workers, maxSlots uint32) bool {
	if workers == 0 || workers > maxSlots {
		return false
	}
	if policy == "fixed" {
		return validPressureRefillWorkers(workers)
	}
	if policy != "adaptive" {
		return false
	}
	want := pressureAdaptiveRefillWorkers
	if maxSlots < want {
		want = maxSlots
	}
	return workers == want
}

func pressureRefillPolicy(configuredWorkers uint) string {
	if configuredWorkers == 0 {
		return "adaptive"
	}
	return "fixed"
}

func validPressureWorkload(workload string, wait time.Duration, dirtyBytes uint64) bool {
	switch workload {
	case "cpu":
		return wait == 0 && dirtyBytes == 0
	case "wasi-timer-wait":
		return wait >= time.Millisecond && wait <= 10*time.Second && dirtyBytes == 0
	case "dirty-hold":
		return wait >= time.Second && wait <= 10*time.Second && dirtyBytes >= 1<<20 && dirtyBytes <= 64<<20
	case "mixed-v1", "heavy-tail-v1", "numpy-v1", "numpy-mixed-v1":
		return wait == 0 && dirtyBytes == 0
	default:
		return false
	}
}

func runCOWPressureMain(options benchmarkOptions, goos string) error {
	if err := validateCOWPressureOptions(options, goos); err != nil {
		return err
	}
	effectivePolicy, err := runtimescheduler.CompileProductionPolicy(runtimescheduler.ProductionPolicy{MaxMemoryBytes: options.MemoryBudgetBytes, MaxCPU: uint32(options.PressureMaxCPU), Greed: uint8(options.PressureGreed)})
	if err != nil {
		return err
	}
	artifact, wasm, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return err
	}
	if err := validateEvidenceClassProfile(options.Class, artifact.ArtifactProfile); err != nil {
		return err
	}
	numpyWorkload := options.PressureWorkload == "numpy-v1" || options.PressureWorkload == "numpy-mixed-v1"
	if (numpyWorkload && artifact.ArtifactProfile != "numpy-core") || (!numpyWorkload && artifact.ArtifactProfile != "base") {
		return errors.New("cow-pressure artifact profile does not match its workload")
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
		PreparedCapacity:       pressureInitialCapacity,
		PreparedMaxCapacity:    uint32(options.MaxPressureSlots),
		PreparedRefillWorkers:  uint32(options.PressureRefillWorkers),
		AdaptivePreparedRefill: options.PressureRefillWorkers == 0,
		Strategy:               enginecontract.StrategyCOWReadySingleUse,
		COWWarmupProfile:       options.COWWarmupProfile,
		Observer:               lifecycle.observe,
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
	arrivalMode := canonicalPressureArrivalMode(options.PressureArrivalMode)
	loadCtx, cancelLoad := context.WithCancel(context.Background())
	if arrivalMode == "closed-loop" {
		loadCtx, cancelLoad = context.WithTimeout(context.Background(), options.PressureDuration)
	}
	defer cancelLoad()
	requestParentCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	var offered, accepted, rejected, started, completed, failed, timedOut atomic.Uint64
	var sequence atomic.Uint64
	latencies := make([]uint64, 0, 4096)
	var latencyMu sync.Mutex
	requestClasses := map[string]*cowPressureRequestClass{}
	var requestClassMu sync.Mutex
	var failureMu sync.Mutex
	var firstFailure string
	recordFailure := func(reason string) {
		failureMu.Lock()
		if firstFailure == "" {
			firstFailure = reason
			cancelLoad()
			cancelRequests()
		}
		failureMu.Unlock()
	}
	failureReason := func() string {
		failureMu.Lock()
		defer failureMu.Unlock()
		return firstFailure
	}
	var workers sync.WaitGroup
	cpuStarted, err := collectPressureCPU()
	if err != nil {
		return err
	}
	loadStarted := time.Now()
	startLoad := make(chan struct{})
	burstRelease := make(chan struct{})
	burstWindowResult := make(chan cowPressureBurstWindow, 1)
	if options.PressureBurstFactor == 1 {
		close(burstRelease)
	} else {
		go func() {
			<-startLoad
			preStarted := time.Now()
			target := loadStarted.Add(options.PressureDuration / 2)
			if !waitPressureBurstStart(loadCtx, target) {
				close(burstRelease)
				burstWindowResult <- cowPressureBurstWindow{}
				return
			}
			preCompleted := completed.Load()
			burstStarted := time.Now()
			close(burstRelease)
			<-loadCtx.Done()
			burstWindowResult <- cowPressureBurstWindow{preCompleted: preCompleted, endCompleted: completed.Load(), preDuration: burstStarted.Sub(preStarted), burstDuration: time.Since(burstStarted)}
		}()
	}
	activeSampleCtx, cancelActiveSamples := context.WithCancel(loadCtx)
	defer cancelActiveSamples()
	var activeSamples <-chan cowPressureActiveSamples
	if options.PressureWorkload == "dirty-hold" || options.PressureWorkload == "mixed-v1" || options.PressureWorkload == "numpy-mixed-v1" {
		result := make(chan cowPressureActiveSamples, 1)
		activeSamples = result
		go func() {
			select {
			case <-startLoad:
			case <-activeSampleCtx.Done():
				result <- cowPressureActiveSamples{Err: activeSampleCtx.Err()}
				return
			}
			sampleWindow := options.PressureWait
			if options.PressureWorkload == "mixed-v1" || options.PressureWorkload == "numpy-mixed-v1" {
				sampleWindow = options.PressureDuration
			}
			result <- collectPressureActiveSamples(activeSampleCtx, loadStarted, sampleWindow, func() (cowPressureSnapshot, error) {
				return collectStableCOWPressureActiveSnapshot(activeSampleCtx, func() (cowPressureSnapshot, error) {
					return collectCOWPressureSnapshot(collector, runner, "load-active", slots)
				}, slots, uint32(options.ConsumerCount*options.PressureBurstFactor))
			})
		}()
	}
	executeRequest := func(id uint64) {
		spec := pressureRequestSpecFor(options, id)
		request, err := makeRequest(fmt.Sprintf("pressure-%d", id), spec.Code, spec.Inputs)
		if err != nil {
			failed.Add(1)
			recordFailure("make request: " + err.Error())
			return
		}
		failureMu.Lock()
		if firstFailure != "" {
			failureMu.Unlock()
			return
		}
		started.Add(1)
		failureMu.Unlock()
		requestClassMu.Lock()
		class := requestClasses[spec.Class]
		if class == nil {
			class = &cowPressureRequestClass{Name: spec.Class}
			requestClasses[spec.Class] = class
		}
		class.Started++
		requestClassMu.Unlock()
		requestCtx, cancel := newPressureRequestContext(requestParentCtx)
		began := time.Now()
		response, runErr := runner.Run(requestCtx, request, "")
		cancel()
		elapsedDuration := time.Since(began)
		elapsed := uint64(elapsedDuration.Nanoseconds())
		failureReason := pressureResponseFailureForSpec(response, spec, elapsedDuration)
		if runErr != nil || failureReason != "" {
			failed.Add(1)
			requestClassMu.Lock()
			class.Failed++
			requestClassMu.Unlock()
			if errors.Is(runErr, context.DeadlineExceeded) {
				timedOut.Add(1)
			}
			if runErr != nil {
				failureReason = "runner error: " + runErr.Error()
			}
			recordFailure(failureReason)
			return
		}
		latencyMu.Lock()
		if len(latencies) >= pressureMaximumLatencySamples {
			latencyMu.Unlock()
			failed.Add(1)
			requestClassMu.Lock()
			class.Failed++
			requestClassMu.Unlock()
			recordFailure("latency sample bound exceeded")
			return
		}
		latencies = append(latencies, elapsed)
		latencyMu.Unlock()
		completed.Add(1)
		requestClassMu.Lock()
		class.Completed++
		requestClassMu.Unlock()
	}

	totalConsumers := options.ConsumerCount * options.PressureBurstFactor
	if arrivalMode == "closed-loop" {
		for worker := uint(0); worker < totalConsumers; worker++ {
			workers.Add(1)
			go func(worker uint) {
				defer workers.Done()
				<-startLoad
				if worker >= options.ConsumerCount {
					<-burstRelease
				}
				for loadCtx.Err() == nil {
					id := sequence.Add(1)
					offered.Add(1)
					accepted.Add(1)
					executeRequest(id)
				}
			}(worker)
		}
		close(startLoad)
		workers.Wait()
	} else {
		offsets, err := fixedOpenLoopArrivalOffsets(options.PressureDuration, options.PressureArrivalRate)
		if err != nil {
			return err
		}
		jobs := make(chan struct{}, options.PressureQueueCapacity)
		for worker := uint(0); worker < options.ConsumerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-startLoad
				executeAcceptedPressureJobs(loadCtx, jobs, &sequence, executeRequest)
			}()
		}
		close(startLoad)
		cancelled := false
		for _, offset := range offsets {
			wait := time.Until(loadStarted.Add(offset))
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-loadCtx.Done():
					timer.Stop()
					cancelled = true
				case <-timer.C:
				}
			}
			if loadCtx.Err() != nil {
				cancelled = true
			}
			if cancelled {
				break
			}
			offered.Add(1)
			select {
			case jobs <- struct{}{}:
				accepted.Add(1)
			default:
				rejected.Add(1)
			}
		}
		close(jobs)
		workers.Wait()
	}
	var burstWindow cowPressureBurstWindow
	if options.PressureBurstFactor > 1 {
		burstWindow = <-burstWindowResult
	}
	cancelActiveSamples()
	loadSamples := make([]cowPressureSnapshot, 0, pressureActiveSampleCount+1)
	if activeSamples != nil {
		result := <-activeSamples
		if result.Err != nil && !(errors.Is(result.Err, context.Canceled) && failureReason() != "") {
			return result.Err
		}
		loadSamples = append(loadSamples, result.Snapshots...)
	}
	if failed.Load() > 0 || timedOut.Load() > 0 {
		return fmt.Errorf("cow-pressure workload failed closed: failed=%d timed_out=%d first_failure=%s", failed.Load(), timedOut.Load(), failureReason())
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
	finalSnapshot, err := collectStableCOWPressureFinalSnapshot(collector, runner, slots, replenishStatus, uint32(options.ConsumerCount))
	if err != nil {
		return err
	}
	loadSamples = append(loadSamples, finalSnapshot)
	readyAfter := finalSnapshot.Pool.Ready

	latencyMu.Lock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	latencyCopy := append([]uint64(nil), latencies...)
	latencyMu.Unlock()
	var latencyTotal uint64
	for _, latency := range latencyCopy {
		latencyTotal += latency
	}
	requestClassMu.Lock()
	requestClassNames := make([]string, 0, len(requestClasses))
	for name := range requestClasses {
		requestClassNames = append(requestClassNames, name)
	}
	sort.Strings(requestClassNames)
	requestClassEvidence := make([]cowPressureRequestClass, 0, len(requestClassNames))
	for _, name := range requestClassNames {
		requestClassEvidence = append(requestClassEvidence, *requestClasses[name])
	}
	requestClassMu.Unlock()
	arrivalWindowNS := uint64(0)
	if arrivalMode == "open-loop-fixed-v1" {
		arrivalWindowNS = uint64(options.PressureDuration.Nanoseconds())
	}
	load := cowPressureLoad{
		Arrival:          cowPressureArrival{Mode: arrivalMode, WindowNS: arrivalWindowNS, RatePerSecond: uint32(options.PressureArrivalRate), QueueCapacity: uint32(options.PressureQueueCapacity), OfferedRequests: offered.Load(), AcceptedRequests: accepted.Load(), RejectedRequests: rejected.Load()},
		ResultOracle:     pressureResultOracle(options.PressureWorkload),
		ValidatedResults: completed.Load(),
		StartedRequests:  started.Load(), CompletedRequests: completed.Load(), FailedRequests: failed.Load(), TimedOutRequests: timedOut.Load(),
		DurationNS: uint64(loadElapsed.Nanoseconds()), ReplenishDrainNS: uint64(replenishElapsed.Nanoseconds()), ReplenishStatus: replenishStatus,
		CPUUserNS: cpuFinished.userNS - cpuStarted.userNS, CPUSystemNS: cpuFinished.systemNS - cpuStarted.systemNS,
		GOMAXPROCS:  goruntime.GOMAXPROCS(0),
		ReadyBefore: readyBefore, ReadyAfter: readyAfter, Phases: aggregatePressurePhases(lifecycle.drain()), RequestClasses: requestClassEvidence,
	}
	load.LatencyTotalNS = latencyTotal
	load.LatencySamplesNS = latencyCopy
	if load.CompletedRequests > 0 {
		load.LatencyMeanNS = latencyTotal / load.CompletedRequests
	}
	if options.PressureBurstFactor > 1 {
		burstCompleted := burstWindow.endCompleted - burstWindow.preCompleted
		load.Burst = &cowPressureBurst{
			Factor: uint32(options.PressureBurstFactor), BaselineConsumers: uint32(options.ConsumerCount), PeakConsumers: uint32(totalConsumers),
			StartOffsetNS: uint64(burstWindow.preDuration.Nanoseconds()), PreWindowDurationNS: uint64(burstWindow.preDuration.Nanoseconds()), BurstWindowDurationNS: uint64(burstWindow.burstDuration.Nanoseconds()),
			PreCompleted: burstWindow.preCompleted, BurstCompleted: burstCompleted,
			PreThroughputPerSec: float64(burstWindow.preCompleted) / burstWindow.preDuration.Seconds(), BurstThroughputPerSec: float64(burstCompleted) / burstWindow.burstDuration.Seconds(),
		}
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
		SchemaVersion: 11, EvidenceKind: "cow-pressure", EvidenceClass: options.Class,
		Artifact:   runtimeevidence.ArtifactIdentity{Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size), SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile, Target: artifact.Target, ExecutionModel: artifact.Execution},
		HostSource: runtimeevidence.HostSourceIdentity{Revision: host.Revision, Modified: host.Modified}, Environment: initial.Environment,
		Strategy:   runtimeevidence.StrategyIdentity{Requested: "cow-ready-single-use", Active: "cow-ready-single-use", Fallback: false},
		Policy:     effectivePolicy.Telemetry(),
		Limits:     cowPressureLimits{RuntimeBudgetBytes: options.MemoryBudgetBytes, ReservedBytes: options.MemoryReserveBytes, AllocationBytes: options.MemoryBudgetBytes + options.MemoryReserveBytes, MaxSlots: uint32(options.MaxPressureSlots), Consumers: uint32(options.ConsumerCount), DurationNS: uint64(options.PressureDuration.Nanoseconds()), InitialCapacity: pressureInitialCapacity, MaxGrowthStep: pressureMaxGrowthStep, Workload: options.PressureWorkload, WaitNS: uint64(options.PressureWait.Nanoseconds()), DirtyBytes: options.PressureDirtyBytes, RefillPolicy: pressureRefillPolicy(options.PressureRefillWorkers), RefillWorkers: runner.PreparedRefillWorkers(), BurstFactor: uint32(options.PressureBurstFactor), WarmupProfile: options.COWWarmupProfile},
		StopReason: stopReason, Spawn: spawn, LoadSamples: loadSamples, Load: load,
		Limitations: []string{
			"Admission uses process PSS with a conservative dynamic headroom; it is not a kernel memory reservation.",
			"One bounded wazero runtime, compiled module, and sealed baseline owns every admitted COW slot.",
			"Full smaps collection is excluded from ordinary timed loads; dirty-hold and versioned mixed workloads take three fixed-offset in-load samples and include that diagnostic perturbation in their timing.",
			"Raw cgroup counters retain their observed scope; shared job-level values are not process attribution or per-slot memory.",
			"Closed-loop consumers and fixed open-loop arrivals are distinct modes; correlated burst is permitted only for closed-loop and is not an arrival process or provider model.",
			"Fixed open-loop uses a deterministic interval tape and bounded in-process queue; it is not a production arrival distribution.",
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

func collectStableCOWPressureFinalSnapshot(
	collector runtimeevidence.LinuxCollector,
	runner growablePreparedBenchmarkRunner,
	slots uint32,
	replenishStatus string,
	consumers uint32,
) (cowPressureSnapshot, error) {
	deadline := time.Now().Add(5 * time.Second)
	var last cowPressureSnapshot
	for {
		snapshot, err := collectCOWPressureSnapshot(collector, runner, "load-final", slots)
		if err != nil {
			return cowPressureSnapshot{}, err
		}
		last = snapshot
		if validStableCOWPressureFinalSnapshot(snapshot, slots, replenishStatus, consumers) {
			return snapshot, nil
		}
		if !time.Now().Before(deadline) {
			return cowPressureSnapshot{}, fmt.Errorf(
				"stable final COW snapshot unavailable: status=%s mappings=%d slots=%d ready=%d queued=%d refilling=%d accounted=%d",
				replenishStatus, last.COWMappings.MappingCount, slots, last.Pool.Ready, last.Pool.Queued, last.Pool.Refilling, last.Pool.SupplyAccounted,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func collectStableCOWPressureActiveSnapshot(
	ctx context.Context,
	collect func() (cowPressureSnapshot, error),
	slots uint32,
	peakConsumers uint32,
) (cowPressureSnapshot, error) {
	deadline := time.Now().Add(time.Second)
	var last cowPressureSnapshot
	for {
		if err := ctx.Err(); err != nil {
			return cowPressureSnapshot{}, err
		}
		snapshot, err := collect()
		if contextErr := ctx.Err(); contextErr != nil {
			return cowPressureSnapshot{}, contextErr
		}
		if err != nil {
			return cowPressureSnapshot{}, err
		}
		last = snapshot
		if snapshot.Phase == "load-active" && snapshot.Slots == slots && snapshot.RuntimeInstances == 1 &&
			snapshot.COWMappings.Name == "memfd:apyrun-cow-image" &&
			validPressureLoadMappingCount(snapshot.COWMappings.MappingCount, slots, peakConsumers) &&
			validPressureActivePoolState(snapshot.Pool, slots, peakConsumers) {
			return snapshot, nil
		}
		if !time.Now().Before(deadline) {
			return cowPressureSnapshot{}, fmt.Errorf(
				"stable active COW snapshot unavailable: mappings=%d slots=%d ready=%d leased=%d executing=%d queued=%d refilling=%d retiring=%d accounted=%d",
				last.COWMappings.MappingCount, slots, last.Pool.Ready, last.Pool.Leased, last.Pool.Executing,
				last.Pool.Queued, last.Pool.Refilling, last.Pool.Retiring, last.Pool.SupplyAccounted,
			)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return cowPressureSnapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func validStableCOWPressureFinalSnapshot(snapshot cowPressureSnapshot, slots uint32, replenishStatus string, consumers uint32) bool {
	mappingsValid := validPressureLoadMappingCount(snapshot.COWMappings.MappingCount, slots, consumers)
	if replenishStatus == "complete" {
		mappingsValid = snapshot.COWMappings.MappingCount == slots
	}
	return mappingsValid && validPressureFinalPoolState(snapshot.Pool, slots, replenishStatus)
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

func pressureResponseFailure(raw []byte, _ string, requestedWait, elapsed time.Duration) string {
	return pressureResponseFailureForSpec(raw, cowPressureRequestSpec{Wait: requestedWait}, elapsed)
}

func pressureResponseFailureForSpec(raw []byte, spec cowPressureRequestSpec, elapsed time.Duration) string {
	var response struct {
		Status string                     `json:"status"`
		Result map[string]json.RawMessage `json:"result"`
		Error  map[string]any             `json:"error"`
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
	if spec.Wait > 0 && elapsed+time.Millisecond < spec.Wait {
		return fmt.Sprintf("timer returned early: elapsed=%s requested=%s", elapsed, spec.Wait)
	}
	if spec.RequireNumPy {
		if len(response.Result) != 3 {
			return "NumPy result does not have the exact expected shape"
		}
		var prepared, sum int64
		var version string
		if err := json.Unmarshal(response.Result["prepared"], &prepared); err != nil || prepared != spec.ExpectedPrepared {
			return "NumPy result prepared value mismatched"
		}
		if err := json.Unmarshal(response.Result["numpy_version"], &version); err != nil || version == "" || len(version) > 64 {
			return "NumPy result version is missing or invalid"
		}
		if err := json.Unmarshal(response.Result["sum"], &sum); err != nil || sum != spec.ExpectedSum {
			return "NumPy result sum mismatched"
		}
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
