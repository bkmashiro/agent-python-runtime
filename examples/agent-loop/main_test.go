package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func toolAnswer(id, source string) Message {
	return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: ToolFunction{
		Name: "execute_python", Arguments: `{"source":` + quoteJSON(source) + `}`,
	}}}}
}

func quoteJSON(value string) string {
	encoded, _ := jsonMarshal(value)
	return encoded
}

// Kept as a tiny indirection so test helpers do not duplicate error handling.
func jsonMarshal(value string) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func TestValidateExecuteArgumentsRejectsWrongFunctionShapes(t *testing.T) {
	cases := []string{
		``,
		`[]`,
		`{"source":"print(1)"} trailing`,
		`{"source":"print(1)","extra":true}`,
		`{"source":1}`,
		`{"source":"import subprocess"}`,
	}
	for _, arguments := range cases {
		if _, err := validateExecuteArguments(arguments); err == nil {
			t.Errorf("validateExecuteArguments(%q) succeeded", arguments)
		}
	}
}

func TestRunAgentStopsAtMaxTurns(t *testing.T) {
	guest := testGuest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"not a tool call"}}]}`))
	}))
	defer server.Close()
	provider := &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, Model: "test"}
	_, err := RunAgent(context.Background(), AgentConfig{
		GuestPath: guest, OutputPath: filepath.Join(t.TempDir(), "report.md"), Model: provider,
		MaxTurns: 2, Deadline: time.Minute, ExecutionTimeout: time.Second,
		MaxResponseBytes: 4096, MaxContextBytes: 1 << 20, MaxOutputBytes: 4096,
	})
	if err == nil || !strings.Contains(err.Error(), "within 2 turns") {
		t.Fatalf("expected max-turn error, got %v", err)
	}
}

func TestRunAgentHonorsCanceledContext(t *testing.T) {
	guest := testGuest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunAgent(ctx, AgentConfig{
		GuestPath: guest, OutputPath: filepath.Join(t.TempDir(), "report.md"), Model: &OpenAIProvider{Client: http.DefaultClient, BaseURL: "http://unused", Model: "test"},
		MaxTurns: 2, Deadline: time.Minute, ExecutionTimeout: time.Second,
		MaxResponseBytes: 4096, MaxContextBytes: 1 << 20, MaxOutputBytes: 4096,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestOpenAIProviderReturnsControlledProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "secret provider response body", http.StatusBadGateway)
	}))
	defer server.Close()
	provider := &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, Model: "test", APIKey: "not-printed"}
	_, err := provider.Complete(context.Background(), []Message{{Role: "user", Content: "test"}}, 1024)
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected provider error: %v", err)
	}
}

func TestExportReportDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := exportReport(path, []byte("new"), AgentResult{Turns: 1})
	if err == nil {
		t.Fatal("expected no-overwrite error")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "existing" {
		t.Fatalf("existing report changed: %q (%v)", data, readErr)
	}
}

func TestAgentLoopRepairsWithFreshGuestAndExportsReport(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "host-only-credential")
	guest := testGuest(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected fake provider request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Messages []Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode fake provider request: %v", err)
		}
		var answer Message
		switch requests.Add(1) {
		case 1:
			answer = toolAnswer("bad", `attempt_only = "not shared"
raise RuntimeError("repair me")`)
		case 2:
			answer = toolAnswer("good", `from pathlib import Path
import csv, os
assert "OPENAI_API_KEY" not in os.environ

try:
    attempt_only
    raise RuntimeError("Python variables leaked between Guests")
except NameError:
    pass

rows = list(csv.DictReader(Path("sales.csv").open()))
counts = {}
total = 0
for row in rows:
    category = catalog.category_for_sku(sku=row["sku"])["category"]
    counts[category] = counts.get(category, 0) + 1
    total += int(row["amount"])
report = "# Sales analysis\\n\\nRows: %d\\nTotal: %d\\nCategories: %s\\n" % (len(rows), total, ", ".join("%s=%d" % item for item in sorted(counts.items())))
Path("REPORT.md").write_text(report)`)
		case 3:
			answer = Message{Role: "assistant", Content: "Done."}
		default:
			t.Fatal("fake provider received too many requests")
		}
		response := struct {
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
		}{Choices: []struct {
			Message Message `json:"message"`
		}{{Message: answer}}}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	model := &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, APIKey: "test-key", Model: "test-model"}
	output := filepath.Join(t.TempDir(), "report.md")
	result, err := RunAgent(context.Background(), AgentConfig{
		GuestPath: guest, OutputPath: output, Model: model, MaxTurns: 4,
		ExecutionTimeout: 10 * time.Second, MaxResponseBytes: 64 << 10,
		MaxContextBytes: 1 << 20, MaxOutputBytes: 64 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionAttempts != 2 || result.Turns != 3 || result.HostToolCalls != 3 {
		t.Fatalf("unexpected run counts: %+v", result)
	}
	if requests.Load() != 3 {
		t.Fatalf("fake provider request count = %d, want 3", requests.Load())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"Rows: 3", "Total: 35", "gadgets=1", "widgets=2"} {
		if !strings.Contains(text, expected) {
			t.Errorf("report %q lacks %q", text, expected)
		}
	}
	if strings.Contains(text, "not shared") {
		t.Fatal("report contains state from the failed Guest")
	}
}

func testGuest(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Pysolate Guest is not built: %s", path)
	}
	return path
}
