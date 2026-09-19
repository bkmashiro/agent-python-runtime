package mechanismcampaign

import "sort"

type MatchedPair struct {
	PairIndex          int    `json:"pair_index"`
	FirstLane          string `json:"first_lane"`
	BaselineLatencyNS  uint64 `json:"baseline_latency_ns"`
	OptimizedLatencyNS uint64 `json:"optimized_latency_ns"`
	ResultSHA256       string `json:"result_sha256"`
}

type MatchedControlResult struct {
	Pairs             []MatchedPair `json:"pairs"`
	BaselineMedianNS  uint64        `json:"baseline_median_ns"`
	OptimizedMedianNS uint64        `json:"optimized_median_ns"`
	SavingsNS         int64         `json:"savings_ns"`
}

func medianUint64(values []uint64) uint64 {
	copyValues := append([]uint64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[middle]
	}
	return (copyValues[middle-1] + copyValues[middle]) / 2
}
