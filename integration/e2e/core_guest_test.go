package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type guestResponse struct {
	Status string         `json:"status"`
	Result any            `json:"result"`
	Error  map[string]any `json:"error"`
}

func guestArtifact(t *testing.T) string {
	t.Helper()
	path := os.Getenv("AGENT_RUNTIME_GUEST")
	if path == "" {
		t.Skip("AGENT_RUNTIME_GUEST is not set; real WASI artifact required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	return newEngineWithConfig(t, runtime.DefaultRunConfig())
}

func newEngineWithConfig(t *testing.T, config runtime.RunConfig) *engine.Engine {
	t.Helper()
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := engine.New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instance.Close(context.Background()); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	return instance
}

func run(t *testing.T, instance *engine.Engine, runID, code string, inputs any) guestResponse {
	t.Helper()
	return runWithPrepare(t, instance, runID, code, inputs, "")
}

func runWithPrepare(t *testing.T, instance *engine.Engine, runID, code string, inputs any, trustedPrepare string) guestResponse {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"run_id": runID,
		"code":   code,
		"inputs": inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := instance.Run(context.Background(), request, trustedPrepare)
	if err != nil {
		t.Fatal(err)
	}
	var response guestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode guest response: %v: %s", err, payload)
	}
	return response
}

func TestCoreGuestExecutesNeutralRequest(t *testing.T) {
	response := run(
		t,
		newEngine(t),
		"core-1",
		"result = {'value': inputs['value'] + 1}",
		map[string]any{"value": 41},
	)
	if response.Status != "ok" {
		t.Fatalf("guest returned error: %#v", response.Error)
	}
	result := response.Result.(map[string]any)
	if result["value"] != float64(42) {
		t.Fatalf("unexpected result: %#v", response.Result)
	}
}

func TestFreshInstanceDoesNotLeakPythonGlobals(t *testing.T) {
	instance := newEngine(t)
	first := run(t, instance, "fresh-1", "import builtins\nbuiltins._agent_runtime_leak = 1\nresult = True", map[string]any{})
	if first.Status != "ok" {
		t.Fatalf("first run failed: %#v", first.Error)
	}
	second := run(t, instance, "fresh-2", "import builtins\nresult = hasattr(builtins, '_agent_runtime_leak')", map[string]any{})
	if second.Status != "ok" || second.Result != false {
		t.Fatalf("state leaked into fresh instance: %#v", second)
	}
}

func TestTrustedPrepareIsAvailableButDoesNotPersistAcrossRuns(t *testing.T) {
	instance := newEngine(t)
	prepared := runWithPrepare(
		t,
		instance,
		"prepared-1",
		"result = double(inputs['value'])",
		map[string]any{"value": 21},
		"def double(value):\n    return value * 2",
	)
	if prepared.Status != "ok" || prepared.Result != float64(42) {
		t.Fatalf("prepared execution failed: %#v", prepared)
	}

	fresh := run(t, instance, "prepared-2", "result = 'double' in globals()", map[string]any{})
	if fresh.Status != "ok" || fresh.Result != false {
		t.Fatalf("trusted prepare leaked to a new instance: %#v", fresh)
	}
}

func TestTimeoutDiscardsInstanceAndNextRunRecovers(t *testing.T) {
	config := runtime.DefaultRunConfig()
	config.Timeout = 10 * time.Second
	instance := newEngineWithConfig(t, config)
	request, err := json.Marshal(map[string]any{
		"run_id": "timeout-1",
		"code":   "while True:\n    pass",
		"inputs": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.Run(context.Background(), request, "")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected bounded timeout, got %v", err)
	}

	recovered := run(t, instance, "timeout-2", "result = inputs['value'] + 1", map[string]any{"value": 41})
	if recovered.Status != "ok" || recovered.Result != float64(42) {
		t.Fatalf("engine did not recover with a fresh instance: %#v", recovered)
	}
}

func TestGuestHasNoAmbientEnvironmentFilesystemOrSocket(t *testing.T) {
	code := `
import os
checks = {"environment": bool(os.environ)}
try:
    open("/etc/passwd", "rb")
    checks["host_file"] = True
except BaseException:
    checks["host_file"] = False
try:
    import socket
    socket.socket()
    checks["socket"] = True
except BaseException:
    checks["socket"] = False
result = checks
`
	response := run(t, newEngine(t), "deny-1", code, map[string]any{})
	if response.Status != "ok" {
		t.Fatalf("denial probe failed: %#v", response.Error)
	}
	result := response.Result.(map[string]any)
	for _, key := range []string{"environment", "host_file", "socket"} {
		if result[key] != false {
			t.Fatalf("ambient authority %s unexpectedly available: %#v", key, result)
		}
	}
}
