package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"time"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

type densityChildInvocation struct {
	Envelope densityChildEnvelope
	Process  boundedChildResult
}

type densityChildInvoker func(context.Context, densitySweepSpec) (densityChildInvocation, error)

func validateLifecycleDensityOptions(options benchmarkOptions, child bool, goos string) error {
	if goos != "linux" {
		return errors.New("lifecycle-density benchmark is Linux-only")
	}
	if options.Kind != "lifecycle-density" || options.ArtifactPath == "" || options.ManifestPath == "" {
		return errors.New("lifecycle-density kind, artifact, and manifest are required")
	}
	validProduction := options.Class == "production-safe" &&
		(options.Strategy == "single-use-preinitialized" || options.Strategy == "cow-ready-single-use")
	validSpike := options.Class == "preinitialization-spike" &&
		(options.Strategy == "single-use-preinitialized" || options.Strategy == "single-use-preinitialized-shared-cache")
	validNumPyExperiment := options.Class == "profile-candidate" && options.PreparedWarmupProfile == "numpy-ready-v1" &&
		(options.Strategy == "single-use-preinitialized" || options.Strategy == "single-use-preinitialized-shared-cache" || options.Strategy == "cow-ready-single-use")
	if !validProduction && !validSpike && !validNumPyExperiment {
		return errors.New("lifecycle-density class, strategy, and prepared warmup are incompatible")
	}
	if !validNumPyExperiment && options.PreparedWarmupProfile != "" {
		return errors.New("prepared lifecycle-density warmup is restricted to the NumPy experiment")
	}
	if options.MaxRSSBytes == 0 || options.MaxRSSBytes > 1<<50 || options.ChildTimeout <= 0 || options.ChildTimeout > 24*time.Hour {
		return errors.New("lifecycle-density RSS guard or child timeout is missing or outside its hard bound")
	}
	if child {
		if !options.LifecycleDensityChild || options.OutputPath != "" || options.DensitySlots > uint(^uint32(0)) {
			return errors.New("lifecycle-density child mode or output boundary is invalid")
		}
		if _, err := preparedDensityShardCapacitiesForStrategy(uint32(options.DensitySlots), options.Strategy, validNumPyExperiment); err != nil {
			return err
		}
		return nil
	}
	if options.LifecycleDensityChild || options.OutputPath == "" || options.DensitySlots != 0 || options.Samples < 1 || options.Samples > 20 {
		return errors.New("lifecycle-density parent output, repeat count, or child-only options are invalid")
	}
	return nil
}

func runLifecycleDensityMain(options benchmarkOptions) error {
	artifact, artifactBytes, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return err
	}
	if options.PreparedWarmupProfile == "" && artifact.ArtifactProfile != "base" {
		return errors.New("lifecycle-density v1 requires the qualified base artifact profile")
	}
	if options.PreparedWarmupProfile != "" && artifact.ArtifactProfile != "numpy-core" {
		return errors.New("NumPy lifecycle-density requires the numpy-core artifact profile")
	}
	hostSource, err := currentHostSource()
	if err != nil {
		return err
	}
	var specs []densitySweepSpec
	if options.PreparedWarmupProfile != "" {
		specs, err = numpyDensitySweepSpecs(options.Strategy, uint32(options.Samples), options.MaxRSSBytes, options.ChildTimeout)
	} else {
		specs, err = densitySweepSpecs(options.Strategy, uint32(options.Samples), options.MaxRSSBytes, options.ChildTimeout)
	}
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve lifecycle-density executable: %w", err)
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("create lifecycle-density process nonce: %w", err)
	}
	runner := boundedChildRunner{executable: executable}
	evidence, encoded, err := assembleLifecycleDensityEvidence(
		context.Background(), artifact, artifactBytes, hostSource, options.Class, specs, nonce,
		func(ctx context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			return invokeOSDensityChild(ctx, runner, options.ArtifactPath, options.ManifestPath, options.Class, spec)
		},
	)
	if err != nil {
		return err
	}
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"output\":%q,\"source_commit\":%q,\"samples\":%d}\n", options.OutputPath, evidence.Artifact.SourceCommit, len(evidence.Samples))
	return nil
}

