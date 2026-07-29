package scheduler

import (
	"container/heap"
	"sort"
)

type EstimateSource string

const (
	EstimateExact    EstimateSource = "exact"
	EstimateWorkload EstimateSource = "workload"
	EstimateArtifact EstimateSource = "artifact"
	EstimateGlobal   EstimateSource = "global"
	EstimateUnknown  EstimateSource = "unknown"
)

type ReservationEstimate struct {
	ProfileKey         string
	Source             EstimateSource
	SampleCount        uint32
	QuantileBPS        uint32
	DirtyQuantileBytes uint64
	MarginBytes        uint64
	ReservationBytes   uint64
}

func (store *ProfileStore) Estimate(profile WorkloadProfile) (ReservationEstimate, error) {
	if store == nil {
		return ReservationEstimate{}, ErrInvalidProfile
	}
	profileKey, err := profile.Key()
	if err != nil {
		return ReservationEstimate{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.estimateLocked(profile, profileKey), nil
}

func (store *ProfileStore) estimateLocked(profile WorkloadProfile, profileKey string) ReservationEstimate {
	if exact := store.profiles[profileKey]; exact != nil && uint32(len(exact.samples)) >= store.config.MinimumSamples {
		return store.estimateFromSamples(profileKey, EstimateExact, append([]profileSample(nil), exact.samples...))
	}
	workload := store.collectRecent(func(candidate WorkloadProfile) bool {
		return candidate.ArtifactDigest == profile.ArtifactDigest && candidate.WorkloadClass == profile.WorkloadClass && candidate.PolicyClass == profile.PolicyClass
	})
	if uint32(len(workload)) >= store.config.MinimumSamples {
		return store.estimateFromSamples(profileKey, EstimateWorkload, workload)
	}
	artifact := store.collectRecent(func(candidate WorkloadProfile) bool {
		return candidate.ArtifactDigest == profile.ArtifactDigest && candidate.PolicyClass == profile.PolicyClass
	})
	if uint32(len(artifact)) >= store.config.MinimumSamples {
		return store.estimateFromSamples(profileKey, EstimateArtifact, artifact)
	}
	global := store.collectRecent(func(WorkloadProfile) bool { return true })
	if uint32(len(global)) >= store.config.MinimumSamples {
		return store.estimateFromSamples(profileKey, EstimateGlobal, global)
	}
	return ReservationEstimate{
		ProfileKey: profileKey, Source: EstimateUnknown, QuantileBPS: store.config.ReservationQuantileBPS,
		MarginBytes: store.config.PerAttemptMarginBytes, ReservationBytes: store.config.UnknownReservationBytes,
	}
}

func (store *ProfileStore) estimateFromSamples(profileKey string, source EstimateSource, samples []profileSample) ReservationEstimate {
	values := make([]uint64, len(samples))
	for index, sample := range samples {
		values[index] = sample.value
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	rank := (uint64(store.config.ReservationQuantileBPS)*uint64(len(values)) + 9999) / 10000
	if rank == 0 {
		rank = 1
	}
	dirty := values[rank-1]
	return ReservationEstimate{
		ProfileKey: profileKey, Source: source, SampleCount: uint32(len(values)), QuantileBPS: store.config.ReservationQuantileBPS,
		DirtyQuantileBytes: dirty, MarginBytes: store.config.PerAttemptMarginBytes,
		ReservationBytes: saturatingAdd(dirty, store.config.PerAttemptMarginBytes, store.config.HardBytes),
	}
}

func (store *ProfileStore) collectRecent(matches func(WorkloadProfile) bool) []profileSample {
	maximum := int(store.config.MaxAggregateSamples)
	selected := make(profileSampleMinHeap, 0, maximum)
	heap.Init(&selected)
	for _, record := range store.profiles {
		if !matches(record.profile) {
			continue
		}
		for _, sample := range record.samples {
			if len(selected) < maximum {
				heap.Push(&selected, sample)
				continue
			}
			if sample.sequence > selected[0].sequence {
				selected[0] = sample
				heap.Fix(&selected, 0)
			}
		}
	}
	return append([]profileSample(nil), selected...)
}

type profileSampleMinHeap []profileSample

func (values profileSampleMinHeap) Len() int { return len(values) }
func (values profileSampleMinHeap) Less(left, right int) bool {
	return values[left].sequence < values[right].sequence
}
func (values profileSampleMinHeap) Swap(left, right int) {
	values[left], values[right] = values[right], values[left]
}
func (values *profileSampleMinHeap) Push(value any) { *values = append(*values, value.(profileSample)) }
func (values *profileSampleMinHeap) Pop() any {
	old := *values
	last := old[len(old)-1]
	*values = old[:len(old)-1]
	return last
}
