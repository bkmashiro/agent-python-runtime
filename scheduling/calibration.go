package scheduling

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

const (
	maxCalibrationToolDispatches = 1_000
	maxCalibrationPopulation     = 10_000
)

// PhaseCaptureMetadata identifies one pysolate-phase-bench JSONL stream.
type PhaseCaptureMetadata struct {
	Type           string `json:"type"`
	Artifact       string `json:"artifact,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	ArtifactBytes  int    `json:"artifact_bytes,omitempty"`
	GOOS           string `json:"goos,omitempty"`
	GOARCH         string `json:"goarch,omitempty"`
	GoVersion      string `json:"go_version,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	SourceModified *bool  `json:"source_modified,omitempty"`
	Preparation    string `json:"preparation"`
	Cases          string `json:"cases"`
	Iterations     int    `json:"iterations"`
	ToolDelayNS    int64  `json:"synthetic_tool_delay_ns,omitempty"`
	ParkDelayNS    int64  `json:"synthetic_park_delay_ns,omitempty"`
	RunnerSetupNS  int64  `json:"runner_setup_ns,omitempty"`
	ExternalIO     bool   `json:"external_io"`
}

// PhaseSample is one measured attempt emitted by pysolate-phase-bench.
type PhaseSample struct {
	Type              string  `json:"type"`
	Case              string  `json:"case"`
	Iteration         int     `json:"iteration"`
	CreateNS          int64   `json:"create_ns,omitempty"`
	FirstAttemptNS    int64   `json:"first_attempt_ns"`
	ParkIntervalNS    int64   `json:"park_interval_ns,omitempty"`
	DecideNS          int64   `json:"decide_ns,omitempty"`
	ReadmitAttemptNS  int64   `json:"readmit_attempt_ns,omitempty"`
	ToolServiceNS     int64   `json:"tool_service_ns"`
	ToolQueueNS       int64   `json:"tool_queue_ns"`
	ToolResumeNS      int64   `json:"tool_resume_ns"`
	ToolFailures      int64   `json:"tool_failures"`
	RuntimeOverheadNS int64   `json:"attempt_minus_tool_ns,omitempty"`
	ToolDispatches    int64   `json:"tool_dispatches"`
	Result            float64 `json:"result,omitempty"`
	TotalNS           int64   `json:"total_ns,omitempty"`
}

// PhaseCapture contains one metadata row followed by measured samples.
type PhaseCapture struct {
	Metadata PhaseCaptureMetadata
	Samples  []PhaseSample
}

// CalibrationOptions selects a non-parked case and its accounting size.
type CalibrationOptions struct {
	Case          string
	ResidentBytes int64
}

// CalibrationProvenance records the explicit approximation used to turn
// aggregate phase measurements into simulator phases.
type CalibrationProvenance struct {
	Case               string `json:"case"`
	Samples            int    `json:"samples"`
	CPUSplit           string `json:"cpu_split"`
	ToolSplit          string `json:"tool_split"`
	ExcludedCreateNS   int64  `json:"excluded_create_ns"`
	ExcludedQueueNS    int64  `json:"excluded_tool_queue_ns"`
	ExcludedResumeNS   int64  `json:"excluded_tool_resume_ns"`
	DurableParkAllowed bool   `json:"durable_park_allowed"`
}

// BatchObservation is one real concurrent read-finish batch.
type BatchObservation struct {
	Case              string  `json:"case"`
	Mode              string  `json:"mode,omitempty"`
	Tasks             int     `json:"tasks"`
	RunningLimit      int     `json:"running_limit"`
	ResidentLimit     int     `json:"resident_limit"`
	ToolLimit         int     `json:"tool_limit"`
	ExternalIO        bool    `json:"external_io"`
	HeapMiB           int     `json:"heap_mib,omitempty"`
	SyntheticHoldNS   int64   `json:"synthetic_hold_ns,omitempty"`
	SetupNS           int64   `json:"setup_ns,omitempty"`
	BatchNS           int64   `json:"batch_ns"`
	RequestNS         []int64 `json:"request_ns,omitempty"`
	PeakHostWaits     int32   `json:"peak_host_waits,omitempty"`
	SampledPeakRSSKiB *int64  `json:"sampled_peak_rss_kib,omitempty"`
	Completed         int     `json:"completed,omitempty"`
}

