package hermesbridge

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "hbr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return runtimeDir
}

func TestListenUnixCreatesPrivateSocketAndRefusesExistingPath(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	socketPath := filepath.Join(runtimeDir, "bridge.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("unsafe socket mode: %v", info.Mode())
	}
	if _, err := ListenUnix(socketPath); err == nil {
		t.Fatal("existing socket path was replaced")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServerExecutesOneFramedRequestOverUnixSocket(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	socketPath := filepath.Join(runtimeDir, "bridge.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{run: func(ctx context.Context, payload []byte) ([]byte, error) { return guestResponse(t, ctx, `42`), nil }}
	service, err := NewService(runner, &fakeTrace{}, func() (string, error) { return "execution-1", nil }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	connection, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(validExecuteRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(connection, payload, MaxFrameBytes); err != nil {
		t.Fatal(err)
	}
	responsePayload, err := ReadFrame(connection, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	var response ExecuteResponse
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != ResponseStatusOK || string(response.Result) != "42" {
		t.Fatalf("unexpected response: %#v", response)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}
