package fakemail_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func BenchmarkFakeMailDevelopmentCheckpoint(b *testing.B) {
	for _, bodyBytes := range []int{1024, 64 << 10} {
		b.Run(fmt.Sprintf("body_bytes=%d", bodyBytes), func(b *testing.B) {
			fixture := newMailFixture(b, []byte("send-token"))
			controller, err := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
			if err != nil {
				b.Fatal(err)
			}
			staged, err := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "benchmark", Body: strings.Repeat("x", bodyBytes)})
			if err != nil {
				b.Fatal(err)
			}
			credential := transaction.CommitCredential{Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
			if err := controller.RegisterApproval(credential, staged.OperationID, "approval:benchmark", "owner", fixture.now.Add(time.Minute)); err != nil {
				b.Fatal(err)
			}
			fixture.provider.SetAmbiguousNextSend()
			_, _ = controller.Commit(context.Background(), credential, staged.OperationID)
			store, err := devsnapshot.Open(filepath.Join(b.TempDir(), "checkpoints.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			if _, err := fakemail.SaveDevelopmentCheckpoint(context.Background(), store, "benchmark:mail", controller); err != nil {
				b.Fatal(err)
			}
			b.Run("save_ambiguous", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for index := 0; index < b.N; index++ {
					if _, err := fakemail.SaveDevelopmentCheckpoint(context.Background(), store, "benchmark:mail", controller); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(bodyBytes), "body-bytes")
				b.ReportMetric(3, "components/checkpoint")
			})
			providerSnapshot, controllerSnapshot, adapterSnapshot, err := fakemail.LoadDevelopmentCheckpoint(context.Background(), store, "benchmark:mail")
			if err != nil {
				b.Fatal(err)
			}
			b.Run("load_and_reopen", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for index := 0; index < b.N; index++ {
					provider, err := fakemail.NewProviderFromSnapshot(providerSnapshot, []byte("read-token"), []byte("draft-token"), []byte("send-token"))
					if err != nil {
						b.Fatal(err)
					}
					config := fakemail.Config{Resolver: fixture.resolver, ReadSecretRef: "mail.read", DraftSecretRef: "mail.draft", SendSecretRef: "mail.send", RunIdentity: "run:mail", TaskIdentity: "task:mail", Tenant: "tenant:mail", AccountAlias: "mailbox:test", PolicyVersion: "mail:v1", LeaseDuration: time.Minute, Provider: provider}
					adapter, err := fakemail.NewAdapterFromSnapshot(config, adapterSnapshot)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := fakemail.NewSendControllerFromSnapshot(fixture.coordinator, adapter, controllerSnapshot); err != nil {
						b.Fatal(err)
					}
					provider.Close()
				}
				b.ReportMetric(float64(bodyBytes), "body-bytes")
				b.ReportMetric(3, "components/checkpoint")
			})
		})
	}
}
