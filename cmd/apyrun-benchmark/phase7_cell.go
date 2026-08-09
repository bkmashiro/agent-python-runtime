package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

const phase7CellChildTimeout = 4 * time.Minute

type phase7CellAllocationIdentity struct {
	JobID            string `json:"job_id"`
	ArrayJobID       string `json:"array_job_id"`
	ArrayTaskID      uint32 `json:"array_task_id"`
	CgroupPathSHA256 string `json:"cgroup_path_sha256"`
	ArmOrder         string `json:"arm_order"`
	Partition        string `json:"partition"`
	CPUsPerTask      uint32 `json:"cpus_per_task"`
	MemoryPerNodeMiB uint64 `json:"memory_per_node_mib"`
	GPUType          string `json:"gpu_type"`
	GPUs             uint32 `json:"gpus"`
}

type phase7CellIdentity struct {
	SampleIndex    uint32 `json:"sample_index"`
	RepeatIndex    uint32 `json:"repeat_index"`
	RequestedSlots uint32 `json:"requested_slots"`
}

type phase7CellOutcome struct {
	Arm      string                                    `json:"arm"`
	Strategy runtimeevidence.StrategyIdentity          `json:"strategy"`
	Status   string                                    `json:"status"`
	Sample   *runtimeevidence.LifecycleDensitySample   `json:"sample,omitempty"`
	Boundary *runtimeevidence.LifecycleDensityBoundary `json:"boundary,omitempty"`
}

type phase7PairedCellFragment struct {
	SchemaVersion int                                    `json:"schema_version"`
	EvidenceClass string                                 `json:"evidence_class"`
	Artifact      runtimeevidence.ArtifactIdentity       `json:"artifact"`
	HostSource    runtimeevidence.HostSourceIdentity     `json:"host_source"`
	Backend       runtimeevidence.BackendIdentity        `json:"backend"`
	Environment   runtimeevidence.EnvironmentIdentity    `json:"environment"`
	Warmup        runtimeevidence.PreparedWarmupIdentity `json:"warmup"`
	Plan          runtimeevidence.SweepPlan              `json:"plan"`
	Allocation    phase7CellAllocationIdentity           `json:"allocation"`
	Cell          phase7CellIdentity                     `json:"cell"`
	Outcomes      []phase7CellOutcome                    `json:"outcomes"`
}

func phase7CellSampleIndex(slots, repeat, repeats uint32) (uint32, error) {
	canonical := []uint32{1, 2, 4, 8, 16, 32, 64}
	if repeats != 1 && repeats != 3 {
		return 0, errors.New("Phase 7 cell repeats must be one or three")
	}
	if repeat >= repeats {
		return 0, errors.New("Phase 7 cell repeat is outside the plan")
	}
	for index, candidate := range canonical {
		if slots == candidate {
			return uint32(index)*repeats + repeat, nil
		}
	}
	return 0, errors.New("Phase 7 cell slot count is noncanonical")
}

