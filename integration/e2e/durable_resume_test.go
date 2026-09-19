package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/durable"
)

func TestRealGuestDurableApprovalSurvivesReopen(t *testing.T) {
	artifact, identity := durableArtifact(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.db")
	store, err := durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var reads, waits atomic.Int32
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"durable-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	readValue := 7
	tools := []durable.Tool{
		{Spec: capability.Spec{Name: "test.read", Version: "v1", Description: "Durable replay test tool", EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "test.read.v1", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"integer"}`), Python: &capability.PythonProjection{Module: "tools", Method: "read", Arguments: []string{}}}, Grant: grant, Recovery: durable.RetrySafe, Handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
			reads.Add(1)
			return json.Marshal(readValue)
		})},
		{Spec: capability.Spec{Name: "test.approve", Version: "v1", Description: "Durable replay test tool", EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "test.approve.v1", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"boolean"}`), Python: &capability.PythonProjection{Module: "tools", Method: "approve", Arguments: []string{}}}, Grant: grant, Recovery: durable.WaitMode, Wait: func(context.Context, capability.LoggedCall) (durable.WaitSpec, error) {
			waits.Add(1)
			deadline := time.Now().Add(time.Hour)
			return durable.WaitSpec{Kind: "approval", Request: json.RawMessage(`{"question":"continue?"}`), Deadline: &deadline}, nil
		}},
	}
	runner, err := durable.NewRunner(store, artifact, identity, "approval-test-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	def := durable.Definition{ID: "approval-run", Code: "value = tools.read()\napproved = tools.approve()\nresult = [value * 2, approved]\n", Seed: "replay-seed", ArtifactSHA256: identity.ArtifactSHA256, EnvironmentVersion: "approval-test-v1", Inputs: json.RawMessage(`{}`)}
	if _, err := runner.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	_, err = runner.Resume(ctx, def.ID)
	var parked *durable.ParkError
	if !errors.As(err, &parked) || parked.WaitID == "" {
		t.Fatalf("want durable wait, got %v", err)
	}
	waitID := parked.WaitID
	persisted, err := store.Get(ctx, def.ID)
	if err != nil || persisted.Status != durable.StatusWaiting {
		t.Fatalf("run=%+v err=%v", persisted, err)
	}
	if reads.Load() != 1 || waits.Load() != 1 {
		t.Fatalf("reads=%d waits=%d", reads.Load(), waits.Load())
	}
	if _, err := runner.Resume(ctx, def.ID); !errors.Is(err, durable.ErrParked) {
		t.Fatalf("unresolved wait did not park again: %v", err)
	}
	stillWaiting, err := store.Get(ctx, def.ID)
	if err != nil || stillWaiting.Status != durable.StatusWaiting || waits.Load() != 1 {
		t.Fatalf("repeated park state=%+v waits=%d err=%v", stillWaiting, waits.Load(), err)
	}
	if err := runner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = durable.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err = durable.NewRunner(store, artifact, identity, "approval-test-v1", tools)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	readValue = 999 // The outside world changed; replay must still return the saved 7.
	if err := runner.Decide(ctx, waitID, durable.Decision{Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	response, err := runner.Resume(ctx, def.ID)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != "ok" || string(decoded.Result) != "[14,true]" {
		t.Fatalf("response=%s", response)
	}
	again, err := runner.Resume(ctx, def.ID)
	if err != nil || !bytes.Equal(response, again) {
		t.Fatalf("completed replay=%s err=%v", again, err)
	}
	if reads.Load() != 1 || waits.Load() != 1 {
		t.Fatalf("replayed live calls: reads=%d waits=%d", reads.Load(), waits.Load())
	}
}

func TestRealGuestDurablePythonErrorIsStableFinalOutcome(t *testing.T) {
	artifact, identity := durableArtifact(t)
	ctx := context.Background()
	store, err := durable.Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := durable.NewRunner(store, artifact, identity, "error-test-v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	def := durable.Definition{ID: "failed-run", Code: "raise ValueError('expected')", Seed: "seed", ArtifactSHA256: identity.ArtifactSHA256, EnvironmentVersion: "error-test-v1", Inputs: json.RawMessage(`{}`)}
	if _, err := runner.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	first, err := runner.Resume(ctx, def.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.Get(ctx, def.ID)
	if err != nil || run.Status != durable.StatusFailed {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	second, err := runner.Resume(ctx, def.ID)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("second=%s err=%v", second, err)
	}
	var response struct {
		Status string `json:"status"`
		Error  struct {
			Type string `json:"error_type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(first, &response); err != nil || response.Status != "error" || response.Error.Type != "ValueError" {
		t.Fatalf("response=%s err=%v", first, err)
	}
}
