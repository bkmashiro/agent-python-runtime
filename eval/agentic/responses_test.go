package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type scriptedAdapter struct {
	responses []provider.Response
	errors    []error
	requests  []provider.Request
}

func (*scriptedAdapter) Protocol() string { return provider.LinkAPIResponsesProtocol }
func (adapter *scriptedAdapter) Exchange(_ context.Context, request provider.Request) (provider.Response, error) {
	adapter.requests = append(adapter.requests, request)
	index := len(adapter.requests) - 1
	if index >= len(adapter.responses) {
		return provider.Response{}, errors.New("unexpected exchange")
	}
	var err error
	if index < len(adapter.errors) {
		err = adapter.errors[index]
	}
	return adapter.responses[index], err
}

func responseFixture(body string, input, output uint64) provider.Response {
	var document map[string]any
	if json.Unmarshal([]byte(body), &document) == nil {
		if _, exists := document["model"]; !exists {
			document["model"] = developmentModel
		}
		encoded, _ := json.Marshal(document)
		body = string(encoded)
	}
	return provider.Response{
		StatusCode: 200, Body: json.RawMessage(body), RequestID: "provider-private-id",
		RequestDigest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ResponseDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Usage:          &provider.Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output},
	}
}

func testTrialLimits() TrialLimits {
	return TrialLimits{
		MaxProviderCalls: 4, MaxToolCalls: 8, MaxPythonRuns: 2,
		MaxInputTokens: 1000, MaxOutputTokens: 100, MaxTotalTokens: 1100, MaxOutputTokensPerCall: 64,
	}
}

func TestResponsesSessionUsesLinkAPIDocumentedWireDialect(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	history := []any{
		map[string]any{"role": "developer", "content": "use tools"},
		map[string]any{"role": "user", "content": "inspect"},
		map[string]any{"type": "function_call", "status": "completed", "call_id": "call-1", "name": "pwd", "arguments": "null"},
		map[string]any{"type": "function_call_output", "call_id": "call-1", "output": `{"path":"/"}`},
	}
	tools := []map[string]any{{"type": "function", "name": "pwd", "description": "working directory", "parameters": map[string]any{"type": "object"}, "strict": false}}
	if _, err := session.Exchange(context.Background(), history, tools, "required", false, map[string]string{"pwd": "pwd"}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("requests=%d", len(adapter.requests))
	}
	var payload map[string]any
	if json.Unmarshal(adapter.requests[0].Payload, &payload) != nil {
		t.Fatal("decode payload")
	}
	for _, forbidden := range []string{"input", "max_output_tokens", "background"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("legacy field %q leaked: %s", forbidden, adapter.requests[0].Payload)
		}
	}
	if payload["max_tokens"] != float64(64) || payload["stream"] != false || payload["parallel_tool_calls"] != false {
		t.Fatalf("payload=%s", adapter.requests[0].Payload)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 4 {
		t.Fatalf("messages=%#v", payload["messages"])
	}
	assistant := messages[2].(map[string]any)
	toolMessage := messages[3].(map[string]any)
	toolCall := assistant["tool_calls"].([]any)[0].(map[string]any)
	if assistant["role"] != "assistant" || len(assistant["tool_calls"].([]any)) != 1 || toolCall["function"].(map[string]any)["arguments"] != "{}" || toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call-1" {
		t.Fatalf("assistant=%#v tool=%#v", assistant, toolMessage)
	}
	wireTools, ok := payload["tools"].([]any)
	if !ok || len(wireTools) != 1 {
		t.Fatalf("tools=%#v", payload["tools"])
	}
	wireTool := wireTools[0].(map[string]any)
	if wireTool["type"] != "function" || wireTool["function"].(map[string]any)["name"] != "pwd" {
		t.Fatalf("wire tool=%#v", wireTool)
	}
}

