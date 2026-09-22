package durable_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

const legacyToolSnapshot = `[{"name":"approve","version":"v1","recovery":"wait"},{"name":"read","version":"v1","recovery":"retry_safe"},{"name":"write","version":"v1","recovery":"idempotent"}]`

func legacyRecoveryTools(readVersion string, writeRecovery durable.RecoveryMode, reads, writes *int) []durable.Tool {
	return []durable.Tool{
		{Name: "read", Version: readVersion, Recovery: durable.RetrySafe, Call: func(context.Context, json.RawMessage) (any, error) {
			*reads++
			return 7, nil
		}},
		{Name: "approve", Version: "v1", Recovery: durable.WaitMode, Wait: func(_ context.Context, args json.RawMessage) (durable.WaitSpec, error) {
			return durable.WaitSpec{Kind: "approval", Request: args}, nil
		}},
		{Name: "write", Version: "v1", Recovery: writeRecovery, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
			*writes++
			key, ok := durable.OperationKey(ctx)
			if !ok || key != "legacy-reopen/2" {
				return nil, errors.New("wrong operation key")
			}
			return json.RawMessage(args), nil
		}},
	}
}

func TestLegacyToolSnapshotReopensAndRejectsVersionAndRecoveryChanges(t *testing.T) {
	ctx := context.Background()
	wasm := realGuest(t)
	path := filepath.Join(t.TempDir(), "runs.db")
	reads, writes := 0, 0
	tools := legacyRecoveryTools("v1", durable.Idempotent, &reads, &writes)

	store, err := durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := durable.NewRunner(ctx, store, wasm, "test-v1", tools)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	definition := durable.Definition{
		ID: "legacy-reopen", Code: "x = read()\nif approve(value=x):\n    result = write(value=x)\n",
		Seed: "seed", ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "test-v1",
		Inputs: json.RawMessage(`{}`), Tools: json.RawMessage(legacyToolSnapshot),
	}
	// Bypass Runner.Create to model a definition written by the old format.
	if err := store.Create(ctx, definition); err != nil {
		runner.Close(ctx)
		store.Close()
		t.Fatal(err)
	}
	_, err = runner.Resume(ctx, definition.ID)
	var parked *durable.ParkError
	if !errors.As(err, &parked) || reads != 1 || writes != 0 {
		runner.Close(ctx)
		store.Close()
		t.Fatalf("park=%v reads=%d writes=%d", err, reads, writes)
	}
	if err := runner.Close(ctx); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err = durable.NewRunner(ctx, store, wasm, "test-v1", tools)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	defer store.Close()
	if err := runner.Decide(ctx, parked.WaitID, durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(ctx, definition.ID); err != nil || reads != 1 || writes != 1 {
		t.Fatalf("reopen resume err=%v reads=%d writes=%d", err, reads, writes)
	}
	got, err := store.Get(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Definition.Tools) != legacyToolSnapshot {
		t.Fatalf("legacy definition was rewritten: %s", got.Definition.Tools)
	}

	versionReads, versionWrites := 0, 0
	versionTools := legacyRecoveryTools("v2", durable.Idempotent, &versionReads, &versionWrites)
	versionRunner, err := durable.NewRunner(ctx, store, wasm, "test-v1", versionTools)
	if err != nil {
		t.Fatal(err)
	}
	versionDefinition := definition
	versionDefinition.ID = "legacy-version-mismatch"
	if err := store.Create(ctx, versionDefinition); err != nil {
		versionRunner.Close(ctx)
		t.Fatal(err)
	}
	if _, err := versionRunner.Resume(ctx, versionDefinition.ID); !errors.Is(err, durable.ErrBlocked) {
		versionRunner.Close(ctx)
		t.Fatalf("version mismatch err=%v", err)
	}
	versionRun, err := store.Get(ctx, versionDefinition.ID)
	if err != nil || versionRun.Status != durable.StatusBlocked {
		versionRunner.Close(ctx)
		t.Fatalf("version mismatch run=%#v err=%v", versionRun, err)
	}
	if err := versionRunner.Close(ctx); err != nil {
		t.Fatal(err)
	}

	recoveryReads, recoveryWrites := 0, 0
	recoveryTools := legacyRecoveryTools("v1", durable.Manual, &recoveryReads, &recoveryWrites)
	recoveryRunner, err := durable.NewRunner(ctx, store, wasm, "test-v1", recoveryTools)
	if err != nil {
		t.Fatal(err)
	}
	recoveryDefinition := definition
	recoveryDefinition.ID = "legacy-recovery-mismatch"
	if err := store.Create(ctx, recoveryDefinition); err != nil {
		recoveryRunner.Close(ctx)
		t.Fatal(err)
	}
	if _, err := recoveryRunner.Resume(ctx, recoveryDefinition.ID); !errors.Is(err, durable.ErrBlocked) {
		recoveryRunner.Close(ctx)
		t.Fatalf("recovery mismatch err=%v", err)
	}
	recoveryRun, err := store.Get(ctx, recoveryDefinition.ID)
	if err != nil || recoveryRun.Status != durable.StatusBlocked {
		recoveryRunner.Close(ctx)
		t.Fatalf("recovery mismatch run=%#v err=%v", recoveryRun, err)
	}
	if err := recoveryRunner.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
