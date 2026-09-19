package durable_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func TestPreparedResumePreservesHistoryAndSeed(t *testing.T) {
	ctx := context.Background()
	wasm := realGuest(t)
	store, err := durable.Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reads := 0
	tools := []durable.Tool{
		{Name: "read", Version: "v1", Recovery: durable.RetrySafe, Call: func(_ context.Context, args json.RawMessage) (any, error) { reads++; return args, nil }},
		{Name: "wait", Version: "v1", Recovery: durable.WaitMode, Wait: func(context.Context, json.RawMessage) (durable.WaitSpec, error) {
			return durable.WaitSpec{Kind: "approval", Request: json.RawMessage(`{}`)}, nil
		}},
	}
	fresh, err := durable.NewRunner(ctx, store, wasm, "prepared-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Create(ctx, durable.Definition{ID: "shared", Code: "import random, time\nx=read(value=random.Random().getrandbits(64), now=time.monotonic())\nwait()\nresult=x", Inputs: json.RawMessage(`{}`), Seed: "seed", ArtifactSHA256: fresh.ArtifactID(), EnvironmentVersion: "prepared-v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fresh.Resume(ctx, "shared")
	var park *durable.ParkError
	if !errors.As(err, &park) {
		t.Fatal(err)
	}
	fresh.Close(ctx)
	bad, err := durable.NewRunner(ctx, store, wasm, "prepared-v1", tools, durable.Preparation{Seed: "different"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Resume(ctx, "shared"); !errors.Is(err, durable.ErrInvalidRunner) {
		t.Fatalf("wrong seed: %v", err)
	}
	bad.Close(ctx)
	current, err := store.Get(ctx, "shared")
	if err != nil || current.Status != durable.StatusWaiting {
		t.Fatalf("resource mismatch changed run: %+v %v", current, err)
	}
	prepared, err := durable.NewRunner(ctx, store, wasm, "prepared-v1", tools, durable.Preparation{Seed: "seed", COW: runtime.GOOS == "linux"})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close(ctx)
	if err = prepared.Decide(ctx, park.WaitID, durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	out, err := prepared.Resume(ctx, "shared")
	if err != nil || reads != 1 || !json.Valid(out.Value) {
		t.Fatalf("result=%s reads=%d error=%v", out.Value, reads, err)
	}
	// A terminal result does not need a matching image seed: no Guest is created.
	other, err := durable.NewRunner(ctx, store, wasm, "prepared-v1", tools, durable.Preparation{Seed: "different"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	if _, err = other.Resume(ctx, "shared"); err != nil || reads != 1 {
		t.Fatalf("completed: reads=%d error=%v", reads, err)
	}
}
