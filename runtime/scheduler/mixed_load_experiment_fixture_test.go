package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var errInvalidE2Experiment = errors.New("invalid E2 mixed-load experiment")

type e2WorkloadClass string

const (
	e2Tiny   e2WorkloadClass = "tiny"
	e2Small  e2WorkloadClass = "small"
	e2Medium e2WorkloadClass = "medium"
	e2Large  e2WorkloadClass = "large"
)

type e2WorkloadTask struct {
	TaskID           string
	Class            e2WorkloadClass
	ActualBytes      uint64
	ReservationBytes uint64
	DurationTicks    uint32
}

func buildE2MixedWorkload(count int) ([]e2WorkloadTask, error) {
	if count < 100 || count > 10_000 || count%100 != 0 {
		return nil, errInvalidE2Experiment
	}
	result := make([]e2WorkloadTask, 0, count)
	classOccurrences := map[e2WorkloadClass]int{}
	for index := 0; index < count; index++ {
		position := (index * 37) % 100
		task := e2WorkloadTask{TaskID: fmt.Sprintf("e2-task-%05d", index)}
		switch {
		case position < 60:
			task.Class, task.ActualBytes, task.DurationTicks = e2Tiny, 2<<20, 2
		case position < 85:
			task.Class, task.ActualBytes, task.DurationTicks = e2Small, 8<<20, 3
		case position < 95:
			task.Class, task.ActualBytes, task.DurationTicks = e2Medium, 32<<20, 5
		default:
			task.Class, task.ActualBytes, task.DurationTicks = e2Large, 64<<20, 8
		}
		factors := [...]uint64{50, 50, 50, 150, 50, 50, 200, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50}
		factor := factors[classOccurrences[task.Class]%len(factors)]
		classOccurrences[task.Class]++
		task.ActualBytes = task.ActualBytes * factor / 100
		task.ReservationBytes = task.ActualBytes / 2
		result = append(result, task)
	}
	return result, nil
}

func e2WorkloadProfile(class e2WorkloadClass) (WorkloadProfile, error) {
	var digestCharacter string
	var bucket RequestSizeBucket
	switch class {
	case e2Tiny:
		digestCharacter, bucket = "1", RequestSizeTiny
	case e2Small:
		digestCharacter, bucket = "2", RequestSizeSmall
	case e2Medium:
		digestCharacter, bucket = "3", RequestSizeMedium
	case e2Large:
		digestCharacter, bucket = "4", RequestSizeLarge
	default:
		return WorkloadProfile{}, errInvalidE2Experiment
	}
	return WorkloadProfile{
		ArtifactDigest: strings.Repeat(digestCharacter, 64), WorkloadClass: "e2_" + string(class),
		RequestSizeBucket: bucket, CapabilityPattern: "none", PolicyClass: "e2",
	}, nil
}

type e2Quantiles struct {
	P50 uint64 `json:"p50"`
	P95 uint64 `json:"p95"`
	P99 uint64 `json:"p99"`
	Max uint64 `json:"max"`
}

func e2NearestRankQuantiles(values []uint64) (e2Quantiles, error) {
	if len(values) == 0 {
		return e2Quantiles{}, errInvalidE2Experiment
	}
	ordered := append([]uint64(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	at := func(percent int) uint64 {
		index := (percent*len(ordered) + 99) / 100
		return ordered[index-1]
	}
	return e2Quantiles{P50: at(50), P95: at(95), P99: at(99), Max: ordered[len(ordered)-1]}, nil
}

type e2MixedLoadReport struct {
	Schema                        string      `json:"schema"`
	TargetEvictionPPM             uint32      `json:"target_eviction_ppm"`
	Tasks                         uint64      `json:"tasks"`
	Completed                     uint64      `json:"completed"`
	Failed                        uint64      `json:"failed"`
	Attempts                      uint64      `json:"attempts"`
	Evictions                     uint64      `json:"evictions"`
	Retries                       uint64      `json:"retries"`
	GuaranteedFallbacks           uint64      `json:"guaranteed_fallbacks"`
	InsufficientVictimWindows     uint64      `json:"insufficient_victim_windows"`
	OOMEvents                     uint64      `json:"oom_events"`
	MaxActive                     uint64      `json:"max_active"`
	MaxCurrentBytes               uint64      `json:"max_current_bytes"`
	MaxUtilizationBPS             uint32      `json:"max_utilization_bps"`
	ControlWindows                uint64      `json:"control_windows"`
	AggressiveDecisions           uint64      `json:"aggressive_decisions"`
	ConservativeDecisions         uint64      `json:"conservative_decisions"`
	HoldDecisions                 uint64      `json:"hold_decisions"`
	FinalQuantileBPS              uint32      `json:"final_quantile_bps"`
	UsefulBytes                   uint64      `json:"useful_bytes"`
	WastedBytes                   uint64      `json:"wasted_bytes"`
	ReservationAbsoluteErrorBytes uint64      `json:"reservation_absolute_error_bytes"`
	QueueWaitTicks                e2Quantiles `json:"queue_wait_ticks"`
}

func (report e2MixedLoadReport) Validate() error {
	decisionTotal := report.AggressiveDecisions + report.ConservativeDecisions
	if decisionTotal < report.AggressiveDecisions || report.HoldDecisions > ^uint64(0)-decisionTotal {
		return errInvalidE2Experiment
	}
	decisionTotal += report.HoldDecisions
	if report.Schema != "apyrun.e2-mixed-load/v1" || report.Tasks < 100 || report.Completed != report.Tasks || report.Failed != 0 ||
		report.Attempts < report.Tasks || report.Evictions > report.Attempts || report.Retries != report.Evictions || report.OOMEvents != 0 ||
		report.MaxActive == 0 || report.MaxCurrentBytes == 0 || report.MaxUtilizationBPS > 10000 || report.ControlWindows == 0 || decisionTotal != report.ControlWindows ||
		report.FinalQuantileBPS < 9000 || report.FinalQuantileBPS > 10000 || report.UsefulBytes == 0 ||
		report.QueueWaitTicks.P50 > report.QueueWaitTicks.P95 || report.QueueWaitTicks.P95 > report.QueueWaitTicks.P99 || report.QueueWaitTicks.P99 > report.QueueWaitTicks.Max {
		return errInvalidE2Experiment
	}
	switch report.TargetEvictionPPM {
	case 1_000, 10_000, 50_000:
		return nil
	default:
		return errInvalidE2Experiment
	}
}
