package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServiceExecutorRejectsReducedUnverifiedArtifactEvidence(t *testing.T) {
	directory := t.TempDir()
	artifact := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	digest := sha256.Sum256(artifact)
	artifactPath := filepath.Join(directory, "agent-python-runtime.wasm")
	manifestPath := filepath.Join(directory, "manifest.json")
	inventoryPath := filepath.Join(directory, "import-inventory.json")
	qualificationPath := filepath.Join(directory, "import-qualification.json")
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"artifact_profile":"base","artifact":{"sha256":"%x"}}`, digest)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inventoryPath, []byte(`{"artifact_profile":"base","discoverable_roots":["sys"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qualificationPath, []byte(`{"artifact_profile":"base","qualified_roots":["sys"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServiceExecutor(artifactPath, manifestPath, inventoryPath, qualificationPath, []string{"sys"}, "seed"); err == nil {
		t.Fatal("service accepted evidence that bypasses the distribution verifier")
	}
}

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
