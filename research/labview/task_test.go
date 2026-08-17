package labview

import (
	"os"
	"path/filepath"
	"testing"
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
		Corpus: taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/public-development-corpus.json")),
		Report: taskRead(t, filepath.Join(root, "docs/evidence/spark-composable-direct-report.json")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != "dev-workspace-summary" || snapshot.Status != "passed" || len(snapshot.Sources) != 3 || len(snapshot.Events) != 37 || snapshot.Stats.Agents != 4 || snapshot.Stats.WorkspaceChanges != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := ValidateTaskSnapshot(snapshot); err != nil {
		t.Fatal(err)
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
			Corpus: taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/public-development-corpus.json")),
			Report: taskRead(t, filepath.Join(root, "docs/evidence/spark-composable-direct-report.json")),
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
		"invalid digest": func(snapshot *TaskSnapshot) { snapshot.Events[0].InputSHA256 = "sha256:forged" },
		"negative time":  func(snapshot *TaskSnapshot) { snapshot.Events[0].StartedMillis = -1 },
		"elapsed rewind": func(snapshot *TaskSnapshot) { snapshot.Events[13].RelativeElapsedMillis = 20000 },
		"unknown agent":  func(snapshot *TaskSnapshot) { snapshot.Events[13].AgentID = "attacker" },
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
		Corpus: taskRead(t, filepath.Join(root, "research/composableacceptance/testdata/public-development-corpus.json")),
		Report: taskRead(t, filepath.Join(root, "docs/evidence/spark-composable-direct-report.json")),
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
