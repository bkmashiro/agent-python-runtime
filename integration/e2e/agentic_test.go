package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestAgenticPythonExecutorUsesRealGuestAndRollsBackFailure(t *testing.T) {
	root := repositoryRoot(t)
	dataset, err := agentic.Load(filepath.Join(root, "eval", "agentic", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task agentic.Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "bfcl-v4-stateful-local-tools-multi_turn_base_12" {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("agentic fixture missing")
	}
	tools, err := agentic.NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agentic.NewWASIPythonExecutor(context.Background(), guest, runtimeconfig.DefaultRunConfig(), tools)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := executor.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := tools.SetTurn(0); err != nil {
		t.Fatal(err)
	}
	initial := tools.FileSystem().Digest()
	committed, err := executor.Execute(context.Background(), "agentic-e2e-commit", `
from host_tools import cd, touch
cd(folder="Documents")
touch(name="agentic-e2e.txt")
result = {"ok": True}
`, 4)
	if err != nil || !committed.Success || committed.CapabilityCalls != 2 || tools.FileSystem().Digest() == initial {
		t.Fatalf("committed=%+v err=%v", committed, err)
	}
	committedState := tools.FileSystem().Digest()
	if err := tools.SetTurn(1); err != nil {
		t.Fatal(err)
	}
	failed, err := executor.Execute(context.Background(), "agentic-e2e-rollback", `
from host_tools import echo
echo(content="must roll back", file_name="agentic-e2e.txt")
raise RuntimeError("expected rollback")
`, 4)
	if err != nil || failed.Success || failed.CapabilityCalls != 1 || tools.FileSystem().Digest() != committedState {
		t.Fatalf("failed=%+v err=%v before=%s after=%s", failed, err, committedState, tools.FileSystem().Digest())
	}
}
