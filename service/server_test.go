package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestHTTPWorkspaceLifecycleAndOverload(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("..", "dist", "pysolate.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	manifest := pysolate.Manifest{"wait": {Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
		started <- struct{}{}
		select {
		case <-release:
			return true, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	service, err := New(ctx, wasm, manifest, manager, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	httpServer := httptest.NewServer(service)
	defer httpServer.Close()
	client := httpServer.Client()

	status, health := requestJSON(t, client, http.MethodGet, httpServer.URL+"/healthz", nil)
	if status != http.StatusOK || health["ready"] != true {
		t.Fatalf("health status=%d body=%#v", status, health)
	}
	status, oversized := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{"source": string(bytes.Repeat([]byte("x"), maxHTTPBody)), "inputs": map[string]any{}})
	if status != http.StatusRequestEntityTooLarge || oversized["error"] != errRequestTooLarge.Error() {
		t.Fatalf("oversized status=%d body=%#v", status, oversized)
	}
	status, created := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces", map[string]any{
		"files": []map[string]any{{"path": "notes.txt", "data": []byte("before")}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%#v", status, created)
	}
	ref, _ := created["workspace"].(string)
	if ref == "" {
		t.Fatalf("missing workspace: %#v", created)
	}
	status, run := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces/"+ref+"/run", map[string]any{
		"source": "open('/workspace/notes.txt','a').write('-after')\nresult=42",
		"inputs": map[string]any{},
	})
	if status != http.StatusOK || run["value"] != float64(42) || run["run_ns"].(float64) <= 0 {
		t.Fatalf("run status=%d body=%#v", status, run)
	}
	changes := run["changes"].(map[string]any)
	if changes["modified"] != float64(1) {
		t.Fatalf("changes=%#v", changes)
	}
	status, fileBody := requestJSON(t, client, http.MethodGet, httpServer.URL+"/v1/workspaces/"+ref+"/files?path=notes.txt", nil)
	if status != http.StatusOK || fileBody["data"] != "YmVmb3JlLWFmdGVy" {
		t.Fatalf("file status=%d body=%#v", status, fileBody)
	}

	requestBody, err := json.Marshal(map[string]any{"source": "result=wait()", "inputs": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	type requestResult struct {
		status int
		err    error
	}
	firstDone := make(chan requestResult, 1)
	go func() {
		request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/v1/run", bytes.NewReader(requestBody))
		if err != nil {
			firstDone <- requestResult{err: err}
			return
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			firstDone <- requestResult{err: err}
			return
		}
		defer response.Body.Close()
		firstDone <- requestResult{status: response.StatusCode}
	}()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("blocking tool did not start")
	}
	status, overloaded := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{"source": "result=1", "inputs": map[string]any{}})
	if status != http.StatusTooManyRequests || overloaded["error"] != "execution capacity exhausted" {
		t.Fatalf("overload status=%d body=%#v", status, overloaded)
	}
	close(release)
	if result := <-firstDone; result.err != nil || result.status != http.StatusOK {
		t.Fatalf("first request status=%d err=%v", result.status, result.err)
	}

	status, destroyed := requestJSON(t, client, http.MethodDelete, httpServer.URL+"/v1/workspaces/"+ref, nil)
	if status != http.StatusOK || destroyed["destroyed"] != true {
		t.Fatalf("destroy status=%d body=%#v", status, destroyed)
	}
	status, _ = requestJSON(t, client, http.MethodGet, httpServer.URL+"/v1/workspaces/"+ref+"/snapshot", nil)
	if status != http.StatusNotFound {
		t.Fatalf("snapshot after destroy status=%d", status)
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, value any) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	if value != nil {
		if err := json.NewEncoder(&body).Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, decoded
}
