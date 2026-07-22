package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestFetchManyAdversarialBoundariesWithRealGuest(t *testing.T) {
	var providerCalls atomic.Int32
	var redirectEscaped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			providerCalls.Add(1)
			writer.Header().Set("Location", "/escaped")
			writer.WriteHeader(http.StatusFound)
		case "/escaped":
			redirectEscaped.Store(true)
			writer.WriteHeader(http.StatusTeapot)
		case "/large":
			providerCalls.Add(1)
			_, _ = writer.Write([]byte(strings.Repeat("x", 64)))
		case "/ok":
			providerCalls.Add(1)
			_, _ = writer.Write([]byte("ok"))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	grant := capability.Grant{
		Name:               capability.FetchManyCapability,
		MaxCalls:           3,
		MaxRequestsPerCall: 2,
		MaxTotalRequests:   4,
		MaxConcurrency:     2,
		MaxResponseBytes:   8,
		PerRequestTimeout:  time.Second,
		Targets: map[string]capability.TargetGrant{
			"fixture": {BaseURL: server.URL},
		},
	}
	factory := wazeroengine.Factory{
		BrokerFactory: func(_ context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{
				RunIdentity: "host-adversarial-run",
				Grants:      map[string]capability.Grant{grant.Name: grant},
			}, capability.NewHTTPFetcher(server.Client()))
		},
	}
	instance := newEngineWithFactory(t, runtime.DefaultRunConfig(), factory)
	response := run(t, instance, "guest-untrusted-id", `
import json
import _agent_runtime_host
from agent_runtime import tools

def raw(call_id, requests):
    payload = {"call_id": call_id, "capability": "fetch_many", "arguments": {"requests": requests}}
    return json.loads(_agent_runtime_host.call(json.dumps(payload, separators=(",", ":"))))

host_header = raw("raw-host-header", [{"request_id": "header", "target": "fixture", "path": "/ok", "headers": {"Host": "evil.invalid"}}])
duplicate = raw("raw-duplicate", [
    {"request_id": "same", "target": "fixture", "path": "/ok"},
    {"request_id": "same", "target": "fixture", "path": "/ok"},
])
malformed_path = raw("raw-path", [{"request_id": "path", "target": "fixture", "path": "https://evil.invalid/escape"}])
redirect = tools.fetch_many([{"request_id": "redirect", "target": "fixture", "path": "/redirect"}])[0]
large = tools.fetch_many([{"request_id": "large", "target": "fixture", "path": "/large"}])[0]
try:
    tools.fetch_many([{"request_id": "budget", "target": "fixture", "path": "/ok"}])
    exhausted = {"unexpected": "accepted"}
except tools.CapabilityError as error:
    exhausted = {"code": error.code}
result = {
    "host_header": {"status": host_header["status"], "code": host_header["error"]["code"]},
    "duplicate": {"status": duplicate["status"], "code": duplicate["error"]["code"]},
    "malformed_path": malformed_path["result"]["items"][0]["status"],
    "redirect": {"status": redirect["status"], "http_status": redirect["http_status"]},
    "large": {"status": large["status"], "code": large["error"]["code"]},
    "exhausted": exhausted,
}
`, nil)
	if response.Status != "ok" {
		t.Fatalf("adversarial guest failed: %#v", response.Error)
	}
	result := response.Result.(map[string]any)
	assertMapValue(t, result, "host_header", "status", "error")
	assertMapValue(t, result, "host_header", "code", "invalid_arguments")
	assertMapValue(t, result, "duplicate", "status", "error")
	assertMapValue(t, result, "duplicate", "code", "invalid_arguments")
	if result["malformed_path"] != "denied" {
		t.Fatalf("malformed path escaped: %#v", result)
	}
	assertMapValue(t, result, "redirect", "status", "ok")
	assertMapValue(t, result, "redirect", "http_status", float64(http.StatusFound))
	assertMapValue(t, result, "large", "status", "error")
	assertMapValue(t, result, "large", "code", "response_too_large")
	assertMapValue(t, result, "exhausted", "code", "call_budget_exceeded")
	if providerCalls.Load() != 2 || redirectEscaped.Load() {
		t.Fatalf("provider boundary violated: calls=%d redirect_escaped=%v", providerCalls.Load(), redirectEscaped.Load())
	}
	if len(response.Receipts) != 3 {
		t.Fatalf("receipt count=%d, want 3: %#v", len(response.Receipts), response.Receipts)
	}
	for index, receipt := range response.Receipts {
		if receipt.RunID != "host-adversarial-run" || receipt.OperationIndex != 0 {
			t.Fatalf("receipt %d is not Host-authored: %#v", index, receipt)
		}
	}
	if response.Receipts[0].Outcome != "denied" || response.Receipts[0].ResponseSHA256 != "" ||
		response.Receipts[1].Outcome != "ok" || response.Receipts[2].Outcome != "error" || response.Receipts[2].ResponseSHA256 != "" {
		t.Fatalf("receipt admission semantics drifted: %#v", response.Receipts)
	}
	if response.Metrics["capability_calls"] != float64(3) {
		t.Fatalf("invalid calls consumed budget: %#v", response.Metrics)
	}
}

func assertMapValue(t *testing.T, outer map[string]any, key, innerKey string, expected any) {
	t.Helper()
	inner, ok := outer[key].(map[string]any)
	if !ok || inner[innerKey] != expected {
		t.Fatalf("%s.%s=%#v, want %#v in %#v", key, innerKey, inner[innerKey], expected, outer)
	}
}
