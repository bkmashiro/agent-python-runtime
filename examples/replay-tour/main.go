// Command replay-tour shows deterministic execution, Tool outcome replay, and
// the separate Host-owned workspace continuation boundary.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const recordedSource = `import os, random, time
before = time.time()
time.sleep(2.0)
result = {
    "python_random": random.Random().getrandbits(64),
    "random_bytes": os.urandom(8).hex(),
    "logical_elapsed": round(time.time() - before, 3),
    "set_order": list({"alpha", "beta", "gamma", "delta"}),
    "tool": catalog.lookup(key="demo"),
}`

const workspaceWriteSource = `from pathlib import Path
Path("/workspace/replay-output.json").write_text(inputs["recorded_json"] + "\n")
result = {"written": "replay-output.json"}`

const workspaceReadSource = `from pathlib import Path
import json
path = Path("/workspace/replay-output.json")
result = {"path": path.name, "saved": json.loads(path.read_text())}`

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

type recordedReport struct {
	Seed           string          `json:"seed"`
	First          json.RawMessage `json:"first_execution"`
	Replay         json.RawMessage `json:"replayed_execution"`
	Identical      bool            `json:"identical"`
	ToolDispatches int64           `json:"tool_dispatches"`
	ToolReplays    int             `json:"tool_outcome_replays"`
}

type workspaceReport struct {
	Boundary                string          `json:"boundary"`
	FirstGuest              json.RawMessage `json:"first_guest"`
	SecondGuest             json.RawMessage `json:"second_guest"`
	PersistedAcrossGuests   bool            `json:"persisted_across_guests"`
	RecordedWorkspaceReplay bool            `json:"recorded_workspace_replay"`
}

type report struct {
	Recorded  recordedReport  `json:"recorded_execution"`
	Workspace workspaceReport `json:"workspace_continuation"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "path to the Guest artifact")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := run(ctx, *guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay tour:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "replay tour:", err)
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
	seed := "replay-tour-seed"
	first, err := runner.RunRecorded(ctx, recordedSource, nil, seed, journal)
	if err != nil {
		_ = runner.Close(context.Background())
		return result, err
	}
	replayed, err := runner.RunRecorded(ctx, recordedSource, nil, seed, journal)
	closeErr := runner.Close(context.Background())
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	identical := bytes.Equal(first.Value, replayed.Value)
	if !identical || dispatches.Load() != 1 || journal.replayCount() != 1 {
		return result, fmt.Errorf("unexpected replay evidence: identical=%t dispatches=%d replays=%d", identical, dispatches.Load(), journal.replayCount())
	}
	result.Recorded = recordedReport{
		Seed: seed, First: first.Value, Replay: replayed.Value, Identical: identical,
		ToolDispatches: dispatches.Load(), ToolReplays: journal.replayCount(),
	}

	workspace, err := runWorkspaceContinuation(ctx, wasm, first.Value)
	if err != nil {
		return result, err
	}
	result.Workspace = workspace
	return result, nil
}

func runWorkspaceContinuation(ctx context.Context, wasm []byte, recorded json.RawMessage) (workspaceReport, error) {
	var result workspaceReport
	temporary, err := os.MkdirTemp("", "pysolate-replay-tour-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temporary)

	source := filepath.Join(temporary, "source")
	base := filepath.Join(temporary, "workspaces")
	if err := os.MkdirAll(source, 0o700); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("private workspace\n"), 0o600); err != nil {
		return result, err
	}
	if err := os.Mkdir(base, 0o700); err != nil {
		return result, err
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		return result, err
	}
	defer manager.Close()
	ref, err := manager.CreateFromDirectory(source, workspacepkg.DefaultLimits())
	if err != nil {
		return result, err
	}
	lease, err := manager.Acquire(ref, "replay-tour")
	if err != nil {
		return result, err
	}
	defer lease.Release()

	runner, err := pysolate.NewPreparedWorkspace(ctx, wasm, nil)
	if err != nil {
		return result, err
	}
	defer runner.Close(context.Background())
	written, err := runner.RunWorkspace(ctx, workspaceWriteSource, map[string]any{"recorded_json": string(recorded)}, lease)
	if err != nil {
		return result, err
	}
	readBack, err := runner.RunWorkspace(ctx, workspaceReadSource, nil, lease)
	if err != nil {
		return result, err
	}
	var decoded struct {
		Saved json.RawMessage `json:"saved"`
	}
	if err := json.Unmarshal(readBack.Value, &decoded); err != nil {
		return result, err
	}
	persisted := jsonEqual(recorded, decoded.Saved)
	if !persisted {
		return result, fmt.Errorf("second Guest did not read the first Guest's workspace value")
	}
	return workspaceReport{
		Boundary:   "Host-owned workspace survives disposable Guests; it is not replayed by RunRecorded",
		FirstGuest: written.Value, SecondGuest: readBack.Value,
		PersistedAcrossGuests: persisted, RecordedWorkspaceReplay: false,
	}, nil
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
