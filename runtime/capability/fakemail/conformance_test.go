package fakemail_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/adaptertest"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
)

func (fixture *mailFixture) rawCallWithID(t *testing.T, callID, tool string, arguments any) ([]byte, error) {
	t.Helper()
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"call_id": callID, "capability": tool, "catalog_digest": mailCatalog, "handler_version": fakemail.HandlerVersion, "arguments": json.RawMessage(args)})
	return fixture.broker.Call(context.Background(), payload)
}

func TestFakeMailDraftReplayConformance(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	callID := "call:mail-conformance"
	invoke := func(subject string) func() ([]byte, error) {
		return func() ([]byte, error) {
			return fixture.rawCallWithID(t, callID, fakemail.DraftPrepareToolID, map[string]any{"to": []string{"recipient@example.invalid"}, "subject": subject, "body": "body"})
		}
	}
	adaptertest.AssertReplayConformance(t, adaptertest.ReplayCase{First: invoke("Draft"), Same: invoke("Draft"), Changed: invoke("Changed"), MutationCount: func() uint64 { return uint64(len(fixture.provider.Drafts())) }, SecretMarkers: [][]byte{[]byte("read-token"), []byte("draft-token"), []byte("send-token")}})
}
