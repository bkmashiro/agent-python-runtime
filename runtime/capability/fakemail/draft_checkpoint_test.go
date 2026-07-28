package fakemail_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
)

func TestFakeMailDraftUndoSurvivesSQLiteReopen(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	created := fixture.call(t, fakemail.DraftPrepareToolID, map[string]any{"to": []string{"recipient@example.invalid"}, "subject": "Durable draft", "body": "body"})
	if created.Status != "ok" || len(fixture.provider.Drafts()) != 1 {
		t.Fatalf("created=%+v drafts=%+v", created, fixture.provider.Drafts())
	}
	controller, err := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mail-draft.db")
	store, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fakemail.SaveDevelopmentCheckpoint(context.Background(), store, "checkpoint:draft", controller); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providerSnapshot, controllerSnapshot, adapterSnapshot, err := fakemail.LoadDevelopmentCheckpoint(context.Background(), store, "checkpoint:draft")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fakemail.NewProviderFromSnapshot(providerSnapshot, []byte("read-token"), []byte("draft-token"), []byte("send-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	config := fakemail.Config{Resolver: fixture.resolver, ReadSecretRef: "mail.read", DraftSecretRef: "mail.draft", SendSecretRef: "mail.send", RunIdentity: "run:mail", TaskIdentity: "task:mail", Tenant: "tenant:mail", AccountAlias: "mailbox:test", PolicyVersion: "mail:v1", LeaseDuration: time.Minute, Provider: provider}
	adapter, err := fakemail.NewAdapterFromSnapshot(config, adapterSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fakemail.NewSendControllerFromSnapshot(fixture.coordinator, adapter, controllerSnapshot); err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(fixture.coordinator, fixture.transaction.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	specs, _ := fakemail.HandlerSpecs(adapter)
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	grants, _ := fakemail.ToolGrants("mail:v1", 32)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:mail", CatalogDigest: mailCatalog, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 64}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.RollbackCurrentTransaction(context.Background(), "restart_abort"); err != nil {
		t.Fatal(err)
	}
	if len(provider.Drafts()) != 0 {
		t.Fatalf("draft rollback lost across restart: %+v", provider.Drafts())
	}
}
