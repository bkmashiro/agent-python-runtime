package fakejob_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/adaptertest"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakejob"
)

func (f *fixture) rawCallWithID(t *testing.T, callID, tool string, arguments any) ([]byte, error) {
	t.Helper()
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"call_id": callID, "capability": tool, "catalog_digest": jobCatalog, "handler_version": fakejob.HandlerVersion, "arguments": json.RawMessage(args)})
	return f.broker.Call(context.Background(), payload)
}

func TestFakeJobSubmitReplayConformance(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	callID := "call:conformance"
	invoke := func(alias string) func() ([]byte, error) {
		return func() ([]byte, error) {
			return f.rawCallWithID(t, callID, fakejob.SubmitToolID, map[string]any{"recipe_alias": alias})
		}
	}
	adaptertest.AssertReplayConformance(t, adaptertest.ReplayCase{First: invoke("recipe:test"), Same: invoke("recipe:test"), Changed: invoke("recipe:missing"), MutationCount: func() uint64 { return uint64(f.provider.JobCount()) }, SecretMarkers: [][]byte{[]byte("read-token"), []byte("control-token")}})
}
