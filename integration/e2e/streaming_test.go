package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestStreamingAuthorityStagedExecution(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	managerRoot := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(managerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(managerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, err := manager.Create([]workspace.InitialFile{{Path: "input.txt", Data: []byte("seed")}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	var qualifiedDispatch, reachedDispatch atomic.Uint32
	registry := capability.NewRegistry()
	register := func(name string, qualified bool, counter *atomic.Uint32) {
		t.Helper()
		grant, err := capability.NewGrant(json.RawMessage(`{"scope":"stream-fixture"}`))
		if err != nil {
			t.Fatal(err)
		}
		spec := capability.Spec{
			Name: name, Version: "stream.fixture.v1", Description: "Deterministic streaming fixture read",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
			HandlerIdentity: "stream-fixture-handler-v1", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
			Python:       &capability.PythonProjection{Module: "tools", Method: name[len("fixture."):], Arguments: []string{"value"}},
		}
		if qualified {
			spec.ReadOnly, spec.Idempotent = true, true
			spec.PreDispatch = &capability.PreDispatchContract{
				Resource:  capability.ResourceReference{Namespace: "fixture", Argument: "value"},
				Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
			}
		}
		if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			counter.Add(1)
			return append(json.RawMessage(nil), arguments...), nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	register("fixture.eager", true, &qualifiedDispatch)
	register("fixture.reached", false, &reachedDispatch)
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 8})
	if err != nil {
		t.Fatal(err)
	}

	run := func(chunks []string) (streaming.RunResult, workspace.Ref, *capability.Broker, error) {
		attempt, err := manager.ForkAttempt(base)
		if err != nil {
			return streaming.RunResult{}, "", nil, err
		}
		prepares, err := streaming.BuildPrepareChunks(streaming.PrepareConfig{Inputs: json.RawMessage(`{}`), Chunks: chunks, Plan: plan, SpeculationMaxCalls: 4})
		if err != nil {
			return streaming.RunResult{}, attempt.Ref(), nil, err
		}
		var broker *capability.Broker
		factory := wazeroengine.Factory{
			WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "stream-e2e",
			BrokerFactory: func(context.Context) (*capability.Broker, error) {
				broker, err = capability.NewBroker(capability.Config{RunIdentity: "stream-e2e", Plan: plan})
				return broker, err
			},
		}
		runConfig := runtimeconfig.DefaultRunConfig()
		runConfig.Mechanisms = runtimeconfig.MechanismSet{
			Streaming: true, StagedObservation: true, PrivateWorkspace: true,
		}
		runner, err := factory.New(context.Background(), artifact, runConfig)
		if err != nil {
			return streaming.RunResult{}, attempt.Ref(), broker, err
		}
		request := []byte(`{"run_id":"stream-e2e","code":"result = stream_final","inputs":{}}`)
		streamRunner, ok := runner.(streaming.StreamRunner)
		if !ok {
			return streaming.RunResult{}, attempt.Ref(), nil, errors.New("wazero runner lacks live stream support")
		}
		prepareChannel := make(chan string)
		type outcome struct {
			result streaming.RunResult
			err    error
		}
		completed := make(chan outcome, 1)
		go func() {
			result, err := streaming.ExecuteStream(context.Background(), streamRunner, attempt, request, prepareChannel)
			completed <- outcome{result: result, err: err}
		}()
		for _, prepare := range prepares {
			select {
			case prepareChannel <- prepare:
			case finished := <-completed:
				close(prepareChannel)
				return finished.result, attempt.Ref(), broker, finished.err
			}
		}
		close(prepareChannel)
		finished := <-completed
		return finished.result, attempt.Ref(), broker, finished.err
	}

	valid, _, broker, err := run([]string{
		"from pathlib import Path\n",
		"if False:\n    tools.eager('orphan')\n",
		"used = tools.eager('used')['value']\n",
		"if False:\n    tools.reached('unreachable')\n",
		"reached = tools.reached('reached')['value']\n",
		"Path('/workspace/output.txt').write_text(used + ':' + reached)\n",
		"result = {'used': used, 'reached': reached}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if qualifiedDispatch.Load() != 1 || reachedDispatch.Load() != 1 || broker == nil || broker.Calls() != 2 {
		t.Fatalf("dispatch qualified=%d reached=%d broker=%v", qualifiedDispatch.Load(), reachedDispatch.Load(), broker)
	}
	var envelope struct {
		Result struct {
			Eager    map[string]int `json:"eager"`
			Timeline []struct {
				Kind    string  `json:"kind"`
				StartMS float64 `json:"start_ms"`
				EndMS   float64 `json:"end_ms"`
			} `json:"timeline"`
		} `json:"result"`
	}
	if err := json.Unmarshal(valid.Response, &envelope); err != nil || envelope.Result.Eager["dispatched"] != 0 || envelope.Result.Eager["consumed"] != 0 || envelope.Result.Eager["orphaned"] != 0 {
		t.Fatalf("capability metadata activated eager stream dispatch err=%v result=%+v", err, envelope.Result)
	}
	assertSnapshotPath(t, manager, base, "output.txt", false)
	assertSnapshotPath(t, manager, valid.PublishedWorkspace, "output.txt", true)

	beforeEager := qualifiedDispatch.Load()
	_, invalidRef, invalidBroker, err := run([]string{"if False:\n    tools.eager('wasted-invalid')\n", "result = )\n"})
	if err == nil || invalidBroker == nil || invalidBroker.Calls() != 0 || qualifiedDispatch.Load() != beforeEager {
		t.Fatalf("invalid suffix err=%v qualified=%d broker=%v", err, qualifiedDispatch.Load(), invalidBroker)
	}
	if _, err := manager.Acquire(invalidRef, "discarded"); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
		t.Fatalf("invalid attempt published: %v", err)
	}
	assertSnapshotPath(t, manager, base, "output.txt", false)
}

func assertSnapshotPath(t *testing.T, manager *workspace.Manager, ref workspace.Ref, path string, present bool) {
	t.Helper()
	lease, err := manager.Acquire(ref, "snapshot-"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	snapshot, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range snapshot.Entries {
		found = found || entry.Path == path
	}
	if found != present {
		t.Fatalf("path %q present=%v want=%v snapshot=%+v", path, found, present, snapshot)
	}
}
