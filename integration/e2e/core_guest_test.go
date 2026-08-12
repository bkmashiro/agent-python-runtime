package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type guestResponse struct {
	Status               string           `json:"status"`
	Result               any              `json:"result"`
	Receipts             []map[string]any `json:"receipts"`
	Metrics              map[string]any   `json:"metrics"`
	Error                map[string]any   `json:"error"`
	CapabilityPlanSHA256 string           `json:"capability_plan_sha256"`
	WorkspaceReceipt     map[string]any   `json:"workspace_receipt"`
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

func newEngine(t *testing.T) enginecontract.Runner {
	t.Helper()
	return newEngineWithConfig(t, runtimeconfig.DefaultRunConfig())
}

func newEngineWithConfig(t *testing.T, config runtimeconfig.RunConfig) enginecontract.Runner {
	t.Helper()
	artifactPath := guestArtifact(t)
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if config.ExecutionProfile != nil && config.ExecutionProfile.ArtifactSHA256() == "" {
		bundleRoot := filepath.Dir(artifactPath)
		manifest, err := os.ReadFile(filepath.Join(bundleRoot, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		inventory, err := os.ReadFile(filepath.Join(bundleRoot, "import-inventory.json"))
		if err != nil {
			t.Fatal(err)
		}
		qualification, err := os.ReadFile(filepath.Join(bundleRoot, "import-qualification.json"))
		if err != nil {
			t.Fatal(err)
		}
		identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), wasm, manifest, inventory, qualification)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := config.ExecutionProfile.BindVerifiedArtifact(identity)
		if err != nil {
			t.Fatal(err)
		}
		config.ExecutionProfile = &bound
	}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return runner
}

func run(t *testing.T, runner enginecontract.Runner, runID, code string, inputs any) guestResponse {
	t.Helper()
	request, err := json.Marshal(map[string]any{"run_id": runID, "code": code, "inputs": inputs})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	var response guestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode guest response: %v: %s", err, payload)
	}
	return response
}

func TestCoreGuestExecutesPython(t *testing.T) {
	response := run(t, newEngine(t), "core-1", "result = {'value': inputs['value'] + 1}", map[string]any{"value": 41})
	if response.Status != "ok" || response.Result.(map[string]any)["value"] != float64(42) {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestFreshGuestDoesNotLeakPythonGlobals(t *testing.T) {
	runner := newEngine(t)
	first := run(t, runner, "fresh-1", "import builtins\nbuiltins._pysolate_leak = 1\nresult = True", map[string]any{})
	second := run(t, runner, "fresh-2", "import builtins\nresult = hasattr(builtins, '_pysolate_leak')", map[string]any{})
	if first.Status != "ok" || second.Status != "ok" || second.Result != false {
		t.Fatalf("state leaked between fresh guests: first=%#v second=%#v", first, second)
	}
}

func TestProfileRejectsLateImport(t *testing.T) {
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	runner := newEngineWithConfig(t, config)
	request, err := json.Marshal(map[string]any{
		"run_id": "late-import", "inputs": map[string]any{},
		"code":          "import sys\nloader = sys.modules['builtins'].__dict__['__' + 'import__']\nresult = loader('fractions')",
		"compatibility": map[string]any{"profile": "base", "imports": []string{"sys"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	var response guestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "error" || response.Error["error_type"] != "ImportError" {
		t.Fatalf("late import was not rejected: %#v", response)
	}
}

func TestTimeoutDiscardsGuestAndNextRunRecovers(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 100 * time.Millisecond
	runner := newEngineWithConfig(t, config)
	request := []byte(`{"run_id":"timeout","code":"while True:\n    pass","inputs":{}}`)
	if _, err := runner.Run(context.Background(), request, ""); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected bounded timeout, got %v", err)
	}
	config.Timeout = 20 * time.Second
	response := run(t, newEngineWithConfig(t, config), "recovered", "result = 42", map[string]any{})
	if response.Status != "ok" || response.Result != float64(42) {
		t.Fatalf("fresh engine did not recover: %#v", response)
	}
}

func TestExperimentalDeterministicProfileRepeatsQualifiedFreshGuests(t *testing.T) {
	artifactPath := guestArtifact(t)
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(wasm)
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(fmt.Sprintf("sha256:%x", artifactDigest[:]), "e2e-qualified-repeat")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"datetime", "sys"})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.DeterministicVerification = &deterministic
	runner := newEngineWithConfig(t, config)
	request := []byte(`{"run_id":"deterministic-e2e","code":"import datetime, sys\nos_module=sys.modules['os']\ntime_module=sys.modules['time']\nresult={'wall':time_module.time_ns(),'datetime':datetime.datetime.now().isoformat(),'urandom':os_module.urandom(16).hex(),'hash':hash('pysolate')}","inputs":{},"compatibility":{"profile":"base","imports":["datetime","sys"]}}`)
	first, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("qualified fresh Guests diverged:\nfirst=%s\nsecond=%s", first, second)
	}
}
