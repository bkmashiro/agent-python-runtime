package e2e_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capabilityrpc"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestRealGuestAndNativeRPCHaveEquivalentCapabilitySemantics(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := unifiedTransportPlan(t)
	var wasmBroker *capability.Broker
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		wasmBroker, err = capability.NewBroker(capability.Config{RunIdentity: "wasm-differential", Plan: plan})
		return wasmBroker, err
	}}).New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, err := json.Marshal(map[string]any{"run_id": "wasm-differential", "code": "result = {'value': math_tools.double(inputs['value'])}", "inputs": map[string]any{"value": 21}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, plan.PythonPrelude())
	if err != nil {
		t.Fatal(err)
	}
	var response guestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" || response.Result.(map[string]any)["value"] != float64(42) || len(response.Receipts) != 1 {
		t.Fatalf("wasm response=%+v", response)
	}
	if wasmBroker == nil || len(wasmBroker.Receipts()) != 1 {
		t.Fatal("WASM Broker receipt missing")
	}

	nativeBroker, err := capability.NewBroker(capability.Config{RunIdentity: "native-differential", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	registry := capabilityrpc.NewRegistry()
	expires := time.Now().Add(time.Minute)
	if err := registry.Open(capabilityrpc.ChannelConfig{ID: "diff-channel", Credential: "transport-only-test", InvocationID: "diff-invocation", ExecutionID: "native-differential", Transport: capabilityrpc.TransportUnixHTTP, ExpiresAt: expires, MaxRequestBytes: 1 << 20, Broker: nativeBroker}); err != nil {
		t.Fatal(err)
	}
	shortDir, err := os.MkdirTemp("/tmp", "pysolate-diff-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(shortDir)
	socket := filepath.Join(shortDir, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: capabilityrpc.HTTPHandler(registry)}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { registry.Revoke("diff-channel"); _ = server.Close(); _ = listener.Close(); <-done }()

	script := plan.PythonPrelude() + "\nimport json\nprint(json.dumps({'value': math_tools.double(21)}, sort_keys=True))\n"
	command := exec.Command("python3", "-c", script)
	command.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join("..", "..", "native", "python"), "PYSOLATE_RPC_SOCKET="+socket, "PYSOLATE_RPC_CHANNEL_ID=diff-channel", "PYSOLATE_RPC_INVOCATION_ID=diff-invocation", "PYSOLATE_RPC_EXECUTION_ID=native-differential", "PYSOLATE_RPC_PLAN_SHA256="+plan.Identity(), "PYSOLATE_RPC_CREDENTIAL=transport-only-test")
	nativeOutput, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var nativeResult map[string]any
	if err := json.Unmarshal(nativeOutput, &nativeResult); err != nil || nativeResult["value"] != float64(42) {
		t.Fatalf("native=%s err=%v", nativeOutput, err)
	}
	nativeReceipts := nativeBroker.Receipts()
	if len(nativeReceipts) != 1 {
		t.Fatalf("native receipts=%+v", nativeReceipts)
	}
	assertReceiptSemanticsEqual(t, wasmBroker.Receipts()[0], nativeReceipts[0])
}

func unifiedTransportPlan(t *testing.T) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"differential"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{Name: "math.double", Version: "v1", Description: "double", EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "differential.math.double.v1", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`), Python: &capability.PythonProjection{Module: "math_tools", Method: "double", Arguments: []string{"value"}, ResultField: "value"}}
	handler := capability.HandlerFunc(func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
		var value struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(input, &value); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]int{"value": value.Value * 2})
	})
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 4})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertReceiptSemanticsEqual(t *testing.T, wasm, native receipt.Receipt) {
	t.Helper()
	if wasm.CapabilityPlanSHA256 != native.CapabilityPlanSHA256 || wasm.Capability != native.Capability || wasm.OperationIndex != native.OperationIndex || wasm.RequestSHA256 != native.RequestSHA256 || wasm.ResponseSHA256 != native.ResponseSHA256 || wasm.Outcome != native.Outcome {
		t.Fatalf("receipt semantics differ: wasm=%+v native=%+v", wasm, native)
	}
}
