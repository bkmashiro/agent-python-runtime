package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestEvidenceFilenameAndPrivateWrite(t *testing.T) {
	if got := evidenceFilename(3, workflowbench.CampaignQualified); got != "rep-03-qualified.json" {
		t.Fatalf("filename=%q", got)
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := writeJSON(path, map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}