func TestResponsesSessionRejectsMalformedLinkAPIHistoryBeforeExchange(t *testing.T) {
	for name, history := range map[string][]any{
		"unmatched output": {map[string]any{"type": "function_call_output", "call_id": "missing-call", "output": "{}"}},
		"non-string type":  {map[string]any{"type": 7, "role": "user", "content": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[]}`, 1, 1)}}
			session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Exchange(context.Background(), history, nil, "auto", false, nil)
			if err == nil || len(adapter.requests) != 0 {
				t.Fatalf("err=%v requests=%d", err, len(adapter.requests))
			}
		})
	}
}

func TestResponsesSessionClassifiesIncompleteResponseBeforeUsageOvershoot(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`, 10, 80)}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Exchange(context.Background(), []any{map[string]any{"role": "user", "content": "x"}}, nil, "auto", false, nil)
	evidence := session.Evidence()
	if !errors.Is(err, ErrAgenticRun) || errors.Is(err, ErrProviderOutputLimitExceeded) || session.ProviderCalls() != 1 || session.Usage().OutputTokens != 80 || len(evidence) != 1 || !evidence[0].ProtocolInvalid {
		t.Fatalf("err=%v calls=%d usage=%+v", err, session.ProviderCalls(), session.Usage())
	}
}

func TestResponsesSessionRejectsResponseModelDrift(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"model":"gpt-5.5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); !errors.Is(err, ErrProviderIdentityMismatch) {
		t.Fatalf("response model drift err=%v", err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); err == nil || len(adapter.requests) != 1 {
		t.Fatalf("identity failure did not close session: requests=%d", len(adapter.requests))
	}
}

func TestResponsesSessionRejectsUnexpectedInstructions(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"instructions":"provider-injected","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); !errors.Is(err, ErrProviderIdentityMismatch) {
		t.Fatalf("provider-injected instructions err=%v", err)
	}
}

func TestResponsesSessionRequiresModelAndAcceptsNullInstructions(t *testing.T) {
	missing := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"model":null,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)}}
	session, err := NewResponsesSession(missing, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); err == nil {
		t.Fatal("missing response model accepted")
	}

	valid := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"instructions":null,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)}}
	session, err = NewResponsesSession(valid, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); err != nil {
		t.Fatalf("null instructions rejected: %v", err)
	}
}

func TestResponsesSessionRejectsCompletelyMissingModelKey(t *testing.T) {
	response := responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 10, 2)
	response.Body = json.RawMessage(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "required", false, map[string]string{}); !errors.Is(err, ErrProviderIdentityMismatch) {
		t.Fatalf("missing model key err=%v", err)
	}
}

