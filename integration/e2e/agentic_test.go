package e2e_test

import (
	"context"
	"encoding/json"
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
touch(file_name="agentic-e2e.txt")
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

func TestAgenticPreboundCompactExecutorUsesDefaultResultAndFreshGlobals(t *testing.T) {
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
	treatment, err := agentic.LoadDevelopmentTreatment(filepath.Join(root, "eval", "agentic", "v1", "treatments", "hybrid-two-stage-prebound-compact-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := agentic.NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agentic.NewWASIPythonExecutorForTreatment(context.Background(), guest, runtimeconfig.DefaultRunConfig(), tools, treatment)
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
	defaulted, err := executor.Execute(context.Background(), "agentic-compact-default", `
ephemeral_marker = "must-not-persist"
cd(folder="Documents")
touch(file_name="agentic-compact-e2e.txt")
`, 4)
	if err != nil || !defaulted.Success || defaulted.CapabilityCalls != 2 || string(defaulted.Observation) != `{}` || defaulted.ModelCodeDigest == "" || defaulted.EffectiveCodeDigest == "" || defaulted.WrapperDigest == "" {
		t.Fatalf("defaulted=%+v err=%v", defaulted, err)
	}
	if err := tools.SetTurn(1); err != nil {
		t.Fatal(err)
	}
	explicit, err := executor.Execute(context.Background(), "agentic-compact-explicit", `from __future__ import annotations
calls = 0
def once():
    global calls
    calls += 1
    return calls
result = {"calls": once()}`, 1)
	if err != nil || !explicit.Success || string(explicit.Observation) != `{"calls":1}` {
		t.Fatalf("explicit=%+v err=%v", explicit, err)
	}
	if err := tools.SetTurn(2); err != nil {
		t.Fatal(err)
	}
	beforeMismatch := tools.FileSystem().Digest()
	mismatch, err := executor.Execute(context.Background(), "agentic-compact-schema-mismatch", `
touch(file_name="schema-mismatch-must-rollback.txt")
result = [pwd()]
`, 2)
	if err != nil || mismatch.Success || mismatch.ErrorCode != "guest_output_schema_mismatch" || mismatch.CapabilityCalls != 2 || tools.FileSystem().Digest() != beforeMismatch {
		t.Fatalf("mismatch=%+v err=%v before=%s after=%s", mismatch, err, beforeMismatch, tools.FileSystem().Digest())
	}
	isolated, err := executor.Execute(context.Background(), "agentic-compact-isolated", `result = {"seen": ephemeral_marker}`, 1)
	if err != nil || isolated.Success || isolated.CapabilityCalls != 0 || isolated.ErrorCode != "python_exception" {
		t.Fatalf("isolated=%+v err=%v", isolated, err)
	}
	encoded, err := json.Marshal(defaulted)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("formal evidence encoding failed: %v %s", err, encoded)
	}
}
