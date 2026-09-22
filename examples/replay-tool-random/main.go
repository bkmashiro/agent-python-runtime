// Command replay-tool-random demonstrates deterministic Guest inputs and one
// recorded Host Tool outcome.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

const source = `import os, random, time
before = time.time()
time.sleep(2.0)
result = {
    "python_random": random.Random().getrandbits(64),
    "random_bytes": os.urandom(8).hex(),
    "logical_elapsed": round(time.time() - before, 3),
    "set_order": list({"alpha", "beta", "gamma", "delta"}),
    "tool": catalog.lookup(key="demo"),
}`

type memoryJournal struct {
	mu       sync.Mutex
	outcomes map[string][]byte
	replays  int
}

func (journal *memoryJournal) Call(ctx context.Context, name string, args json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
	key := name + "\x00" + string(args)
	journal.mu.Lock()
	if outcome, ok := journal.outcomes[key]; ok {
		journal.replays++
		copyOf := append([]byte(nil), outcome...)
		journal.mu.Unlock()
		return copyOf, nil
	}
	journal.mu.Unlock()

	outcome := next(ctx)
	journal.mu.Lock()
	journal.outcomes[key] = append([]byte(nil), outcome...)
	journal.mu.Unlock()
	return outcome, nil
}

func (journal *memoryJournal) replayCount() int {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return journal.replays
}

type report struct {
	Seed           string          `json:"seed"`
	First          json.RawMessage `json:"first_execution"`
	Replay         json.RawMessage `json:"replayed_execution"`
	Identical      bool            `json:"identical"`
	ToolDispatches int64           `json:"tool_dispatches"`
	ToolReplays    int             `json:"tool_outcome_replays"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "path to the Guest artifact")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := run(ctx, *guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay tool/random demo:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "replay tool/random demo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, guestPath string) (report, error) {
	var result report
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return result, err
	}

	journal := &memoryJournal{outcomes: make(map[string][]byte)}
	var dispatches atomic.Int64
	manifest := pysolate.Manifest{
		"catalog/lookup": {
			PythonPath: "catalog.lookup",
			Call: func(context.Context, json.RawMessage) (any, error) {
				dispatches.Add(1)
				return map[string]any{"key": "demo", "value": 42}, nil
			},
		},
	}
	runner, err := pysolate.New(ctx, wasm, manifest)
	if err != nil {
		return result, err
	}
	defer runner.Close(context.Background())

	seed := "replay-demo-seed"
	first, err := runner.RunRecorded(ctx, source, nil, seed, journal)
	if err != nil {
		return result, err
	}
	replayed, err := runner.RunRecorded(ctx, source, nil, seed, journal)
	if err != nil {
		return result, err
	}
	identical := bytes.Equal(first.Value, replayed.Value)
	if !identical || dispatches.Load() != 1 || journal.replayCount() != 1 {
		return result, fmt.Errorf("unexpected evidence: identical=%t dispatches=%d replays=%d", identical, dispatches.Load(), journal.replayCount())
	}
	return report{
		Seed: seed, First: first.Value, Replay: replayed.Value, Identical: identical,
		ToolDispatches: dispatches.Load(), ToolReplays: journal.replayCount(),
	}, nil
}