func TestResponsesSessionParsesCallsAndKeepsRawProtocolInMemoryOnly(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{
		"id":"response-private",
		"status":"completed",
		"output":[{"type":"function_call","id":"item-private","status":"completed","call_id":"call-private","name":"pwd","arguments":"{}"}]
	}`, 20, 5)}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := session.Exchange(context.Background(), []any{map[string]any{"role": "user", "content": "where"}}, []map[string]any{{"type": "function", "name": "pwd", "parameters": map[string]any{"type": "object"}}}, "required", true, map[string]string{"pwd": "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Calls) != 1 || parsed.Calls[0].CallID != "call-private" || parsed.Calls[0].CanonicalName != "pwd" || string(parsed.Calls[0].Arguments) != "{}" {
		t.Fatalf("parsed=%+v", parsed)
	}
	if len(parsed.replayItems) != 1 || session.ProviderCalls() != 1 || session.Usage().TotalTokens != 25 {
		t.Fatalf("parsed=%+v calls=%d usage=%+v", parsed, session.ProviderCalls(), session.Usage())
	}
	encodedParsed, _ := json.Marshal(parsed)
	encodedEvidence, _ := json.Marshal(session.Evidence())
	for _, secret := range []string{"response-private", "item-private", "call-private", "where"} {
		if containsBytes(encodedParsed, []byte(secret)) || containsBytes(encodedEvidence, []byte(secret)) {
			t.Fatalf("serialized evidence leaked %q: parsed=%s evidence=%s", secret, encodedParsed, encodedEvidence)
		}
	}
}

func TestResponsesSessionStopsPermanentlyAfterMissingUsage(t *testing.T) {
	response := responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 1, 1)
	response.Usage = nil
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); !errors.Is(err, ErrUsageMissing) {
		t.Fatalf("missing usage err=%v", err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); !errors.Is(err, ErrBudgetClosed) {
		t.Fatalf("second exchange err=%v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("provider requests=%d want=1", len(adapter.requests))
	}
}

func TestResponsesSessionUsesActualUsageAndCheckedRemainingOutput(t *testing.T) {
	limits := testTrialLimits()
	limits.MaxOutputTokens = 70
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}]}`, 10, 60),
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}]}`, 10, 11),
	}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); !errors.Is(err, ErrProviderOutputLimitExceeded) {
		t.Fatalf("overflow err=%v", err)
	}
	var first, second map[string]any
	if json.Unmarshal(adapter.requests[0].Payload, &first) != nil || json.Unmarshal(adapter.requests[1].Payload, &second) != nil {
		t.Fatal("decode requests")
	}
	if first["max_tokens"] != float64(64) || second["max_tokens"] != float64(10) {
		t.Fatalf("max outputs first=%v second=%v", first["max_tokens"], second["max_tokens"])
	}
	if session.Usage().OutputTokens != 71 || session.ProviderCalls() != 2 {
		t.Fatalf("usage=%+v calls=%d", session.Usage(), session.ProviderCalls())
	}
}

func TestResponsesSessionClassifiesProviderIgnoringRequestedOutputLimit(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"overshoot"}]}]}`, 10, 65),
	}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); !errors.Is(err, ErrProviderOutputLimitExceeded) {
		t.Fatalf("overshoot err=%v", err)
	}
	if session.ProviderCalls() != 1 || session.Usage().OutputTokens != 65 || len(session.Evidence()) != 1 || classifyTrialError(ErrProviderOutputLimitExceeded) != "provider_output_limit_exceeded" {
		t.Fatalf("calls=%d usage=%+v evidence=%+v", session.ProviderCalls(), session.Usage(), session.Evidence())
	}
}

func TestResponsesSessionCapsOutputByRemainingTotalBudget(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}]}`, 90, 5),
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}]}`, 1, 1),
	}}
	limits := TrialLimits{MaxProviderCalls: 2, MaxToolCalls: 1, MaxPythonRuns: 1, MaxInputTokens: 100, MaxOutputTokens: 100, MaxTotalTokens: 100, MaxOutputTokensPerCall: 50}
	session, err := NewResponsesSession(adapter, developmentModel, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if json.Unmarshal(adapter.requests[1].Payload, &payload) != nil || payload["max_tokens"].(float64) != 5 {
		t.Fatalf("payload=%s", adapter.requests[1].Payload)
	}
}

func TestParseResponsesPreservesParallelCallOrder(t *testing.T) {
	body := json.RawMessage(`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"},{"type":"function_call","call_id":"c2","name":"ls","arguments":"{}"}]}`)
	parsed, err := ParseResponsesOutput(body, map[string]string{"pwd": "pwd", "ls": "ls"})
	if err != nil || len(parsed.Calls) != 2 || parsed.Calls[0].CallID != "c1" || parsed.Calls[1].CallID != "c2" || len(parsed.replayItems) != 2 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestResponsesSessionReplaysToolApplicationErrorWithoutRetry(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"}]}`, 10, 2),
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 8, 1),
	}}
	session, err := NewResponsesSession(adapter, developmentModel, testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	history := []any{map[string]any{"role": "user", "content": "work"}}
	parsed, err := session.Exchange(context.Background(), history, []map[string]any{{"type": "function", "name": "pwd", "parameters": map[string]any{"type": "object"}}}, "required", false, map[string]string{"pwd": "pwd"})
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, parsed.replayItems...)
	history = append(history, map[string]any{"type": "function_call_output", "call_id": "c1", "output": `{"error_code":"tool_application_error","status":"error"}`})
	if _, err := session.Exchange(context.Background(), history, nil, "auto", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(adapter.requests) != 2 || !containsBytes(adapter.requests[1].Payload, []byte("tool_application_error")) {
		t.Fatalf("requests=%d second=%s", len(adapter.requests), adapter.requests[1].Payload)
	}
}

