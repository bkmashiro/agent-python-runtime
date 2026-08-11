package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
)

func TestProgramProposalValidationFailsClosed(t *testing.T) {
	if validatePythonProposal(programProposal{Code: "result = {}", Imports: []string{}}) != nil {
		t.Fatal("bounded import-free proposal rejected")
	}
	for _, proposal := range []programProposal{
		{Code: "", Imports: []string{}},
		{Code: "result = {}", Imports: nil},
		{Code: "import os\nresult = {}", Imports: []string{"os"}},
		{Code: "import json\nresult = {}", Imports: []string{"json", "json"}},
	} {
		if validatePythonProposal(proposal) == nil {
			t.Fatalf("invalid proposal admitted: %+v", proposal)
		}
	}
	if validateComputerProposal(computerProposal{Source: "export default async () => ({status: 'ok'});"}) != nil {
		t.Fatal("bounded Computer proposal rejected")
	}
	if validateComputerProposal(computerProposal{Source: "\x00"}) == nil {
		t.Fatal("NUL source admitted")
	}
}

func TestFrozenCanaryTreatmentsExposeOnlyDeclaredSurfaces(t *testing.T) {
	task := agentic.Task{
		Tools: []agentic.Tool{{Name: "cat", Parameters: json.RawMessage(`{"type":"object","properties":{"file_name":{"type":"string"}},"required":["file_name"]}`)}},
	}
	pythonInstructions, pythonTool := treatment("pysolate", task)
	if pythonTool["name"] != "submit_python" || !strings.Contains(pythonInstructions, "profile-qualified") ||
		!strings.Contains(pythonInstructions, "Do not import host_tools") || strings.Contains(pythonInstructions, "expected") {
		t.Fatalf("invalid Python treatment: %s %+v", pythonInstructions, pythonTool)
	}
	computerInstructions, computerTool := treatment("computer", task)
	if computerTool["name"] != "submit_javascript" || !strings.Contains(computerInstructions, "Cloudflare Computer") ||
		!strings.Contains(computerInstructions, "/workspace/alex/Documents/metrics.csv") || strings.Contains(computerInstructions, "alpha,gamma") {
		t.Fatalf("invalid Computer treatment: %s %+v", computerInstructions, computerTool)
	}
}