// CalibrationComparison compares one deterministic prediction with one real
// batch observation. RelativeError is absolute error divided by observed time.
type CalibrationComparison struct {
	Type                string  `json:"type"`
	Case                string  `json:"case"`
	Tasks               int     `json:"tasks"`
	RunningLimit        int     `json:"running_limit"`
	ResidentLimit       int     `json:"resident_limit"`
	ToolLimit           int     `json:"tool_limit"`
	ExternalIO          bool    `json:"external_io"`
	ObservedBatchNS     int64   `json:"observed_batch_ns"`
	PredictedMakespanNS int64   `json:"predicted_makespan_ns"`
	AbsoluteErrorNS     int64   `json:"absolute_error_ns"`
	RelativeError       float64 `json:"relative_error"`
	Tolerance           float64 `json:"tolerance"`
	WithinTolerance     bool    `json:"within_tolerance"`
}

// CalibrationSummary aggregates repeated observations of one resource arm.
type CalibrationSummary struct {
	Type                    string  `json:"type"`
	Case                    string  `json:"case"`
	Tasks                   int     `json:"tasks"`
	RunningLimit            int     `json:"running_limit"`
	ResidentLimit           int     `json:"resident_limit"`
	ToolLimit               int     `json:"tool_limit"`
	ExternalIO              bool    `json:"external_io"`
	Samples                 int     `json:"samples"`
	MeanObservedBatchNS     int64   `json:"mean_observed_batch_ns"`
	MeanPredictedMakespanNS int64   `json:"mean_predicted_makespan_ns"`
	MeanRelativeError       float64 `json:"mean_relative_error"`
	MaxRelativeError        float64 `json:"max_relative_error"`
	WithinTolerance         int     `json:"within_tolerance"`
}

// DecodePhaseCapture strictly decodes a phase-bench JSONL stream.
func DecodePhaseCapture(reader io.Reader) (PhaseCapture, error) {
	decoder := json.NewDecoder(reader)
	var capture PhaseCapture
	seenMetadata := false
	for row := 0; ; row++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return PhaseCapture{}, fmt.Errorf("decode phase row %d: %w", row, err)
		}
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil {
			return PhaseCapture{}, fmt.Errorf("decode phase row type %d: %w", row, err)
		}
		switch kind.Type {
		case "metadata":
			if seenMetadata || len(capture.Samples) > 0 {
				return PhaseCapture{}, errors.New("phase capture must contain one leading metadata row")
			}
			if err := decodeStrict(raw, &capture.Metadata); err != nil {
				return PhaseCapture{}, fmt.Errorf("decode phase metadata: %w", err)
			}
			seenMetadata = true
		case "sample":
			if !seenMetadata {
				return PhaseCapture{}, errors.New("phase sample precedes metadata")
			}
			var sample PhaseSample
			if err := decodeStrict(raw, &sample); err != nil {
				return PhaseCapture{}, fmt.Errorf("decode phase sample %d: %w", row, err)
			}
			capture.Samples = append(capture.Samples, sample)
		default:
			return PhaseCapture{}, fmt.Errorf("unknown phase row type %q", kind.Type)
		}
	}
	if !seenMetadata || len(capture.Samples) == 0 {
		return PhaseCapture{}, errors.New("phase capture requires metadata and samples")
	}
	if capture.Metadata.Preparation == "" || capture.Metadata.Cases == "" || capture.Metadata.Iterations < 1 || capture.Metadata.ToolDelayNS < 0 || capture.Metadata.ParkDelayNS < 0 || capture.Metadata.RunnerSetupNS < 0 {
		return PhaseCapture{}, errors.New("invalid phase metadata")
	}
	return capture, nil
}

