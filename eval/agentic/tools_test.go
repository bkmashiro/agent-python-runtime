package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func findAgenticTask(t *testing.T, id string) Task {
	t.Helper()
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range dataset.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %s not found", id)
	return Task{}
}

func TestToolRuntimeBuildsSealedCatalogAndTrustedPrepare(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot()
	if len(snapshot.Tools()) != len(task.Tools) || snapshot.Digest() == "" || snapshot.Revision() != 1 {
		t.Fatalf("snapshot tools=%d digest=%q revision=%d", len(snapshot.Tools()), snapshot.Digest(), snapshot.Revision())
	}
	prepare, err := runtime.TrustedPrepare()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepare, "from typing import") || !strings.Contains(prepare, "def touch(") || strings.Contains(prepare, "def rm(") || strings.Contains(strings.ToLower(prepare), "authorization") {
		t.Fatal("trusted prepare does not match bounded catalog")
	}
	for _, tool := range snapshot.Tools() {
		if tool.Projection == "unsupported" || tool.MaxCalls != maxFunctionCalls {
			t.Fatalf("tool=%+v", tool)
		}
	}
}

func TestToolRuntimeDirectCallsUseHostTransactionsAndTrace(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.FileSystem().Digest()
	if err := runtime.SetTurn(0); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.InvokeDirect(context.Background(), "run-direct-1", "call-direct-1", "cd", json.RawMessage(`{"folder":"Documents"}`))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(output, &value) != nil || value["current_working_directory"] != "Documents" {
		t.Fatalf("output=%s", output)
	}
	if runtime.FileSystem().Digest() == before {
		t.Fatal("direct transaction did not apply state")
	}
	trace := runtime.Trace()
	if len(trace) != len(task.Interaction.Turns) || len(trace[0]) != 1 || trace[0][0].Name != "cd" {
		t.Fatalf("trace=%+v", trace)
	}
}

func TestToolRuntimePromotesFilesystemApplicationErrorsToFailedReceipts(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTurn(0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.InvokeDirect(context.Background(), "run-setup", "call-setup", "cd", json.RawMessage(`{"folder":"Documents"}`)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTurn(1); err != nil {
		t.Fatal(err)
	}
	before := runtime.FileSystem().Digest()
	broker, err := runtime.NewWorkflowBroker("run-failed-cd", 1)
	if err != nil {
		t.Fatal(err)
	}
	tool := toolByID(t, runtime.Snapshot(), "cd")
	payload, err := json.Marshal(map[string]any{
		"call_id": "typed:failed-cd", "capability": "cd", "catalog_digest": runtime.Snapshot().Digest(),
		"handler_version": tool.HandlerVersion, "arguments": json.RawMessage(`{"folder":"Documents"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), payload)
	var envelope struct {
		Status capability.Status `json:"status"`
		Error  *capability.Error `json:"error"`
	}
	if err != nil || json.Unmarshal(response, &envelope) != nil || envelope.Status != capability.StatusError || envelope.Error == nil || envelope.Error.Code != "handler_failed" {
		t.Fatalf("response=%s envelope=%+v err=%v", response, envelope, err)
	}
	receipts := broker.Receipts()
	if len(receipts) != 1 || receipts[0].Outcome != string(capability.StatusError) || receipts[0].ResponseSHA256 != "" {
		t.Fatalf("failed receipt=%+v", receipts)
	}
	if runtime.FileSystem().Digest() != before {
		t.Fatalf("failed cd changed state: before=%s after=%s", before, runtime.FileSystem().Digest())
	}
	raw := runtime.RawTrace()
	if len(raw[1]) != 1 || raw[1][0].Name != "cd" || raw[1][0].Error == "" || !strings.Contains(string(raw[1][0].Output), "No such file or directory") {
		t.Fatalf("raw trace=%+v", raw)
	}
}

func TestToolRuntimeWorkflowRollbackRestoresState(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTurn(0); err != nil {
		t.Fatal(err)
	}
	before := runtime.FileSystem().Digest()
	broker, err := runtime.NewWorkflowBroker("run-workflow-1", 4)
	if err != nil {
		t.Fatal(err)
	}
	callTypedTool(t, broker, runtime.Snapshot(), "typed:1", "cd", `{"folder":"Documents"}`)
	callTypedTool(t, broker, runtime.Snapshot(), "typed:2", "touch", `{"file_name":"temporary.txt"}`)
	if runtime.FileSystem().Digest() == before {
		t.Fatal("workflow calls did not mutate state")
	}
	if err := broker.FinalizeRun(context.Background(), false, "guest_error"); err != nil {
		t.Fatal(err)
	}
	if runtime.FileSystem().Digest() != before {
		t.Fatalf("rollback digest=%s want=%s", runtime.FileSystem().Digest(), before)
	}
	inspection, err := broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionRolledBack {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestToolRuntimeWorkflowBudgetFailsClosed(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := runtime.NewWorkflowBroker("run-budget-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	callTypedTool(t, broker, runtime.Snapshot(), "typed:1", "pwd", `{}`)
	tool := toolByID(t, runtime.Snapshot(), "ls")
	payload, _ := json.Marshal(map[string]any{
		"call_id": "typed:2", "capability": "ls", "catalog_digest": runtime.Snapshot().Digest(),
		"handler_version": tool.HandlerVersion, "arguments": map[string]any{},
	})
	response, err := broker.Call(context.Background(), payload)
	var envelope struct {
		Status capability.Status `json:"status"`
		Error  *capability.Error `json:"error"`
	}
	if err != nil || json.Unmarshal(response, &envelope) != nil || envelope.Status != capability.StatusDenied || envelope.Error == nil || envelope.Error.Code != "transaction_call_budget_exceeded" {
		t.Fatalf("response=%s envelope=%+v err=%v", response, envelope, err)
	}
	if err := broker.FinalizeRun(context.Background(), true, "success"); err != nil {
		t.Fatal(err)
	}
}

func callTypedTool(t *testing.T, broker *capability.Broker, snapshot toolcatalog.Snapshot, callID, toolID, arguments string) json.RawMessage {
	t.Helper()
	tool := toolByID(t, snapshot, toolID)
	payload, err := json.Marshal(map[string]any{
		"call_id": callID, "capability": toolID, "catalog_digest": snapshot.Digest(),
		"handler_version": tool.HandlerVersion, "arguments": json.RawMessage(arguments),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), payload)
	var envelope struct {
		Status capability.Status `json:"status"`
		Result json.RawMessage   `json:"result"`
		Error  *capability.Error `json:"error"`
	}
	if err != nil || json.Unmarshal(response, &envelope) != nil || envelope.Status != capability.StatusOK || envelope.Error != nil {
		t.Fatalf("call %s response=%s envelope=%+v err=%v", toolID, response, envelope, err)
	}
	return envelope.Result
}

func toolByID(t *testing.T, snapshot toolcatalog.Snapshot, toolID string) toolcatalog.Tool {
	t.Helper()
	for _, tool := range snapshot.Tools() {
		if tool.ToolID == toolID {
			return tool
		}
	}
	t.Fatalf("tool %s not found", toolID)
	return toolcatalog.Tool{}
}