func assemblePhase7PairedCellFragment(
	ctx context.Context,
	artifact artifactIdentity,
	artifactBytes []byte,
	hostSource hostSourceIdentity,
	slots uint32,
	repeat uint32,
	repeats uint32,
	order string,
	allocation phase7CellAllocationIdentity,
	nonce []byte,
	invoke densityChildInvoker,
) (phase7PairedCellFragment, []byte, error) {
	if len(nonce) != 32 || invoke == nil || artifact.Size <= 0 || artifact.ArtifactProfile != "numpy-core" {
		return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell producer configuration is incomplete")
	}
	sampleIndex, err := phase7CellSampleIndex(slots, repeat, repeats)
	if err != nil {
		return phase7PairedCellFragment{}, nil, err
	}
	if allocation.ArrayTaskID != sampleIndex || allocation.ArmOrder != order {
		return phase7PairedCellFragment{}, nil, errors.New("Phase 7 allocation identity does not match the cell")
	}
	strategies := []string{"cow-ready-single-use", "single-use-preinitialized"}
	arms := []string{"cow", "non_cow"}
	if order == "non-cow-first" {
		strategies[0], strategies[1] = strategies[1], strategies[0]
		arms[0], arms[1] = arms[1], arms[0]
	} else if order != "cow-first" {
		return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell arm order is unsupported")
	}
	artifactDigest := sha256.Sum256(artifactBytes)
	warmupDigest := sha256.New()
	_, _ = warmupDigest.Write(artifactDigest[:])
	_, _ = warmupDigest.Write([]byte{0})
	_, _ = warmupDigest.Write([]byte("numpy-ready-v1"))
	expectedWarmup := hex.EncodeToString(warmupDigest.Sum(nil))
	outcomes := make([]phase7CellOutcome, 0, 2)
	var backend runtimeevidence.BackendIdentity
	var environment runtimeevidence.EnvironmentIdentity
	haveMeasuredIdentity := false
	for index, strategy := range strategies {
		specs, err := numpyDensitySweepSpecs(strategy, repeats, 8589934592, phase7CellChildTimeout)
		if err != nil {
			return phase7PairedCellFragment{}, nil, err
		}
		spec := specs[sampleIndex]
		invocation, invokeErr := invoke(ctx, spec)
		outcome := phase7CellOutcome{Arm: arms[index], Strategy: runtimeevidence.StrategyIdentity{Requested: strategy, Active: strategy}}
		if invokeErr != nil {
			var guard *processRSSGuardError
			if strategy != "single-use-preinitialized" || slots != 64 || !errors.As(invokeErr, &guard) {
				return phase7PairedCellFragment{}, nil, fmt.Errorf("Phase 7 cell %s arm: %w", arms[index], invokeErr)
			}
			if invocation.Process.PID <= 0 || invocation.Process.StartedAtUnixNS <= 0 || guard.Observed != invocation.Process.MaxObservedRSSBytes ||
				guard.Observed > math.MaxInt64 || guard.Limit != spec.MaxRSSBytes || guard.Observed <= guard.Limit ||
				len(invocation.Process.Stdout) != 0 || invocation.Process.StderrBytes != 0 {
				return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell RSS boundary evidence drifted")
			}
			outcome.Status = "rss_guard"
			outcome.Boundary = &runtimeevidence.LifecycleDensityBoundary{SampleIndex: sampleIndex, RepeatIndex: repeat, RequestedSlots: slots, ProcessInstanceSHA256: processInstanceSHA256(nonce, invocation.Process), Status: "rss_guard", MaxObservedRSSBytes: guard.Observed, GuardRSSBytes: guard.Limit}
			outcomes = append(outcomes, outcome)
			continue
		}
		if err := validateDensityChildEnvelope(invocation.Envelope, spec, artifact); err != nil {
			return phase7PairedCellFragment{}, nil, fmt.Errorf("Phase 7 cell %s arm: %w", arms[index], err)
		}
		if invocation.Envelope.Warmup == nil || invocation.Envelope.Warmup.Profile != "numpy-ready-v1" || invocation.Envelope.Warmup.GenerationSHA256 != expectedWarmup {
			return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell warmup identity is not artifact-bound")
		}
		if invocation.Process.PID <= 0 || invocation.Process.StartedAtUnixNS <= 0 || invocation.Process.MaxObservedRSSBytes == 0 || invocation.Process.MaxObservedRSSBytes > spec.MaxRSSBytes {
			return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell process evidence or RSS guard drifted")
		}
		if !haveMeasuredIdentity {
			backend = invocation.Envelope.Backend
			environment = invocation.Envelope.Environment
			haveMeasuredIdentity = true
		} else if !reflect.DeepEqual(backend, invocation.Envelope.Backend) || !reflect.DeepEqual(environment, invocation.Envelope.Environment) {
			return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell backend or environment drifted across arms")
		}
		sample := invocation.Envelope.Sample
		sample.SampleIndex = sampleIndex
		sample.RepeatIndex = repeat
		sample.ProcessInstanceSHA256 = processInstanceSHA256(nonce, invocation.Process)
		outcome.Status = "measured"
		outcome.Sample = &sample
		outcomes = append(outcomes, outcome)
	}
	if !haveMeasuredIdentity {
		return phase7PairedCellFragment{}, nil, errors.New("Phase 7 cell has no measured arm identity")
	}
	fragment := phase7PairedCellFragment{
		SchemaVersion: 1, EvidenceClass: "phase7-paired-density-cell",
		Artifact:   runtimeevidence.ArtifactIdentity{Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size), SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile, Target: artifact.Target, ExecutionModel: artifact.Execution},
		HostSource: runtimeevidence.HostSourceIdentity{Revision: hostSource.Revision, Modified: hostSource.Modified}, Backend: backend, Environment: environment,
		Warmup:     runtimeevidence.PreparedWarmupIdentity{Profile: "numpy-ready-v1", GenerationSHA256: expectedWarmup},
		Plan:       runtimeevidence.SweepPlan{Workload: "numpy-ready-idle", SlotCounts: []uint32{1, 2, 4, 8, 16, 32, 64}, RepeatsPerSlot: repeats, FreshProcessPerSample: true, MaxProcessRSSBytes: 8589934592, ChildTimeoutNS: uint64(phase7CellChildTimeout.Nanoseconds())},
		Allocation: allocation, Cell: phase7CellIdentity{SampleIndex: sampleIndex, RepeatIndex: repeat, RequestedSlots: slots}, Outcomes: outcomes,
	}
	if err := validatePhase7PairedCellFragment(fragment); err != nil {
		return phase7PairedCellFragment{}, nil, err
	}
	encoded, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return phase7PairedCellFragment{}, nil, err
	}
	return fragment, append(encoded, '\n'), nil
}

