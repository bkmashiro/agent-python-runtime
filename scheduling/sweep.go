package scheduling

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// These are engineering guards for this research helper, not runtime limits.
	maxCanonicalTasksPerPoint = 10_000
	maxCanonicalSweepPoints   = 100_000
)

// CanonicalSweep defines a deterministic CPU -> ExternalIO -> CPU grid.
type CanonicalSweep struct {
	TaskCounts          []int
	IORatios            []float64
	RunningLimits       []int
	ResidentMultipliers []int
	ToolLimits          []int
	Policies            []Policy
	CPUBefore           time.Duration
	CPUAfter            time.Duration
	ResidentBytes       int64
}

// SweepPoint compares Inline and live-I/O scheduling at one grid point.
type SweepPoint struct {
	RecordType            string        `json:"type"`
	Tasks                 int           `json:"tasks"`
	IORatio               float64       `json:"io_ratio"`
	CPUBefore             time.Duration `json:"cpu_before_ns"`
	IODuration            time.Duration `json:"io_ns"`
	CPUAfter              time.Duration `json:"cpu_after_ns"`
	RunningLimit          int           `json:"running_limit"`
	ResidentMultiplier    int           `json:"resident_multiplier"`
	ResidentLimit         int           `json:"resident_limit"`
	ToolLimit             int           `json:"tool_limit"`
	Policy                Policy        `json:"policy"`
	InlineMakespan        time.Duration `json:"inline_makespan_ns"`
	LiveMakespan          time.Duration `json:"live_makespan_ns"`
	Speedup               float64       `json:"speedup"`
	InlineMeanLatency     time.Duration `json:"inline_mean_latency_ns"`
	LiveMeanLatency       time.Duration `json:"live_mean_latency_ns"`
	InlineP95Latency      time.Duration `json:"inline_p95_latency_ns"`
	LiveP95Latency        time.Duration `json:"live_p95_latency_ns"`
	InlinePeakResident    int           `json:"inline_peak_resident"`
	LivePeakResident      int           `json:"live_peak_resident"`
	InlineResidentMiBSecs float64       `json:"inline_resident_mib_seconds"`
	LiveResidentMiBSecs   float64       `json:"live_resident_mib_seconds"`
	ResidentAreaRatio     float64       `json:"resident_area_ratio"`
}

// SweepBoundary summarizes where live-I/O first reaches fixed speedup levels
// for one resource configuration.
type SweepBoundary struct {
	RecordType             string   `json:"type"`
	Tasks                  int      `json:"tasks"`
	RunningLimit           int      `json:"running_limit"`
	ResidentMultiplier     int      `json:"resident_multiplier"`
	ResidentLimit          int      `json:"resident_limit"`
	ToolLimit              int      `json:"tool_limit"`
	Policy                 Policy   `json:"policy"`
	FirstFivePercentRatio  *float64 `json:"first_five_percent_ratio"`
	FirstTenPercentRatio   *float64 `json:"first_ten_percent_ratio"`
	MaxSpeedup             float64  `json:"max_speedup"`
	MaxSpeedupRatio        float64  `json:"max_speedup_ratio"`
	ResidentAreaRatioAtMax float64  `json:"resident_area_ratio_at_max"`
}

