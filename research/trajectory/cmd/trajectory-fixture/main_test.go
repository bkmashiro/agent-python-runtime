package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestGenerateCreatesInspectableCompleteDevelopmentTrajectory(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "trajectory.json")
	if err := generate(output, filepath.Join(root, "private-store"), "0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var exported trajectory.PrivateExport
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.Privacy != "private" || len(exported.Events) < 18 {
		t.Fatalf("incomplete export: %+v", exported)
	}
	seen := map[trajectory.EventType]bool{}
	for _, event := range exported.Events {
		seen[event.Type] = true
	}
	for _, required := range []trajectory.EventType{trajectory.EventContext, trajectory.EventRequestHeader, trajectory.EventModelRequest, trajectory.EventAssistantChunk, trajectory.EventAssistantReasoning, trajectory.EventAssistantOutput, trajectory.EventToolCall, trajectory.EventToolResult, trajectory.EventSubagentDispatch, trajectory.EventRuntime, trajectory.EventWorkspace, trajectory.EventSessionEnd} {
		if !seen[required] {
			t.Fatalf("missing event type %s", required)
		}
	}
}