func validatePhase7PairedCellFragment(fragment phase7PairedCellFragment) error {
	if fragment.SchemaVersion != 1 || fragment.EvidenceClass != "phase7-paired-density-cell" || fragment.Artifact.Filename == "" ||
		!lowerHexString(fragment.Artifact.SHA256, 64) || fragment.Artifact.SizeBytes == 0 || !lowerHexString(fragment.Artifact.SourceCommit, 40) ||
		fragment.Artifact.ArtifactProfile != "numpy-core" || fragment.Artifact.Target != "wasm32-wasip1" || fragment.Artifact.ExecutionModel != "reactor" ||
		!lowerHexString(fragment.HostSource.Revision, 40) || fragment.HostSource.Modified || fragment.Backend.Name != "wazero" || fragment.Backend.Version == "" || fragment.Backend.ResetMode != "fresh-instance" ||
		fragment.Environment.GOOS != "linux" || fragment.Environment.GOARCH == "" || fragment.Environment.GoVersion == "" || fragment.Environment.KernelRelease == "" || fragment.Environment.PageSizeBytes == 0 || fragment.Environment.CgroupVersion != "v2" {
		return errors.New("Phase 7 cell top-level identity is invalid")
	}
	if !canonicalPhase7SlurmDecimal(fragment.Allocation.JobID) || !canonicalPhase7SlurmDecimal(fragment.Allocation.ArrayJobID) || !lowerHexString(fragment.Allocation.CgroupPathSHA256, 64) ||
		(fragment.Allocation.ArmOrder != "cow-first" && fragment.Allocation.ArmOrder != "non-cow-first") || fragment.Allocation.Partition != "t4" ||
		fragment.Allocation.CPUsPerTask != 4 || fragment.Allocation.MemoryPerNodeMiB != 16384 || fragment.Allocation.GPUType != "tesla_t4" || fragment.Allocation.GPUs != 1 {
		return errors.New("Phase 7 cell allocation identity is invalid")
	}
	expectedIndex, err := phase7CellSampleIndex(fragment.Cell.RequestedSlots, fragment.Cell.RepeatIndex, fragment.Plan.RepeatsPerSlot)
	if err != nil || expectedIndex != fragment.Cell.SampleIndex || fragment.Allocation.ArrayTaskID != expectedIndex {
		return errors.New("Phase 7 cell key is invalid")
	}
	if fragment.Plan.Workload != "numpy-ready-idle" || !reflect.DeepEqual(fragment.Plan.SlotCounts, []uint32{1, 2, 4, 8, 16, 32, 64}) || !fragment.Plan.FreshProcessPerSample || fragment.Plan.MaxProcessRSSBytes != 8589934592 || fragment.Plan.ChildTimeoutNS != uint64(phase7CellChildTimeout.Nanoseconds()) {
		return errors.New("Phase 7 cell plan is invalid")
	}
	artifactDigest, err := hex.DecodeString(fragment.Artifact.SHA256)
	if err != nil {
		return errors.New("Phase 7 cell artifact digest is invalid")
	}
	warmupDigest := sha256.New()
	_, _ = warmupDigest.Write(artifactDigest)
	_, _ = warmupDigest.Write([]byte{0})
	_, _ = warmupDigest.Write([]byte("numpy-ready-v1"))
	if fragment.Warmup.Profile != "numpy-ready-v1" || fragment.Warmup.GenerationSHA256 != hex.EncodeToString(warmupDigest.Sum(nil)) || len(fragment.Outcomes) != 2 {
		return errors.New("Phase 7 cell warmup or outcome count is invalid")
	}
	expectedArms := []string{"cow", "non_cow"}
	if fragment.Allocation.ArmOrder == "non-cow-first" {
		expectedArms[0], expectedArms[1] = expectedArms[1], expectedArms[0]
	}
	processInstances := make(map[string]struct{}, 2)
	for index := range fragment.Outcomes {
		outcome := fragment.Outcomes[index]
		if outcome.Arm != expectedArms[index] {
			return errors.New("Phase 7 cell outcome order is invalid")
		}
		expectedStrategy := "cow-ready-single-use"
		if outcome.Arm == "non_cow" {
			expectedStrategy = "single-use-preinitialized"
		}
		if outcome.Strategy.Requested != expectedStrategy || outcome.Strategy.Active != expectedStrategy || outcome.Strategy.Fallback {
			return errors.New("Phase 7 cell strategy identity is invalid")
		}
		if outcome.Status == "measured" {
			if outcome.Sample == nil || outcome.Boundary != nil || outcome.Sample.SampleIndex != fragment.Cell.SampleIndex || outcome.Sample.RepeatIndex != fragment.Cell.RepeatIndex || outcome.Sample.RequestedSlots != fragment.Cell.RequestedSlots || !lowerHexString(outcome.Sample.ProcessInstanceSHA256, 64) {
				return errors.New("Phase 7 measured cell outcome is invalid")
			}
			if err := runtimeevidence.ValidateLifecycleDensitySampleFragment(*outcome.Sample, outcome.Strategy, fragment.Environment); err != nil {
				return err
			}
			if _, duplicate := processInstances[outcome.Sample.ProcessInstanceSHA256]; duplicate {
				return errors.New("Phase 7 cell reused a process identity across arms")
			}
			processInstances[outcome.Sample.ProcessInstanceSHA256] = struct{}{}
		} else if outcome.Status == "rss_guard" {
			if outcome.Arm != "non_cow" || fragment.Cell.RequestedSlots != 64 || outcome.Sample != nil || outcome.Boundary == nil || outcome.Boundary.SampleIndex != fragment.Cell.SampleIndex || outcome.Boundary.RepeatIndex != fragment.Cell.RepeatIndex || outcome.Boundary.RequestedSlots != 64 || outcome.Boundary.Status != "rss_guard" || outcome.Boundary.GuardRSSBytes != 8589934592 || outcome.Boundary.MaxObservedRSSBytes <= outcome.Boundary.GuardRSSBytes || !lowerHexString(outcome.Boundary.ProcessInstanceSHA256, 64) {
				return errors.New("Phase 7 RSS boundary cell outcome is invalid")
			}
			if err := runtimeevidence.ValidateLifecycleDensityBoundaryFragment(*outcome.Boundary, outcome.Strategy, fragment.Plan); err != nil {
				return err
			}
			if _, duplicate := processInstances[outcome.Boundary.ProcessInstanceSHA256]; duplicate {
				return errors.New("Phase 7 cell reused a process identity across arms")
			}
			processInstances[outcome.Boundary.ProcessInstanceSHA256] = struct{}{}
		} else {
			return errors.New("Phase 7 cell outcome status is invalid")
		}
	}
	return nil
}

func lowerHexString(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}
