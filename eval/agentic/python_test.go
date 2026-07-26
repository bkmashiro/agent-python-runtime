package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type fakeGuestRunner struct {
	request []byte
	prepare string
	payload []byte
	err     error
	runs    int
	props   engine.Properties
}

func (runner *fakeGuestRunner) Run(_ context.Context, request []byte, prepare string) ([]byte, error) {
	runner.runs++
	runner.request = append([]byte(nil), request...)
	runner.prepare = prepare
	return append([]byte(nil), runner.payload...), runner.err
}
func (*fakeGuestRunner) Close(context.Context) error          { return nil }
func (runner *fakeGuestRunner) Properties() engine.Properties { return runner.props }

func successfulGuestPayload() []byte {
	return []byte(`{"status":"ok","result":{"done":true},"receipts":[],"metrics":{"capability_calls":2,"result_bytes":13},"error":null}`)
}

func TestPythonExecutorBuildsBoundedAuthorityFreeRunRequest(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeGuestRunner{payload: successfulGuestPayload(), props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}}
	executor, err := NewPythonExecutor(runner, tools)
	if err != nil {
		t.Fatal(err)
	}
	code := "from host_tools import pwd\nresult = {'cwd': pwd()}"
	result, err := executor.Execute(context.Background(), "python-run-1", code, 4)
	if err != nil || !result.Success || result.CapabilityCalls != 2 || result.ResultDigest == "" || string(result.Observation) != `{"done":true}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	request, err := runtimeconfig.DecodeRunRequest(runner.request)
	if err != nil {
		t.Fatal(err)
	}
	if request.RunID != "python-run-1" || request.Code != code || string(request.Inputs) != "{}" || string(request.OutputSchema) != `{"type":"object"}` {
		t.Fatalf("request=%+v", request)
	}
	var envelope map[string]any
	if json.Unmarshal(runner.request, &envelope) != nil || len(envelope) != 4 {
		t.Fatalf("request envelope=%s", runner.request)
	}
	for _, forbidden := range []string{"credential", "mount", "network", "transaction", "budget"} {
		if _, exists := envelope[forbidden]; exists {
			t.Fatalf("request exposed %s", forbidden)
		}
	}
	if !strings.Contains(runner.prepare, "from typing import") || !strings.Contains(runner.prepare, "def pwd(") {
		t.Fatal("trusted prepare missing generated SDK")
	}
	encoded, _ := json.Marshal(result)
	if containsBytes(encoded, []byte("host_tools")) || containsBytes(encoded, []byte(`"done"`)) {
		t.Fatalf("serialized result leaked code or observation: %s", encoded)
	}
}

func TestPythonExecutorProjectsGuestErrorsWithoutTraceback(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, _ := NewToolRuntime(task)
	runner := &fakeGuestRunner{props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}, payload: []byte(`{
		"status":"error","result":null,"receipts":[],"metrics":{"capability_calls":1,"result_bytes":4},
		"error":{"code":"python_exception","message":"private message","error_type":"ValueError","traceback":"private traceback"}
	}`)}
	executor, err := NewPythonExecutor(runner, tools)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), "python-error-1", "raise ValueError('private')", 2)
	if err != nil || result.Success || result.ErrorCode != "python_exception" || result.FailureClass != FailureClassPythonException || string(result.Observation) != `{"error_code":"python_exception","status":"error"}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"private message", "private traceback", "ValueError", "raise ValueError"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("serialized result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestPythonExecutorClassifiesHostToolErrorsWithoutLeakingDetails(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, _ := NewToolRuntime(task)
	runner := &fakeGuestRunner{props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}, payload: []byte(`{
		"status":"error","result":null,"receipts":[],"metrics":{"capability_calls":1,"result_bytes":0},
		"error":{"code":"python_exception","message":"private Host message","error_type":"HostToolError","traceback":"private Host traceback"}
	}`)}
	executor, err := NewPythonExecutor(runner, tools)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), "python-host-error-1", "result = {}", 2)
	if err != nil || result.Success || result.FailureClass != FailureClassHostToolError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"private Host message", "private Host traceback", "HostToolError"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("serialized result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestPythonExecutorClassifiesOutputSchemaMismatch(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, _ := NewToolRuntime(task)
	runner := &fakeGuestRunner{
		props:   engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance},
		payload: []byte(`{"status":"ok","result":null,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":4},"error":null}`),
		err:     runtimeconfig.ErrRunResultSchemaMismatch,
	}
	executor, err := NewPythonExecutor(runner, tools)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), "python-schema-1", "print('missing result')", 2)
	if err != nil || result.Success || result.ErrorCode != "guest_output_schema_mismatch" || result.FailureClass != FailureClassGuestOutputSchemaMismatch {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestPythonExecutorRejectsInvalidPropertiesAndInputsBeforeRun(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, _ := NewToolRuntime(task)
	bad := &fakeGuestRunner{props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModePreparedRestore}}
	if _, err := NewPythonExecutor(bad, tools); err == nil {
		t.Fatal("accepted non-fresh reset mode")
	}
	runner := &fakeGuestRunner{payload: successfulGuestPayload(), props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}}
	executor, err := NewPythonExecutor(runner, tools)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		runID, code string
		calls       uint32
	}{
		{"bad run id", "result = {}", 1},
		{"run-1", "", 1},
		{"run-1", strings.Repeat("x", maxPythonCodeBytes+1), 1},
		{"run-1", "result = {}", 0},
	}
	for _, test := range invalid {
		if _, err := executor.Execute(context.Background(), test.runID, test.code, test.calls); err == nil {
			t.Fatalf("accepted run=%q code_bytes=%d calls=%d", test.runID, len(test.code), test.calls)
		}
	}
	if runner.runs != 0 {
		t.Fatalf("invalid inputs reached runner %d times", runner.runs)
	}
}

func TestPythonExecutorPropagatesEngineFailure(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, _ := NewToolRuntime(task)
	runner := &fakeGuestRunner{err: errors.New("engine failed"), props: engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}}
	executor, _ := NewPythonExecutor(runner, tools)
	result, err := executor.Execute(context.Background(), "python-engine-1", "result = {}", 1)
	if err == nil || result.RequestDigest == "" || result.ResponseDigest != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
