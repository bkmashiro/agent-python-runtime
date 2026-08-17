package labview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
)

func taskRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuildTaskSnapshotProjectsRealWorkspaceTask(t *testing.T) {
	root := filepath.Clean("../..")
	snapshot, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != "dev-release-readiness" || snapshot.Status != "passed" || len(snapshot.Sources) != 3 || len(snapshot.Outputs) != 3 || len(snapshot.Events) != 37 || snapshot.Stats.Agents != 4 || snapshot.Stats.WorkspaceChanges != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := ValidateTaskSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTaskBranchEventsRejectsMismatchedObservedIdentities(t *testing.T) {
	root := filepath.Clean("../..")
	captureRaw := taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json"))
	capture, _, err := composableacceptance.DecodeBodyCapture(captureRaw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: captureRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateTaskBranchEvents(capture.SelectedChildDescriptor, capture.SelectedRootSHA256, snapshot.Events); err != nil {
		t.Fatal(err)
	}
	for name, sequence := range map[string]int{"selected descriptor": 16, "selected root": 18} {
		t.Run(name, func(t *testing.T) {
			forged := append([]TaskEvent(nil), snapshot.Events...)
			for index := range forged {
				if forged[index].Sequence == sequence {
					forged[index].InputSHA256 = latestSHA([]byte("forged-observed-identity"))
				}
			}
			if _, err := validateTaskBranchEvents(capture.SelectedChildDescriptor, capture.SelectedRootSHA256, forged); err == nil {
				t.Fatal("accepted branch event with mismatched observed identity")
			}
		})
	}
}

func resealTask(t *testing.T, snapshot *TaskSnapshot) {
	t.Helper()
	snapshot.Identity = ""
	identity, err := taskSnapshotIdentity(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Identity = identity
}

func TestTaskSnapshotRejectsIdentityValidPrivateAndForgedFields(t *testing.T) {
	root := filepath.Clean("../..")
	build := func(t *testing.T) TaskSnapshot {
		t.Helper()
		snapshot, err := BuildTaskSnapshot(TaskInputs{
			Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
			Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
			Capture: taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json")),
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	mutations := map[string]func(*TaskSnapshot){
		"private source": func(snapshot *TaskSnapshot) { snapshot.Sources[0].Source = "/Users/private/task.py" },
		"forged source file": func(snapshot *TaskSnapshot) {
			for index := range snapshot.Events {
				if snapshot.Events[index].Source != nil {
					snapshot.Events[index].Source.File = "forged.py"
					return
				}
			}
		},
		"workspace traversal": func(snapshot *TaskSnapshot) {
			for eventIndex := range snapshot.Events {
				if len(snapshot.Events[eventIndex].WorkspaceChanges) > 0 {
					snapshot.Events[eventIndex].WorkspaceChanges[0].Path = "../private.txt"
					return
				}
			}
		},
		"invalid digest":            func(snapshot *TaskSnapshot) { snapshot.Events[0].InputSHA256 = "sha256:forged" },
		"forged output body":        func(snapshot *TaskSnapshot) { snapshot.Outputs[1].Body = "forged" },
		"forged branch disposition": func(snapshot *TaskSnapshot) { snapshot.Outputs[1].Disposition = "workflow_result" },
		"reordered sources": func(snapshot *TaskSnapshot) {
			snapshot.Sources[1], snapshot.Sources[2] = snapshot.Sources[2], snapshot.Sources[1]
		},
		"output event drift":      func(snapshot *TaskSnapshot) { snapshot.Outputs[1].EventSequence++ },
		"workflow oracle misbind": func(snapshot *TaskSnapshot) { snapshot.Outputs[0].EventSequence = 36 },
		"negative time":           func(snapshot *TaskSnapshot) { snapshot.Events[0].StartedMillis = -1 },
		"elapsed rewind":          func(snapshot *TaskSnapshot) { snapshot.Events[13].RelativeElapsedMillis = 20000 },
		"unknown agent":           func(snapshot *TaskSnapshot) { snapshot.Events[13].AgentID = "attacker" },
		"future parent": func(snapshot *TaskSnapshot) {
			snapshot.Events[1].ParentSequence = snapshot.Events[len(snapshot.Events)-1].Sequence
		},
		"future parent span": func(snapshot *TaskSnapshot) {
			snapshot.Events[1].ParentSpanID = snapshot.Events[len(snapshot.Events)-1].SpanID
		},
		"duplicate span": func(snapshot *TaskSnapshot) { snapshot.Events[1].SpanID = snapshot.Events[0].SpanID },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			snapshot := build(t)
			mutate(&snapshot)
			resealTask(t, &snapshot)
			if err := ValidateTaskSnapshot(snapshot); err == nil {
				t.Fatal("accepted identity-valid forged task snapshot")
			}
		})
	}
}

func TestTaskSnapshotRejectsIdentityValidMissingTrace(t *testing.T) {
	root := filepath.Clean("../..")
	snapshot, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Events = []TaskEvent{}
	snapshot.Stats = TaskStats{}
	resealTask(t, &snapshot)
	if err := ValidateTaskSnapshot(snapshot); err == nil {
		t.Fatal("accepted missing task trace")
	}
}

func TestDecodeTaskSnapshotRejectsExplicitNullOptionalField(t *testing.T) {
	root := filepath.Clean("../..")
	snapshot, err := BuildTaskSnapshot(TaskInputs{
		Corpus:  taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/lab-release-readiness-corpus.json")),
		Report:  taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-direct-report.json")),
		Capture: taskRead(t, filepath.Join(root, "docs/evidence/lab-release-readiness-body-capture.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := TaskSnapshotJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(raw), `"type": "run_start"`, `"workspace_changes": null, "type": "run_start"`, 1)
	if forged == string(raw) {
		t.Fatal("failed to construct null-field probe")
	}
	if _, err := DecodeTaskSnapshot([]byte(forged)); err == nil {
		t.Fatal("accepted explicit null optional field")
	}
}
