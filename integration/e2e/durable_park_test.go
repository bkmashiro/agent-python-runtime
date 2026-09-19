package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type durableParkJournal struct {
	mu         sync.Mutex
	failed     bool
	controlErr error
}

func (journal *durableParkJournal) Call(ctx context.Context, _ capability.LoggedCall, dispatch func(context.Context) ([]byte, error)) ([]byte, error) {
	journal.mu.Lock()
	if !journal.failed {
		journal.failed = true
		err := journal.controlErr
		journal.mu.Unlock()
		return nil, err
	}
	journal.mu.Unlock()
	return dispatch(ctx)
}

func TestRealGuestCannotCatchJournalParkOrProduceSuccess(t *testing.T) {
	artifact, err := readGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	var handlerCalls atomic.Uint32
	spec := capability.Spec{
		Name: "tools.effect", Version: "test.tools.effect.v1", Description: "durable park test",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "test.tools.effect.handler.v1", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"durable-park-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		return json.RawMessage(`{"ok":true}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	controlErr := errors.New("durable journal write failed")
	journal := &durableParkJournal{controlErr: controlErr}
	var broker *capability.Broker
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: "durable-park-run", Plan: plan, CallJournal: journal,
		})
		if createErr == nil {
			broker = created
		}
		return created, createErr
	}}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()

	code := `import _agent_runtime_host

events = []
try:
    _agent_runtime_host.call('{"call_id":"park","capability":"tools.effect","arguments":{}}')
    events.append("park-returned")
except BaseException as exc:
    events.append(["park-caught", type(exc).__name__])
try:
    _agent_runtime_host.call('{"call_id":"second","capability":"tools.effect","arguments":{}}')
    events.append("second-returned")
except BaseException as exc:
    events.append(["second-caught", type(exc).__name__])
result = {"events": events, "final_success": True}`
	request, err := json.Marshal(map[string]any{
		"run_id": "durable-park-run", "code": code, "inputs": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, runErr := runner.Run(context.Background(), request, "")
	if !errors.Is(runErr, controlErr) {
		t.Fatalf("run error=%v, want original control error %v", runErr, controlErr)
	}
	if len(payload) != 0 {
		t.Fatalf("Guest produced a final payload after park: %s", payload)
	}
	if broker == nil || !errors.Is(broker.ControlError(), controlErr) || broker.Calls() != 1 || handlerCalls.Load() != 0 {
		t.Fatalf("broker=%v control=%v calls=%d handler_calls=%d", broker, broker.ControlError(), broker.Calls(), handlerCalls.Load())
	}
}

func readGuestArtifact(t *testing.T) ([]byte, error) {
	t.Helper()
	path := guestArtifact(t)
	return os.ReadFile(path)
}
