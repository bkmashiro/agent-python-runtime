// Compare deterministic scheduling policies over explicit phase traces.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/scheduling"
)

type metadata struct {
	Type      string `json:"type"`
	Source    string `json:"source"`
	Tasks     int    `json:"tasks"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	Method    string `json:"method"`
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
	input := flag.String("input", "", "optional JSON array of scheduling.Task traces")
	policiesFlag := flag.String("policies", "fifo,ready_first,finish_soon", "comma-separated policies")
	liveFlag := flag.String("live", "both", "false, true, or both")
	running := flag.Int("running", 2, "running slots")
	resident := flag.Int("resident", 8, "resident Guest slots")
	tools := flag.Int("tools", 4, "in-flight external-tool slots")
	queued := flag.Int("queued", 0, "admission queue bound; zero is unlimited in the simulator")
	flag.Parse()

	tasks, source, err := loadWorkload(*input)
	if err != nil {
		return err
	}
	policies, err := parsePolicies(*policiesFlag)
	if err != nil {
		return err
	}
	liveModes, err := parseLiveModes(*liveFlag)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(metadata{
		Type: "metadata", Source: source, Tasks: len(tasks), GOOS: runtime.GOOS,
		GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		Method: "deterministic discrete-event simulation; durations are inputs, not predictions",
	}); err != nil {
		return err
	}
	for _, live := range liveModes {
		for _, policy := range policies {
			result, err := scheduling.Simulate(tasks, scheduling.Config{
				MaxRunning: *running, MaxResident: *resident,
				MaxInflightTools: *tools, MaxQueued: *queued,
				LiveIO: live, Policy: policy,
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