func assembleLifecycleDensityEvidence(
	ctx context.Context,
	artifact artifactIdentity,
	artifactBytes []byte,
	hostSource hostSourceIdentity,
	benchmarkClass string,
	specs []densitySweepSpec,
	nonce []byte,
	invoke densityChildInvoker,
) (runtimeevidence.LifecycleDensityEvidence, []byte, error) {
	if len(nonce) != 32 || invoke == nil || len(specs) == 0 {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("lifecycle-density parent configuration is incomplete")
	}
	numpyReady := specs[0].WarmupProfile != ""
	slotCount := 5
	if numpyReady {
		slotCount = 7
	}
	if len(specs)%slotCount != 0 ||
		(!numpyReady && benchmarkClass != "production-safe" && benchmarkClass != "preinitialization-spike") ||
		(numpyReady && benchmarkClass != "profile-candidate") {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("lifecycle-density parent class or matrix shape is invalid")
	}
	repeats := uint32(len(specs) / slotCount)
	var expected []densitySweepSpec
	var err error
	if numpyReady {
		expected, err = numpyDensitySweepSpecs(specs[0].Strategy, repeats, specs[0].MaxRSSBytes, specs[0].Timeout)
	} else {
		expected, err = densitySweepSpecs(specs[0].Strategy, repeats, specs[0].MaxRSSBytes, specs[0].Timeout)
	}
	if err != nil || !reflect.DeepEqual(specs, expected) {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("lifecycle-density child plan is noncanonical")
	}
	if artifact.Size <= 0 {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("lifecycle-density artifact size is invalid")
	}
	if numpyReady && artifact.ArtifactProfile != "numpy-core" {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("NumPy lifecycle-density artifact profile drifted")
	}
	if !numpyReady && artifact.ArtifactProfile != "base" {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("lifecycle-density v1 artifact profile drifted")
	}
	expectedWarmupGeneration := ""
	if numpyReady {
		digest := sha256.New()
		_, _ = digest.Write(artifactBytes)
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(specs[0].WarmupProfile))
		expectedWarmupGeneration = hex.EncodeToString(digest.Sum(nil))
	}

	samples := make([]runtimeevidence.LifecycleDensitySample, 0, len(specs))
	var backend runtimeevidence.BackendIdentity
	var environment runtimeevidence.EnvironmentIdentity
	for index, spec := range specs {
		invocation, err := invoke(ctx, spec)
		if err != nil {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d: %w", index, err)
		}
		if err := validateDensityChildEnvelope(invocation.Envelope, spec, artifact); err != nil {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d: %w", index, err)
		}
		if numpyReady {
			if invocation.Envelope.Warmup == nil || invocation.Envelope.Warmup.Profile != specs[0].WarmupProfile ||
				invocation.Envelope.Warmup.GenerationSHA256 != expectedWarmupGeneration {
				return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d warmup identity is not artifact-bound", index)
			}
		} else if invocation.Envelope.Warmup != nil {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d carries an unexpected warmup identity", index)
		}
		if invocation.Process.PID <= 0 || invocation.Process.StartedAtUnixNS <= 0 || invocation.Process.MaxObservedRSSBytes == 0 ||
			invocation.Process.MaxObservedRSSBytes > spec.MaxRSSBytes {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d process evidence or RSS guard drifted", index)
		}
		if index == 0 {
			backend = invocation.Envelope.Backend
			environment = invocation.Envelope.Environment
		} else if !reflect.DeepEqual(backend, invocation.Envelope.Backend) {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d backend identity drifted", index)
		} else if !reflect.DeepEqual(environment, invocation.Envelope.Environment) {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("lifecycle-density child %d environment identity drifted", index)
		}
		sample := invocation.Envelope.Sample
		sample.SampleIndex = spec.SampleIndex
		sample.RepeatIndex = spec.RepeatIndex
		sample.ProcessInstanceSHA256 = processInstanceSHA256(nonce, invocation.Process)
		samples = append(samples, sample)
	}

	peakRSS, err := peakDensityMetric(samples, func(sample runtimeevidence.LifecycleDensitySample) runtimeevidence.Metric {
		return sample.Process.RSSBytes
	})
	if err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, err
	}
	peakCgroup, err := peakDensityMetric(samples, func(sample runtimeevidence.LifecycleDensitySample) runtimeevidence.Metric {
		return sample.Cgroup.MemoryCurrentBytes
	})
	if err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, err
	}
	peakHeap, err := peakDensityMetric(samples, func(sample runtimeevidence.LifecycleDensitySample) runtimeevidence.Metric {
		return sample.GoRuntime.HeapLiveBytes
	})
	if err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, err
	}

	schemaVersion := 1
	workload := "idle-ready"
	slotCounts := []uint32{1, 2, 4, 8, 16}
	var warmup *runtimeevidence.PreparedWarmupIdentity
	if numpyReady {
		schemaVersion = 2
		workload = "numpy-ready-idle"
		slotCounts = []uint32{1, 2, 4, 8, 16, 32, 64}
		warmup = &runtimeevidence.PreparedWarmupIdentity{Profile: specs[0].WarmupProfile, GenerationSHA256: expectedWarmupGeneration}
	}
	evidence := runtimeevidence.LifecycleDensityEvidence{
		SchemaVersion: schemaVersion,
		EvidenceClass: "lifecycle-density",
		Artifact: runtimeevidence.ArtifactIdentity{
			Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size),
			SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile,
			Target: artifact.Target, ExecutionModel: artifact.Execution,
		},
		HostSource:  runtimeevidence.HostSourceIdentity{Revision: hostSource.Revision, Modified: hostSource.Modified},
		Backend:     backend,
		Environment: environment,
		Strategy: runtimeevidence.StrategyIdentity{
			Requested: specs[0].Strategy, Active: specs[0].Strategy, Fallback: false,
		},
		Warmup: warmup,
		Plan: runtimeevidence.SweepPlan{
			Workload: workload, SlotCounts: slotCounts, RepeatsPerSlot: repeats,
			FreshProcessPerSample: true, MaxProcessRSSBytes: specs[0].MaxRSSBytes,
			ChildTimeoutNS: uint64(specs[0].Timeout.Nanoseconds()),
		},
		Samples: samples,
		Summary: runtimeevidence.DerivedSummary{
			SampleCount: len(samples), PeakProcessRSSBytes: peakRSS,
			PeakCgroupMemoryCurrentBytes: peakCgroup, PeakGoHeapLiveBytes: peakHeap,
		},
		Limitations: []string{
			"Idle-ready evidence covers never-served prepared slots only; execution-time dirty growth is outside this density sweep.",
			"The parent samples child RSS and kills above the configured threshold; this is a bounded safety guard, not a kernel memory reservation.",
			"Cgroup counters remain unavailable unless isolation is independently proven; shared or unverified totals are not attributed to this process.",
		},
	}
	if numpyReady {
		evidence.Limitations = append(evidence.Limitations,
			"Both arms use the exact numpy-core artifact and numpy-ready-v1 warmup generation; COW warms once per canonical image while non-COW warms every independent slot.",
			"COW uses one runtime shard for each requested capacity; non-COW retains the four-slot hard bound and records all independent runtime shards.",
		)
	}
	if benchmarkClass == "preinitialization-spike" && specs[0].Strategy == "single-use-preinitialized-shared-cache" {
		evidence.Limitations = append(evidence.Limitations,
			"The first shard populates one borrowed in-memory compilation cache before followers start; every shard still owns a separate wazero runtime and closes before the cache owner.",
		)
	}
	if benchmarkClass == "preinitialization-spike" {
		evidence.Limitations = append(evidence.Limitations,
			"Preinitialization-spike lifecycle density is exploratory and does not approve the transformed artifact for default, release, deployment, or production-safe status.",
		)
	}
	if specs[0].Strategy == "cow-ready-single-use" {
		evidence.Limitations = append(evidence.Limitations,
			"COW mapping metrics aggregate only memfd:apyrun-cow-image VMAs; process metrics include compiled code, Go, WASI, Host state, page tables, and other mappings.",
			"Prepared retained guest bytes are logical linear-memory bytes and must not be interpreted as physical RSS, PSS, or private dirty bytes.",
			"Ready slots are never served in this idle-ready sweep; execution-time private dirty growth is outside this evidence.",
		)
	}
	if err := evidence.Validate(); err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("validate lifecycle-density evidence: %w", err)
	}
	if err := evidence.ValidateArtifactBytes(artifactBytes); err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("bind lifecycle-density artifact bytes: %w", err)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, err
	}
	encoded = append(encoded, '\n')
	if err := runtimeevidence.ValidateLifecycleDensityJSON(encoded); err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("semantic lifecycle-density JSON gate: %w", err)
	}
	return evidence, encoded, nil
}

