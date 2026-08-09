package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

func aggregatePhase7PairedCellFragments(fragments []phase7PairedCellFragment, strategy string, repeats uint32) (runtimeevidence.LifecycleDensityEvidence, []byte, error) {
	expectedCount := int(7 * repeats)
	if (repeats != 1 && repeats != 3) || len(fragments) != expectedCount || (strategy != "cow-ready-single-use" && strategy != "single-use-preinitialized") {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 fragment aggregate configuration is invalid")
	}
	ordered := append([]phase7PairedCellFragment(nil), fragments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Cell.SampleIndex < ordered[j].Cell.SampleIndex })
	jobIDs := map[string]struct{}{}
	cgroups := map[string]struct{}{}
	first := ordered[0]
	for index := range ordered {
		fragment := ordered[index]
		if err := validatePhase7PairedCellFragment(fragment); err != nil {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("validate Phase 7 fragment %d: %w", index, err)
		}
		if fragment.Cell.SampleIndex != uint32(index) || fragment.Plan.RepeatsPerSlot != repeats {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 fragment coverage is incomplete or duplicated")
		}
		if fragment.Allocation.ArmOrder != first.Allocation.ArmOrder ||
			!reflect.DeepEqual(fragment.Artifact, first.Artifact) || !reflect.DeepEqual(fragment.HostSource, first.HostSource) ||
			!reflect.DeepEqual(fragment.Backend, first.Backend) || !reflect.DeepEqual(fragment.Environment, first.Environment) ||
			!reflect.DeepEqual(fragment.Warmup, first.Warmup) || !reflect.DeepEqual(fragment.Plan, first.Plan) {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 identity drifted across cell allocations")
		}
		if _, exists := jobIDs[fragment.Allocation.JobID]; exists {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 allocation job identity is duplicated")
		}
		if _, exists := cgroups[fragment.Allocation.CgroupPathSHA256]; exists {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 allocation cgroup identity is duplicated")
		}
		jobIDs[fragment.Allocation.JobID] = struct{}{}
		cgroups[fragment.Allocation.CgroupPathSHA256] = struct{}{}
	}
	arm := "cow"
	if strategy == "single-use-preinitialized" {
		arm = "non_cow"
	}
	samples := make([]runtimeevidence.LifecycleDensitySample, 0, expectedCount)
	boundaries := make([]runtimeevidence.LifecycleDensityBoundary, 0, repeats)
	for _, fragment := range ordered {
		var selected *phase7CellOutcome
		for index := range fragment.Outcomes {
			if fragment.Outcomes[index].Arm == arm {
				selected = &fragment.Outcomes[index]
				break
			}
		}
		if selected == nil {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 cell is missing the requested arm")
		}
		if selected.Sample != nil {
			samples = append(samples, *selected.Sample)
		} else if selected.Boundary != nil {
			boundaries = append(boundaries, *selected.Boundary)
		} else {
			return runtimeevidence.LifecycleDensityEvidence{}, nil, errors.New("Phase 7 cell outcome is empty")
		}
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
	evidence := runtimeevidence.LifecycleDensityEvidence{
		SchemaVersion: 3, EvidenceClass: "lifecycle-density", Artifact: first.Artifact, HostSource: first.HostSource,
		Backend: first.Backend, Environment: first.Environment,
		Strategy: runtimeevidence.StrategyIdentity{Requested: strategy, Active: strategy, Fallback: false},
		Warmup:   &runtimeevidence.PreparedWarmupIdentity{Profile: first.Warmup.Profile, GenerationSHA256: first.Warmup.GenerationSHA256},
		Plan:     first.Plan, Samples: samples, Boundaries: boundaries,
		Summary: runtimeevidence.DerivedSummary{SampleCount: len(samples), BoundaryCount: len(boundaries), PeakProcessRSSBytes: peakRSS, PeakCgroupMemoryCurrentBytes: peakCgroup, PeakGoHeapLiveBytes: peakHeap},
		Limitations: []string{
			"Idle-ready evidence covers never-served prepared slots only; execution-time dirty growth is outside this density sweep.",
			"The parent samples child RSS and kills above the configured threshold; this is a bounded safety guard, not a kernel memory reservation.",
			"Cgroup counters remain unavailable unless isolation is independently proven; shared or unverified totals are not attributed to this process.",
			"Both arms use the exact numpy-core artifact and numpy-ready-v1 warmup generation; COW warms once per canonical image while non-COW warms every independent slot.",
			"COW uses one runtime shard for each requested capacity; non-COW retains the four-slot hard bound and records all independent runtime shards.",
			"A non-COW 64-slot child that crosses the fixed RSS guard is retained only as an explicit rss_guard capacity boundary; it is never converted into a successful ready sample.",
		},
	}
	if strategy == "cow-ready-single-use" {
		evidence.Limitations = append(evidence.Limitations,
			"COW mapping metrics aggregate only memfd:apyrun-cow-image VMAs; process metrics include compiled code, Go, WASI, Host state, page tables, and other mappings.",
			"Prepared retained guest bytes are logical linear-memory bytes and must not be interpreted as physical RSS, PSS, or private dirty bytes.",
			"Ready slots are never served in this idle-ready sweep; execution-time private dirty growth is outside this evidence.")
	}
	if err := evidence.Validate(); err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("validate aggregated lifecycle-density evidence: %w", err)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, err
	}
	encoded = append(encoded, '\n')
	if err := runtimeevidence.ValidateLifecycleDensityJSON(encoded); err != nil {
		return runtimeevidence.LifecycleDensityEvidence{}, nil, fmt.Errorf("semantic aggregated lifecycle-density JSON gate: %w", err)
	}
	return evidence, encoded, nil
}
