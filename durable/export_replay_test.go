package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportReplayPurePythonAfterStoreClosed(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, nil)
	definition := Definition{
		ID: "offline-pure", Code: "print('private'); result = {'answer': 42}", Seed: "offline-seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{"n":42}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	original, err := runner.Resume(context.Background(), definition.ID)
	if err != nil || string(original.Value) != `{"answer": 42}` || original.Stdout != "private\n" {
		t.Fatalf("original=%+v err=%v", original, err)
	}
	bundle, err := store.Export(context.Background(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The replay input is the closed-store export, not a live Runner or Store.
	guest, err := os.ReadFile(testGuestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReplayBundle(context.Background(), bundle, guest)
	if err != nil || !bytes.Equal(got.Value, original.Value) || got.Stdout != original.Stdout {
		t.Fatalf("replayed=%+v err=%v", got, err)
	}
}

func TestExportRejectsPendingAndCancelledRuns(t *testing.T) {
	store, _ := openTestStore(t)
	pending := storeTestDefinition("offline-pending")
	if err := store.Create(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.BeginCall(context.Background(), pending.ID, LoggedCall{
		Sequence: 0, CallID: "call-0", Capability: "tool", Arguments: json.RawMessage(`{}`),
	}); err != nil || !created {
		t.Fatalf("pending call created=%v err=%v", created, err)
	}
	if err := store.SetState(context.Background(), pending.ID, StatusCompleted, json.RawMessage(`{"Value":null,"Stdout":""}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Export(context.Background(), pending.ID); !errors.Is(err, ErrBundleInvalid) {
		t.Fatalf("pending export err=%v", err)
	}
	cancelled := storeTestDefinition("offline-cancelled")
	if err := store.Create(context.Background(), cancelled); err != nil {
		t.Fatal(err)
	}
	if err := store.SetState(context.Background(), cancelled.ID, StatusCancelled, nil, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Export(context.Background(), cancelled.ID); !errors.Is(err, ErrBundleInvalid) {
		t.Fatalf("cancelled export err=%v", err)
	}
}

func TestReplayRejectsArtifactAndCallTampering(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, []Tool{{
		Name: "echo", Version: "v1", Recovery: RetrySafe,
		Call: func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	}})
	definition := Definition{
		ID: "offline-tamper", Code: "result = echo(value=1)", Seed: "offline-seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(context.Background(), definition.ID); err != nil {
		t.Fatal(err)
	}
	bundle, err := store.Export(context.Background(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := os.ReadFile(testGuestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	badArtifact := bundle
	badArtifact.Run.ArtifactSHA256 = "sha256:tampered"
	artifactErr := error(nil)
	if _, artifactErr = ReplayBundle(context.Background(), badArtifact, guest); !errors.Is(artifactErr, ErrBundleMismatch) {
		t.Fatalf("artifact tamper err=%v", artifactErr)
	}
	var artifactMismatch *ReplayMismatchError
	if !errors.As(artifactErr, &artifactMismatch) || artifactMismatch.Location != ReplayMismatchLocationArtifact || artifactMismatch.Reason != ReplayMismatchReasonArtifactIdentity {
		t.Fatalf("artifact mismatch details=%+v err=%v", artifactMismatch, artifactErr)
	}
	badCall := bundle
	badCall.Calls = append([]BundleCall(nil), bundle.Calls...)
	badCall.Calls[0].Arguments = json.RawMessage(`{"changed":true}`)
	callErr := error(nil)
	if _, callErr = ReplayBundle(context.Background(), badCall, guest); !errors.Is(callErr, ErrBundleMismatch) {
		t.Fatalf("call tamper err=%v", callErr)
	}
	var callMismatch *ReplayMismatchError
	if !errors.As(callErr, &callMismatch) || callMismatch.Location != ReplayMismatchLocationCall || callMismatch.Reason != ReplayMismatchReasonCallArguments || !callMismatch.HasSequence || callMismatch.Sequence != 0 {
		t.Fatalf("call mismatch details=%+v err=%v", callMismatch, callErr)
	}
}

func TestReplayPreservesToolErrors(t *testing.T) {
	store, _ := openTestStore(t)
	runner := realRunner(t, store, []Tool{{
		Name: "boom", Version: "v1", Recovery: RetrySafe,
		Call: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("tool failure") },
	}})
	definition := Definition{
		ID: "offline-tool-error", Code: "result = boom(value=1)", Seed: "offline-seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	}
	if _, err := runner.Create(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Resume(context.Background(), definition.ID); err == nil {
		t.Fatal("uncaught tool error unexpectedly succeeded")
	}
	bundle, err := store.Export(context.Background(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := os.ReadFile(testGuestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReplayBundle(context.Background(), bundle, guest); err != nil {
		t.Fatalf("tool error replay failed: %v", err)
	}
}

func testGuestPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = filepath.Join("..", "dist", "pysolate.wasm")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("real Guest unavailable: %v", err)
	}
	return path
}
