package fakeworkspace_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/adaptertest"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
)

func (fixture *brokerFixture) rawCallWithID(t *testing.T, callID, tool string, arguments any) ([]byte, error) {
	t.Helper()
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"call_id": callID, "capability": tool, "catalog_digest": catalog, "handler_version": fakeworkspace.HandlerVersion, "arguments": json.RawMessage(args)})
	return fixture.broker.Call(context.Background(), payload)
}

func TestFakeWorkspaceOpenReplayConformance(t *testing.T) {
	store, _ := newStore(t, fakeworkspace.DefaultLimits())
	fixture := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:workspace-conformance", TaskIdentity: "task:workspace-conformance"})
	callID := "call:conformance"
	invoke := func(alias string) func() ([]byte, error) {
		return func() ([]byte, error) {
			return fixture.rawCallWithID(t, callID, fakeworkspace.RepoOpenToolID, map[string]any{"alias": alias, "revision": revision})
		}
	}
	adaptertest.AssertReplayConformance(t, adaptertest.ReplayCase{First: invoke("demo"), Same: invoke("demo"), Changed: invoke("missing"), MutationCount: func() uint64 { return uint64(store.WorkspaceCount()) }})
}
