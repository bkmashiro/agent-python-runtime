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

func collectPreparedDensityChild(parent context.Context, artifactPath, manifestPath string, spec densitySweepSpec) (densityChildEnvelope, error) {
	if runtime.GOOS != "linux" {
		return densityChildEnvelope{}, errors.New("lifecycle-density collection is Linux-only")
	}
	if spec.Strategy != "single-use-preinitialized" {
		return densityChildEnvelope{}, errors.New("prepared lifecycle-density child requires single-use-preinitialized strategy")
	}
	artifact, wasm, err := loadArtifactIdentity(artifactPath, manifestPath)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	if artifact.ArtifactProfile != "base" {
		return densityChildEnvelope{}, errors.New("initial lifecycle-density sweep is restricted to the qualified base profile")
	}
	capacities, err := preparedDensityShardCapacities(spec.RequestedSlots)
	if err != nil {
		return densityChildEnvelope{}, err
	}
	ctx, cancel := context.WithTimeout(parent, spec.Timeout)
	defer cancel()

	started := time.Now()
	results := make(chan struct {
		index int
		shard preparedDensityShardResult
	}, len(capacities))
	for index, capacity := range capacities {
		go func() {
			collector := &lifecycleCollector{}
			config := runtimeconfig.DefaultRunConfig()
			config.Timeout = spec.Timeout
			neutralRunner, err := (wazeroengine.Factory{PreparedCapacity: capacity, Observer: collector.observe}).New(ctx, wasm, config)
			shard := preparedDensityShardResult{capacity: capacity, observations: collector.drain(), err: err}
			if err == nil {
				var ok bool
				shard.runner, ok = neutralRunner.(preparedBenchmarkRunner)
				if !ok {
					shard.err = errors.New("wazero prepared diagnostics are unavailable")
					_ = neutralRunner.Close(context.Background())
				}
			}
			results <- struct {
				index int
				shard preparedDensityShardResult
			}{index: index, shard: shard}
		}()
	}
	shards := make([]preparedDensityShardResult, len(capacities))
	for range capacities {
		result := <-results
		shards[result.index] = result.shard
	}
	readyElapsed := time.Since(started)
	for index := range shards {
		if shards[index].runner != nil {
			defer shards[index].runner.Close(context.Background())
		}
		if shards[index].err != nil {
			return densityChildEnvelope{}, fmt.Errorf("create prepared density shard %d: %w", index, shards[index].err)
		}
		if shards[index].runner.PreparedReady() != int(shards[index].capacity) || shards[index].runner.PreparedRetainedGuestMemoryBytes() == 0 {
			return densityChildEnvelope{}, fmt.Errorf("prepared density shard %d is not fully ready", index)
		}
	}

	phases, err := preparedDensityPhases(shards, readyElapsed)
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
	if linuxSnapshot.Process.RSSBytes.Value == nil || *linuxSnapshot.Process.RSSBytes.Value > spec.MaxRSSBytes {
		return densityChildEnvelope{}, fmt.Errorf("lifecycle-density safety guard: ready RSS exceeds %d", spec.MaxRSSBytes)
	}
	version, err := dependencyVersion("github.com/tetratelabs/wazero")
	if err != nil {
		return densityChildEnvelope{}, err
	}
	properties := shards[0].runner.Properties()
	if properties.Backend != "wazero" || properties.ResetMode != enginecontract.ResetModeFreshInstance {
		return densityChildEnvelope{}, errors.New("prepared density child backend properties drifted")
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
			Phases:    phases,
			GoRuntime: goMetrics,
			Process:   linuxSnapshot.Process,
			Cgroup:    linuxSnapshot.Cgroup,
		},
	}, nil
}

func preparedDensityPhases(shards []preparedDensityShardResult, readyElapsed time.Duration) (runtimeevidence.PhaseTimings, error) {
	if len(shards) == 0 || readyElapsed <= 0 {
		return runtimeevidence.PhaseTimings{}, errors.New("prepared density phases lack shards or ready elapsed time")
	}
	var instantiateNS uint64
	var initializeNS uint64
	var runtimeInitNS uint64
	for shardIndex, shard := range shards {
		wantCounts := map[string]uint32{
			"instantiate_host":               1,
			"compile":                        1,
			"pool_prepare_instantiate_guest": shard.capacity,
			"pool_prepare__initialize":       shard.capacity,
			"pool_prepare_runtime_init":      shard.capacity,
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
			case "pool_prepare_instantiate_guest":
				if math.MaxUint64-instantiateNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared instantiate duration overflow")
				}
				instantiateNS += value
			case "pool_prepare__initialize":
				if math.MaxUint64-initializeNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared initialize duration overflow")
				}
				initializeNS += value
			case "pool_prepare_runtime_init":
				if math.MaxUint64-runtimeInitNS < value {
					return runtimeevidence.PhaseTimings{}, errors.New("prepared runtime-init duration overflow")
				}
				runtimeInitNS += value
			}
		}
		for phase, count := range wantCounts {
			if seen[phase] != count {
				return runtimeevidence.PhaseTimings{}, fmt.Errorf("prepared density shard %d phase %s count=%d, want %d", shardIndex, phase, seen[phase], count)
			}
		}
	}
	totalNS := uint64(readyElapsed.Nanoseconds())
	return runtimeevidence.PhaseTimings{
		QueueNS:       densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonNotApplicable),
		InstantiateNS: densityMeasured(instantiateNS),
		InitializeNS:  densityMeasured(initializeNS),
		RuntimeInitNS: densityMeasured(runtimeInitNS),
		PrepareNS:     densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun),
		ExecuteNS:     densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun),
		CapabilityNS:  densityUnavailable(runtimeevidence.MetricSkipped, runtimeevidence.ReasonWorkloadNotRun),
		TotalNS:       densityMeasured(totalNS),
	}, nil
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