// BuildCalibratedTasks converts aggregate, non-parked phase samples into fixed
// simulator traces. Local CPU residual and Tool service are split evenly while
// preserving each measured total exactly.
func BuildCalibratedTasks(capture PhaseCapture, options CalibrationOptions) ([]Task, CalibrationProvenance, error) {
	if options.Case == "" || options.ResidentBytes < 0 {
		return nil, CalibrationProvenance{}, errors.New("calibration case is required and resident bytes must be non-negative")
	}
	provenance := CalibrationProvenance{
		Case: options.Case, CPUSplit: "equal_between_tool_calls", ToolSplit: "equal_across_tool_calls",
		DurableParkAllowed: false,
	}
	var tasks []Task
	seen := map[string]bool{}
	for _, sample := range capture.Samples {
		if sample.Case != options.Case {
			continue
		}
		if sample.ParkIntervalNS != 0 || sample.ReadmitAttemptNS != 0 || sample.DecideNS != 0 {
			return nil, CalibrationProvenance{}, fmt.Errorf("case %q iteration %d contains durable park/re-admit timings", sample.Case, sample.Iteration)
		}
		if sample.Case == "" || sample.Iteration < 0 || sample.CreateNS < 0 || sample.FirstAttemptNS <= 0 || sample.ToolDispatches < 0 || sample.ToolDispatches > maxCalibrationToolDispatches || sample.ToolServiceNS < 0 || sample.ToolQueueNS < 0 || sample.ToolResumeNS < 0 || sample.ToolFailures != 0 {
			return nil, CalibrationProvenance{}, fmt.Errorf("case %q iteration %d has invalid timing fields", sample.Case, sample.Iteration)
		}
		if sample.ToolDispatches == 0 && (sample.ToolServiceNS != 0 || sample.ToolQueueNS != 0 || sample.ToolResumeNS != 0) {
			return nil, CalibrationProvenance{}, fmt.Errorf("case %q iteration %d has Tool timing without dispatches", sample.Case, sample.Iteration)
		}
		localNS := sample.FirstAttemptNS - sample.ToolServiceNS - sample.ToolQueueNS - sample.ToolResumeNS
		segments := int(sample.ToolDispatches) + 1
		if localNS < int64(segments) || (sample.ToolDispatches > 0 && sample.ToolServiceNS < sample.ToolDispatches) {
			return nil, CalibrationProvenance{}, fmt.Errorf("case %q iteration %d has no positive phase decomposition", sample.Case, sample.Iteration)
		}
		cpuDurations := splitDuration(localNS, segments)
		toolDurations := splitDuration(sample.ToolServiceNS, int(sample.ToolDispatches))
		phases := make([]Phase, 0, segments+len(toolDurations))
		for index, cpu := range cpuDurations {
			phases = append(phases, Phase{Kind: CPU, Duration: time.Duration(cpu)})
			if index < len(toolDurations) {
				phases = append(phases, Phase{Kind: ExternalIO, Duration: time.Duration(toolDurations[index])})
			}
		}
		id := fmt.Sprintf("%s-%06d", sample.Case, sample.Iteration)
		if seen[id] {
			return nil, CalibrationProvenance{}, fmt.Errorf("duplicate calibrated task %q", id)
		}
		seen[id] = true
		tasks = append(tasks, Task{ID: id, ResidentBytes: options.ResidentBytes, Phases: phases})
		provenance.Samples++
		provenance.ExcludedCreateNS += sample.CreateNS
		provenance.ExcludedQueueNS += sample.ToolQueueNS
		provenance.ExcludedResumeNS += sample.ToolResumeNS
	}
	if len(tasks) == 0 {
		return nil, CalibrationProvenance{}, fmt.Errorf("phase capture has no samples for case %q", options.Case)
	}
	sort.Slice(tasks, func(left, right int) bool { return tasks[left].ID < tasks[right].ID })
	return tasks, provenance, nil
}

// DecodeBatchObservations strictly decodes queue-bench JSON or JSONL rows.
func DecodeBatchObservations(reader io.Reader) ([]BatchObservation, error) {
	decoder := json.NewDecoder(reader)
	var rows []BatchObservation
	for index := 0; ; index++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode observed row %d: %w", index, err)
		}
		var row BatchObservation
		if err := decodeStrict(raw, &row); err != nil {
			return nil, fmt.Errorf("decode observed row %d: %w", index, err)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errors.New("no observed batch rows")
	}
	return rows, nil
}

