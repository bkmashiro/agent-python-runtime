package e2e_test

import (
	"os"
	"testing"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestWorkspaceContinuesFilesAcrossDisposablePreparedInstances(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close workspace manager: %v", err)
		}
	})
	ref, err := manager.Create([]workspace.InitialFile{{Path: "state/count.txt", Data: []byte("0")}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), wazeroengine.Factory{
		Strategy:         engine.StrategySingleUsePrepared,
		PreparedCapacity: 1,
		WorkspaceManager: manager,
		WorkspaceRef:     ref,
		WorkspaceOwner:   "e2e-workspace-runner",
	})

	first := run(t, instance, "workspace-1", `
from pathlib import Path
path = Path("/workspace/state/count.txt")
count = int(path.read_text()) + 1
path.write_text(str(count))
heap_only = 99
result = {"count": count}
`, map[string]any{})
	if first.Status != "ok" || first.Result.(map[string]any)["count"] != float64(1) {
		t.Fatalf("first workspace Run: %#v", first)
	}

	second := run(t, instance, "workspace-2", `
from pathlib import Path
count = int(Path("/workspace/state/count.txt").read_text())
result = {"count": count, "heap_continued": "heap_only" in globals()}
`, map[string]any{})
	secondResult := second.Result.(map[string]any)
	if second.Status != "ok" || secondResult["count"] != float64(1) || secondResult["heap_continued"] != false {
		t.Fatalf("second workspace Run: %#v", second)
	}

	failed := run(t, instance, "workspace-failed", `
from pathlib import Path
Path("/workspace/state/count.txt").write_text("2")
raise RuntimeError("intentional")
`, map[string]any{})
	if failed.Status != "error" {
		t.Fatalf("failed Run unexpectedly succeeded: %#v", failed)
	}
	last := run(t, instance, "workspace-after-failure", `
from pathlib import Path
result = int(Path("/workspace/state/count.txt").read_text())
`, map[string]any{})
	if last.Status != "ok" || last.Result != float64(2) {
		t.Fatalf("completed pre-failure write did not persist: %#v", last)
	}
}
