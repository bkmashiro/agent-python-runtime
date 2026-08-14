package capabilityrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestUnixHTTPNativePythonUsesGeneratedProjection(t *testing.T) {
	broker, plan := testBroker(t, capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(map[string]int{"value": input.Value * 2})
		return encoded, nil
	}))
	registry := NewRegistry()
	if err := registry.Open(ChannelConfig{
		ID: "channel-1", Credential: "secret-credential", InvocationID: "invocation-1", ExecutionID: "execution-1",
		Transport: TransportUnixHTTP, ExpiresAt: time.Now().Add(time.Minute), MaxRequestBytes: 64 << 10, Broker: broker,
	}); err != nil {
		t.Fatal(err)
	}

	shortTemp, err := os.MkdirTemp("/tmp", "pysolate-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortTemp) })
	socketPath := filepath.Join(shortTemp, "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: HTTPHandler(registry), ReadHeaderTimeout: time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()); <-serveDone })

	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := plan.PythonPrelude() + "\nimport json\nprint(json.dumps({'result': math_tools.double(21)}, separators=(',', ':')))\n"
	command := exec.Command("python3", "-c", script)
	command.Env = append(os.Environ(),
		"PYTHONPATH="+filepath.Join(repositoryRoot, "native", "python"),
		"PYSOLATE_RPC_SOCKET="+socketPath,
		"PYSOLATE_RPC_CHANNEL_ID=channel-1",
		"PYSOLATE_RPC_CREDENTIAL=secret-credential",
		"PYSOLATE_RPC_INVOCATION_ID=invocation-1",
		"PYSOLATE_RPC_EXECUTION_ID=execution-1",
		"PYSOLATE_RPC_PLAN_SHA256="+plan.Identity(),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native Python failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != `{"result":42}` {
		t.Fatalf("output=%q", output)
	}
	if broker.CallCount() != 1 || len(broker.SnapshotReceipts()) != 1 {
		t.Fatalf("calls=%d receipts=%d", broker.CallCount(), len(broker.SnapshotReceipts()))
	}
}
