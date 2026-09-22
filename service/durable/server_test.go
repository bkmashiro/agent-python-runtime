package durableservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
)

type fakeRuntime struct {
	mu             sync.Mutex
	runs           map[string]durable.Run
	decisions      map[string]durable.Decision
	result         durable.Result
	history        durable.ReplayHistoryPage
	historyOptions durable.ReplayHistoryOptions
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

func (runtime *fakeRuntime) ReplayHistory(_ context.Context, id string, options durable.ReplayHistoryOptions) (durable.ReplayHistoryPage, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if _, ok := runtime.runs[id]; !ok {
		return durable.ReplayHistoryPage{}, durable.ErrNotFound
	}
	runtime.historyOptions = options
	return runtime.history, nil
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

func TestDurableHTTPHistoryIsPagedAndPrivacySafe(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.history = durable.ReplayHistoryPage{
		Calls: []durable.ReplayCall{{
			Sequence: 1, CallID: "secret-call-id", Capability: "catalog.lookup", Version: "v3",
			State: durable.CallCompleted, OperationKey: "secret-operation-key",
			OutcomeClass: durable.OutcomeError, Arguments: json.RawMessage(`{"token":"secret"}`), Outcome: json.RawMessage(`{"error":"secret"}`),
		}},
		NextSequence: 2, HasMore: true,
	}
	server, err := New(runtime, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	if _, err := runtime.Create(context.Background(), durable.Definition{ID: "history-run", Code: "result = 1", Seed: "seed", Inputs: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	status, body := durableRequest(t, httpServer.Client(), http.MethodGet, httpServer.URL+"/v1/durable/runs/history-run/history?from_sequence=1&limit=1", nil)
	if status != http.StatusOK {
		t.Fatalf("history status=%d body=%#v", status, body)
	}
	if body["has_more"] != true || body["next_sequence"] != float64(2) {
		t.Fatalf("history paging=%#v", body)
	}
	calls, ok := body["calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("history calls=%#v", body["calls"])
	}
	call, ok := calls[0].(map[string]any)
	if !ok || call["sequence"] != float64(1) || call["tool"] != "catalog.lookup" || call["version"] != "v3" || call["state"] != durable.CallCompleted || call["outcome_class"] != string(durable.OutcomeError) {
		t.Fatalf("history projection=%#v", call)
	}
	for _, forbidden := range []string{"call_id", "operation_key", "arguments", "outcome", "capability"} {
		if _, present := call[forbidden]; present {
			t.Fatalf("history leaked %q: %#v", forbidden, call)
		}
	}
	if runtime.historyOptions.FromSequence != 1 || runtime.historyOptions.Limit != 1 || runtime.historyOptions.IncludePayloads {
		t.Fatalf("history options=%+v", runtime.historyOptions)
	}

	status, _ = durableRequest(t, http.DefaultClient, http.MethodPost, httpServer.URL+"/v1/durable/runs/history-run/history", nil)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("history write status=%d", status)
	}
	status, _ = durableRequest(t, http.DefaultClient, http.MethodGet, httpServer.URL+"/v1/durable/runs/history-run/history?limit=bad", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid history query status=%d", status)
	}
	status, _ = durableRequest(t, http.DefaultClient, http.MethodGet, httpServer.URL+"/v1/durable/runs/unknown/history", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown history status=%d", status)
	}
}

func TestDurableHTTPEnforcesPreparedSeed(t *testing.T) {
	runtime := newFakeRuntime()
	server, err := New(runtime, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1}, Options{PreparationSeed: "prepared-seed"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	request := func(seed string) int {
		status, _ := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs", map[string]any{
			"id": "run-" + seed, "source": "result = 1", "seed": seed,
		})
		return status
	}
	if status := request("other-seed"); status != http.StatusBadRequest {
		t.Fatalf("mismatched seed status=%d", status)
	}
	if status := request("prepared-seed"); status != http.StatusCreated {
		t.Fatalf("prepared seed status=%d", status)
	}
}

func TestDurableHTTPAttemptTimeoutValidation(t *testing.T) {
	runtime := newFakeRuntime()
	server, err := New(runtime, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1}, Options{MaxRunDuration: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	status, _ := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs", map[string]any{
		"id": "timeout-run", "source": "result=1", "seed": "seed",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status=%d", status)
	}
	for _, body := range []map[string]any{
		{"timeout_ms": 0}, {"timeout_ms": 51}, {"timeout_ms": 1, "early_reads": true},
	} {
		status, response := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/timeout-run/attempts", body)
		if status != http.StatusBadRequest {
			t.Fatalf("body=%#v status=%d response=%#v", body, status, response)
		}
	}
}

func TestDurableHTTPRealGuestTimeoutReleasesSlot(t *testing.T) {
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	wasm, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	store, err := durable.Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	startup, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStartup()
	runner, err := durable.NewRunner(startup, store, wasm, "env-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	server, err := New(runner, "env-v1", durable.Limits{MaxRunning: 1, MaxResident: 1, MaxInflightTools: 1, MaxQueued: 1}, Options{MaxRunDuration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	create := func(id, source string) {
		t.Helper()
		status, body := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs", map[string]any{
			"id": id, "source": source, "seed": "seed",
		})
		if status != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%#v", id, status, body)
		}
	}
	// Cold Guest setup is included in the attempt budget. First prove a short
	// request fits this host's budget; the cancellation case still gets 10 ms.
	create("positive-control", "result=7")
	status, body := durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/positive-control/attempts", nil)
	if status != http.StatusOK {
		t.Fatalf("positive control failed: %d %#v", status, body)
	}
	create("timed", "while True:\n pass")
	status, body = durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/timed/attempts", map[string]any{"timeout_ms": 10})
	if status != http.StatusRequestTimeout {
		t.Fatalf("timed attempt status=%d body=%#v", status, body)
	}

	create("after-timeout", "result=7")
	status, body = durableRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/v1/durable/runs/after-timeout/attempts", nil)
	if status != http.StatusOK || body["state"] != string(durable.StateCompleted) || body["value"] != float64(7) {
		t.Fatalf("slot was not released status=%d body=%#v", status, body)
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
