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

func TestResponsesSessionParsesCallsAndKeepsRawProtocolInMemoryOnly(t *testing.T) {
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{
		"id":"response-private",
		"output":[{"type":"function_call","id":"item-private","status":"completed","call_id":"call-private","name":"pwd","arguments":"{}"}]
	}`, 20, 5)}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := session.Exchange(context.Background(), []any{map[string]any{"role": "user", "content": "where"}}, []map[string]any{{"type": "function", "name": "pwd", "parameters": map[string]any{"type": "object"}}}, "required", map[string]string{"pwd": "pwd"})
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
	response := responseFixture(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 1, 1)
	response.Usage = nil
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", testTrialLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", nil); !errors.Is(err, ErrUsageMissing) {
		t.Fatalf("missing usage err=%v", err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", nil); !errors.Is(err, ErrBudgetClosed) {
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
		responseFixture(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}]}`, 10, 60),
		responseFixture(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"two"}]}]}`, 10, 11),
	}}
	session, err := NewResponsesSession(adapter, "gpt-5.4", limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", nil); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("overflow err=%v", err)
	}
	var first, second map[string]any
	if json.Unmarshal(adapter.requests[0].Payload, &first) != nil || json.Unmarshal(adapter.requests[1].Payload, &second) != nil {
		t.Fatal("decode requests")
	}
	if first["max_output_tokens"] != float64(64) || second["max_output_tokens"] != float64(10) {
		t.Fatalf("max outputs first=%v second=%v", first["max_output_tokens"], second["max_output_tokens"])
	}
	if session.Usage().OutputTokens != 71 || session.ProviderCalls() != 2 {
		t.Fatalf("usage=%+v calls=%d", session.Usage(), session.ProviderCalls())
	}
}

func TestParseResponsesRejectsUnknownDuplicateAndAmbiguousCalls(t *testing.T) {
	invalid := []string{
		`{"output":[],"output":[]}`,
		`{"output":[{"type":"function_call","call_id":"c1","name":"unknown","arguments":"{}"}]}`,
		`{"output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"},{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{}"}]}`,
		`{"output":[{"type":"function_call","call_id":"c1","name":"pwd","arguments":"{\"x\":1,\"x\":2}"}]}`,
	}
	for _, body := range invalid {
		if _, err := ParseResponsesOutput(json.RawMessage(body), map[string]string{"pwd": "pwd"}); err == nil {
			t.Fatalf("accepted invalid body: %s", body)
		}
	}
}

func TestParseResponsesRefusalUsesOfficialField(t *testing.T) {
	parsed, err := ParseResponsesOutput(json.RawMessage(`{
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
	if _, err := session.Exchange(context.Background(), []any{}, nil, "auto", nil); err == nil {
		t.Fatal("provider error succeeded")
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("provider was retried %d times", len(adapter.requests))
	}
}