// RunCanonicalSweep executes every normalized grid point without randomness.
func RunCanonicalSweep(input CanonicalSweep) ([]SweepPoint, []SweepBoundary, error) {
	grid, err := normalizeCanonicalSweep(input)
	if err != nil {
		return nil, nil, err
	}
	points := make([]SweepPoint, 0, len(grid.TaskCounts)*len(grid.RunningLimits)*len(grid.ResidentMultipliers)*len(grid.ToolLimits)*len(grid.Policies)*len(grid.IORatios))
	boundaries := make([]SweepBoundary, 0, len(grid.TaskCounts)*len(grid.RunningLimits)*len(grid.ResidentMultipliers)*len(grid.ToolLimits)*len(grid.Policies))
	cpuTotal := grid.CPUBefore + grid.CPUAfter

	for _, tasks := range grid.TaskCounts {
		for _, running := range grid.RunningLimits {
			for _, multiplier := range grid.ResidentMultipliers {
				if running > int(^uint(0)>>1)/multiplier {
					return nil, nil, fmt.Errorf("resident limit overflows int: running=%d multiplier=%d", running, multiplier)
				}
				resident := running * multiplier
				for _, tools := range grid.ToolLimits {
					for _, policy := range grid.Policies {
						boundary := SweepBoundary{
							RecordType: "boundary", Tasks: tasks, RunningLimit: running,
							ResidentMultiplier: multiplier, ResidentLimit: resident,
							ToolLimit: tools, Policy: policy,
						}
						for _, ratio := range grid.IORatios {
							ioDuration := time.Duration(math.Round(float64(cpuTotal) * ratio))
							if ioDuration < time.Nanosecond {
								ioDuration = time.Nanosecond
							}
							workload := canonicalTasks(tasks, grid.CPUBefore, ioDuration, grid.CPUAfter, grid.ResidentBytes)
							config := Config{MaxRunning: running, MaxResident: resident, MaxInflightTools: tools, Policy: policy}
							inline, err := Simulate(workload, config)
							if err != nil {
								return nil, nil, fmt.Errorf("inline tasks=%d ratio=%g running=%d resident=%d tools=%d policy=%s: %w", tasks, ratio, running, resident, tools, policy, err)
							}
							config.LiveIO = true
							live, err := Simulate(workload, config)
							if err != nil {
								return nil, nil, fmt.Errorf("live tasks=%d ratio=%g running=%d resident=%d tools=%d policy=%s: %w", tasks, ratio, running, resident, tools, policy, err)
							}
							point := SweepPoint{
								RecordType: "point", Tasks: tasks, IORatio: ratio,
								CPUBefore: grid.CPUBefore, IODuration: ioDuration, CPUAfter: grid.CPUAfter,
								RunningLimit: running, ResidentMultiplier: multiplier, ResidentLimit: resident,
								ToolLimit: tools, Policy: policy,
								InlineMakespan: inline.Makespan, LiveMakespan: live.Makespan,
								InlineMeanLatency: inline.MeanLatency, LiveMeanLatency: live.MeanLatency,
								InlineP95Latency: inline.P95Latency, LiveP95Latency: live.P95Latency,
								InlinePeakResident: inline.PeakResident, LivePeakResident: live.PeakResident,
								InlineResidentMiBSecs: inline.ResidentMiBSeconds, LiveResidentMiBSecs: live.ResidentMiBSeconds,
							}
							if live.Makespan > 0 {
								point.Speedup = float64(inline.Makespan) / float64(live.Makespan)
							}
							if inline.ResidentMiBSeconds > 0 {
								point.ResidentAreaRatio = live.ResidentMiBSeconds / inline.ResidentMiBSeconds
							}
							points = append(points, point)
							updateBoundary(&boundary, point)
						}
						boundaries = append(boundaries, boundary)
					}
				}
			}
		}
	}
	return points, boundaries, nil
}

func canonicalTasks(count int, before, ioDuration, after time.Duration, residentBytes int64) []Task {
	tasks := make([]Task, count)
	for index := range tasks {
		tasks[index] = Task{
			ID: fmt.Sprintf("task-%06d", index), ResidentBytes: residentBytes,
			Phases: []Phase{{Kind: CPU, Duration: before}, {Kind: ExternalIO, Duration: ioDuration}, {Kind: CPU, Duration: after}},
		}
	}
	return tasks
}

func updateBoundary(boundary *SweepBoundary, point SweepPoint) {
	if boundary.FirstFivePercentRatio == nil && point.Speedup >= 1.05 {
		value := point.IORatio
		boundary.FirstFivePercentRatio = &value
	}
	if boundary.FirstTenPercentRatio == nil && point.Speedup >= 1.10 {
		value := point.IORatio
		boundary.FirstTenPercentRatio = &value
	}
	if point.Speedup > boundary.MaxSpeedup {
		boundary.MaxSpeedup = point.Speedup
		boundary.MaxSpeedupRatio = point.IORatio
		boundary.ResidentAreaRatioAtMax = point.ResidentAreaRatio
	}
}

