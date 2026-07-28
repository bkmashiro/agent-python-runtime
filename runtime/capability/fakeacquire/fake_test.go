package fakeacquire_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/adaptertest"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeacquire"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const acquireCatalog = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

type testIDs struct{ next int }

func (ids *testIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_%d", prefix, ids.next), nil
}

type fixture struct {
	broker *capability.Broker
	store  *fakeworkspace.Store
}
type toolResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func sourceFixture() fakeworkspace.Fixture {
	return fakeworkspace.Fixture{Alias: "downloaded", Revision: "2222222222222222222222222222222222222222", Files: map[string][]byte{"README.md": []byte("downloaded fixture"), "src/main.py": []byte("print('ok')\n")}}
}

func newFixture(t *testing.T, manifest string, secret []byte) *fixture {
	t.Helper()
	limits := fakeworkspace.DefaultLimits()
	store, err := fakeworkspace.NewStore([]fakeworkspace.Fixture{{Alias: "seed", Revision: "1111111111111111111111111111111111111111", Files: map[string][]byte{"seed.txt": []byte("seed")}}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{"secret:source": secret}, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolver.Close)
	source := sourceFixture()
	provider, err := fakeacquire.NewProvider([]fakeacquire.Source{{Alias: "source:demo", RepositoryAlias: source.Alias, Revision: source.Revision, ManifestDigest: manifest, Files: source.Files}}, []byte("source-token"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.Close)
	binding := fakeworkspace.Binding{RunIdentity: "run:acquire", TaskIdentity: "task:acquire"}
	adapter, err := fakeacquire.NewAdapter(fakeacquire.Config{Resolver: resolver, SourceSecretRef: "secret:source", RunIdentity: binding.RunIdentity, TaskIdentity: binding.TaskIdentity, Tenant: "tenant:test", SourceRegistryAlias: "sources:test", PolicyVersion: "acquire:v1", LeaseDuration: time.Minute, Provider: provider, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grants := map[string]capability.ToolGrant{}
	specs, _ := fakeacquire.HandlerSpecs(adapter)
	workspaceSpecs, _ := fakeworkspace.HandlerSpecs(store, binding)
	for _, spec := range append(specs, workspaceSpecs...) {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
		grants[spec.ToolID] = capability.ToolGrant{ToolID: spec.ToolID, HandlerVersion: spec.HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: "acquire:v1", MaxCalls: 32}
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &testIDs{}, func() time.Time { return time.Unix(1000, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: binding.RunIdentity, CatalogDigest: acquireCatalog, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: binding.RunIdentity, CatalogDigest: acquireCatalog, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 64}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{broker: broker, store: store}
}

func call(t *testing.T, f *fixture, callID, tool string, args any) toolResponse {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	version := fakeacquire.HandlerVersion
	if tool != fakeacquire.ToolID {
		version = fakeworkspace.HandlerVersion
	}
	payload, _ := json.Marshal(map[string]any{"call_id": callID, "capability": tool, "catalog_digest": acquireCatalog, "handler_version": version, "arguments": json.RawMessage(rawArgs)})
	raw, err := f.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var response toolResponse
	if json.Unmarshal(raw, &response) != nil {
		t.Fatalf("response=%s", raw)
	}
	return response
}

func TestFakeAcquireCreatesReadableBoundWorkspace(t *testing.T) {
	manifest, err := fakeworkspace.FixtureManifest(sourceFixture(), fakeworkspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, manifest, []byte("source-token"))
	response := call(t, f, "call:acquire", fakeacquire.ToolID, map[string]any{"source_alias": "source:demo"})
	if response.Status != "ok" {
		t.Fatalf("response=%+v", response)
	}
	var acquired fakeacquire.Result
	if json.Unmarshal(response.Result, &acquired) != nil || acquired.WorkspaceID == "" || acquired.ManifestDigest != manifest {
		t.Fatalf("acquired=%+v", acquired)
	}
	read := call(t, f, "call:read", fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": acquired.WorkspaceID, "paths": []string{"README.md"}})
	if read.Status != "ok" || !contains(read.Result, "downloaded fixture") {
		t.Fatalf("read=%+v result=%s", read, read.Result)
	}
}

func TestFakeAcquireManifestDriftDoesNotCreateWorkspace(t *testing.T) {
	f := newFixture(t, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("source-token"))
	before := f.store.WorkspaceCount()
	response := call(t, f, "call:drift", fakeacquire.ToolID, map[string]any{"source_alias": "source:demo"})
	if response.Error == nil || response.Error.Code != "source_drift" || f.store.WorkspaceCount() != before {
		t.Fatalf("response=%+v count=%d", response, f.store.WorkspaceCount())
	}
}

func TestFakeAcquireCredentialDenialDoesNotCreateWorkspace(t *testing.T) {
	manifest, _ := fakeworkspace.FixtureManifest(sourceFixture(), fakeworkspace.DefaultLimits())
	f := newFixture(t, manifest, []byte("wrong-source-token"))
	before := f.store.WorkspaceCount()
	response := call(t, f, "call:credential", fakeacquire.ToolID, map[string]any{"source_alias": "source:demo"})
	if response.Error == nil || response.Error.Code != "credential_denied" || f.store.WorkspaceCount() != before {
		t.Fatalf("response=%+v count=%d", response, f.store.WorkspaceCount())
	}
}

func TestFakeAcquireRejectsGuestURLAndBytes(t *testing.T) {
	f := newFixture(t, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("source-token"))
	for index, args := range []map[string]any{{"source_alias": "source:demo", "url": "https://example.invalid/archive"}, {"source_alias": "source:demo", "bytes": "payload"}} {
		response := call(t, f, fmt.Sprintf("call:unsafe:%d", index), fakeacquire.ToolID, args)
		if response.Error == nil || response.Error.Code != "invalid_arguments" {
			t.Fatalf("response=%+v", response)
		}
	}
}

func TestFakeAcquireReplayConformance(t *testing.T) {
	manifest, _ := fakeworkspace.FixtureManifest(sourceFixture(), fakeworkspace.DefaultLimits())
	f := newFixture(t, manifest, []byte("source-token"))
	invoke := func(alias string) func() ([]byte, error) {
		return func() ([]byte, error) {
			args, _ := json.Marshal(map[string]any{"source_alias": alias})
			payload, _ := json.Marshal(map[string]any{"call_id": "call:conformance", "capability": fakeacquire.ToolID, "catalog_digest": acquireCatalog, "handler_version": fakeacquire.HandlerVersion, "arguments": json.RawMessage(args)})
			return f.broker.Call(context.Background(), payload)
		}
	}
	adaptertest.AssertReplayConformance(t, adaptertest.ReplayCase{First: invoke("source:demo"), Same: invoke("source:demo"), Changed: invoke("source:missing"), MutationCount: func() uint64 { return uint64(f.store.WorkspaceCount()) }, SecretMarkers: [][]byte{[]byte("source-token")}})
}

func contains(value []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if string(value[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
