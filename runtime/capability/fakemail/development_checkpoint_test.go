package fakemail_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func TestFakeMailCheckpointSurvivesSQLiteReopen(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	controller, err := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "Durable", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	credential := transaction.CommitCredential{Token: "8888888888888888888888888888888888888888888888888888888888888888"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:mail-sqlite", "owner", fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	fixture.provider.SetAmbiguousNextSend()
	_, _ = controller.Commit(context.Background(), credential, staged.OperationID)
	if fixture.provider.SentCount() != 1 {
		t.Fatalf("sent=%d", fixture.provider.SentCount())
	}
	path := filepath.Join(t.TempDir(), "mail-checkpoints.db")
	store, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fakemail.SaveDevelopmentCheckpoint(context.Background(), store, "checkpoint:mail", controller); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		content, readErr := os.ReadFile(path + suffix)
		if readErr == nil {
			for _, marker := range [][]byte{[]byte(credential.Token), []byte("read-token"), []byte("draft-token"), []byte("send-token")} {
				if bytes.Contains(content, marker) {
					t.Fatalf("checkpoint file leaked credential marker in %s", suffix)
				}
			}
		}
	}
	reopenedStore, err := devsnapshot.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedStore.Close()
	providerSnapshot, controllerSnapshot, err := fakemail.LoadDevelopmentCheckpoint(context.Background(), reopenedStore, "checkpoint:mail")
	if err != nil {
		t.Fatal(err)
	}
	reopenedProvider, err := fakemail.NewProviderFromSnapshot(providerSnapshot, []byte("read-token"), []byte("draft-token"), []byte("send-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedProvider.Close()
	reopenedAdapter, err := fakemail.NewAdapter(fakemail.Config{Resolver: fixture.resolver, ReadSecretRef: "mail.read", DraftSecretRef: "mail.draft", SendSecretRef: "mail.send", RunIdentity: "run:mail", TaskIdentity: "task:mail", Tenant: "tenant:mail", AccountAlias: "mailbox:test", PolicyVersion: "mail:v1", LeaseDuration: time.Minute, Provider: reopenedProvider})
	if err != nil {
		t.Fatal(err)
	}
	reopenedController, err := fakemail.NewSendControllerFromSnapshot(fixture.coordinator, reopenedAdapter, controllerSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := reopenedController.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.ProviderMessageID == "" || reopenedProvider.SentCount() != 1 {
		t.Fatalf("receipt=%+v sent=%d err=%v", receipt, reopenedProvider.SentCount(), err)
	}
}
