package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Deliberately fail if the real artifact is missing: no native substitute or skip.
func TestRealGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	manifest := Manifest{"lookup": {Call: func(ctx context.Context, args json.RawMessage) (any, error) {
		calls++
		var a struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Key == "empty-error" {
			return nil, errors.New("")
		}
		if a.Key == "null" {
			return nil, nil
		}
		if a.Key == "price" {
			return 21, nil
		}
		return nil, errors.New("missing key: " + a.Key)
	}}}
	ctx := context.Background()
	r, err := New(ctx, wasm, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(ctx)
	manifest["lookup"] = ToolSpec{} // Runner owns a copy of the caller's manifest.
	cases := []struct {
		name, source, value, failure string
		calls                        int
	}{
		{"python", `import sys; result = {"answer": 6 * 7, "version": sys.version_info[:2]}`, `{"answer":42,"version":[3,14]}`, "", 0},
		{"host", `result = lookup(key="price") * inputs["quantity"]`, `42`, "", 1},
		{"null tool result", `result = lookup(key="null")`, `null`, "", 1},
		{"caught tool error", "try:\n    lookup(key='absent')\nexcept RuntimeError as e:\n    result = str(e)", `"missing key: absent"`, "", 1},
		{"uncaught tool error", `result = lookup(key="absent")`, "", "RuntimeError: missing key: absent", 1},
		{"empty tool error", `result = lookup(key="empty-error")`, "", "RuntimeError", 1},
		{"malformed tool request", `from _pysolate import call; import json; result = "error" in json.loads(call("{"))`, `true`, "", 0},
		{"unmanifested function", `result = delete(key="price")`, "", "NameError", 0},
		{"unknown tool request", `from _pysolate import call; import json; result = json.loads(call('{"tool":"delete","args":{}}'))["error"]`, `"unknown tool: delete"`, "", 0},
		{"python error", `result = 1 / 0`, "", "ZeroDivisionError", 0},
		{"syntax error", `result = (`, "", "SyntaxError", 0},
		{"non JSON result", `result = {1, 2}`, "", "TypeError", 0},
		{"private state write", `import builtins; builtins.secret = 123; result = 1`, `1`, "", 0},
		{"private state read", `import builtins; result = hasattr(builtins, "secret")`, `false`, "", 0},
		{"no host filesystem", "try:\n    open('/etc/passwd').read()\nexcept OSError:\n    result = 'denied'", `"denied"`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			out, err := r.Run(ctx, tc.source, map[string]any{"quantity": 2})
			if calls-before != tc.calls {
				t.Fatalf("tool calls = %d, want %d", calls-before, tc.calls)
			}
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("got %v; want %q", err, tc.failure)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal(out.Value, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.value), &want); err != nil {
					t.Fatal(err)
				}
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(want)
				if string(g) != string(w) {
					t.Fatalf("got %s; want %s", g, w)
				}
			}
		})
	}
	t.Run("stdout", func(t *testing.T) {
		out, err := r.Run(ctx, "print('hello'); result = 42", nil)
		if err != nil || out.Stdout != "hello\n" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := r.Run(deadline, "while True: pass", nil)
		if err == nil || deadline.Err() == nil {
			t.Fatalf("expected deadline, got %v", err)
		}
		out, err := r.Run(ctx, "result = 7", nil)
		if err != nil || string(out.Value) != "7" {
			t.Fatalf("run after cancellation: %+v, %v", out, err)
		}
	})
}

func TestManifestValidation(t *testing.T) {
	noop := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	for _, tc := range []struct {
		name     string
		manifest Manifest
	}{
		{"invalid identifier", Manifest{"not-valid": {Call: noop}}},
		{"Python keyword", Manifest{"class": {Call: noop}}},
		{"reserved input", Manifest{"inputs": {Call: noop}}},
		{"internal helper", Manifest{"_pysolate_prepare": {Call: noop}}},
		{"missing implementation", Manifest{"lookup": {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r, err := New(context.Background(), nil, tc.manifest); err == nil {
				r.Close(context.Background())
				t.Fatal("expected manifest validation error")
			}
		})
	}
}

func readGuestArtifact() ([]byte, error) {
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = "dist/pysolate.wasm"
	}
	return os.ReadFile(path)
}