func peakDensityMetric(samples []runtimeevidence.LifecycleDensitySample, selectMetric func(runtimeevidence.LifecycleDensitySample) runtimeevidence.Metric) (runtimeevidence.Metric, error) {
	if len(samples) == 0 {
		return runtimeevidence.Metric{}, errors.New("cannot derive lifecycle-density peak from empty samples")
	}
	first := selectMetric(samples[0])
	if first.Status != runtimeevidence.MetricMeasured {
		for _, sample := range samples[1:] {
			candidate := selectMetric(sample)
			if candidate.Status != first.Status || candidate.ReasonCode != first.ReasonCode || candidate.Value != nil || candidate.Model != "" {
				return runtimeevidence.Metric{}, errors.New("lifecycle-density metric availability drifted across child samples")
			}
		}
		return first, nil
	}
	if first.Value == nil {
		return runtimeevidence.Metric{}, errors.New("measured lifecycle-density metric lacks a value")
	}
	peak := *first.Value
	for _, sample := range samples[1:] {
		candidate := selectMetric(sample)
		if candidate.Status != runtimeevidence.MetricMeasured || candidate.Value == nil || candidate.ReasonCode != "" || candidate.Model != "" {
			return runtimeevidence.Metric{}, errors.New("lifecycle-density measured metric drifted across child samples")
		}
		if *candidate.Value > peak {
			peak = *candidate.Value
		}
	}
	return runtimeevidence.Metric{Status: runtimeevidence.MetricMeasured, Value: &peak}, nil
}

