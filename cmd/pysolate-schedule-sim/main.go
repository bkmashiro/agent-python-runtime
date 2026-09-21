// Compare deterministic scheduling policies over explicit phase traces.
package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/scheduling"
)

type metadata struct {
	Type      string `json:"type"`
	Scenario  string `json:"scenario"`
	Source    string `json:"source"`
	Tasks     int    `json:"tasks"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	Method    string `json:"method"`
}

type sweepMetadata struct {
	Type                string              `json:"type"`
	Scenario            string              `json:"scenario"`
	GOOS                string              `json:"goos"`
	GOARCH              string              `json:"goarch"`
	GoVersion           string              `json:"go_version"`
	Method              string              `json:"method"`
	TaskCounts          []int               `json:"task_counts"`
	IORatios            []float64           `json:"io_ratios"`
	RunningLimits       []int               `json:"running_limits"`
	ResidentMultipliers []int               `json:"resident_multipliers"`
	ToolLimits          []int               `json:"tool_limits"`
	Policies            []scheduling.Policy `json:"policies"`
	CPUBeforeNS         int64               `json:"cpu_before_ns"`
	CPUAfterNS          int64               `json:"cpu_after_ns"`
	ResidentBytes       int64               `json:"resident_bytes"`
	Points              int                 `json:"points"`
	Boundaries          int                 `json:"boundaries"`
}

type resultRow struct {
	Type string `json:"type"`
	scheduling.Result
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	scenario := flag.String("scenario", "mixed", "mixed or canonical")
	input := flag.String("input", "", "optional JSON array of scheduling.Task traces for mixed scenario")
	policiesFlag := flag.String("policies", "fifo,ready_first,finish_soon", "comma-separated policies")
	liveFlag := flag.String("live", "both", "false, true, or both; canonical requires both")
	runningFlag := flag.String("running", "2", "running slots, or comma-separated canonical grid")
	residentFlag := flag.String("resident", "8", "resident Guest slots for mixed scenario")
	toolsFlag := flag.String("tools", "4", "in-flight Tool slots, or comma-separated canonical grid")
	queued := flag.Int("queued", 0, "mixed admission queue bound; zero is unlimited in the simulator")
	tasksFlag := flag.String("tasks", "2,8,32", "canonical task-count grid")
	ioRatiosFlag := flag.String("io-ratios", "0.1,0.25,0.5,1,2,5,10,50,100", "canonical I/O-to-total-CPU ratios")
	residentMultipliersFlag := flag.String("resident-multipliers", "1,2,4,8", "canonical resident/running multipliers")
	cpuBefore := flag.Duration("cpu-before", time.Millisecond, "canonical CPU phase before I/O")
	cpuAfter := flag.Duration("cpu-after", time.Millisecond, "canonical CPU phase after I/O")
	residentMiB := flag.Int64("resident-mib", 8, "canonical estimated resident MiB per task")
	format := flag.String("format", "jsonl", "jsonl or csv; csv is canonical-only")
	flag.Parse()
	visited := map[string]bool{}
	flag.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	if *scenario == "canonical" {
		if visited["resident"] || visited["queued"] {
			return errors.New("canonical scenario uses -resident-multipliers and does not accept mixed -resident or -queued")
		}
		if !visited["running"] {
			*runningFlag = "1,2,4"
		}
		if !visited["tools"] {
			*toolsFlag = "1,2,4,8"
		}
		if !visited["policies"] {
			*policiesFlag = "fifo"
		}
	}

	policies, err := parsePolicies(*policiesFlag)
	if err != nil {
		return err
	}
	switch *scenario {
	case "mixed":
		if *format != "jsonl" {
			return errors.New("mixed scenario supports only jsonl output")
		}
		return runMixed(*input, policies, *liveFlag, *runningFlag, *residentFlag, *toolsFlag, *queued)
	case "canonical":
		if *input != "" {
			return errors.New("canonical scenario does not accept -input")
		}
		if *liveFlag != "both" {
			return errors.New("canonical scenario compares both Inline and live-I/O")
		}
		return runCanonical(canonicalFlags{
			tasks: *tasksFlag, ioRatios: *ioRatiosFlag, running: *runningFlag,
			residentMultipliers: *residentMultipliersFlag, tools: *toolsFlag,
			cpuBefore: *cpuBefore, cpuAfter: *cpuAfter, residentMiB: *residentMiB,
			format: *format, policies: policies,
		})
	default:
		return fmt.Errorf("unknown scenario %q", *scenario)
	}
}

func runMixed(input string, policies []scheduling.Policy, liveFlag, runningFlag, residentFlag, toolsFlag string, queued int) error {
	tasks, source, err := loadWorkload(input)
	if err != nil {
		return err
	}
	liveModes, err := parseLiveModes(liveFlag)
	if err != nil {
		return err
	}
	running, err := parseSinglePositiveInt(runningFlag, "running")
	if err != nil {
		return err
	}
	resident, err := parseSinglePositiveInt(residentFlag, "resident")
	if err != nil {
		return err
	}
	tools, err := parseSinglePositiveInt(toolsFlag, "tools")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(metadata{
		Type: "metadata", Scenario: "mixed", Source: source, Tasks: len(tasks),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		Method: "deterministic discrete-event simulation; durations are inputs, not predictions",
	}); err != nil {
		return err
	}
	for _, live := range liveModes {
		for _, policy := range policies {
			result, err := scheduling.Simulate(tasks, scheduling.Config{
				MaxRunning: running, MaxResident: resident, MaxInflightTools: tools,
				MaxQueued: queued, LiveIO: live, Policy: policy,
			})
			if err != nil {
				return err
			}
			if err := encoder.Encode(resultRow{Type: "result", Result: result}); err != nil {
				return err
			}
		}
	}
	return nil
}

type canonicalFlags struct {
	tasks, ioRatios, running, residentMultipliers, tools string
	cpuBefore, cpuAfter                                  time.Duration
	residentMiB                                          int64
	format                                               string
	policies                                             []scheduling.Policy
}

func runCanonical(flags canonicalFlags) error {
	tasks, err := parsePositiveInts(flags.tasks, "tasks")
	if err != nil {
		return err
	}
	ratios, err := parsePositiveFloats(flags.ioRatios, "io-ratios")
	if err != nil {
		return err
	}
	running, err := parsePositiveInts(flags.running, "running")
	if err != nil {
		return err
	}
	multipliers, err := parsePositiveInts(flags.residentMultipliers, "resident-multipliers")
	if err != nil {
		return err
	}
	tools, err := parsePositiveInts(flags.tools, "tools")
	if err != nil {
		return err
	}
	if flags.residentMiB < 0 || flags.residentMiB > 1<<20 {
		return errors.New("resident-mib must be between 0 and 1048576")
	}
	grid := scheduling.CanonicalSweep{
		TaskCounts: tasks, IORatios: ratios, RunningLimits: running,
		ResidentMultipliers: multipliers, ToolLimits: tools, Policies: flags.policies,
		CPUBefore: flags.cpuBefore, CPUAfter: flags.cpuAfter, ResidentBytes: flags.residentMiB << 20,
	}
	points, boundaries, err := scheduling.RunCanonicalSweep(grid)
	if err != nil {
		return err
	}
	switch flags.format {
	case "jsonl":
		return writeSweepJSONL(os.Stdout, grid, points, boundaries)
	case "csv":
		return writeSweepCSV(os.Stdout, points, boundaries)
	default:
		return fmt.Errorf("unknown format %q", flags.format)
	}
}

func writeSweepJSONL(writer io.Writer, grid scheduling.CanonicalSweep, points []scheduling.SweepPoint, boundaries []scheduling.SweepBoundary) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(sweepMetadata{
		Type: "metadata", Scenario: "canonical", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		GoVersion: runtime.Version(), Method: "deterministic canonical grid; no randomness or runtime prediction",
		TaskCounts: grid.TaskCounts, IORatios: grid.IORatios, RunningLimits: grid.RunningLimits,
		ResidentMultipliers: grid.ResidentMultipliers, ToolLimits: grid.ToolLimits, Policies: grid.Policies,
		CPUBeforeNS: grid.CPUBefore.Nanoseconds(), CPUAfterNS: grid.CPUAfter.Nanoseconds(), ResidentBytes: grid.ResidentBytes,
		Points: len(points), Boundaries: len(boundaries),
	}); err != nil {
		return err
	}
	for _, point := range points {
		if err := encoder.Encode(point); err != nil {
			return err
		}
	}
	for _, boundary := range boundaries {
		if err := encoder.Encode(boundary); err != nil {
			return err
		}
	}
	return nil
}

func writeSweepCSV(writer io.Writer, points []scheduling.SweepPoint, boundaries []scheduling.SweepBoundary) error {
	csvWriter := csv.NewWriter(writer)
	header := []string{
		"type", "tasks", "io_ratio", "cpu_before_ns", "io_ns", "cpu_after_ns",
		"running_limit", "resident_multiplier", "resident_limit", "tool_limit", "policy",
		"inline_makespan_ns", "live_makespan_ns", "speedup", "inline_mean_latency_ns", "live_mean_latency_ns",
		"inline_p95_latency_ns", "live_p95_latency_ns", "inline_peak_resident", "live_peak_resident",
		"inline_resident_mib_seconds", "live_resident_mib_seconds", "resident_area_ratio",
		"first_five_percent_ratio", "first_ten_percent_ratio", "max_speedup", "max_speedup_ratio", "resident_area_ratio_at_max",
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}
	for _, point := range points {
		row := []string{
			"point", integer(point.Tasks), decimal(point.IORatio), duration(point.CPUBefore), duration(point.IODuration), duration(point.CPUAfter),
			integer(point.RunningLimit), integer(point.ResidentMultiplier), integer(point.ResidentLimit), integer(point.ToolLimit), string(point.Policy),
			duration(point.InlineMakespan), duration(point.LiveMakespan), decimal(point.Speedup), duration(point.InlineMeanLatency), duration(point.LiveMeanLatency),
			duration(point.InlineP95Latency), duration(point.LiveP95Latency), integer(point.InlinePeakResident), integer(point.LivePeakResident),
			decimal(point.InlineResidentMiBSecs), decimal(point.LiveResidentMiBSecs), decimal(point.ResidentAreaRatio), "", "", "", "", "",
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	for _, boundary := range boundaries {
		row := []string{
			"boundary", integer(boundary.Tasks), "", "", "", "",
			integer(boundary.RunningLimit), integer(boundary.ResidentMultiplier), integer(boundary.ResidentLimit), integer(boundary.ToolLimit), string(boundary.Policy),
			"", "", "", "", "", "", "", "", "", "", "", "",
			optionalDecimal(boundary.FirstFivePercentRatio), optionalDecimal(boundary.FirstTenPercentRatio), decimal(boundary.MaxSpeedup), decimal(boundary.MaxSpeedupRatio), decimal(boundary.ResidentAreaRatioAtMax),
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func integer(value int) string            { return strconv.Itoa(value) }
func duration(value time.Duration) string { return strconv.FormatInt(value.Nanoseconds(), 10) }
func decimal(value float64) string        { return strconv.FormatFloat(value, 'g', -1, 64) }
func optionalDecimal(value *float64) string {
	if value == nil {
		return ""
	}
	return decimal(*value)
}

func loadWorkload(path string) ([]scheduling.Task, string, error) {
	if path == "" {
		return builtInWorkload(), "built-in-common-agent-v1", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	var tasks []scheduling.Task
	if err := decoder.Decode(&tasks); err != nil {
		return nil, "", fmt.Errorf("decode workload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("workload contains trailing JSON")
	}
	return tasks, path, nil
}

func parsePolicies(value string) ([]scheduling.Policy, error) {
	parts := strings.Split(value, ",")
	result := make([]scheduling.Policy, 0, len(parts))
	seen := map[scheduling.Policy]bool{}
	for _, part := range parts {
		policy := scheduling.Policy(strings.TrimSpace(part))
		switch policy {
		case scheduling.FIFO, scheduling.ReadyFirst, scheduling.FinishSoon:
		default:
			return nil, fmt.Errorf("unknown policy %q", part)
		}
		if !seen[policy] {
			seen[policy] = true
			result = append(result, policy)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no policies selected")
	}
	return result, nil
}

func parseLiveModes(value string) ([]bool, error) {
	switch value {
	case "false":
		return []bool{false}, nil
	case "true":
		return []bool{true}, nil
	case "both":
		return []bool{false, true}, nil
	default:
		return nil, fmt.Errorf("invalid live mode %q", value)
	}
}

func parseSinglePositiveInt(value, label string) (int, error) {
	values, err := parsePositiveInts(value, label)
	if err != nil {
		return 0, err
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("%s requires one value in mixed scenario", label)
	}
	return values[0], nil
}

func parsePositiveInts(value, label string) ([]int, error) {
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	seen := map[int]bool{}
	for _, part := range parts {
		parsed, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%s values must be positive integers", label)
		}
		if !seen[parsed] {
			seen[parsed] = true
			result = append(result, parsed)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no %s values", label)
	}
	sort.Ints(result)
	return result, nil
}

func parsePositiveFloats(value, label string) ([]float64, error) {
	parts := strings.Split(value, ",")
	result := make([]float64, 0, len(parts))
	seen := map[float64]bool{}
	for _, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, fmt.Errorf("%s values must be finite positive numbers", label)
		}
		if !seen[parsed] {
			seen[parsed] = true
			result = append(result, parsed)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no %s values", label)
	}
	sort.Float64s(result)
	return result, nil
}

func builtInWorkload() []scheduling.Task {
	shapes := [][]scheduling.Phase{
		{{Kind: scheduling.CPU, Duration: 5 * time.Millisecond}, {Kind: scheduling.ExternalIO, Duration: 100 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 2 * time.Millisecond}},
		{{Kind: scheduling.CPU, Duration: 5 * time.Millisecond}, {Kind: scheduling.ExternalIO, Duration: 80 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 3 * time.Millisecond}, {Kind: scheduling.ExternalIO, Duration: 80 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 2 * time.Millisecond}},
		{{Kind: scheduling.CPU, Duration: 5 * time.Millisecond}, {Kind: scheduling.ExternalIO, Duration: 100 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 25 * time.Millisecond}},
		{{Kind: scheduling.CPU, Duration: 5 * time.Millisecond}, {Kind: scheduling.ExternalIO, Duration: 50 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 2 * time.Millisecond}, {Kind: scheduling.DurableWait, Duration: 200 * time.Millisecond}, {Kind: scheduling.CPU, Duration: 10 * time.Millisecond}},
	}
	resident := []int64{8 << 20, 8 << 20, 32 << 20, 8 << 20}
	result := make([]scheduling.Task, 0, 12)
	for repeat := 0; repeat < 3; repeat++ {
		for index, phases := range shapes {
			result = append(result, scheduling.Task{
				ID:            fmt.Sprintf("shape-%d-%d", index, repeat),
				Arrival:       time.Duration(repeat*len(shapes)+index) * 2 * time.Millisecond,
				ResidentBytes: resident[index], Phases: append([]scheduling.Phase(nil), phases...),
			})
		}
	}
	return result
}
