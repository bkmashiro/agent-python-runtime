package fakejob_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakejob"
	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func BenchmarkFakeJobDevelopmentCheckpoint(b *testing.B) {
	fixture := newFixture(b, []byte("read-token"), []byte("control-token"))
	job := submit(b, fixture)
	controller, err := fakejob.NewCancelController(fixture.coordinator, fixture.tx.ID, fixture.adapter)
	if err != nil {
		b.Fatal(err)
	}
	staged, err := controller.Prepare(context.Background(), job.ID, job.Version)
	if err != nil {
		b.Fatal(err)
	}
	credential := transaction.CommitCredential{Token: "9999999999999999999999999999999999999999999999999999999999999999"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:benchmark", "owner", time.Unix(1060, 0).UTC()); err != nil {
		b.Fatal(err)
	}
	fixture.provider.SetAmbiguousNextCancel()
	_, _ = controller.Commit(context.Background(), credential, staged.OperationID)
	store, err := devsnapshot.Open(filepath.Join(b.TempDir(), "checkpoints.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := fakejob.SaveDevelopmentCheckpoint(context.Background(), store, "benchmark:job", controller); err != nil {
		b.Fatal(err)
	}
	b.Run("save_ambiguous", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			if _, err := fakejob.SaveDevelopmentCheckpoint(context.Background(), store, "benchmark:job", controller); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(2, "components/checkpoint")
	})
	providerSnapshot, controllerSnapshot, err := fakejob.LoadDevelopmentCheckpoint(context.Background(), store, "benchmark:job")
	if err != nil {
		b.Fatal(err)
	}
	b.Run("load_and_reopen", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			provider, err := fakejob.NewProviderFromSnapshot(providerSnapshot, []byte("read-token"), []byte("control-token"))
			if err != nil {
				b.Fatal(err)
			}
			adapter, err := fakejob.NewAdapter(fakejob.Config{Resolver: fixture.resolver, ReadSecretRef: "jobs.read", ControlSecretRef: "jobs.control", RunIdentity: "run:jobs", TaskIdentity: "task:jobs", Tenant: "tenant:jobs", QueueAlias: "queue:test", PolicyVersion: "jobs:v1", LeaseDuration: time.Minute, Provider: provider})
			if err != nil {
				b.Fatal(err)
			}
			if _, err := fakejob.NewCancelControllerFromSnapshot(fixture.coordinator, adapter, controllerSnapshot); err != nil {
				b.Fatal(err)
			}
			provider.Close()
		}
		b.ReportMetric(2, "components/checkpoint")
	})
}
