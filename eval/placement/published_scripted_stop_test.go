package placement

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedScriptedCanaryStopIsBoundAndRejectsInvalidRerunCells(t *testing.T) {
	root := filepath.Join("..", "agentic", "results", "placement-scripted-canary-stop-2026-08-11")
	data, err := os.ReadFile(filepath.Join(root, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&summary) != nil {
		t.Fatal("invalid summary")
	}
	if summary["schema_version"] != "placement-scripted-canary-stop/v1" ||
		summary["status"] != "stopped_after_infrastructure_repair_limit" ||
		summary["plan_sha256"] != "sha256:ea3faf8b2930734b38e757d7727581141c69d84c42f85f701133d6b5dd84c1ce" ||
		summary["initial_source_commit"] != "e7efe3f849687a23d8d70e91e7582bcb24ef6c76" ||
		summary["repair_source_commit"] != "389ffeaf2684d8c31256aa15009319a6756af361" {
		t.Fatalf("identity drift: %+v", summary)
	}
	formal := summary["formal_cells"].(map[string]any)
	if formal["accepted"] != json.Number("2") || formal["planned"] != json.Number("18") || formal["passed"] != json.Number("2") {
		t.Fatalf("formal cells=%+v", formal)
	}
	rerun := summary["repair_rerun"].(map[string]any)
	if rerun["cells_accepted"] != json.Number("0") || rerun["identity_valid"] != false || rerun["failure"] != "worker_javascript_node_path_not_configured" {
		t.Fatalf("rerun=%+v", rerun)
	}
}
