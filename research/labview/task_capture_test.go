package labview

import (
	"path/filepath"
	"testing"
)

func TestBuildTaskSnapshotRejectsBodyCaptureAnchorDrift(t *testing.T) {
	root := filepath.Clean("../..")
	capture := taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json"))
	capture[len(capture)-2] ^= 1
	_, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: capture,
	})
	if err == nil {
		t.Fatal("expected body capture anchor drift to fail closed")
	}
}
