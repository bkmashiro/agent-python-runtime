// Convert measured single-Run phases into deterministic simulator workloads and
// compare simulator predictions with real bounded batches.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/bkmashiro/agent-python-runtime/scheduling"
)

const maxCalibrationInputBytes = 64 << 20

type reportMetadata struct {
	Type            string                           `json:"type"`
	Method          string                           `json:"method"`
	PhaseSource     string                           `json:"phase_source"`
	ObservedSource  string                           `json:"observed_source"`
	Case            string                           `json:"case"`
	Policy          scheduling.Policy                `json:"policy"`
	Tolerance       float64                          `json:"tolerance"`
	Comparisons     int                              `json:"comparisons"`
	Summaries       int                              `json:"summaries"`
	WithinTolerance int                              `json:"within_tolerance"`
	MaxRelativeErr  float64                          `json:"max_relative_error"`
	Provenance      scheduling.CalibrationProvenance `json:"provenance"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("pysolate-calibrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	phasePath := flags.String("phase", "", "phase-bench JSONL input")
	caseName := flags.String("case", "read-finish", "non-parked phase case")
	residentMiB := flags.Int64("resident-mib", 0, "estimated resident MiB per simulated task")
	taskCount := flags.Int("tasks", 0, "workload output size; zero keeps every measured sample")
	observedPath := flags.String("observed", "", "optional queue-bench JSON/JSONL rows")
	policyName := flags.String("policy", string(scheduling.FIFO), "fifo, ready_first, or finish_soon")
	tolerance := flags.Float64("tolerance", 0.15, "maximum accepted relative makespan error")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *phasePath == "" || *caseName == "" || *residentMiB < 0 || *residentMiB > 65536 || *taskCount < 0 || *taskCount > 10000 || *tolerance < 0 || *tolerance > 1 || math.IsNaN(*tolerance) {
		return errors.New("invalid calibration arguments")
	}
	policy, err := parsePolicy(*policyName)
	if err != nil {
		return err
	}
	phaseFile, err := openBounded(*phasePath)
	if err != nil {
		return err
	}
	capture, err := scheduling.DecodePhaseCapture(phaseFile)
	closeErr := phaseFile.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	templates, provenance, err := scheduling.BuildCalibratedTasks(capture, scheduling.CalibrationOptions{
		Case: *caseName, ResidentBytes: *residentMiB << 20,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if *observedPath == "" {
		tasks := templates
		if *taskCount > 0 {
			tasks = scheduling.RepeatCalibratedTasks(templates, *taskCount)
		}
		return encoder.Encode(tasks)
	}
	if *taskCount != 0 {
		return errors.New("-tasks is only valid when writing a workload")
	}
	observedFile, err := openBounded(*observedPath)
	if err != nil {
		return err
	}
	observations, err := scheduling.DecodeBatchObservations(observedFile)
	closeErr = observedFile.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	filtered := observations[:0]
	for index, observation := range observations {
		if observation.Case != *caseName {
			continue
		}
		if observation.Mode != "" && observation.Mode != "executor" {
			return fmt.Errorf("observed row %d uses unsupported mode %q", index, observation.Mode)
		}
		if observation.Completed != 0 && observation.Completed != observation.Tasks {
			return fmt.Errorf("observed row %d did not complete every task", index)
		}
		if capture.Metadata.ToolDelayNS > 0 && observation.SyntheticHoldNS != capture.Metadata.ToolDelayNS {
			return fmt.Errorf("observed row %d hold %d does not match phase delay %d", index, observation.SyntheticHoldNS, capture.Metadata.ToolDelayNS)
		}
		filtered = append(filtered, observation)
	}
	if len(filtered) == 0 {
		return fmt.Errorf("observed input has no rows for case %q", *caseName)
	}
	comparisons, err := scheduling.CompareObservedBatches(templates, filtered, policy, *tolerance)
	if err != nil {
		return err
	}
	summaries := scheduling.SummarizeCalibrationComparisons(comparisons)
	metadata := reportMetadata{
		Type: "metadata", Method: "measured phase replay with equal residual CPU split",
		PhaseSource: *phasePath, ObservedSource: *observedPath, Case: *caseName,
		Policy: policy, Tolerance: *tolerance, Comparisons: len(comparisons), Summaries: len(summaries), Provenance: provenance,
	}
	for _, comparison := range comparisons {
		if comparison.WithinTolerance {
			metadata.WithinTolerance++
		}
		if comparison.RelativeError > metadata.MaxRelativeErr {
			metadata.MaxRelativeErr = comparison.RelativeError
		}
	}
	if err := encoder.Encode(metadata); err != nil {
		return err
	}
	for _, comparison := range comparisons {
		if err := encoder.Encode(comparison); err != nil {
			return err
		}
	}
	for _, summary := range summaries {
		if err := encoder.Encode(summary); err != nil {
			return err
		}
	}
	return nil
}

func openBounded(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCalibrationInputBytes {
		file.Close()
		return nil, fmt.Errorf("calibration input %q must be a regular file no larger than %d bytes", path, maxCalibrationInputBytes)
	}
	return file, nil
}

func parsePolicy(value string) (scheduling.Policy, error) {
	policy := scheduling.Policy(value)
	switch policy {
	case scheduling.FIFO, scheduling.ReadyFirst, scheduling.FinishSoon:
		return policy, nil
	default:
		return "", fmt.Errorf("unknown policy %q", value)
	}
}
