package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLinkAPIResponsesProducesDigestOnlyEvidence(t *testing.T) {
	const secret = "test-secret-never-record"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer "+secret || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request=%+v", request)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("x-request-id", "req_fixture")
		_, _ = writer.Write([]byte(`{"id":"resp_fixture","output":[],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}`))
	}))
	defer server.Close()
	adapter, err := newLinkAPIResponses(server.Client(), server.URL+"/v1/responses", func() (string, bool) { return secret, true }, true)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"model":"openai/gpt-test","input":"private prompt"}`)
	response, err := adapter.Exchange(context.Background(), Request{Model: "openai/gpt-test", Payload: payload})
	if err != nil || response.StatusCode != 200 || response.RequestID != "req_fixture" || response.Usage == nil || response.Usage.TotalTokens != 10 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	evidenceBytes, err := json.Marshal(response.Evidence())
	responseBytes, responseErr := json.Marshal(response)
	if err != nil || responseErr != nil || strings.Contains(string(evidenceBytes), secret) || strings.Contains(string(evidenceBytes), "private prompt") || strings.Contains(string(responseBytes), "resp_fixture") || !strings.Contains(string(evidenceBytes), "sha256:") {
		t.Fatalf("evidence=%s response=%s err=%v response_err=%v", evidenceBytes, responseBytes, err, responseErr)
	}
}

func TestLinkAPIResponsesDoesNotImplicitlyRetryPaidRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"code":"rate_limit"}}`))
	}))
	defer server.Close()
	adapter, err := newLinkAPIResponses(server.Client(), server.URL+"/v1/responses", func() (string, bool) { return "secret", true }, true)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x"}`)})
	if !errors.Is(err, ErrExchange) || response.StatusCode != http.StatusTooManyRequests || response.RequestDigest == "" || response.ResponseDigest == "" || calls.Load() != 1 {
		t.Fatalf("response=%+v calls=%d err=%v", response, calls.Load(), err)
	}
}

func TestLinkAPIResponsesRejectsNonObjectResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`"not-a-responses-object"`))
	}))
	defer server.Close()
	adapter, err := newLinkAPIResponses(server.Client(), server.URL+"/v1/responses", func() (string, bool) { return "secret", true }, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x"}`)}); !errors.Is(err, ErrExchange) {
		t.Fatalf("err=%v", err)
	}
}

func TestLinkAPIResponsesFailsClosedBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for name, fixture := range map[string]struct {
		credential func() (string, bool)
		request    Request
	}{
		"missing credential":        {credential: func() (string, bool) { return "", false }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x"}`)}},
		"invalid credential":        {credential: func() (string, bool) { return " secret", true }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x"}`)}},
		"model mismatch":            {credential: func() (string, bool) { return "secret", true }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"other","input":"x"}`)}},
		"streaming not registered":  {credential: func() (string, bool) { return "secret", true }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x","stream":true}`)}},
		"invalid stream type":       {credential: func() (string, bool) { return "secret", true }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x","stream":"false"}`)}},
		"background not registered": {credential: func() (string, bool) { return "secret", true }, request: Request{Model: "gpt-test", Payload: json.RawMessage(`{"model":"gpt-test","input":"x","background":true}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			adapter, err := newLinkAPIResponses(server.Client(), server.URL+"/v1/responses", fixture.credential, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Exchange(context.Background(), fixture.request); !errors.Is(err, ErrExchange) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached provider: %d", calls.Load())
	}
}
