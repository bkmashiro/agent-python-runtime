package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

type preparedDensityShardResult struct {
	capacity     uint32
	runner       preparedBenchmarkRunner
	observations []wazeroengine.Observation
	err          error
}

func densityExecutionStrategy(strategy string) (enginecontract.ExecutionStrategy, error) {
	switch strategy {
	case "cow-ready-single-use":
		return enginecontract.StrategyCOWReadySingleUse, nil
	case "single-use-preinitialized", "single-use-preinitialized-shared-cache":
		return enginecontract.StrategySingleUsePrepared, nil
	default:
		return "", fmt.Errorf("unsupported prepared density execution strategy %q", strategy)
	}
}

type preparedDensityShardFactory func(index int, capacity uint32) preparedDensityShardResult

func createPreparedDensityShards(
	capacities []uint32,
	strategy string,
	create preparedDensityShardFactory,
) ([]preparedDensityShardResult, error) {
	if len(capacities) == 0 || create == nil {
		return nil, errors.New("prepared density shard plan is incomplete")
	}
	sharedCache := strategy == "single-use-preinitialized-shared-cache"
	if !sharedCache && strategy != "single-use-preinitialized" && strategy != "cow-ready-single-use" {
		return nil, fmt.Errorf("unsupported prepared density shard strategy %q", strategy)
	}
	shards := make([]preparedDensityShardResult, len(capacities))
	firstFollower := 0
	if sharedCache {
		shards[0] = create(0, capacities[0])
		if shards[0].err != nil {
			return shards, nil
		}
		firstFollower = 1
	}
	results := make(chan struct {
		index int
		shard preparedDensityShardResult
	}, len(capacities)-firstFollower)
	for index := firstFollower; index < len(capacities); index++ {
		capacity := capacities[index]
		go func() {
			results <- struct {
				index int
				shard preparedDensityShardResult
			}{index: index, shard: create(index, capacity)}
		}()
	}
	for index := firstFollower; index < len(capacities); index++ {
		result := <-results
		shards[result.index] = result.shard
	}
	return shards, nil
}

