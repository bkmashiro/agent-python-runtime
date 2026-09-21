package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/scheduling"
)

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunWritesDeterministicWorkload(t *testing.T) {
	phase := writeFixture(t, "phase.jsonl", `{"type":"metadata","preparation":"copy","cases":"read-finish","iterations":1,"synthetic_tool_delay_ns":50000000,"external_io":true}
{"type":"sample","case":"read-finish","iteration":0,"first_attempt_ns":70000000,"tool_service_ns":50000000,"tool_queue_ns":1000,"tool_resume_ns":2000,"tool_failures":0,"tool_dispatches":1}
`)
	var output bytes.Buffer
	if err := run([]string{"-phase", phase, "-case", "read-finish", "-tasks", "3"}, &output); err != nil {
		t.Fatal(err)
	}
	var tasks []scheduling.Task
	if err := json.Unmarshal(output.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 || tasks[0].ID != "task-000000" || tasks[2].ID != "task-000002" {
		t.Fatalf("tasks=%+v", tasks)
	}
}

func TestRunComparesObservedBatchesAndSummarizesTolerance(t *testing.T) {
	phase := writeFixture(t, "phase.jsonl", `{"type":"metadata","preparation":"copy","cases":"read-finish","iterations":1,"synthetic_tool_delay_ns":50000000,"external_io":true}
{"type":"sample","case":"read-finish","iteration":0,"first_attempt_ns":70000000,"tool_service_ns":50000000,"tool_queue_ns":1000,"tool_resume_ns":2000,"tool_failures":0,"tool_dispatches":1}
`)
	observed := writeFixture(t, "observed.jsonl", `{"case":"read-finish","mode":"executor","tasks":2,"running_limit":1,"resident_limit":2,"tool_limit":2,"external_io":false,"synthetic_hold_ns":50000000,"batch_ns":140000000,"completed":2}
{"case":"read-finish","mode":"executor","tasks":2,"running_limit":1,"resident_limit":2,"tool_limit":2,"external_io":true,"synthetic_hold_ns":50000000,"batch_ns":90000000,"completed":2}
`)
	var output bytes.Buffer
	if err := run([]string{"-phase", phase, "-case", "read-finish", "-observed", observed, "-tolerance", "0.15"}, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var metadata reportMetadata
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Type != "metadata" || metadata.Comparisons != 2 || metadata.Case != "read-finish" {
		t.Fatalf("metadata=%+v", metadata)
	}
	for index := 0; index < 2; index++ {
		var comparison scheduling.CalibrationComparison
		if err := decoder.Decode(&comparison); err != nil {
			t.Fatal(err)
		}
		if comparison.Type != "comparison" {
			t.Fatalf("comparison=%+v", comparison)
		}
	}
}

func TestRunRejectsMismatchedControlledDelay(t *testing.T) {
	phase := writeFixture(t, "phase.jsonl", `{"type":"metadata","preparation":"copy","cases":"read-finish","iterations":1,"synthetic_tool_delay_ns":50000000,"external_io":true}
{"type":"sample","case":"read-finish","iteration":0,"first_attempt_ns":70000000,"tool_service_ns":50000000,"tool_failures":0,"tool_dispatches":1}
`)
	observed := writeFixture(t, "observed.json", `{"case":"read-finish","tasks":2,"running_limit":1,"resident_limit":2,"tool_limit":2,"external_io":true,"synthetic_hold_ns":200000000,"batch_ns":250000000}`)
	if err := run([]string{"-phase", phase, "-case", "read-finish", "-observed", observed}, &bytes.Buffer{}); err == nil {
		t.Fatal("mismatched controlled delay accepted")
	}
}