func TestResponsesSessionAccountsProviderReportedReasoningUsage(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","usage":{"output_tokens_details":{"reasoning_tokens":90}},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}]}`, 10, 100)}}
	limits := testTrialLimits()
	limits.MaxOutputTokens = 120
	limits.MaxTotalTokens = 200
	limits.MaxOutputTokensPerCall = 120
	session, err := NewResponsesSession(adapter, developmentModel, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), nil, nil, "auto", false, nil); err != nil {
		t.Fatal(err)
	}
	if session.Usage().OutputTokens != 100 || session.Usage().TotalTokens != 110 {
		t.Fatalf("usage=%+v", session.Usage())
	}
}

func TestParseResponsesAllowsMissingItemStatusOnlyWhenEnvelopeCompleted(t *testing.T) {
	completed := json.RawMessage(`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"}]}`)
	parsed, err := ParseResponsesOutput(completed, map[string]string{"pwd": "pwd"})
	if err != nil || len(parsed.Calls) != 1 {
		t.Fatalf("completed parsed=%+v err=%v", parsed, err)
	}
	inProgress := json.RawMessage(`{"status":"in_progress","output":[{"type":"function_call","status":"completed","call_id":"c1","name":"pwd","arguments":"{}"}]}`)
	if _, err := ParseResponsesOutput(inProgress, map[string]string{"pwd": "pwd"}); err == nil {
		t.Fatal("in-progress response accepted")
	}
}

func TestParseResponsesNormalizesNullFunctionArgumentsToEmptyObject(t *testing.T) {
	body := json.RawMessage(`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"ls","arguments":"null"}]}`)
	parsed, err := ParseResponsesOutput(body, map[string]string{"ls": "ls"})
	if err != nil || len(parsed.Calls) != 1 || string(parsed.Calls[0].Arguments) != "{}" {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestParseResponsesOutputRejectsUnknownDuplicateAndLeakyMarshal(t *testing.T) {
	invalid := []string{
		`{"status":"completed","output":[],"output":[]}`,
		`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"unknown","arguments":"{}"}]}`,
		`{"status":"completed","output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"},{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"}]}`,
		`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"c1","name":"pwd","arguments":"{\"x\":1,\"x\":2}"}]}`,
		`{"status":"completed","output":[{"type":"function_call","status":"in_progress","call_id":"c1","name":"pwd","arguments":"{}"}]}`,
		`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text"}]}]}`,
		`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal"}]}]}`,
	}
	for _, body := range invalid {
		if _, err := ParseResponsesOutput(json.RawMessage(body), map[string]string{"pwd": "pwd"}); err == nil {
			t.Fatalf("accepted invalid body: %s", body)
		}
	}
}

func TestParseResponsesRefusalUsesOfficialField(t *testing.T) {
	parsed, err := ParseResponsesOutput(json.RawMessage(`{
		"status":"completed",
		"output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"cannot comply"}]}]
	}`), nil)
	if err != nil || !parsed.HasMessage || !parsed.Refused || parsed.TextDigest == "" || len(parsed.replayItems) != 1 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
}

func TestResponsesSessionDoesNotRetryProviderErrors(t *testing.T) {
	adapter := &scriptedAdapter{
		responses: []provider.Response{responseFixture(`{"error":"temporary"}`, 1, 1)},
		errors:    []error{provider.ErrExchange},
	}
	session, err := NewResponsesSession(adapter, "gpt-5.4", testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", false, nil); err == nil {
		t.Fatal("provider error succeeded")
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("provider was retried %d times", len(adapter.requests))
	}
}