func invokeOSDensityChild(
	ctx context.Context,
	runner boundedChildRunner,
	artifactPath string,
	manifestPath string,
	benchmarkClass string,
	spec densitySweepSpec,
) (densityChildInvocation, error) {
	args := []string{
		"-lifecycle-density-child",
		"-kind", "lifecycle-density",
		"-class", benchmarkClass,
		"-artifact", artifactPath,
		"-manifest", manifestPath,
		"-strategy", spec.Strategy,
		"-density-slots", strconv.FormatUint(uint64(spec.RequestedSlots), 10),
		"-max-rss-bytes", strconv.FormatUint(spec.MaxRSSBytes, 10),
		"-child-timeout", spec.Timeout.String(),
	}
	if spec.WarmupProfile != "" {
		args = append(args, "-prepared-warmup-profile", spec.WarmupProfile)
	}
	result, err := runner.run(ctx, boundedChildSpec{
		args:    args,
		timeout: spec.Timeout, maxRSSBytes: spec.MaxRSSBytes,
	})
	if err != nil {
		return densityChildInvocation{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	var envelope densityChildEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return densityChildInvocation{}, fmt.Errorf("decode lifecycle-density child output: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return densityChildInvocation{}, errors.New("lifecycle-density child output has trailing JSON")
	}
	if err := validateDensityChildEnvelope(envelope, spec, artifactIdentity{SHA256: envelope.ArtifactSHA256, ArtifactProfile: envelope.ArtifactProfile}); err != nil {
		return densityChildInvocation{}, err
	}
	return densityChildInvocation{Envelope: envelope, Process: result}, nil
}
