package agenttrajectory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestDeepSeekProviderSendsBoundedJSONRequestAndReturnsRawEnvelope(t *testing.T) {
	var calls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("request=%s %s auth=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "deepseek-v4-flash" || body["temperature"] != float64(0) || body["stream"] != false {
			t.Fatalf("request body=%#v", body)
		}
		responseFormat, ok := body["response_format"].(map[string]any)
		if !ok || responseFormat["type"] != "json_object" {
			t.Fatalf("response format=%#v", body["response_format"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"response-1","model":"deepseek-v4-flash","choices":[{"index":0,"message":{"role":"assistant","content":"{\"schema_version\":\"pysolate.day-trip-planning-brief.v1\",\"task\":\"Plan a Saturday day trip for two people from London within a GBP 100 total budget.\",\"candidate_ids\":[\"brighton\",\"oxford\"]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":40,"total_tokens":160}}`))
	}))
	defer server.Close()

	provider, err := agenttrajectory.NewDeepSeekProvider(agenttrajectory.DeepSeekConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "deepseek-v4-flash", MaxCalls: 5,
		Timeout: 2 * time.Second, MaxResponseBytes: 64 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Complete(context.Background(), agenttrajectory.ModelRequest{
		CallID: "main-plan", ActorID: "main", ResponseKind: agenttrajectory.ResponsePlanningBrief,
		Messages: []agenttrajectory.ModelMessage{{Role: "system", Content: "public system"}, {Role: "user", Content: "public request"}},
	})
	if err != nil || result.Content == "" || result.ResponseID != "response-1" || result.Model != "deepseek-v4-flash" || len(result.RawRequest) == 0 || len(result.RawResponse) == 0 || result.Usage.TotalTokens != 160 || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
	if provider.CallCount() != 1 {
		t.Fatalf("call count=%d", provider.CallCount())
	}
}

func TestDeepSeekProviderFailsClosedWithoutRetry(t *testing.T) {
	var calls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()
	provider, err := agenttrajectory.NewDeepSeekProvider(agenttrajectory.DeepSeekConfig{BaseURL: server.URL, APIKey: "test-key", Model: "deepseek-v4-flash", MaxCalls: 1, Timeout: time.Second, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	request := agenttrajectory.ModelRequest{CallID: "main-plan", ActorID: "main", ResponseKind: agenttrajectory.ResponsePlanningBrief, Messages: []agenttrajectory.ModelMessage{{Role: "user", Content: "request"}}}
	if _, err := provider.Complete(context.Background(), request); err == nil || calls.Load() != 1 {
		t.Fatalf("first failure err=%v calls=%d", err, calls.Load())
	}
	if _, err := provider.Complete(context.Background(), request); err == nil || calls.Load() != 1 {
		t.Fatalf("budget failure dispatched err=%v calls=%d", err, calls.Load())
	}
	if provider.CallCount() != 1 {
		t.Fatalf("rejected call consumed budget: %d", provider.CallCount())
	}
}

func TestDeepSeekProviderRejectsInvalidConfigurationAndMessages(t *testing.T) {
	for _, config := range []agenttrajectory.DeepSeekConfig{
		{},
		{BaseURL: "http://example.com", APIKey: "x", Model: "deepseek-v4-flash", MaxCalls: 5, Timeout: time.Second, MaxResponseBytes: 1024},
		{BaseURL: "https://api.deepseek.com", APIKey: "x", Model: "unknown", MaxCalls: 5, Timeout: time.Second, MaxResponseBytes: 1024},
	} {
		if _, err := agenttrajectory.NewDeepSeekProvider(config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