func collectPreparedDensityChild(parent context.Context, artifactPath, manifestPath string, spec densitySweepSpec) (densityChildEnvelope, error) {
	if runtime.GOOS != "linux" {
		return densityChildEnvelope{}, errors.New("lifecycle-density collection is Linux-only")
	}
	if spec.Strategy != "single-use-preinitialized" && spec.Strategy != "single-use-preinitialized-shared-cache" && spec.Strategy != "cow-ready-single-use" {
		return densityChildEnvelope{}, errors.New("prepared lifecycle-density child requires a supported single-use preinitialized strategy")
	}
	artifact, wasm, err := loadArtifactIdentity(artifactPath, manifestPath)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	extended := spec.WarmupProfile != ""
	switch artifact.ArtifactProfile {
	case "base":
		if extended {
			return densityChildEnvelope{}, errors.New("base lifecycle-density artifact cannot carry a prepared warmup")
		}
	case "numpy-core":
		if spec.WarmupProfile != wazeroengine.COWWarmupNumPyReadyV1 {
			return densityChildEnvelope{}, errors.New("NumPy lifecycle-density requires numpy-ready-v1 warmup")
		}
	default:
		return densityChildEnvelope{}, errors.New("unsupported lifecycle-density artifact profile")
	}
	capacities, err := preparedDensityShardCapacitiesForStrategy(spec.RequestedSlots, spec.Strategy, extended)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	ctx, cancel := context.WithTimeout(parent, spec.Timeout)
	defer cancel()

	started := time.Now()
	executionStrategy, err := densityExecutionStrategy(spec.Strategy)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	var compilationCache *wazeroengine.CompilationCache
	if spec.Strategy == "single-use-preinitialized-shared-cache" {
		compilationCache = wazeroengine.NewCompilationCache()
		defer compilationCache.Close(context.Background())
	}
	shards, err := createPreparedDensityShards(
		capacities,
		spec.Strategy,
		func(_ int, capacity uint32) preparedDensityShardResult {
			collector := &lifecycleCollector{}
			config := runtimeconfig.DefaultRunConfig()
			config.Timeout = spec.Timeout
			factory := wazeroengine.Factory{
				PreparedCapacity: capacity,
				Strategy:         executionStrategy,
				Observer:         collector.observe,
				CompilationCache: compilationCache,
			}
			if spec.Strategy == "cow-ready-single-use" {
				factory.COWWarmupProfile = spec.WarmupProfile
			} else {
				factory.PreparedWarmupProfile = spec.WarmupProfile
			}
			neutralRunner, err := factory.New(ctx, wasm, config)
			shard := preparedDensityShardResult{capacity: capacity, observations: collector.drain(), err: err}
			if err == nil {
				var ok bool
				shard.runner, ok = neutralRunner.(preparedBenchmarkRunner)
				if !ok {
					shard.err = errors.New("wazero prepared diagnostics are unavailable")
					_ = neutralRunner.Close(context.Background())
				}
			}
			return shard
		},
	)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	readyElapsed := time.Since(started)
	for index := range shards {
		if shards[index].runner != nil {
			defer shards[index].runner.Close(context.Background())
		}
	}
	for index := range shards {
		if shards[index].err != nil {
			return densityChildEnvelope{}, fmt.Errorf("create prepared density shard %d: %w", index, shards[index].err)
		}
		if shards[index].runner == nil || shards[index].runner.PreparedReady() != int(shards[index].capacity) || shards[index].runner.PreparedRetainedGuestMemoryBytes() == 0 {
			return densityChildEnvelope{}, fmt.Errorf("prepared density shard %d is not fully ready", index)
		}
	}
	var warmup *runtimeevidence.PreparedWarmupIdentity
	if extended {
		for index := range shards {
			reporter, ok := shards[index].runner.(interface {
				PreparedWarmupState() wazeroengine.PreparedWarmupState
			})
			if !ok {
				return densityChildEnvelope{}, fmt.Errorf("prepared density shard %d lacks warmup identity", index)
			}
			state := reporter.PreparedWarmupState()
			if state.Profile != spec.WarmupProfile || len(state.GenerationSHA256) != 64 {
				return densityChildEnvelope{}, fmt.Errorf("prepared density shard %d warmup identity drifted", index)
			}
			candidate := &runtimeevidence.PreparedWarmupIdentity{Profile: state.Profile, GenerationSHA256: state.GenerationSHA256}
			if warmup == nil {
				warmup = candidate
			} else if *warmup != *candidate {
				return densityChildEnvelope{}, errors.New("prepared density warmup generation drifted across runtime shards")
			}
		}
	}

	phases, err := preparedDensityPhasesWithWarmup(shards, readyElapsed, spec.Strategy, spec.WarmupProfile)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	goMetrics, err := runtimeevidence.CollectGoRuntimeMetrics()
	if err != nil {
		return densityChildEnvelope{}, err
	}
	linuxSnapshot, err := runtimeevidence.DefaultLinuxCollector().Collect()
	if err != nil {
		return densityChildEnvelope{}, err
	}
	var cowMappings *runtimeevidence.MappingMetrics
	if spec.Strategy == "cow-ready-single-use" {
		mappingMetrics, err := runtimeevidence.DefaultLinuxCollector().CollectNamedMappings("memfd:apyrun-cow-image")
		if err != nil {
			return densityChildEnvelope{}, err
		}
		if mappingMetrics.MappingCount != spec.RequestedSlots {
			return densityChildEnvelope{}, fmt.Errorf("COW mapping count=%d, want ready slots=%d", mappingMetrics.MappingCount, spec.RequestedSlots)
		}
		cowMappings = &mappingMetrics
	}
	if linuxSnapshot.Process.RSSBytes.Value == nil || *linuxSnapshot.Process.RSSBytes.Value > spec.MaxRSSBytes {
		return densityChildEnvelope{}, fmt.Errorf("lifecycle-density safety guard: ready RSS exceeds %d", spec.MaxRSSBytes)
	}
	version, err := dependencyVersion("github.com/tetratelabs/wazero")
	if err != nil {
		return densityChildEnvelope{}, err
	}
	properties := shards[0].runner.Properties()
	if err := properties.Validate(); err != nil || properties.Backend != "wazero" || properties.ResetMode != enginecontract.ResetModeFreshInstance {
		return densityChildEnvelope{}, errors.New("prepared density child backend properties drifted")
	}
	if spec.Strategy == "cow-ready-single-use" {
		if properties.RequestedStrategy != enginecontract.StrategyCOWReadySingleUse ||
			properties.ActiveStrategy != enginecontract.StrategyCOWReadySingleUse || properties.Fallback {
			return densityChildEnvelope{}, errors.New("COW density child strategy drifted or fell back")
		}
	} else if properties.RequestedStrategy != enginecontract.StrategySingleUsePrepared ||
		properties.ActiveStrategy != enginecontract.StrategySingleUsePrepared || properties.Fallback {
		return densityChildEnvelope{}, errors.New("non-COW prepared density child strategy drifted or fell back")
	}
	observedAt := uint64(time.Now().UnixNano())
	return densityChildEnvelope{
		ProtocolVersion: 1,
		ArtifactSHA256:  artifact.SHA256,
		ArtifactProfile: artifact.ArtifactProfile,
		Backend: runtimeevidence.BackendIdentity{
			Name: properties.Backend, Version: version, ResetMode: string(properties.ResetMode),
		},
		Environment: linuxSnapshot.Environment,
		Strategy: runtimeevidence.StrategyIdentity{
			Requested: spec.Strategy, Active: spec.Strategy, Fallback: false,
		},
		Warmup: warmup,
		Sample: runtimeevidence.LifecycleDensitySample{
			RequestedSlots:    spec.RequestedSlots,
			RuntimeShards:     uint32(len(shards)),
			ActiveConcurrency: 0,
			ObservedAtUnixNS:  runtimeevidence.Metric{Status: runtimeevidence.MetricTimestampObserved, Value: &observedAt},
			Pool: runtimeevidence.PoolState{
				TargetCapacity: spec.RequestedSlots,
				Ready:          spec.RequestedSlots,
				AccountedSlots: spec.RequestedSlots,
			},
			Phases:      phases,
			GoRuntime:   goMetrics,
			Process:     linuxSnapshot.Process,
			COWMappings: cowMappings,
			Cgroup:      linuxSnapshot.Cgroup,
		},
	}, nil
}

