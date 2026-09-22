// Command workspace-continuation demonstrates that a Host-owned workspace
// survives between disposable Guests. It does not use RunRecorded replay.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const firstGuestSource = `from pathlib import Path
import json
path = Path("/workspace/progress.json")
path.write_text(json.dumps({"completed": ["fetch"]}) + "\n")
result = {"written": path.name}`

const secondGuestSource = `from pathlib import Path
import json
path = Path("/workspace/progress.json")
progress = json.loads(path.read_text())
progress["completed"].append("summarize")
path.write_text(json.dumps(progress) + "\n")
result = {"read": "fetch" in progress["completed"], "progress": progress}`

type report struct {
	Boundary              string          `json:"boundary"`
	FirstGuest            json.RawMessage `json:"first_guest"`
	SecondGuest           json.RawMessage `json:"second_guest"`
	PersistedAcrossGuests bool            `json:"persisted_across_guests"`
	UsesRunRecordedReplay bool            `json:"uses_run_recorded_replay"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "path to the Guest artifact")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := run(ctx, *guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace continuation demo:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "workspace continuation demo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, guestPath string) (report, error) {
	var result report
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return result, err
	}
	temporary, err := os.MkdirTemp("", "pysolate-workspace-continuation-")
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
	lease, err := manager.Acquire(ref, "workspace-continuation-demo")
	if err != nil {
		return result, err
	}
	defer lease.Release()

	runner, err := pysolate.NewPreparedWorkspace(ctx, wasm, nil)
	if err != nil {
		return result, err
	}
	defer runner.Close(context.Background())
	first, err := runner.RunWorkspace(ctx, firstGuestSource, nil, lease)
	if err != nil {
		return result, err
	}
	second, err := runner.RunWorkspace(ctx, secondGuestSource, nil, lease)
	if err != nil {
		return result, err
	}
	var observed struct {
		Read bool `json:"read"`
	}
	if err := json.Unmarshal(second.Value, &observed); err != nil {
		return result, err
	}
	if !observed.Read {
		return result, fmt.Errorf("second Guest did not observe first Guest's file")
	}
	return report{
		Boundary:   "Host-owned workspace continuation across disposable Guests",
		FirstGuest: first.Value, SecondGuest: second.Value,
		PersistedAcrossGuests: true, UsesRunRecordedReplay: false,
	}, nil
}
