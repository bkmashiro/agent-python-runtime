package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type capturedCommand struct {
	command string
	args    []string
	stdin   []byte
	workdir string
	timeout time.Duration
}

func commandRunnerFromOutput(t *testing.T, output []byte, err error, capture *capturedCommand) codexCommandRunner {
	t.Helper()
	return func(_ context.Context, command string, args []string, stdin []byte, workdir string, timeout time.Duration) ([]byte, error) {
		if capture != nil {
			capture.command = command
			capture.args = append([]string(nil), args...)
			capture.stdin = append([]byte(nil), stdin...)
			capture.workdir = workdir
			capture.timeout = timeout
		}
		return output, err
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustEvent(t *testing.T, value any) string {
	t.Helper()
	return mustJSON(t, value)
}

func mustRawPayload(t *testing.T) json.RawMessage {
	t.Helper()
	raw := map[string]any{
		"model":             "gpt-test",
		"input":             "x",
		"max_output_tokens": 64,
		"stream":            false,
		"background":        false,
		"instructions":      "reply with bounded response",
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustAgentOutputEvent(t *testing.T, outputItems ...any) string {
	t.Helper()
	return mustEvent(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": mustJSON(t, map[string]any{"output": outputItems}),
		},
	})
}

func mustUsage(t *testing.T, usage map[string]any) string {
	t.Helper()
	return mustEvent(t, map[string]any{
		"type":  "turn.completed",
		"usage": usage,
	})
}

func TestCodexCLIAdapterParsesRealProbeShape(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	output := []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "019f000000000000"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		mustAgentOutputEvent(t, map[string]any{"type": "output_text", "text": "ok"}),
		mustUsage(t, map[string]any{
			"input_tokens":             float64(14445),
			"output_tokens":            float64(108),
			"cached_input_tokens":      float64(1024),
			"cache_write_input_tokens": float64(256),
			"reasoning_output_tokens":  float64(97),
		}),
	}, "\n"))
	capture := &capturedCommand{}
	adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, 2*time.Second, commandRunnerFromOutput(t, output, nil, capture))
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload})
	if err != nil {
		t.Fatalf("exchange err=%v", err)
	}
	if response.RequestID != "019f000000000000" || response.Usage == nil || response.Usage.InputTokens != 14445 || response.Usage.OutputTokens != 108 || response.Usage.TotalTokens != 14553 {
		t.Fatalf("response usage=%+v requestID=%s", response.Usage, response.RequestID)
	}
	var body struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil || body.Status != "completed" || len(body.Output) != 1 {
		t.Fatalf("response body=%s err=%v", string(response.Body), err)
	}
	if len(capture.args) == 0 {
		t.Fatalf("expected args captured")
	}
}

func TestCodexCLIAdapterParsesParallelFunctionCalls(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	output := []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-123"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		mustAgentOutputEvent(t,
			map[string]any{"type": "function_call", "name": "run_python", "arguments": map[string]any{"code": "x=1"}},
			map[string]any{"type": "function_call", "name": "notify_user", "arguments": map[string]any{"message": "done"}},
		),
		mustUsage(t, map[string]any{"input_tokens": 10, "output_tokens": 4}),
	}, "\n"))
	adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, output, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload})
	if err != nil {
		t.Fatalf("exchange err=%v", err)
	}
	var body struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("response body=%s err=%v", string(response.Body), err)
	}
	if len(body.Output) != 2 {
		t.Fatalf("unexpected output count=%d", len(body.Output))
	}
	for index, raw := range body.Output {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("item=%s err=%v", string(raw), err)
		}
		var arguments string
		if err := json.Unmarshal(item["arguments"], &arguments); err != nil || !json.Valid([]byte(arguments)) {
			t.Fatalf("arguments must be a JSON string: %s err=%v", item["arguments"], err)
		}
		switch index {
		case 0:
			if string(item["call_id"]) != `"call_1"` {
				t.Fatalf("first call id=%s", string(item["call_id"]))
			}
		case 1:
			if string(item["call_id"]) != `"call_2"` {
				t.Fatalf("second call id=%s", string(item["call_id"]))
			}
		}
	}
}

func TestCodexCLIAdapterRejectsCommandExecutionActivity(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	nonAgentLine := mustEvent(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "command_execution",
			"text": mustJSON(t, map[string]any{"output": []any{map[string]any{"type": "output_text", "text": "bad"}}}),
		},
	})
	output := []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-123"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		nonAgentLine,
		mustUsage(t, map[string]any{"input_tokens": 10, "output_tokens": 4}),
	}, "\n"))
	adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, output, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload}); err == nil {
		t.Fatalf("expected forbidden activity error")
	}
}

func TestCodexCLIAdapterPassesPromptViaStdin(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	capture := &capturedCommand{}
	output := []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-123"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		mustAgentOutputEvent(t, map[string]any{"type": "output_text", "text": "ok"}),
		mustUsage(t, map[string]any{"input_tokens": 1, "output_tokens": 1}),
	}, "\n"))
	adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, output, nil, capture))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload}); err != nil {
		t.Fatalf("exchange err=%v", err)
	}
	if capture.args[len(capture.args)-3] != "exec" || capture.args[len(capture.args)-2] != "--json" || capture.args[len(capture.args)-1] != "-" {
		t.Fatalf("command args=%v", capture.args)
	}
	if strings.Contains(strings.Join(capture.args, " "), string(payload)) {
		t.Fatalf("payload leaked into argv=%v", capture.args)
	}
	if !bytes.Contains(capture.stdin, payload) {
		t.Fatalf("payload not present in stdin")
	}
	if !strings.Contains(string(capture.stdin), "Never execute tools") {
		t.Fatalf("prompt guidance missing")
	}
}

