package placement

import (
	"reflect"
	"testing"
)

func TestAgentStdlibWorkspaceProfileIsIntuitiveAndAuthorityBounded(t *testing.T) {
	want := []string{
		"agent_runtime", "base64", "collections", "csv", "datetime", "decimal",
		"fractions", "functools", "hashlib", "itertools", "json", "math",
		"pathlib", "re", "statistics", "sys", "xml",
	}
	if AgentStdlibWorkspacePolicyID != "stdlib-workspace-v1" || !reflect.DeepEqual(AgentStdlibWorkspaceImports(), want) {
		t.Fatalf("policy=%q imports=%v", AgentStdlibWorkspacePolicyID, AgentStdlibWorkspaceImports())
	}
	for _, forbidden := range []string{"ctypes", "importlib", "multiprocessing", "os", "socket", "subprocess", "urllib"} {
		if AgentStdlibWorkspaceAllows(forbidden) {
			t.Fatalf("authority-sensitive root %q admitted", forbidden)
		}
	}
}

func TestAgentStdlibWorkspaceImportsReturnsACopy(t *testing.T) {
	imports := AgentStdlibWorkspaceImports()
	imports[0] = "subprocess"
	if AgentStdlibWorkspaceImports()[0] != "agent_runtime" {
		t.Fatal("caller mutated frozen policy")
	}
}