// CompareObservedBatches repeats calibrated samples deterministically to each
// observed population size, then evaluates the matching resource configuration.
func CompareObservedBatches(templates []Task, observations []BatchObservation, policy Policy, tolerance float64) ([]CalibrationComparison, error) {
	if len(templates) == 0 || len(observations) == 0 || tolerance < 0 || tolerance > 1 || math.IsNaN(tolerance) {
		return nil, errors.New("invalid calibration comparison inputs")
	}
	var comparisons []CalibrationComparison
	for index, observation := range observations {
		if observation.Case == "" || observation.Tasks < 1 || observation.Tasks > maxCalibrationPopulation || observation.RunningLimit < 1 || observation.RunningLimit > maxCalibrationPopulation || observation.ResidentLimit < observation.RunningLimit || observation.ResidentLimit > maxCalibrationPopulation || observation.ToolLimit < 1 || observation.ToolLimit > maxCalibrationPopulation || observation.BatchNS <= 0 {
			return nil, fmt.Errorf("invalid observed batch row %d", index)
		}
		tasks := RepeatCalibratedTasks(templates, observation.Tasks)
		result, err := Simulate(tasks, Config{
			MaxRunning: observation.RunningLimit, MaxResident: observation.ResidentLimit,
			MaxInflightTools: observation.ToolLimit, MaxQueued: observation.Tasks,
			LiveIO: observation.ExternalIO, Policy: policy,
		})
		if err != nil {
			return nil, fmt.Errorf("simulate observed batch row %d: %w", index, err)
		}
		absolute := result.Makespan.Nanoseconds() - observation.BatchNS
		if absolute < 0 {
			absolute = -absolute
		}
		relative := float64(absolute) / float64(observation.BatchNS)
		comparisons = append(comparisons, CalibrationComparison{
			Type: "comparison", Case: observation.Case, Tasks: observation.Tasks,
			RunningLimit: observation.RunningLimit, ResidentLimit: observation.ResidentLimit,
			ToolLimit: observation.ToolLimit, ExternalIO: observation.ExternalIO,
			ObservedBatchNS: observation.BatchNS, PredictedMakespanNS: result.Makespan.Nanoseconds(),
			AbsoluteErrorNS: absolute, RelativeError: relative, Tolerance: tolerance,
			WithinTolerance: relative <= tolerance,
		})
	}
	return comparisons, nil
}

// SummarizeCalibrationComparisons groups repeated rows by resource arm.
func SummarizeCalibrationComparisons(comparisons []CalibrationComparison) []CalibrationSummary {
	type key struct {
		caseName                        string
		tasks, running, resident, tools int
		external                        bool
	}
	groups := map[key]*CalibrationSummary{}
	for _, comparison := range comparisons {
		groupKey := key{
			caseName: comparison.Case, tasks: comparison.Tasks, running: comparison.RunningLimit,
			resident: comparison.ResidentLimit, tools: comparison.ToolLimit, external: comparison.ExternalIO,
		}
		summary := groups[groupKey]
		if summary == nil {
			summary = &CalibrationSummary{
				Type: "summary", Case: comparison.Case, Tasks: comparison.Tasks,
				RunningLimit: comparison.RunningLimit, ResidentLimit: comparison.ResidentLimit,
				ToolLimit: comparison.ToolLimit, ExternalIO: comparison.ExternalIO,
			}
			groups[groupKey] = summary
		}
		summary.Samples++
		count := int64(summary.Samples)
		summary.MeanObservedBatchNS += (comparison.ObservedBatchNS - summary.MeanObservedBatchNS) / count
		summary.MeanPredictedMakespanNS += (comparison.PredictedMakespanNS - summary.MeanPredictedMakespanNS) / count
		summary.MeanRelativeError += (comparison.RelativeError - summary.MeanRelativeError) / float64(summary.Samples)
		if comparison.RelativeError > summary.MaxRelativeError {
			summary.MaxRelativeError = comparison.RelativeError
		}
		if comparison.WithinTolerance {
			summary.WithinTolerance++
		}
	}
	summaries := make([]CalibrationSummary, 0, len(groups))
	for _, summary := range groups {
		summaries = append(summaries, *summary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		a, b := summaries[left], summaries[right]
		if a.Case != b.Case {
			return a.Case < b.Case
		}
		if a.Tasks != b.Tasks {
			return a.Tasks < b.Tasks
		}
		if a.RunningLimit != b.RunningLimit {
			return a.RunningLimit < b.RunningLimit
		}
		if a.ResidentLimit != b.ResidentLimit {
			return a.ResidentLimit < b.ResidentLimit
		}
		if a.ToolLimit != b.ToolLimit {
			return a.ToolLimit < b.ToolLimit
		}
		return !a.ExternalIO && b.ExternalIO
	})
	return summaries
}

func splitDuration(total int64, parts int) []int64 {
	if parts == 0 {
		return nil
	}
	result := make([]int64, parts)
	base, remainder := total/int64(parts), total%int64(parts)
	for index := range result {
		result[index] = base
		if int64(index) < remainder {
			result[index]++
		}
	}
	return result
}

// RepeatCalibratedTasks cycles fixed samples into a simultaneous population.
func RepeatCalibratedTasks(templates []Task, count int) []Task {
	if len(templates) == 0 || count <= 0 {
		return nil
	}
	result := make([]Task, count)
	for index := range result {
		template := templates[index%len(templates)]
		result[index] = template
		result[index].ID = fmt.Sprintf("task-%06d", index)
		result[index].Arrival = 0
		result[index].Phases = append([]Phase(nil), template.Phases...)
	}
	return result
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON row contains trailing data")
	}
	return nil
}
