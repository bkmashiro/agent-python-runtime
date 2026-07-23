package evidence

import (
	"errors"
	"fmt"
	"math"
	goruntime "runtime"
	runtimemetrics "runtime/metrics"
)

const (
	goHeapLiveMetric   = "/gc/heap/live:bytes"
	goHeapGoalMetric   = "/gc/heap/goal:bytes"
	goGCCyclesMetric   = "/gc/cycles/total:gc-cycles"
	goSchedulerLatency = "/sched/latencies:seconds"
)

func CollectGoRuntimeMetrics() (GoRuntimeMetrics, error) {
	samples := []runtimemetrics.Sample{
		{Name: goHeapLiveMetric},
		{Name: goHeapGoalMetric},
		{Name: goGCCyclesMetric},
		{Name: goSchedulerLatency},
	}
	runtimemetrics.Read(samples)
	values := make(map[string]runtimemetrics.Value, len(samples))
	for _, sample := range samples {
		values[sample.Name] = sample.Value
	}
	heapLive, err := runtimeUint64Metric(values, goHeapLiveMetric)
	if err != nil {
		return GoRuntimeMetrics{}, err
	}
	heapGoal, err := runtimeUint64Metric(values, goHeapGoalMetric)
	if err != nil {
		return GoRuntimeMetrics{}, err
	}
	gcCycles, err := runtimeUint64Metric(values, goGCCyclesMetric)
	if err != nil {
		return GoRuntimeMetrics{}, err
	}
	schedulerValue, ok := values[goSchedulerLatency]
	if !ok || schedulerValue.Kind() != runtimemetrics.KindFloat64Histogram {
		return GoRuntimeMetrics{}, errors.New("Go scheduler latency metric is unavailable or has unexpected kind")
	}
	scheduler := schedulerValue.Float64Histogram()
	var memory goruntime.MemStats
	goruntime.ReadMemStats(&memory)
	return GoRuntimeMetrics{
		HeapLiveBytes:    measuredMetric(heapLive),
		HeapGoalBytes:    measuredMetric(heapGoal),
		GCCyclesTotal:    measuredMetric(gcCycles),
		GCPauseTotalNS:   measuredMetric(memory.PauseTotalNs),
		Goroutines:       measuredMetric(uint64(goruntime.NumGoroutine())),
		SchedulerLatency: convertRuntimeHistogram(scheduler.Buckets, scheduler.Counts),
	}, nil
}

func runtimeUint64Metric(values map[string]runtimemetrics.Value, name string) (uint64, error) {
	value, ok := values[name]
	if !ok || value.Kind() != runtimemetrics.KindUint64 {
		return 0, fmt.Errorf("Go runtime metric %q is unavailable or has unexpected kind", name)
	}
	return value.Uint64(), nil
}

func convertRuntimeHistogram(bounds []float64, counts []uint64) Histogram {
	if len(bounds) != len(counts)+1 || len(counts) == 0 {
		return unavailableHistogram(ReasonCollectionError)
	}
	result := Histogram{Status: MetricMeasured}
	for index, count := range counts {
		upperSeconds := bounds[index+1]
		if math.IsInf(upperSeconds, 1) {
			if count != 0 {
				return unavailableHistogram(ReasonCollectionError)
			}
			continue
		}
		if math.IsNaN(upperSeconds) || upperSeconds <= 0 {
			if count != 0 {
				return unavailableHistogram(ReasonCollectionError)
			}
			continue
		}
		upperNanoseconds := upperSeconds * 1_000_000_000
		if math.IsInf(upperNanoseconds, 0) || upperNanoseconds > math.MaxUint64 {
			return unavailableHistogram(ReasonCollectionError)
		}
		upper := uint64(math.Ceil(upperNanoseconds))
		if len(result.UpperBoundsNS) > 0 {
			last := result.UpperBoundsNS[len(result.UpperBoundsNS)-1]
			if upper < last {
				return unavailableHistogram(ReasonCollectionError)
			}
			if upper == last {
				if math.MaxUint64-result.Counts[len(result.Counts)-1] < count {
					return unavailableHistogram(ReasonCollectionError)
				}
				result.Counts[len(result.Counts)-1] += count
				continue
			}
		}
		result.UpperBoundsNS = append(result.UpperBoundsNS, upper)
		result.Counts = append(result.Counts, count)
	}
	if len(result.Counts) == 0 {
		return unavailableHistogram(ReasonCollectionError)
	}
	return result
}

func unavailableHistogram(reason UnavailableReason) Histogram {
	return Histogram{Status: MetricUnsupported, ReasonCode: reason}
}
