// Command pysolate-corpus replays frozen programs and Host tool fixtures through the real Guest.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/corpus"
	"github.com/tetratelabs/wazero"
)

type config struct {
	guest      string
	corpus     string
	prepared   string
	execution  string
	caseID     string
	limit      int
	iterations int
	timeout    time.Duration
}

type sample struct {
	Type      string          `json:"type"`
	CaseID    string          `json:"case_id"`
	Dataset   string          `json:"dataset"`
	Revision  string          `json:"revision"`
	Artifact  string          `json:"artifact"`
	Prepared  string          `json:"prepared"`
	Execution string          `json:"execution"`
	Iteration int             `json:"iteration"`
	SetupNS   int64           `json:"setup_ns"`
	RunNS     int64           `json:"run_ns"`
	Passed    bool            `json:"passed"`
	Value     json.RawMessage `json:"value,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type summary struct {
	Type    string `json:"type"`
	Cases   int    `json:"cases"`
	Samples int    `json:"samples"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config{}
	flag.StringVar(&cfg.guest, "guest", "dist/pysolate.wasm", "agent-core Guest artifact")
	flag.StringVar(&cfg.corpus, "corpus", "", "frozen corpus JSONL")
	flag.StringVar(&cfg.prepared, "prepared", "copy", "fresh, copy, or Linux cow")
	flag.StringVar(&cfg.execution, "execution", "normal", "normal or early-reads")
	flag.StringVar(&cfg.caseID, "case", "", "optional exact case id")
	flag.IntVar(&cfg.limit, "limit", 0, "maximum selected cases; 0 means all")
	flag.IntVar(&cfg.iterations, "iterations", 1, "replays per prepared Runner")
	flag.DurationVar(&cfg.timeout, "timeout", 30*time.Second, "per setup and run deadline")
	flag.Parse()
	if cfg.corpus == "" || cfg.iterations < 1 || cfg.limit < 0 || cfg.timeout <= 0 {
		return errors.New("invalid corpus runner configuration")
	}
	if cfg.prepared != "fresh" && cfg.prepared != "copy" && cfg.prepared != "cow" {
		return fmt.Errorf("unknown preparation mode %q", cfg.prepared)
	}
	if cfg.execution != "normal" && cfg.execution != "early-reads" {
		return fmt.Errorf("unknown execution mode %q", cfg.execution)
	}
	wasm, err := os.ReadFile(cfg.guest)
	if err != nil {
		return err
	}
	file, err := os.Open(cfg.corpus)
	if err != nil {
		return err
	}
	cases, decodeErr := corpus.DecodeJSONL(file)
	closeErr := file.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return err
	}
	selected := selectCases(cases, cfg.caseID, cfg.limit)
	if len(selected) == 0 {
		return errors.New("no corpus cases selected")
	}
	cache := wazero.NewCompilationCache()
	defer cache.Close(context.Background())
	base := pysolate.WithCompilationCache(context.Background(), cache)
	encoder := json.NewEncoder(os.Stdout)
	artifact := fmt.Sprintf("sha256:%x", sha256.Sum256(wasm))
	result := summary{Type: "summary", Cases: len(selected)}
	for _, item := range selected {
		rows := executeCase(base, wasm, artifact, item, cfg)
		for _, row := range rows {
			result.Samples++
			if row.Passed {
				result.Passed++
			} else {
				result.Failed++
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
	}
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if result.Failed > 0 {
		return fmt.Errorf("%d corpus samples failed", result.Failed)
	}
	return nil
}

func selectCases(items []corpus.Case, id string, limit int) []corpus.Case {
	selected := make([]corpus.Case, 0, len(items))
	for _, item := range items {
		if id != "" && item.ID != id {
			continue
		}
		selected = append(selected, item)
		if limit > 0 && len(selected) == limit {
			break
		}
	}
	return selected
}

func executeCase(base context.Context, wasm []byte, artifact string, item corpus.Case, cfg config) []sample {
	provider, err := corpus.NewReplayProvider(item)
	if err != nil {
		return []sample{failedSample(item, artifact, cfg, 0, 0, 0, err)}
	}
	manifest, err := pysolate.ManifestFromProviders(base, provider)
	if err != nil {
		return []sample{failedSample(item, artifact, cfg, 0, 0, 0, err)}
	}
	setupCtx, cancelSetup := context.WithTimeout(base, cfg.timeout)
	started := time.Now()
	var runner *pysolate.Runner
	switch cfg.prepared {
	case "fresh":
		runner, err = pysolate.New(setupCtx, wasm, manifest)
	case "copy":
		runner, err = pysolate.NewPrepared(setupCtx, wasm, manifest)
	case "cow":
		runner, err = pysolate.NewPreparedCOW(setupCtx, wasm, manifest)
	}
	setupNS := time.Since(started).Nanoseconds()
	cancelSetup()
	if err != nil {
		return []sample{failedSample(item, artifact, cfg, 0, setupNS, 0, err)}
	}
	var inputs any
	decoder := json.NewDecoder(bytes.NewReader(item.Inputs))
	decoder.UseNumber()
	if err := decoder.Decode(&inputs); err != nil {
		_ = runner.Close(context.Background())
		return []sample{failedSample(item, artifact, cfg, 0, setupNS, 0, err)}
	}
	rows := make([]sample, 0, cfg.iterations)
	for iteration := 0; iteration < cfg.iterations; iteration++ {
		if iteration > 0 {
			if err := provider.Reset(); err != nil {
				rows = append(rows, failedSample(item, artifact, cfg, iteration, setupNS, 0, err))
				break
			}
		}
		runCtx, cancelRun := context.WithTimeout(base, cfg.timeout)
		started = time.Now()
		var output pysolate.Output
		if cfg.execution == "early-reads" {
			output, err = runner.RunWithEarlyReads(runCtx, item.Source, inputs)
		} else {
			output, err = runner.Run(runCtx, item.Source, inputs)
		}
		runNS := time.Since(started).Nanoseconds()
		cancelRun()
		if replayErr := provider.Verify(); replayErr != nil {
			err = errors.Join(err, replayErr)
		}
		row := sample{
			Type: "sample", CaseID: item.ID, Dataset: item.Origin.Dataset, Revision: item.Origin.Revision,
			Artifact: artifact, Prepared: cfg.prepared, Execution: cfg.execution, Iteration: iteration,
			SetupNS: setupNS, RunNS: runNS, Value: append(json.RawMessage(nil), output.Value...),
		}
		if err != nil {
			row.Error = err.Error()
		} else if !corpus.EqualJSON(output.Value, item.Expected) {
			row.Error = fmt.Sprintf("result mismatch: got %s want %s", output.Value, item.Expected)
		} else {
			row.Passed = true
		}
		rows = append(rows, row)
		if !row.Passed {
			break
		}
	}
	if closeErr := runner.Close(context.Background()); closeErr != nil {
		if len(rows) == 0 {
			rows = append(rows, failedSample(item, artifact, cfg, 0, setupNS, 0, closeErr))
		} else {
			last := &rows[len(rows)-1]
			last.Passed = false
			if last.Error == "" {
				last.Error = closeErr.Error()
			} else {
				last.Error = errors.Join(errors.New(last.Error), closeErr).Error()
			}
		}
	}
	return rows
}

func failedSample(item corpus.Case, artifact string, cfg config, iteration int, setupNS, runNS int64, err error) sample {
	return sample{
		Type: "sample", CaseID: item.ID, Dataset: item.Origin.Dataset, Revision: item.Origin.Revision,
		Artifact: artifact, Prepared: cfg.prepared, Execution: cfg.execution, Iteration: iteration,
		SetupNS: setupNS, RunNS: runNS, Error: err.Error(),
	}
}
