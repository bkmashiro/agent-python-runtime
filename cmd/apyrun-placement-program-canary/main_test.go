package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/placement"
)

func TestProgramProposalValidationFailsClosed(t *testing.T) {
	if validatePythonProposal(programProposal{Code: "result = {}"}) != nil {
		t.Fatal("bounded import-free proposal rejected")
	}
	for _, proposal := range []programProposal{
		{Code: ""},
		{Code: "\x00"},
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

func TestPythonProposalImportsAreHostInferredAgainstWorkspacePolicy(t *testing.T) {
	imports, err := admittedPythonImports("import csv\nimport statistics\nresult = {}")
	if err != nil || len(imports) != 2 || imports[0] != "csv" || imports[1] != "statistics" {
		t.Fatalf("imports=%v err=%v", imports, err)
	}
	for _, source := range []string{
		"import subprocess\nresult = {}",
		"result = __import__('csv')",
		"result = {}\nimport csv",
	} {
		if imports, err := admittedPythonImports(source); err == nil {
			t.Fatalf("source admitted with imports=%v: %q", imports, source)
		}
	}
}

func TestGeneratedPythonFailureIsATerminalTrialResult(t *testing.T) {
	execution, score := failedPysolateProgram(agentic.PythonRunResult{
		ErrorCode: "python_exception", FailureClass: agentic.FailureClassPythonException,
		Backend: "wazero", CapabilityCalls: 1,
	}, placement.AgentStdlibWorkspacePolicyID, []string{"csv"}, 123)
	if execution["status"] != "failed" || execution["failure_layer"] != "model_program" ||
		execution["error_code"] != "python_exception" || score["passed"] != false {
		t.Fatalf("execution=%+v score=%+v", execution, score)
	}
}

func TestFrozenCanaryTreatmentsExposeOnlyDeclaredSurfaces(t *testing.T) {
	task := agentic.Task{
		Tools: []agentic.Tool{{Name: "cat", Parameters: json.RawMessage(`{"type":"object","properties":{"file_name":{"type":"string"}},"required":["file_name"]}`)}},
	}
	pythonInstructions, pythonTool := treatment("pysolate", task)
	if pythonTool["name"] != "submit_python" || !strings.Contains(pythonInstructions, "profile-qualified") ||
		!strings.Contains(pythonInstructions, "standard library") || !strings.Contains(pythonInstructions, "csv") ||
		!strings.Contains(pythonInstructions, "Do not import host_tools") || strings.Contains(pythonInstructions, "expected") {
		t.Fatalf("invalid Python treatment: %s %+v", pythonInstructions, pythonTool)
	}
	parameters := pythonTool["parameters"].(map[string]any)
	required := parameters["required"].([]string)
	if len(required) != 1 || required[0] != "code" || parameters["properties"].(map[string]any)["imports"] != nil {
		t.Fatalf("model-facing proposal still exposes Host admission metadata: %+v", parameters)
	}
	computerInstructions, computerTool := treatment("computer", task)
	if computerTool["name"] != "submit_javascript" || !strings.Contains(computerInstructions, "Cloudflare Computer") ||
		!strings.Contains(computerInstructions, "/workspace/alex/Documents/metrics.csv") || strings.Contains(computerInstructions, "alpha,gamma") {
		t.Fatalf("invalid Computer treatment: %s %+v", computerInstructions, computerTool)
	}
}
