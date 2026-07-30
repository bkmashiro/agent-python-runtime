package scheduler

import (
	"errors"
	"fmt"
	"time"
)

const (
	ProductionPolicyVersion = "production-policy-v1"

	minimumProductionMemoryBytes = uint64(64 << 20)
	maximumProductionMemoryBytes = uint64(1 << 60)
	maximumProductionCPU         = uint32(1024)
	maxProductionActive          = uint32(4096)
	productionCPUPeriodMicros    = uint64(100_000)
)

var ErrInvalidProductionPolicy = errors.New("invalid production policy")

// ProductionPolicy is the complete public resource-policy surface. MaxMemoryBytes
// and MaxCPU are hard deployment limits; Greed is a bounded 0..100 preference
// that only changes soft admission, retry, and observation policy.
type ProductionPolicy struct {
	MaxMemoryBytes uint64
	MaxCPU         uint32
	Greed          uint8
}

// EffectivePolicy is an auditable compilation of ProductionPolicy into the
// existing scheduler components. Applying CPUQuotaMicros remains the deployment
// layer's responsibility; MaxActive is only an in-process concurrency bound.
type EffectivePolicy struct {
	Version        string
	MaxMemoryBytes uint64
	MaxCPU         uint32
	Greed          uint8

	CPUPeriodMicros uint64
	CPUQuotaMicros  uint64
	MaxActive       uint32

	TargetMemoryBPS   uint32
	HighMemoryBPS     uint32
	CriticalMemoryBPS uint32

	UnknownReservationBytes uint64
	PerAttemptMarginBytes   uint64
	SpeculativeMaxEvictions uint32
	DefaultRetryDelay       time.Duration
	ControlInterval         time.Duration

	Scheduler       Config
	Profiles        ProfileConfig
	GreedController GreedConfig
}

// EffectivePolicyTelemetry is the stable, JSON-safe explanation of an
// EffectivePolicy. It intentionally excludes functions and live controller
// state.
type EffectivePolicyTelemetry struct {
	Version        string `json:"version"`
	MaxMemoryBytes uint64 `json:"max_memory_bytes"`
	MaxCPU         uint32 `json:"max_cpu"`
	Greed          uint8  `json:"greed"`

	CPUPeriodMicros uint64 `json:"cpu_period_micros"`
	CPUQuotaMicros  uint64 `json:"cpu_quota_micros"`
	MaxActive       uint32 `json:"max_active"`

	TargetMemoryBPS     uint32 `json:"target_memory_bps"`
	TargetMemoryBytes   uint64 `json:"target_memory_bytes"`
	HighMemoryBPS       uint32 `json:"high_memory_bps"`
	HighMemoryBytes     uint64 `json:"high_memory_bytes"`
	CriticalMemoryBPS   uint32 `json:"critical_memory_bps"`
	CriticalMemoryBytes uint64 `json:"critical_memory_bytes"`
	HardMemoryBytes     uint64 `json:"hard_memory_bytes"`

	UnknownReservationBytes uint64 `json:"unknown_reservation_bytes"`
	PerAttemptMarginBytes   uint64 `json:"per_attempt_margin_bytes"`
	ReservationQuantileBPS  uint32 `json:"reservation_quantile_bps"`
	TargetEvictionPPM       uint32 `json:"target_eviction_ppm"`
	SpeculativeMaxEvictions uint32 `json:"speculative_max_evictions"`
	DefaultRetryDelayNS     uint64 `json:"default_retry_delay_ns"`
	ControlIntervalNS       uint64 `json:"control_interval_ns"`
	StableSampleEvery       uint32 `json:"stable_sample_every"`
	MaximumSomePSIAvg10BPS  uint32 `json:"maximum_some_psi_avg10_bps"`
	MaximumFullPSIAvg10BPS  uint32 `json:"maximum_full_psi_avg10_bps"`
	MaxTasks                uint32 `json:"max_tasks"`
	MaxAttempts             uint32 `json:"max_attempts"`
}

func (effective EffectivePolicy) Telemetry() EffectivePolicyTelemetry {
	return EffectivePolicyTelemetry{
		Version: effective.Version, MaxMemoryBytes: effective.MaxMemoryBytes, MaxCPU: effective.MaxCPU, Greed: effective.Greed,
		CPUPeriodMicros: effective.CPUPeriodMicros, CPUQuotaMicros: effective.CPUQuotaMicros, MaxActive: effective.MaxActive,
		TargetMemoryBPS: effective.TargetMemoryBPS, TargetMemoryBytes: effective.Scheduler.TargetBytes,
		HighMemoryBPS: effective.HighMemoryBPS, HighMemoryBytes: effective.Scheduler.HighBytes,
		CriticalMemoryBPS: effective.CriticalMemoryBPS, CriticalMemoryBytes: effective.Scheduler.CriticalBytes, HardMemoryBytes: effective.Scheduler.HardBytes,
		UnknownReservationBytes: effective.UnknownReservationBytes, PerAttemptMarginBytes: effective.PerAttemptMarginBytes,
		ReservationQuantileBPS: effective.Profiles.ReservationQuantileBPS, TargetEvictionPPM: effective.GreedController.TargetEvictionPPM,
		SpeculativeMaxEvictions: effective.SpeculativeMaxEvictions, DefaultRetryDelayNS: uint64(effective.DefaultRetryDelay), ControlIntervalNS: uint64(effective.ControlInterval),
		StableSampleEvery: effective.Profiles.StableSampleEvery, MaximumSomePSIAvg10BPS: effective.GreedController.MaximumSomePSIAvg10BPS,
		MaximumFullPSIAvg10BPS: effective.GreedController.MaximumFullPSIAvg10BPS, MaxTasks: effective.Scheduler.MaxTasks, MaxAttempts: effective.Scheduler.MaxAttempts,
	}
}