func TestCodexCLIAdapterRejectsMissingAndDuplicateEvents(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	threadLine := mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-123"})
	turnLine := mustEvent(t, map[string]any{"type": "turn.started"})
	agentLine := mustAgentOutputEvent(t, map[string]any{"type": "output_text", "text": "ok"})
	completedLine := mustUsage(t, map[string]any{"input_tokens": 1, "output_tokens": 1})

	testCases := map[string]struct {
		lines []string
	}{
		"missing item":             {[]string{threadLine, turnLine, completedLine}},
		"missing turn.started":     {[]string{threadLine, agentLine, completedLine}},
		"duplicate item.completed": {[]string{threadLine, turnLine, agentLine, agentLine, completedLine}},
		"duplicate turn.completed": {[]string{threadLine, turnLine, agentLine, completedLine, completedLine}},
		"unknown interleaved event": {[]string{
			threadLine,
			turnLine,
			agentLine,
			mustEvent(t, map[string]any{"type": "item.updated", "item": map[string]any{"id": "item_1", "type": "agent_message", "text": "x"}}),
			completedLine,
		}},
		"blank line": {[]string{threadLine, turnLine, agentLine, "", completedLine}},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			raw := []byte(strings.Join(tc.lines, "\n"))
			adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, raw, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload}); err == nil {
				t.Fatalf("expected malformed event sequence: %s", name)
			}
		})
	}
}

func TestCodexCLIAdapterRejectsDuplicateAndUnknownOutputKeys(t *testing.T) {
	payload := mustRawPayload(t)
	workdir := t.TempDir()
	badText := `{"output":[{"type":"output_text","text":"a","text":"b"}]}`
	badLine := mustEvent(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": badText,
		},
	})
	output := []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-123"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		badLine,
		mustUsage(t, map[string]any{"input_tokens": 1, "output_tokens": 1}),
	}, "\n"))
	adapter, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, output, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload}); err == nil {
		t.Fatalf("expected duplicate-key failure")
	}
	unknownLine := mustEvent(t, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"id":   "item_0",
			"type": "agent_message",
			"text": mustJSON(t, map[string]any{"output": []any{map[string]any{"type": "function_call", "name": "bad-name", "arguments": map[string]any{}, "extra": "nope"}}}),
		},
	})
	output = []byte(strings.Join([]string{
		mustEvent(t, map[string]any{"type": "thread.started", "thread_id": "thread-124"}),
		mustEvent(t, map[string]any{"type": "turn.started"}),
		unknownLine,
		mustUsage(t, map[string]any{"input_tokens": 1, "output_tokens": 1}),
	}, "\n"))
	if _, err := adapter.Exchange(context.Background(), Request{Model: "gpt-test", Payload: payload}); err == nil {
		t.Fatalf("expected extra-key failure")
	}
}

func TestRunCodexCLICommandBoundsStreamsAndTimeout(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("sh unavailable: %v", err)
	}
	workdir := t.TempDir()
	t.Run("stdout overflow", func(t *testing.T) {
		args := []string{"-c", fmt.Sprintf("head -c %d </dev/zero", maxExchangeBytes+1)}
		if _, err := runCodexCLICommand(context.Background(), sh, args, nil, workdir, 2*time.Second); err == nil {
			t.Fatalf("expected stdout overflow error")
		}
	})
	t.Run("stderr overflow", func(t *testing.T) {
		args := []string{"-c", fmt.Sprintf("head -c %d </dev/zero 1>&2", maxExchangeBytes+1)}
		if _, err := runCodexCLICommand(context.Background(), sh, args, nil, workdir, 2*time.Second); err == nil {
			t.Fatalf("expected stderr overflow error")
		}
	})
	t.Run("context timeout", func(t *testing.T) {
		args := []string{"-c", "sleep 0.2"}
		_, err := runCodexCLICommand(context.Background(), sh, args, nil, workdir, 5*time.Millisecond)
		if err == nil {
			t.Fatalf("expected timeout error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected timeout error: %v", err)
		}
	})
}

func TestCodexCLIAdapterValidatesConstructorInputs(t *testing.T) {
	workdir := t.TempDir()
	if _, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, 0, commandRunnerFromOutput(t, []byte{}, nil, nil)); err == nil {
		t.Fatalf("expected non-positive timeout error")
	}
	relPath, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Base(relPath)
	if _, err := NewCodexCLIAdapterWithRunner("", "gpt-test", nested, time.Second, commandRunnerFromOutput(t, []byte{}, nil, nil)); err == nil {
		t.Fatalf("expected relative workdir error")
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir); err != nil {
		t.Fatal(err)
	}
	defer os.Setenv("PATH", oldPath)
	if _, err := NewCodexCLIAdapterWithRunner("", "gpt-test", workdir, time.Second, commandRunnerFromOutput(t, []byte{}, nil, nil)); err != nil {
		t.Fatalf("expected LookPath resolution success with fake codex")
	}
}
