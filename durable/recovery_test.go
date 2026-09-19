package durable_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
)

func realGuest(t *testing.T) []byte {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = "../dist/pysolate.wasm"
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func TestRealApprovalReopenAndFinalReplay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.db")
	wasm := realGuest(t)
	reads, writes, value := 0, 0, 7
	tools := []durable.Tool{
		{Name: "read", Version: "v1", Recovery: durable.RetrySafe, Call: func(context.Context, json.RawMessage) (any, error) { reads++; return value, nil }},
		{Name: "approve", Version: "v1", Recovery: durable.WaitMode, Wait: func(_ context.Context, args json.RawMessage) (durable.WaitSpec, error) {
			return durable.WaitSpec{Kind: "approval", Request: args}, nil
		}},
		{Name: "write", Version: "v1", Recovery: durable.Idempotent, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
			writes++
			key, ok := durable.OperationKey(ctx)
			if !ok || key != "approval/2" {
				return nil, errors.New("wrong operation key")
			}
			return json.RawMessage(args), nil
		}},
	}
	store, err := durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := durable.NewRunner(ctx, store, wasm, "test-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	definition := durable.Definition{ID: "approval", Code: "x = read()\nif approve(value=x):\n    result = write(value=x)\n", Inputs: json.RawMessage(`{}`), Seed: "seed", ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "test-v1"}
	if _, err = runner.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	_, err = runner.Resume(ctx, "approval")
	var parked *durable.ParkError
	if !errors.As(err, &parked) || reads != 1 || writes != 0 {
		t.Fatalf("park=%v reads=%d writes=%d", err, reads, writes)
	}
	if err = runner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	value = 999
	store, err = durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err = durable.NewRunner(ctx, store, wasm, "test-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	if err = runner.Decide(ctx, parked.WaitID, durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	out, err := runner.Resume(ctx, "approval")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]int
	if err = json.Unmarshal(out.Value, &result); err != nil || result["value"] != 7 || reads != 1 || writes != 1 {
		t.Fatalf("out=%s error=%v reads=%d writes=%d", out.Value, err, reads, writes)
	}
	repeated, err := runner.Resume(ctx, "approval")
	var replayed map[string]int
	decodeErr := json.Unmarshal(repeated.Value, &replayed)
	if err != nil || decodeErr != nil || replayed["value"] != result["value"] || reads != 1 || writes != 1 {
		t.Fatalf("repeat=%+v error=%v", repeated, err)
	}
	failed := definition
	failed.ID = "python-error"
	failed.Code = "raise ValueError('saved failure')"
	if _, err = runner.Create(ctx, failed); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err = runner.Resume(ctx, failed.ID)
		var py *pysolate.PythonError
		if !errors.As(err, &py) {
			t.Fatalf("error replay %d: %v", i, err)
		}
	}
}
