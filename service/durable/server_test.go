package durableservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
)

type fakeRuntime struct {
	mu        sync.Mutex
	runs      map[string]durable.Run
	decisions map[string]durable.Decision
	result    durable.Result
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{runs: make(map[string]durable.Run), decisions: make(map[string]durable.Decision), result: durable.Result{State: durable.StateCompleted, Output: pysolate.Output{Value: json.RawMessage(`42`)}}}
}

func (runtime *fakeRuntime) ArtifactID() string { return "sha256:guest" }
func (runtime *fakeRuntime) Create(_ context.Context, definition durable.Definition) (durable.Run, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if _, exists := runtime.runs[definition.ID]; exists {
		return durable.Run{}, durable.ErrConflict
	}
	run := durable.Run{Definition: definition, Status: durable.StatusActive}
	runtime.runs[definition.ID] = run
	return run, nil
}
func (runtime *fakeRuntime) Get(_ context.Context, id string) (durable.Run, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	run, ok := runtime.runs[id]
	if !ok {
		return durable.Run{}, durable.ErrNotFound
	}
	return run, nil
}
func (runtime *fakeRuntime) Advance(_ context.Context, id string) (durable.Result, error) {
	if _, err := runtime.Get(context.Background(), id); err != nil {
		return durable.Result{}, err
	}
	return runtime.result, nil
}
func (runtime *fakeRuntime) Cancel(_ context.Context, id string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	run, ok := runtime.runs[id]
	if !ok {
		return durable.ErrNotFound
	}
	run.Status = durable.StatusCancelled
	runtime.runs[id] = run
	return nil
}
func (runtime *fakeRuntime) Decide(_ context.Context, waitID string, decision durable.Decision) error {
	if waitID == "" {
		return durable.ErrNotFound
	}
	runtime.mu.Lock()
	runtime.decisions[waitID] = decision
	runtime.mu.Unlock()
	return nil
}

func TestDurableHTTPRunLifecycle(t *testing.T) {
	runtime := newFakeRuntime()
	server, err := New(runtime, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	status, health := durableRequest(t, httpServer.Client(), http.MethodGet, httpServer.URL+"/healthz", nil)
	if status != http.StatusOK || health["ready"] != true {
		t.Fatalf("health status=%d body=%#v", status, health)
	}
	status, created := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs", map[string]any{
		"id": "run-1", "source": "result = inputs['value']", "inputs": map[string]any{"value": 42}, "seed": "seed-1",
	})
	if status != http.StatusCreated || created["id"] != "run-1" || created["status"] != durable.StatusActive {
		t.Fatalf("create status=%d body=%#v", status, created)
	}
	run := runtime.runs["run-1"]
	if run.Definition.ArtifactSHA256 != "sha256:guest" || run.Definition.EnvironmentVersion != "env-v1" || string(run.Definition.Inputs) != `{"value":42}` {
		t.Fatalf("definition=%+v", run.Definition)
	}

	status, attempt := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/run-1/attempts", map[string]any{})
	if status != http.StatusOK || attempt["state"] != string(durable.StateCompleted) || attempt["value"] != float64(42) {
		t.Fatalf("attempt status=%d body=%#v", status, attempt)
	}
	status, got := durableRequest(t, httpServer.Client(), http.MethodGet, httpServer.URL+"/v1/durable/runs/run-1", nil)
	if status != http.StatusOK || got["id"] != "run-1" {
		t.Fatalf("get status=%d body=%#v", status, got)
	}

	status, decided := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/waits/resolve", map[string]any{
		"wait_id": "run-1/wait/0", "result": map[string]any{"approved": true},
	})
	if status != http.StatusOK || decided["resolved"] != true {
		t.Fatalf("decide status=%d body=%#v", status, decided)
	}
	if string(runtime.decisions["run-1/wait/0"].Result) != `{"approved":true}` {
		t.Fatalf("decision=%s", runtime.decisions["run-1/wait/0"].Result)
	}

	status, cancelled := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/run-1/cancel", map[string]any{})
	if status != http.StatusOK || cancelled["cancelled"] != true {
		t.Fatalf("cancel status=%d body=%#v", status, cancelled)
	}
}

func TestDurableHTTPRejectsInvalidRequests(t *testing.T) {
	server, err := New(newFakeRuntime(), "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	status, _ := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs", map[string]any{"id": "missing-fields"})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid create status=%d", status)
	}
	status, _ = durableRequest(t, httpServer.Client(), http.MethodGet, httpServer.URL+"/v1/durable/runs/missing", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing get status=%d", status)
	}
	status, _ = durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/waits/resolve", map[string]any{
		"wait_id": "wait-1", "result": map[string]any{"ok": true}, "error": "ambiguous",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("ambiguous decision status=%d", status)
	}
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/v1/durable/runs", bytes.NewReader(bytes.Repeat([]byte("x"), maxHTTPBody+1)))
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d", response.StatusCode)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	_, err := New(nil, "env-v1", durable.Limits{MaxRunning: 1})
	if err == nil {
		t.Fatal("expected nil runtime rejection")
	}
	_, err = New(newFakeRuntime(), "", durable.Limits{MaxRunning: 1})
	if err == nil {
		t.Fatal("expected empty environment rejection")
	}
}

func durableRequest(t *testing.T, client *http.Client, method, url string, value any) (int, map[string]any) {
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
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	return response.StatusCode, decoded
}
