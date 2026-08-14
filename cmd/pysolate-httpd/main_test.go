package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionEndpointRequiresBearerAndReturnsEvidence(t *testing.T) {
	server := newHTTPServer("secret", func(_ context.Context, request executeRequest) (executeResponse, error) {
		if request.ProtocolVersion != protocolVersion || request.RequestID != "logical-1" {
			t.Fatalf("request=%+v", request)
		}
		return executeResponse{ProtocolVersion: protocolVersion, RequestID: request.RequestID, InvocationIdentity: "sha256:invocation", PhysicalExecutionID: "physical-1", Disposition: "leader", Result: json.RawMessage(`42`)}, nil
	})
	body := []byte(`{"protocol_version":"pysolate.remote-execution.v1","request_id":"logical-1","run_request":{"run_id":"guest-1","code":"result = 42","inputs":{}}}`)

	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/executions", bytes.NewReader(body))
	unauthorizedResult := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResult, unauthorized)
	if unauthorizedResult.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", unauthorizedResult.Code)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/v1/executions", bytes.NewReader(body))
	authorized.Header.Set("Authorization", "Bearer secret")
	authorizedResult := httptest.NewRecorder()
	server.ServeHTTP(authorizedResult, authorized)
	if authorizedResult.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", authorizedResult.Code, authorizedResult.Body.String())
	}
	var response executeResponse
	if err := json.Unmarshal(authorizedResult.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.InvocationIdentity != "sha256:invocation" || response.PhysicalExecutionID != "physical-1" || string(response.Result) != "42" {
		t.Fatalf("response=%+v", response)
	}
}

func TestExecutionEndpointRejectsUnknownAndOversizedJSON(t *testing.T) {
	server := newHTTPServer("secret", func(context.Context, executeRequest) (executeResponse, error) {
		t.Fatal("executor must not be called")
		return executeResponse{}, nil
	})
	for _, body := range [][]byte{
		[]byte(`{"protocol_version":"pysolate.remote-execution.v1","request_id":"logical-1","run_request":{"run_id":"guest-1","code":"result=1","inputs":{}},"authority":"ambient"}`),
		bytes.Repeat([]byte("x"), maxHTTPBodyBytes+1),
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/executions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		result := httptest.NewRecorder()
		server.ServeHTTP(result, request)
		if result.Code != http.StatusBadRequest && result.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
	}
}

func TestExecutionEndpointCanonicalizesObjectInputs(t *testing.T) {
	server := newHTTPServer("secret", func(_ context.Context, request executeRequest) (executeResponse, error) {
		if string(request.RunRequest.Inputs) != `{"z":1,"a":2}` {
			t.Fatalf("HTTP decode changed caller bytes before executor: %s", request.RunRequest.Inputs)
		}
		return executeResponse{ProtocolVersion: protocolVersion, RequestID: request.RequestID, Result: json.RawMessage(`{"ok":true}`)}, nil
	})
	body := `{"protocol_version":"pysolate.remote-execution.v1","request_id":"canonical","run_request":{"run_id":"canonical","code":"result=1","inputs":{"z":1,"a":2}}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/executions", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer secret")
	result := httptest.NewRecorder()
	server.ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	server := newHTTPServer("secret", nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	result := httptest.NewRecorder()
	server.ServeHTTP(result, request)
	var health map[string]string
	if err := json.Unmarshal(result.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if result.Code != http.StatusOK || health["status"] != "ready" || health["protocol_version"] != protocolVersion || health["execution_instance"] != "fresh_per_physical_execution" || health["prepared_runtime"] != "disabled" || health["memory_cow"] != "disabled" {
		t.Fatalf("status=%d health=%v", result.Code, health)
	}
}
