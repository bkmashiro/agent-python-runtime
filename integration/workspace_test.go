package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestWorkspaceGuestReadsAndEditsPrivateFiles(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("host-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.CreateFromDirectory(source, workspacepkg.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "guest-test")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := pysolate.New(ctx, wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	out, err := runner.RunWorkspace(ctx, `from pathlib import Path
p=Path('/workspace/notes.txt')
p.write_text(p.read_text() + '-after')
Path('/workspace/new.txt').write_text('created')
result=sorted(x.name for x in Path('/workspace').iterdir())`, nil, lease)
	var names []string
	decodeErr := json.Unmarshal(out.Value, &names)
	if err != nil || decodeErr != nil || len(names) != 2 || names[0] != "new.txt" || names[1] != "notes.txt" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	files, err := lease.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != "new.txt" || string(files[1].Data) != "before-after" {
		t.Fatalf("files=%#v", files)
	}
	original, err := os.ReadFile(filepath.Join(source, "notes.txt"))
	if err != nil || string(original) != "before" {
		t.Fatalf("source changed: %q err=%v", original, err)
	}
	if _, err := os.Stat(filepath.Join(source, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("workspace write escaped to source: %v", err)
	}
}

func TestWorkspaceIsCurrentDirectoryAndImportRoot(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.Create([]workspacepkg.InitialFile{{Path: "helper.py", Data: []byte("value = 41\n")}}, workspacepkg.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "workspace-python-test")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := pysolate.New(ctx, wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	out, err := runner.RunWorkspace(ctx, `import os
from helper import value
open("relative.txt", "w").write(str(value + 1))
result=[os.getcwd(), value]`, nil, lease)
	var value []any
	decodeErr := json.Unmarshal(out.Value, &value)
	if err != nil || decodeErr != nil || len(value) != 2 || value[0] != "/workspace" || value[1] != float64(41) {
		t.Fatalf("out=%+v err=%v decodeErr=%v", out, err, decodeErr)
	}
	file, err := lease.ReadFile("relative.txt", 16)
	if err != nil || string(file.Data) != "42" {
		t.Fatalf("file=%#v err=%v", file, err)
	}
}

func TestWorkspaceIsNotAmbient(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := pysolate.New(ctx, wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	out, err := runner.Run(ctx, `from pathlib import Path
result=Path('/workspace').exists()`, nil)
	if err != nil || string(out.Value) != "false" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestPreparedRunnersAttachWorkspacePerRun(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	constructors := map[string]func(context.Context, []byte, pysolate.Manifest) (*pysolate.Runner, error){"copy": pysolate.NewPreparedWorkspace}
	if runtime.GOOS == "linux" {
		constructors["cow"] = pysolate.NewPreparedWorkspaceCOW
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "workspaces")
			if err := os.Mkdir(base, 0o700); err != nil {
				t.Fatal(err)
			}
			manager, err := workspacepkg.NewManager(base)
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			ref, err := manager.Create([]workspacepkg.InitialFile{{Path: "value.txt", Data: []byte("41")}}, workspacepkg.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			lease, err := manager.Acquire(ref, "prepared-test")
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			runner, err := construct(ctx, wasm, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close(context.Background())
			out, err := runner.RunWorkspace(ctx, `result=int(open('/workspace/value.txt').read())+1`, nil, lease)
			if err != nil || string(out.Value) != "42" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestFailedWorkspaceRunRemainsInspectableUntilCleanup(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.Create([]workspacepkg.InitialFile{{Path: "before.txt", Data: []byte("before")}}, workspacepkg.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "failed-run-test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := pysolate.New(ctx, wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := runner.RunWorkspace(ctx, `open('/workspace/partial.txt', 'w').write('inspect me')
raise RuntimeError('expected failure')`, nil, lease)
	if runErr == nil || !strings.Contains(runErr.Error(), "expected failure") {
		t.Fatalf("run error=%v", runErr)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	changes := workspacepkg.Diff(before, after)
	if changes.Added != 1 || len(changes.Changes) != 1 || changes.Changes[0].Path != "partial.txt" {
		t.Fatalf("changes=%#v", changes)
	}
	files, err := lease.Files()
	if err != nil || len(files) != 2 || files[1].Path != "partial.txt" || string(files[1].Data) != "inspect me" {
		t.Fatalf("files=%#v err=%v", files, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Files(); !errors.Is(err, workspacepkg.ErrWorkspaceClosed) {
		t.Fatalf("files after cleanup error=%v", err)
	}
	if _, err := manager.Acquire(ref, "after-cleanup"); !errors.Is(err, workspacepkg.ErrWorkspaceClosed) {
		t.Fatalf("acquire after cleanup error=%v", err)
	}
}