func CompileProductionPolicy(policy ProductionPolicy) (EffectivePolicy, error) {
	if policy.MaxMemoryBytes < minimumProductionMemoryBytes || policy.MaxMemoryBytes > maximumProductionMemoryBytes ||
		policy.MaxCPU == 0 || policy.MaxCPU > maximumProductionCPU || policy.Greed > 100 {
		return EffectivePolicy{}, fmt.Errorf("%w: max memory, max CPU, or greed is outside its bound", ErrInvalidProductionPolicy)
	}
	greed := uint32(policy.Greed)
	targetBPS := uint32(8000 + greed*10)
	highBPS := uint32(8800 + greed*6)
	criticalBPS := uint32(9500 + greed*3)
	targetBytes := scaleBPS(policy.MaxMemoryBytes, targetBPS)
	highBytes := scaleBPS(policy.MaxMemoryBytes, highBPS)
	criticalBytes := scaleBPS(policy.MaxMemoryBytes, criticalBPS)
	if targetBytes == 0 || targetBytes >= highBytes || highBytes >= criticalBytes || criticalBytes >= policy.MaxMemoryBytes {
		return EffectivePolicy{}, fmt.Errorf("%w: compiled memory watermarks are not strictly ordered", ErrInvalidProductionPolicy)
	}

	maxActive := ceilRatio(uint64(policy.MaxCPU)*uint64(100+3*greed), 100)
	if maxActive > uint64(maxProductionActive) {
		maxActive = uint64(maxProductionActive)
	}
	maxTasks := uint64(max(1024, uint32(maxActive)*64))
	if maxTasks > 1<<20 {
		maxTasks = 1 << 20
	}
	maxAttempts := maxTasks * 3
	if maxAttempts > 1<<22 {
		maxAttempts = 1 << 22
	}

	unknownReservation := ceilRatio(targetBytes, maxActive)
	if unknownReservation < 1<<20 {
		unknownReservation = 1 << 20
	}
	margin := policy.MaxMemoryBytes / 1000
	if margin < 1<<20 {
		margin = 1 << 20
	}
	if margin > 64<<20 {
		margin = 64 << 20
	}

	quantile := uint32(10000 - greed*10)
	retryDelay := 250*time.Millisecond - time.Duration(greed)*2*time.Millisecond
	controlInterval := 200*time.Millisecond - time.Duration(greed)*1500*time.Microsecond
	stableSampleEvery := uint32(8 - greed*6/100)
	speculativeMaxEvictions := uint32(0)
	if greed > 0 {
		speculativeMaxEvictions = 1
	}
	if greed > 50 {
		speculativeMaxEvictions = 2
	}
	minimumAttempts := uint64(max(32, uint32(maxActive)))
	maxAggregateSamples := uint32(maxTasks)
	if maxAggregateSamples < 4096 {
		maxAggregateSamples = 4096
	}

	effective := EffectivePolicy{
		Version: ProductionPolicyVersion, MaxMemoryBytes: policy.MaxMemoryBytes, MaxCPU: policy.MaxCPU, Greed: policy.Greed,
		CPUPeriodMicros: productionCPUPeriodMicros, CPUQuotaMicros: uint64(policy.MaxCPU) * productionCPUPeriodMicros, MaxActive: uint32(maxActive),
		TargetMemoryBPS: targetBPS, HighMemoryBPS: highBPS, CriticalMemoryBPS: criticalBPS,
		UnknownReservationBytes: unknownReservation, PerAttemptMarginBytes: margin, SpeculativeMaxEvictions: speculativeMaxEvictions,
		DefaultRetryDelay: retryDelay, ControlInterval: controlInterval,
	}
	effective.Scheduler = Config{
		TargetBytes: targetBytes, HighBytes: highBytes, CriticalBytes: criticalBytes, HardBytes: policy.MaxMemoryBytes,
		MaxTasks: uint32(maxTasks), MaxAttempts: uint32(maxAttempts), RetryMarginBytes: margin, DefaultRetryDelay: retryDelay, Clock: time.Now,
	}
	effective.Profiles = ProfileConfig{
		HardBytes: policy.MaxMemoryBytes, UnknownReservationBytes: unknownReservation, PerAttemptMarginBytes: margin,
		MaxProfiles: uint32(maxTasks), MaxTrackedAttempts: uint32(maxAttempts), MaxSamplesPerProfile: 128, MaxAggregateSamples: maxAggregateSamples,
		ColdRuns: 16, StableSampleEvery: stableSampleEvery, MinimumSamples: 16, ReservationQuantileBPS: quantile,
	}
	effective.GreedController = GreedConfig{
		InitialQuantileBPS: quantile, MinimumQuantileBPS: 9000, MaximumQuantileBPS: 10000, QuantileStepBPS: 100,
		TargetEvictionPPM: greed * 500, TargetUtilizationBPS: highBPS - 300,
		MaximumSomePSIAvg10BPS: 100 + greed*4, MaximumFullPSIAvg10BPS: 10 + greed,
		MinimumAttempts: minimumAttempts,
	}
	return effective, nil
}

func scaleBPS(value uint64, bps uint32) uint64 {
	return (value/10000)*uint64(bps) + (value%10000)*uint64(bps)/10000
}

func ceilRatio(numerator, denominator uint64) uint64 {
	return numerator/denominator + boolUint64(numerator%denominator != 0)
}

func boolUint64(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}
