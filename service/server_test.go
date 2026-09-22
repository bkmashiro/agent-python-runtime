package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestHTTPWorkspaceLifecycleAndOverload(t *testing.T) {
	t.Run("default", func(t *testing.T) { testHTTPWorkspace(t, false) })
	if runtime.GOOS == "linux" {
		t.Run("cow-data-image", func(t *testing.T) { testHTTPWorkspace(t, true) })
	}
}

func testHTTPWorkspace(t *testing.T, dataImage bool) {
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "dist", "pysolate.wasm")
	}
	wasm, err := os.ReadFile(guest)
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
	var options []Options
	if dataImage {
		options = []Options{{COWDataImage: true}}
	}
	service, err := New(ctx, wasm, manifest, manager, 1, options...)
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

func TestServiceRejectsMultipleOptions(t *testing.T) {
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = New(context.Background(), nil, nil, manager, 1, Options{}, Options{})
	if err == nil || err.Error() != "service accepts at most one options value" {
		t.Fatalf("options error=%v", err)
	}
}

func TestServiceRejectsInvalidCOWDataImageArtifact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("COW data-image is Linux-only")
	}
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = New(context.Background(), []byte("not wasm"), nil, manager, 1, Options{COWDataImage: true})
	if err == nil || !strings.Contains(err.Error(), "COW data image") {
		t.Fatalf("invalid artifact error=%v", err)
	}
}

func TestHTTPPlainRunEarlyReadsRequiresRequestOptIn(t *testing.T) {
	manifest := pysolate.Manifest{"lookup": {
		AllowEarlyRead: true,
		Call:           func(context.Context, json.RawMessage) (any, error) { return 21, nil },
	}}
	_, httpServer := newGuestHTTPService(t, manifest, 1, Options{})
	client := httpServer.Client()

	status, ordinary := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{
		"source": `result=lookup(key="book")`, "inputs": map[string]any{},
	})
	if status != http.StatusOK || ordinary["value"] != float64(21) {
		t.Fatalf("ordinary status=%d body=%#v", status, ordinary)
	}
	if _, present := ordinary["transformed"]; present {
		t.Fatalf("ordinary request unexpectedly used early reads: %#v", ordinary)
	}

	status, early := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{
		"source": `result=lookup(key="book")`, "inputs": map[string]any{}, "early_reads": true,
	})
	if status != http.StatusOK || early["value"] != float64(21) || early["transformed"] == "" {
		t.Fatalf("early-read status=%d body=%#v", status, early)
	}

	_, deniedServer := newGuestHTTPService(t, pysolate.Manifest{"lookup": {
		Call: func(context.Context, json.RawMessage) (any, error) { return 21, nil },
	}}, 1, Options{})
	status, denied := requestJSON(t, deniedServer.Client(), http.MethodPost, deniedServer.URL+"/v1/run", map[string]any{
		"source": `result=lookup(key="book")`, "early_reads": true,
	})
	if status != http.StatusOK || denied["value"] != float64(21) {
		t.Fatalf("request early_reads bypassed Host permission status=%d body=%#v", status, denied)
	}
	if _, transformed := denied["transformed"]; transformed {
		t.Fatalf("Host-denied early read unexpectedly transformed source: %#v", denied)
	}
}

func TestHTTPRunTimeoutReleasesSlotAndWorkspaceLease(t *testing.T) {
	_, httpServer := newGuestHTTPService(t, pysolate.Manifest{}, 1, Options{MaxRunDuration: 2 * time.Second})
	client := httpServer.Client()
	client.Timeout = 5 * time.Second
	// Prove the short request fits the cap even under race instrumentation.
	status, body := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{"source": "result=7"})
	if status != http.StatusOK {
		t.Fatalf("positive control: %d %#v", status, body)
	}
	infinite := `while True:
 pass`

	status, body = requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{
		"source": infinite,
	})
	if status != http.StatusRequestTimeout || !strings.Contains(body["error"].(string), "deadline") {
		t.Fatalf("timed ordinary run status=%d body=%#v", status, body)
	}
	status, body = requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{"source": "result=7"})
	if status != http.StatusOK || body["value"] != float64(7) {
		t.Fatalf("slot was not released status=%d body=%#v", status, body)
	}

	status, created := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces", map[string]any{
		"files": []map[string]any{{"path": "value.txt", "data": []byte("before")}},
	})
	if status != http.StatusCreated {
		t.Fatalf("workspace create status=%d body=%#v", status, created)
	}
	ref := created["workspace"].(string)
	status, body = requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces/"+ref+"/run", map[string]any{
		"source": infinite, "timeout_ms": 10,
	})
	if status != http.StatusRequestTimeout {
		t.Fatalf("timed workspace run status=%d body=%#v", status, body)
	}
	status, body = requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces/"+ref+"/run", map[string]any{
		"source": `result=open('/workspace/value.txt').read()`,
	})
	if status != http.StatusOK || body["value"] != "before" {
		t.Fatalf("workspace lease was not released status=%d body=%#v", status, body)
	}
	status, body = requestJSON(t, client, http.MethodDelete, httpServer.URL+"/v1/workspaces/"+ref, nil)
	if status != http.StatusOK || body["destroyed"] != true {
		t.Fatalf("workspace destroy status=%d body=%#v", status, body)
	}
}

func TestHTTPRunTimeoutValidationAndWorkspaceEarlyReadRejection(t *testing.T) {
	_, httpServer := newGuestHTTPService(t, pysolate.Manifest{}, 1, Options{MaxRunDuration: time.Second})
	client := httpServer.Client()
	for _, timeout := range []any{0, 1001} {
		status, body := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/run", map[string]any{
			"source": "result=1", "timeout_ms": timeout,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("timeout=%v status=%d body=%#v", timeout, status, body)
		}
	}
	status, created := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("workspace create status=%d body=%#v", status, created)
	}
	ref := created["workspace"].(string)
	status, body := requestJSON(t, client, http.MethodPost, httpServer.URL+"/v1/workspaces/"+ref+"/run", map[string]any{
		"source": "result=1", "early_reads": true,
	})
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "unsupported") {
		t.Fatalf("workspace early-read status=%d body=%#v", status, body)
	}
}

func newGuestHTTPService(t *testing.T, manifest pysolate.Manifest, maxActive int, options Options) (*Server, *httptest.Server) {
	t.Helper()
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "dist", "pysolate.wasm")
	}
	wasm, err := os.ReadFile(guest)
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	service, err := New(ctx, wasm, manifest, manager, maxActive, options)
	if err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(service)
	t.Cleanup(func() {
		httpServer.Close()
		if err := service.Close(context.Background()); err != nil {
			t.Errorf("close service: %v", err)
		}
	})
	return service, httpServer
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
