package codexmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/hermesbridge"
)

type recordingExecutor struct {
	requests []hermesbridge.ExecuteRequest
	response hermesbridge.ExecuteResponse
}

func (executor *recordingExecutor) Execute(_ context.Context, request hermesbridge.ExecuteRequest) hermesbridge.ExecuteResponse {
	executor.requests = append(executor.requests, request)
	response := executor.response
	response.RequestID = request.RequestID
	return response
}

func newTestServer(t *testing.T, executor Executor) *Server {
	t.Helper()
	ids := []string{"request-host-1", "invocation-host-1"}
	index := 0
	server, err := NewServer(executor, "codex-mcp-process-host-1", func() (string, error) {
		value := ids[index]
		index++
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func decodeResponse(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestInitializeNegotiatesMCPAndAdvertisesOnlyTools(t *testing.T) {
	server := newTestServer(t, &recordingExecutor{})
	payload, respond := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"codex","title":"Codex","version":"test"}}}`))
	if !respond {
		t.Fatal("initialize did not respond")
	}
	response := decodeResponse(t, payload)
	result := response["result"].(map[string]any)
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("unexpected protocol version: %#v", result)
	}
	capabilities := result["capabilities"].(map[string]any)
	if _, ok := capabilities["tools"]; !ok || len(capabilities) != 1 {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}

func TestToolsListExposesOneClosedPythonRuntimeTool(t *testing.T) {
	server := newTestServer(t, &recordingExecutor{})
	payload, respond := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":"list-1","method":"tools/list","params":{"_meta":{"progressToken":0}}}`))
	if !respond {
		t.Fatal("tools/list did not respond")
	}
	response := decodeResponse(t, payload)
	tools := response["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != ToolName {
		t.Fatalf("unexpected tool: %#v", tool)
	}
	schema := tool["inputSchema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("tool schema is not closed: %#v", schema)
	}
}

func TestToolsCallUsesHostOwnedInvocationAndReturnsStructuredEvidence(t *testing.T) {
	executionRef := map[string]any{
		"agent_run_id": "codex-mcp-process-host-1", "turn_seq": float64(1),
		"output_item_seq": float64(1), "segment_seq": float64(1),
		"invocation_id": "invocation-host-1", "invocation_attempt": float64(1),
		"execution_id": "execution-host-1", "executed_code_sha256": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	refBytes, err := json.Marshal(executionRef)
	if err != nil {
		t.Fatal(err)
	}
	var typedRef hermesbridge.ExecuteResponse
	if err := json.Unmarshal([]byte(`{"version":"hermes-python-runtime-bridge/v1","request_id":"placeholder","status":"ok","result":42,"execution_ref":`+string(refBytes)+`}`), &typedRef); err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{response: typedRef}
	server := newTestServer(t, executor)
	payload, respond := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":"provider-tool-call-id","method":"tools/call","params":{"name":"python_runtime","arguments":{"code":"result = inputs['x'] * 2","inputs":{"x":21},"output_schema":{"type":"integer"}},"_meta":{"progressToken":"call-1"}}}`))
	if !respond || len(executor.requests) != 1 {
		t.Fatalf("unexpected response/executions: %t %d", respond, len(executor.requests))
	}
	request := executor.requests[0]
	if request.RequestID != "request-host-1" || request.Invocation.AgentRunID != "codex-mcp-process-host-1" ||
		request.Invocation.InvocationID != "invocation-host-1" || request.Invocation.TurnSeq != 1 || request.Invocation.InvocationAttempt != 1 {
		t.Fatalf("untrusted MCP identity crossed boundary: %#v", request)
	}
	if string(request.Inputs) != `{"x":21}` || string(request.OutputSchema) != `{"type":"integer"}` {
		t.Fatalf("unexpected runtime payload: %#v", request)
	}
	response := decodeResponse(t, payload)
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("unexpected MCP result: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["status"] != "ok" || structured["result"] != float64(42) {
		t.Fatalf("missing structured evidence: %#v", structured)
	}
}

func TestToolsCallMapsRuntimeFailureToToolErrorWithoutJSONRPCFailure(t *testing.T) {
	executor := &recordingExecutor{response: hermesbridge.ExecuteResponse{
		Version: hermesbridge.ProtocolVersion, Status: hermesbridge.ResponseStatusError,
		Error: &hermesbridge.BridgeError{Code: "guest_error", Message: "Python execution failed"},
	}}
	server := newTestServer(t, executor)
	payload, respond := server.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"python_runtime","arguments":{"code":"raise RuntimeError()"}}}`))
	if !respond {
		t.Fatal("tools/call did not respond")
	}
	response := decodeResponse(t, payload)
	if _, exists := response["error"]; exists {
		t.Fatalf("tool failure became protocol failure: %#v", response)
	}
	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("runtime failure was not a tool error: %#v", result)
	}
}

func TestToolsCallRejectsUnknownOrDuplicateArgumentsBeforeExecution(t *testing.T) {
	cases := map[string]struct {
		request string
		code    float64
	}{
		"unknown": {
			request: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"python_runtime","arguments":{"code":"result = 1","credential":"secret"}}}`,
			code:    -32602,
		},
		"duplicate": {
			request: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"python_runtime","arguments":{"code":"result = 1","code":"result = 2"}}}`,
			code:    -32700,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			executor := &recordingExecutor{}
			server := newTestServer(t, executor)
			payload, respond := server.Handle(context.Background(), []byte(testCase.request))
			if !respond || len(executor.requests) != 0 {
				t.Fatalf("invalid arguments reached runtime: %t %d", respond, len(executor.requests))
			}
			response := decodeResponse(t, payload)
			if response["error"].(map[string]any)["code"] != testCase.code {
				t.Fatalf("unexpected error: %#v", response)
			}
		})
	}
}

func TestServeUsesBoundedNewlineDelimitedJSONAndIgnoresNotifications(t *testing.T) {
	server := newTestServer(t, &recordingExecutor{})
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":5`) {
		t.Fatalf("unexpected stdio responses: %q", output.String())
	}
}

func TestServeReturnsWhenContextIsCancelledWhileStdinIsIdle(t *testing.T) {
	server := newTestServer(t, &recordingExecutor{})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, reader, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdio server remained blocked on stdin after cancellation")
	}
}

func TestServeAllowsBoundedMCPEnvelopeExpansionForRuntimeResult(t *testing.T) {
	result, err := json.Marshal(strings.Repeat("x", 700<<10))
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, &recordingExecutor{response: hermesbridge.ExecuteResponse{
		Version: hermesbridge.ProtocolVersion,
		Status:  hermesbridge.ResponseStatusOK,
		Result:  result,
	}})
	input := strings.NewReader(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"python_runtime","arguments":{"code":"result = 'bounded'"}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() <= MaxMessageBytes || output.Len() > MaxResponseMessageBytes {
		t.Fatalf("unexpected expanded response size: %d", output.Len())
	}
}

func TestServeRejectsOversizedRequestBeforeExecution(t *testing.T) {
	executor := &recordingExecutor{}
	server := newTestServer(t, executor)
	input := strings.NewReader(strings.Repeat("x", MaxMessageBytes+1) + "\n")
	if err := server.Serve(context.Background(), input, io.Discard); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("unexpected oversized request result: %v", err)
	}
	if len(executor.requests) != 0 {
		t.Fatalf("oversized request reached executor: %d", len(executor.requests))
	}
}
