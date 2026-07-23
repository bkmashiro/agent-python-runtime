package evidence

import (
	"math"
	"testing"
)

func TestCollectGoRuntimeMetricsReturnsMeasuredRuntimeState(t *testing.T) {
	metrics, err := CollectGoRuntimeMetrics()
	if err != nil {
		t.Fatal(err)
	}
	for name, metric := range map[string]Metric{
		"heap live":  metrics.HeapLiveBytes,
		"heap goal":  metrics.HeapGoalBytes,
		"GC cycles":  metrics.GCCyclesTotal,
		"GC pause":   metrics.GCPauseTotalNS,
		"goroutines": metrics.Goroutines,
	} {
		if err := validateRawMetric(metric); err != nil || metric.Status != MetricMeasured {
			t.Fatalf("%s was not measured: %#v err=%v", name, metric, err)
		}
	}
	if err := metrics.SchedulerLatency.Validate(); err != nil || metrics.SchedulerLatency.Status != MetricMeasured {
		t.Fatalf("scheduler latency was not measured: %#v err=%v", metrics.SchedulerLatency, err)
	}
}

func TestConvertRuntimeHistogramTrimsOnlyEmptyInfiniteRanges(t *testing.T) {
	histogram := convertRuntimeHistogram(
		[]float64{math.Inf(-1), 0, 0.000001, math.Inf(1)},
		[]uint64{0, 3, 0},
	)
	if err := histogram.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(histogram.UpperBoundsNS) != 1 || histogram.UpperBoundsNS[0] != 1000 || len(histogram.Counts) != 1 || histogram.Counts[0] != 3 {
		t.Fatalf("unexpected converted histogram: %#v", histogram)
	}

	unrepresentable := convertRuntimeHistogram(
		[]float64{0, 0.000001, math.Inf(1)},
		[]uint64{1, 1},
	)
	if unrepresentable.Status != MetricUnsupported || unrepresentable.ReasonCode != ReasonCollectionError || len(unrepresentable.Counts) != 0 {
		t.Fatalf("nonempty infinite tail was not rejected: %#v", unrepresentable)
	}
}
