package workflowbench

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckedInEvidenceAndLabCopyRemainSealedAndIdentical(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller path unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	evidencePath := filepath.Join(root, "docs", "evidence", "workflow-benchmark-evidence-v0.json")
	publicPath := filepath.Join(root, "apps", "lab-web", "public", "lab-data", "workflow-benchmark-evidence-v0.json")
	evidence, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(evidence, public) {
		t.Fatal("Lab copy differs from sealed repository evidence")
	}
	decoded, err := DecodeEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Divergences != 0 || len(decoded.Tasks) != 14 || len(decoded.Reports) != 14 {
		t.Fatalf("unexpected checked-in evidence summary: %+v", decoded)
	}
}
