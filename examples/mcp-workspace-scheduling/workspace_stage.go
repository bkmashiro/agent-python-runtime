package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const workspaceProgram = `from pathlib import Path
import json

root = Path("/workspace")
record = inputs["record"]
config_path = root / "config.json"
config = json.loads(config_path.read_text())
config.update({
    "sku": record["sku"],
    "price": record["price"],
    "currency": record["currency"],
})
config_path.write_text(json.dumps(config, sort_keys=True) + "\n")
(root / "REPORT.md").write_text(
    f"# {record['sku']}\n\nPrice: {record['price']} {record['currency']}\n\n{record['note']}\n"
)
result = {"sku": record["sku"], "changed": ["REPORT.md", "config.json"]}
`

func runWorkspaceStage(ctx context.Context, runner *pysolate.Runner, manager *workspacepkg.Manager, source string, record workflowRecord, owner string) (int, bool, error) {
	ref, err := manager.CreateFromDirectory(source, workspacepkg.DefaultLimits())
	if err != nil {
		return 0, false, err
	}
	lease, err := manager.Acquire(ref, owner)
	if err != nil {
		return 0, false, err
	}
	defer lease.Release()
	before, err := lease.Snapshot()
	if err != nil {
		return 0, false, err
	}
	output, err := runner.RunWorkspace(ctx, workspaceProgram, map[string]any{"record": record}, lease)
	if err != nil {
		return 0, false, err
	}
	var guestResult struct {
		SKU     string   `json:"sku"`
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal(output.Value, &guestResult); err != nil {
		return 0, false, err
	}
	if guestResult.SKU != record.SKU || len(guestResult.Changed) != 2 {
		return 0, false, errors.New("workspace Guest returned unexpected result")
	}
	checkpoint, err := lease.Checkpoint()
	if err != nil {
		return 0, false, err
	}
	if err := lease.Release(); err != nil {
		return 0, false, err
	}
	reviewer, err := manager.AcquireCheckpoint(checkpoint, owner+"-review")
	if err != nil {
		return 0, false, err
	}
	defer reviewer.Release()
	bundle, err := reviewer.ExportChanges(before, 1<<20)
	if err != nil {
		return 0, false, err
	}
	conflicts, err := workspacepkg.CheckDirectoryConflicts(source, bundle, workspacepkg.DefaultLimits())
	if err != nil {
		return 0, false, err
	}
	if len(bundle.Changes) != 2 {
		return 0, conflicts.Clean, fmt.Errorf("expected two exported changes, got %d", len(bundle.Changes))
	}
	return len(bundle.Changes), conflicts.Clean, nil
}

func createSourceFixture(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"enabled\":true}\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "README.md"), []byte("workflow fixture\n"), 0o600)
}
