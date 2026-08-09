package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func runPhase7PairedCellMain(options benchmarkOptions, goos string) error {
	if err := validatePhase7PairedCellOptions(options, goos); err != nil {
		return err
	}
	artifact, artifactBytes, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return err
	}
	if artifact.ArtifactProfile != "numpy-core" {
		return errors.New("Phase 7 paired cell requires the numpy-core artifact profile")
	}
	hostSource, err := currentHostSource()
	if err != nil {
		return err
	}
	allocation, err := phase7AllocationIdentityFromEnvironment(os.Getenv, "/proc/self/cgroup")
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve Phase 7 cell executable: %w", err)
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("create Phase 7 cell process nonce: %w", err)
	}
	runner := boundedChildRunner{executable: executable}
	fragment, encoded, err := assemblePhase7PairedCellFragment(
		context.Background(), artifact, artifactBytes, hostSource,
		uint32(options.DensitySlots), uint32(options.DensityRepeat), uint32(options.Samples), options.ArmOrder,
		allocation, nonce,
		func(ctx context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			return invokeOSDensityChild(ctx, runner, options.ArtifactPath, options.ManifestPath, "profile-candidate", spec)
		},
	)
	if err != nil {
		return err
	}
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"output\":%q,\"source_commit\":%q,\"sample_index\":%d,\"job_id\":%q}\n", options.OutputPath, fragment.HostSource.Revision, fragment.Cell.SampleIndex, fragment.Allocation.JobID)
	return nil
}

func validatePhase7PairedCellOptions(options benchmarkOptions, goos string) error {
	if goos != "linux" {
		return errors.New("Phase 7 paired-cell benchmark is Linux-only")
	}
	if options.Kind != "phase7-paired-density-cell" || options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath == "" ||
		options.InputPath != "" || options.SchemaPath != "" || options.Class != "profile-candidate" || options.Strategy != "fresh" ||
		options.PreparedWarmupProfile != "numpy-ready-v1" || options.LifecycleDensityChild || options.DensitySlots == 0 ||
		options.DensitySlots > uint(^uint32(0)) || options.DensityRepeat > uint(^uint32(0)) ||
		(options.ArmOrder != "cow-first" && options.ArmOrder != "non-cow-first") || (options.Samples != 1 && options.Samples != 3) ||
		options.MaxRSSBytes != 8589934592 || options.ChildTimeout != phase7CellChildTimeout {
		return errors.New("Phase 7 paired-cell options are incomplete or noncanonical")
	}
	_, err := phase7CellSampleIndex(uint32(options.DensitySlots), uint32(options.DensityRepeat), uint32(options.Samples))
	return err
}

func phase7AllocationIdentityFromEnvironment(getenv func(string) string, cgroupPath string) (phase7CellAllocationIdentity, error) {
	if getenv == nil {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 Slurm environment reader is missing")
	}
	required := []string{"SLURM_JOB_ID", "SLURM_ARRAY_JOB_ID", "SLURM_ARRAY_TASK_ID", "SLURM_JOB_PARTITION", "SLURM_CPUS_PER_TASK", "SLURM_MEM_PER_NODE", "SLURM_GPUS_ON_NODE", "P7_ARM_ORDER"}
	values := make(map[string]string, len(required))
	for _, key := range required {
		values[key] = getenv(key)
		if values[key] == "" {
			return phase7CellAllocationIdentity{}, fmt.Errorf("Phase 7 Slurm environment is missing %s", key)
		}
	}
	parseUint := func(key string, width int) (uint64, error) {
		value, err := strconv.ParseUint(values[key], 10, width)
		if err != nil || strconv.FormatUint(value, 10) != values[key] {
			return 0, fmt.Errorf("Phase 7 Slurm environment %s is invalid", key)
		}
		return value, nil
	}
	jobID, err := parseUint("SLURM_JOB_ID", 64)
	if err != nil || jobID == 0 {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 Slurm environment SLURM_JOB_ID is invalid")
	}
	arrayJobID, err := parseUint("SLURM_ARRAY_JOB_ID", 64)
	if err != nil || arrayJobID == 0 {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 Slurm environment SLURM_ARRAY_JOB_ID is invalid")
	}
	task, err := parseUint("SLURM_ARRAY_TASK_ID", 32)
	if err != nil {
		return phase7CellAllocationIdentity{}, err
	}
	cpus, err := parseUint("SLURM_CPUS_PER_TASK", 32)
	if err != nil {
		return phase7CellAllocationIdentity{}, err
	}
	memory := uint64(16384)
	if values["SLURM_MEM_PER_NODE"] != "16384" && values["SLURM_MEM_PER_NODE"] != "16G" {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 Slurm environment SLURM_MEM_PER_NODE is invalid")
	}
	gpus, err := parseUint("SLURM_GPUS_ON_NODE", 32)
	if err != nil {
		return phase7CellAllocationIdentity{}, err
	}
	cgroupBytes, err := readPhase7CgroupV2PathBounded(cgroupPath)
	if err != nil {
		return phase7CellAllocationIdentity{}, fmt.Errorf("read Phase 7 allocation cgroup: %w", err)
	}
	if !bytes.HasPrefix(cgroupBytes, []byte("0::/")) || bytes.Count(cgroupBytes, []byte("\n")) > 1 || len(strings.TrimSpace(string(cgroupBytes))) == 0 {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 allocation is not in one canonical cgroup v2 path")
	}
	digest := sha256.Sum256(cgroupBytes)
	allocation := phase7CellAllocationIdentity{
		JobID: values["SLURM_JOB_ID"], ArrayJobID: values["SLURM_ARRAY_JOB_ID"], ArrayTaskID: uint32(task),
		CgroupPathSHA256: hex.EncodeToString(digest[:]), ArmOrder: values["P7_ARM_ORDER"], Partition: values["SLURM_JOB_PARTITION"],
		CPUsPerTask: uint32(cpus), MemoryPerNodeMiB: memory, GPUType: "tesla_t4", GPUs: uint32(gpus),
	}
	if allocation.Partition != "t4" || allocation.CPUsPerTask != 4 || allocation.MemoryPerNodeMiB != 16384 || allocation.GPUs != 1 ||
		(allocation.ArmOrder != "cow-first" && allocation.ArmOrder != "non-cow-first") {
		return phase7CellAllocationIdentity{}, errors.New("Phase 7 Slurm allocation resource shape drifted")
	}
	return allocation, nil
}

func readPhase7CgroupV2PathBounded(path string) ([]byte, error) {
	const maximum = 16 * 1024
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cgroup path must be a regular pseudo-file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() > maximum {
		return nil, errors.New("cgroup pseudo-file metadata is invalid")
	}
	document, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if len(document) == 0 || len(document) > maximum {
		return nil, errors.New("cgroup pseudo-file content is empty or oversized")
	}
	return document, nil
}