func normalizeCanonicalSweep(input CanonicalSweep) (CanonicalSweep, error) {
	if input.CPUBefore <= 0 || input.CPUAfter <= 0 || input.ResidentBytes < 0 {
		return CanonicalSweep{}, errors.New("canonical CPU durations must be positive and resident bytes non-negative")
	}
	if input.CPUBefore > time.Duration(math.MaxInt64)-input.CPUAfter {
		return CanonicalSweep{}, errors.New("canonical CPU duration sum overflows time.Duration")
	}
	var err error
	if input.TaskCounts, err = sortedUniquePositiveInts(input.TaskCounts, "task count"); err != nil {
		return CanonicalSweep{}, err
	}
	if input.TaskCounts[len(input.TaskCounts)-1] > maxCanonicalTasksPerPoint {
		return CanonicalSweep{}, fmt.Errorf("task count exceeds research guard %d", maxCanonicalTasksPerPoint)
	}
	if input.RunningLimits, err = sortedUniquePositiveInts(input.RunningLimits, "running limit"); err != nil {
		return CanonicalSweep{}, err
	}
	if input.ResidentMultipliers, err = sortedUniquePositiveInts(input.ResidentMultipliers, "resident multiplier"); err != nil {
		return CanonicalSweep{}, err
	}
	if input.ToolLimits, err = sortedUniquePositiveInts(input.ToolLimits, "tool limit"); err != nil {
		return CanonicalSweep{}, err
	}
	if len(input.IORatios) == 0 {
		return CanonicalSweep{}, errors.New("I/O ratios are required")
	}
	input.IORatios = append([]float64(nil), input.IORatios...)
	sort.Float64s(input.IORatios)
	maxIORatio := math.Nextafter(float64(math.MaxInt64), 0) / float64(input.CPUBefore+input.CPUAfter)
	for index, ratio := range input.IORatios {
		if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return CanonicalSweep{}, errors.New("I/O ratios must be finite and positive")
		}
		if ratio > maxIORatio {
			return CanonicalSweep{}, errors.New("I/O ratio overflows time.Duration")
		}
		if index > 0 && ratio == input.IORatios[index-1] {
			return CanonicalSweep{}, errors.New("duplicate I/O ratio")
		}
	}
	if len(input.Policies) == 0 {
		return CanonicalSweep{}, errors.New("policies are required")
	}
	input.Policies = append([]Policy(nil), input.Policies...)
	sort.Slice(input.Policies, func(i, j int) bool { return input.Policies[i] < input.Policies[j] })
	for index, policy := range input.Policies {
		if policy != FIFO && policy != ReadyFirst && policy != FinishSoon {
			return CanonicalSweep{}, fmt.Errorf("unknown policy %q", policy)
		}
		if index > 0 && policy == input.Policies[index-1] {
			return CanonicalSweep{}, errors.New("duplicate policy")
		}
	}
	pointCount := 1
	for _, factor := range []int{len(input.TaskCounts), len(input.IORatios), len(input.RunningLimits), len(input.ResidentMultipliers), len(input.ToolLimits), len(input.Policies)} {
		if pointCount > maxCanonicalSweepPoints/factor {
			return CanonicalSweep{}, fmt.Errorf("canonical grid exceeds research guard of %d points", maxCanonicalSweepPoints)
		}
		pointCount *= factor
	}
	return input, nil
}

func sortedUniquePositiveInts(values []int, label string) ([]int, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s values are required", label)
	}
	result := append([]int(nil), values...)
	sort.Ints(result)
	for index, value := range result {
		if value <= 0 {
			return nil, fmt.Errorf("%s values must be positive", label)
		}
		if index > 0 && value == result[index-1] {
			return nil, fmt.Errorf("duplicate %s value", label)
		}
	}
	return result, nil
}
