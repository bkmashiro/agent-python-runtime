package scheduler

import (
	"errors"
	"testing"
)

func TestE2MixedWorkloadCorpusHasExactDistribution(t *testing.T) {
	workload, err := buildE2MixedWorkload(200)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[e2WorkloadClass]int{}
	prefixClasses := map[e2WorkloadClass]bool{}
	for index, task := range workload {
		counts[task.Class]++
		if index < 20 {
			prefixClasses[task.Class] = true
		}
		if task.TaskID == "" || task.ActualBytes == 0 || task.ReservationBytes == 0 || task.ReservationBytes > task.ActualBytes || task.DurationTicks == 0 {
			t.Fatalf("task[%d] = %#v", index, task)
		}
	}
	if len(prefixClasses) < 3 {
		t.Fatalf("arrival prefix is not mixed: %#v", prefixClasses)
	}
	if counts[e2Tiny] != 120 || counts[e2Small] != 50 || counts[e2Medium] != 20 || counts[e2Large] != 10 {
		t.Fatalf("distribution = %#v", counts)
	}
	second, err := buildE2MixedWorkload(200)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workload {
		if workload[index] != second[index] {
			t.Fatalf("corpus is nondeterministic at %d", index)
		}
	}
	for _, count := range []int{0, 99, 10_100} {
		if _, err := buildE2MixedWorkload(count); !errors.Is(err, errInvalidE2Experiment) {
			t.Fatalf("count %d error = %v", count, err)
		}
	}
}

func TestE2NearestRankQuantiles(t *testing.T) {
	values := make([]uint64, 100)
	for index := range values {
		values[index] = uint64(index + 1)
	}
	quantiles, err := e2NearestRankQuantiles(values)
	if err != nil {
		t.Fatal(err)
	}
	if quantiles != (e2Quantiles{P50: 50, P95: 95, P99: 99, Max: 100}) {
		t.Fatalf("quantiles = %#v", quantiles)
	}
	if _, err := e2NearestRankQuantiles(nil); !errors.Is(err, errInvalidE2Experiment) {
		t.Fatalf("empty quantiles error = %v", err)
	}
}

func TestE2MixedLoadReportValidation(t *testing.T) {
	valid := e2MixedLoadReport{
		Schema: "apyrun.e2-mixed-load/v1", TargetEvictionPPM: 10_000,
		Tasks: 200, Completed: 200, Attempts: 205, Evictions: 5, Retries: 5,
		MaxActive: 64, MaxCurrentBytes: 500 << 20, MaxUtilizationBPS: 1300,
		ControlWindows: 20, AggressiveDecisions: 2, ConservativeDecisions: 3, HoldDecisions: 15,
		UsefulBytes: 2 << 30, WastedBytes: 64 << 20, ReservationAbsoluteErrorBytes: 1 << 30,
		QueueWaitTicks: e2Quantiles{P50: 2, P95: 10, P99: 20, Max: 25},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*e2MixedLoadReport){
		"wrong schema":       func(value *e2MixedLoadReport) { value.Schema = "other" },
		"incomplete":         func(value *e2MixedLoadReport) { value.Completed-- },
		"oom":                func(value *e2MixedLoadReport) { value.OOMEvents = 1 },
		"retry mismatch":     func(value *e2MixedLoadReport) { value.Retries++ },
		"eviction overflow":  func(value *e2MixedLoadReport) { value.Evictions = value.Attempts + 1 },
		"invalid budget":     func(value *e2MixedLoadReport) { value.TargetEvictionPPM = 123 },
		"unordered quantile": func(value *e2MixedLoadReport) { value.QueueWaitTicks.P99 = 9 },
		"decision mismatch":  func(value *e2MixedLoadReport) { value.HoldDecisions-- },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := value.Validate(); !errors.Is(err, errInvalidE2Experiment) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
