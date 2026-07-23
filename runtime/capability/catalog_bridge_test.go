package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

func TestBuildRegistryFromSnapshotBindsSchemasAuthorityAndBudgets(t *testing.T) {
	snapshot, err := toolcatalog.BuildSnapshot([]toolcatalog.DiscoveredTool{{
		ToolID: "demo.echo", ServerID: "demo", Name: "echo", HandlerVersion: "v3",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
	}}, map[string]toolcatalog.Grant{
		"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "grant_v2", MaxCalls: 2},
	}, toolcatalog.BuildOptions{Revision: 4})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handlers := map[string]capability.Handler{
		"demo.echo": capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			calls++
			return append(json.RawMessage(nil), call.Arguments...), nil
		}),
	}
	registry, grants, err := capability.BuildRegistryFromSnapshot(snapshot, handlers)
	if err != nil {
		t.Fatal(err)
	}
	grant := grants["demo.echo"]
	if grant.HandlerVersion != "v3" || grant.EffectClass != "read_only" || grant.Policy != "AUTO_COMMIT" || grant.MaxCalls != 2 {
		t.Fatalf("snapshot authority drifted in typed grant: %+v", grant)
	}
	delete(handlers, "demo.echo")
	if _, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-drift", CatalogDigest: digestForTest("different-catalog"), Registry: registry,
		Binder: &recordingBinder{}, ToolGrants: grants,
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	})); err == nil {
		t.Fatal("snapshot-derived registry accepted a different Broker catalog digest")
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-catalog", CatalogDigest: snapshot.Digest(), Registry: registry,
		Binder: &recordingBinder{}, ToolGrants: grants,
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"call_id":"catalog-call","capability":"demo.echo","catalog_digest":"` + snapshot.Digest() + `","handler_version":"v3","arguments":{"text":"hello"}}`)
	response, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status capability.Status `json:"status"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Status != capability.StatusOK || calls != 1 {
		t.Fatalf("snapshot-derived dispatch failed: response=%s calls=%d err=%v", response, calls, err)
	}
}

func TestBuildRegistryFromSnapshotRejectsStaleOrExtraHandlers(t *testing.T) {
	snapshot, err := toolcatalog.BuildSnapshot([]toolcatalog.DiscoveredTool{{
		ToolID: "demo.echo", ServerID: "demo", Name: "echo", HandlerVersion: "v1",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
	}}, map[string]toolcatalog.Grant{
		"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1},
	}, toolcatalog.BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler := capability.HandlerFunc(func(context.Context, capability.HostCall) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	for name, handlers := range map[string]map[string]capability.Handler{
		"missing": {},
		"extra":   {"demo.echo": handler, "demo.stale": handler},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := capability.BuildRegistryFromSnapshot(snapshot, handlers); err == nil {
				t.Fatal("non-exact handler set was accepted")
			}
		})
	}
}
