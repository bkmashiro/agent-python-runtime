package labview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestRecordTaskExperimentCapturesSourcesAndAgentOutputBodies(t *testing.T) {
	root := filepath.Clean("../..")
	snapshot, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	recordingRoot, err := filepath.Abs(filepath.Join(t.TempDir(), "recording"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(recordingRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	exported, err := RecordTaskExperiment(recordingRoot, snapshot, "d6d72c66c738f6f906a35bd78e9f885bd286ee75")
	if err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(recordingRoot)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("recording root mode=%v", rootInfo.Mode().Perm())
	}
	traceInfo, err := os.Stat(filepath.Join(recordingRoot, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if traceInfo.Mode().Perm() != 0o600 {
		t.Fatalf("recording trace mode=%v", traceInfo.Mode().Perm())
	}
	if exported.Profile != trajectory.ProfileExperimentFull || len(exported.Events) != 50 {
		t.Fatalf("recording profile=%s events=%d", exported.Profile, len(exported.Events))
	}
	bodies := 0
	for _, event := range exported.Events {
		if event.Body != nil {
			bodies++
		}
	}
	if bodies != 44 {
		t.Fatalf("body events=%d, want context + 37 runtime events + 3 sources + 3 outputs", bodies)
	}
}
