package placement

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
)

func TestCompileScriptedCanaryCaseBuildsWorkspaceProgramsAndOracleState(t *testing.T) {
	corpus, task := loadScriptedCanaryTask(t, "pl-hp-dedupe-contacts")
	compiled, err := CompileScriptedCanaryCase(task)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Tools) != 2 || len(compiled.Calls) != 2 ||
		!strings.Contains(compiled.PythonSource, "workspace_read_text") ||
		!strings.Contains(compiled.JavaScriptSource, `from "ws:tools"`) ||
		!strings.Contains(compiled.JavaScriptSource, `from "node:fs/promises"`) {
		t.Fatalf("compiled=%+v", compiled)
	}
	result := TrialResult{
		SchemaVersion: "placement-trial-result/v1", TrialID: "scripted-test", TaskID: task.ID,
		TaskSHA256: corpusEntryDigest(t, corpus, task.ID), SourceCommit: strings.Repeat("a", 40),
		TreatmentSHA256: "sha256:" + strings.Repeat("b", 64), RuntimeIdentitySHA256: "sha256:" + strings.Repeat("c", 64),
		Arm: "pysolate", Mode: "scripted", Replicate: 1,
		Admission: ObservedAdmission{Status: "admitted", Reason: "frozen", BeforeProvider: true},
		Execution: ExecutionEvidence{Status: "completed"}, ObservedFinalState: compiled.ObservedFinalState,
		ObservedEffects: []SemanticCall{{Name: compiled.Calls[0].Name, Arguments: compiled.Calls[0].Arguments}, {Name: compiled.Calls[1].Name, Arguments: compiled.Calls[1].Arguments}},
	}
	score, err := Score(task, result)
	if err != nil || !score.Pass {
		t.Fatalf("score=%+v err=%v final=%s", score, err, compiled.ObservedFinalState)
	}
}

func TestCompileScriptedCanaryCaseBuildsFixtureResult(t *testing.T) {
	_, task := loadScriptedCanaryTask(t, "pl-dev_fanout_join_filter_001")
	compiled, err := CompileScriptedCanaryCase(task)
	if err != nil || len(compiled.Calls) != 2 || !json.Valid(compiled.ExpectedResult) {
		t.Fatalf("compiled=%+v err=%v", compiled, err)
	}
	var result map[string]any
	if json.Unmarshal(compiled.ExpectedResult, &result) != nil || result["value"] != float64(102) {
		t.Fatalf("result=%s", compiled.ExpectedResult)
	}
}

func TestCompileScriptedCanaryCaseLeavesRejectedTaskUnstarted(t *testing.T) {
	_, task := loadScriptedCanaryTask(t, "pl-ba-native-process")
	compiled, err := CompileScriptedCanaryCase(task)
	if err != nil || len(compiled.Tools) != 0 || compiled.PythonSource != "" || compiled.JavaScriptSource != "" {
		t.Fatalf("compiled=%+v err=%v", compiled, err)
	}
}

func loadScriptedCanaryTask(t *testing.T, id string) (*Corpus, Task) {
	t.Helper()
	corpus, err := Load(filepath.Join("..", "agentic", "placement", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range corpus.Tasks {
		if task.ID == id {
			return corpus, task
		}
	}
	t.Fatalf("missing task %s", id)
	return nil, Task{}
}

func corpusEntryDigest(t *testing.T, corpus *Corpus, id string) string {
	t.Helper()
	for _, entry := range corpus.Manifest.Tasks {
		if entry.ID == id {
			return entry.SHA256
		}
	}
	t.Fatalf("missing manifest entry %s", id)
	return ""
}

var _ = agentic.ScriptedTool{}
