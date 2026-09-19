package pysolate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

type journalFunc func(context.Context, string, json.RawMessage, func(context.Context) []byte) ([]byte, error)

func (f journalFunc) Call(ctx context.Context, name string, args json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
	return f(ctx, name, args, next)
}

func TestRecordedControlCannotBeCaught(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner, err := New(context.Background(), wasm, Manifest{"action": {Call: func(context.Context, json.RawMessage) (any, error) { calls++; return 1, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	parked := errors.New("park")
	journalCalls := 0
	journal := journalFunc(func(context.Context, string, json.RawMessage, func(context.Context) []byte) ([]byte, error) {
		journalCalls++
		return nil, parked
	})
	out, err := runner.RunRecorded(context.Background(), "try:\n    action()\nexcept BaseException:\n    action()\nresult = 99", nil, "seed", journal)
	if !errors.Is(err, parked) || journalCalls != 1 || calls != 0 || out.Value != nil {
		t.Fatalf("output=%+v err=%v journal=%d tools=%d", out, err, journalCalls, calls)
	}
	// A Guest exit code alone cannot forge the Host's stop reason.
	_, err = runner.RunRecorded(context.Background(), "import os; os._exit(125)", nil, "seed", journal)
	if err == nil || errors.Is(err, parked) || journalCalls != 1 {
		t.Fatalf("forged park: %v", err)
	}
}

func TestRecordedToolOutcomeAndPythonFailure(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	live := 0
	runner, err := New(context.Background(), wasm, Manifest{"action": {Call: func(context.Context, json.RawMessage) (any, error) { live++; return nil, errors.New("business error") }}})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	var saved []byte
	journal := journalFunc(func(ctx context.Context, _ string, _ json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
		if saved == nil {
			saved = append([]byte(nil), next(ctx)...)
		}
		return saved, nil
	})
	for i := 0; i < 2; i++ {
		_, err := runner.RunRecorded(context.Background(), "result = action()", nil, "seed", journal)
		var python *PythonError
		if !errors.As(err, &python) || python.Message != "RuntimeError: business error" {
			t.Fatalf("wrong failure: %v", err)
		}
	}
	if live != 1 {
		t.Fatalf("replay dispatched %d times", live)
	}
}

func TestRecordedDeterminismAcrossProcesses(t *testing.T) {
	run := func(seed string) []byte {
		command := exec.Command(os.Args[0], "-test.run=^TestRecordedProcessHelper$")
		command.Env = append(os.Environ(), "PYSOLATE_RECORD_CHILD="+seed)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, stderr.String())
		}
		if !json.Valid(bytes.TrimSpace(output)) {
			t.Fatalf("invalid child output: %s", output)
		}
		return output
	}
	first, second := run("same-seed"), run("same-seed")
	if !bytes.Equal(first, second) {
		t.Fatalf("different executions:\n%s\n%s", first, second)
	}
	if bytes.Equal(first, run("other-seed")) {
		t.Fatal("seed did not change randomness")
	}
}

func TestRecordedProcessHelper(t *testing.T) {
	seed := os.Getenv("PYSOLATE_RECORD_CHILD")
	if seed == "" {
		return
	}
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := New(context.Background(), wasm, nil)
	if err != nil {
		t.Fatal(err)
	}
	journal := journalFunc(func(ctx context.Context, _ string, _ json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
		return next(ctx), nil
	})
	out, err := runner.RunRecorded(context.Background(), `import random, os, time
import numpy as np
result = {
    "python": random.Random().getrandbits(64),
    "bytes": os.urandom(16).hex(),
    "wall": time.time(),
    "monotonic": time.monotonic(),
    "set": list({"alpha", "beta", "gamma", "delta"}),
    "numpy": np.random.default_rng().integers(0, 1000000, size=8).tolist(),
    "matrix": (np.array([[1,2],[3,4]]) @ np.array([[2,0],[0,2]])).tolist(),
}`, nil, seed, journal)
	closeErr := runner.Close(context.Background())
	if err != nil || closeErr != nil {
		t.Fatalf("run=%v close=%v", err, closeErr)
	}
	fmt.Println(string(out.Value))
	os.Exit(0)
}