func preparedDensityPhases(shards []preparedDensityShardResult, readyElapsed time.Duration, strategy string) (runtimeevidence.PhaseTimings, error) {
	return preparedDensityPhasesWithWarmup(shards, readyElapsed, strategy, "")
}

func preparedDensityPhasesWithWarmup(shards []preparedDensityShardResult, readyElapsed time.Duration, strategy, warmupProfile string) (runtimeevidence.PhaseTimings, error) {
	if len(shards) == 0 || readyElapsed <= 0 {
		return runtimeevidence.PhaseTimings{}, errors.New("prepared density phases lack shards or ready elapsed time")
	}
	var instantiateNS uint64
	var initializeNS uint64
	var runtimeInitNS uint64
	var compileNS uint64
	var prepareNS uint64
	var warmupNS uint64
	for shardIndex, shard := range shards {
		wantCounts := map[string]uint32{
			"instantiate_host": 1,
			"compile":          1,
		}
		if strategy == "cow-ready-single-use" {
			wantCounts["cow_image_instantiate_guest"] = 1
			wantCounts["cow_image__initialize"] = 1
			wantCounts["cow_image_runtime_init"] = 1
			wantCounts["pool_prepare_instantiate_guest"] = shard.capacity
			wantCounts["pool_prepare__initialize"] = shard.capacity
			wantCounts["pool_prepare_cow_restore"] = shard.capacity
			if warmupProfile != "" {
				wantCounts["cow_image_warmup"] = 1
			}
		} else if strategy == "single-use-preinitialized" || strategy == "single-use-preinitialized-shared-cache" {
			wantCounts["pool_prepare_instantiate_guest"] = shard.capacity
			wantCounts["pool_prepare__initialize"] = shard.capacity
			wantCounts["pool_prepare_runtime_init"] = shard.capacity
			if warmupProfile != "" {
				wantCounts["pool_prepare_warmup"] = shard.capacity
			}
		} else {
			return runtimeevidence.PhaseTimings{}, fmt.Errorf("unsupported prepared density phase strategy %q", strategy)
		}
		seen := make(map[string]uint32, len(wantCounts))
		for _, observation := range shard.observations {
			want, expected := wantCounts[observation.Phase]
			if !expected || !observation.Success || observation.Duration <= 0 || seen[observation.Phase] >= want {
				return runtimeevidence.PhaseTimings{}, fmt.Errorf("unexpected prepared density observation in shard %d: %#v", shardIndex, observation)
			}
			seen[observation.Phase]++
			value := uint64(observation.Duration.Nanoseconds())
			switch observation.Phase {
			case "compile":
				if math.MaxUint64-compileNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared compile duration overflow")
				}
				compileNS += value
			case "pool_prepare_instantiate_guest", "cow_image_instantiate_guest":
				if math.MaxUint64-instantiateNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared instantiate duration overflow")
				}
				instantiateNS += value
			case "pool_prepare__initialize", "cow_image__initialize":
				if math.MaxUint64-initializeNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared initialize duration overflow")
				}
				initializeNS += value
			case "pool_prepare_runtime_init", "cow_image_runtime_init":
				if math.MaxUint64-runtimeInitNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared runtime-init duration overflow")
				}
				runtimeInitNS += value
			case "pool_prepare_cow_restore":
				if math.MaxUint64-prepareNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("COW restore duration overflow")
				}
				prepareNS += value
			case "pool_prepare_warmup", "cow_image_warmup":
				if math.MaxUint64-warmupNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared warmup duration overflow")
				}
				warmupNS += value
			}
		}
		for phase, count := range wantCounts {
			if seen[phase] != count {
				return runtimeevidence.PhaseTimings{}, fmt.Errorf("prepared density shard %d phase %s count=%d, want %d", shardIndex, phase, seen[phase], count)
			}
		}
	}
	totalNS := uint64(readyElapsed.Nanoseconds())
	compileMetric := densityMeasured(compileNS)
	prepareMetric := densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun)
	if strategy == "cow-ready-single-use" {
		prepareMetric = densityMeasured(prepareNS)
	}
	phases := runtimeevidence.PhaseTimings{
		QueueNS:       densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonNotApplicable),
		CompileNS:     &compileMetric,
		InstantiateNS: densityMeasured(instantiateNS),
		InitializeNS:  densityMeasured(initializeNS),
		RuntimeInitNS: densityMeasured(runtimeInitNS),
		PrepareNS:     prepareMetric,
		ExecuteNS:     densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun),
		CapabilityNS:  densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun),
		TotalNS:       densityMeasured(totalNS),
	}
	if warmupProfile != "" {
		warmupMetric := densityMeasured(warmupNS)
		phases.WarmupNS = &warmupMetric
	}
	return phases, nil
}

func densityMeasured(value uint64) runtimeevidence.Metric {
	return runtimeevidence.Metric{Status: runtimeevidence.MetricMeasured, Value: &value}
}

func densityUnavailable(status runtimeevidence.MetricStatus, reason runtimeevidence.UnavailableReason) runtimeevidence.Metric {
	return runtimeevidence.Metric{Status: status, ReasonCode: reason}
}

func dependencyVersion(modulePath string) (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("Go build information is unavailable")
	}
	for _, dependency := range info.Deps {
		if dependency.Path != modulePath {
			continue
		}
		if dependency.Replace != nil {
			dependency = dependency.Replace
		}
		if dependency.Version == "" || dependency.Version == "(devel)" {
			return "", fmt.Errorf("dependency %s version is unavailable", modulePath)
		}
		return dependency.Version, nil
	}
	return "", fmt.Errorf("dependency %s is absent from Go build information", modulePath)
}
