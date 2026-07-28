package fakejob_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakejob"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func TestFakeJobProviderSnapshotReopensAmbiguousCancel(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	staged, _ := controller.Prepare(context.Background(), job.ID, job.Version)
	credential := transaction.CommitCredential{Token: "6666666666666666666666666666666666666666666666666666666666666666"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:provider-reopen", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	f.provider.SetAmbiguousNextCancel()
	_, _ = controller.Commit(context.Background(), credential, staged.OperationID)
	controllerSnapshot, _ := controller.Snapshot()
	providerSnapshot, err := f.provider.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(providerSnapshot)
	for _, marker := range []string{"read-token", "control-token", credential.Token} {
		if strings.Contains(string(encoded), marker) {
			t.Fatalf("provider snapshot leaked marker %q", marker)
		}
	}
	reopenedProvider, err := fakejob.NewProviderFromSnapshot(providerSnapshot, []byte("read-token"), []byte("control-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedProvider.Close()
	reopenedAdapter, err := fakejob.NewAdapter(fakejob.Config{Resolver: f.resolver, ReadSecretRef: "jobs.read", ControlSecretRef: "jobs.control", RunIdentity: "run:jobs", TaskIdentity: "task:jobs", Tenant: "tenant:jobs", QueueAlias: "queue:test", PolicyVersion: "jobs:v1", LeaseDuration: time.Minute, Provider: reopenedProvider})
	if err != nil {
		t.Fatal(err)
	}
	reopenedController, err := fakejob.NewCancelControllerFromSnapshot(f.coordinator, reopenedAdapter, controllerSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := reopenedController.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.JobID != job.ID || reopenedProvider.Snapshot(job.ID).Status != "canceled" {
		t.Fatalf("receipt=%+v job=%+v err=%v", receipt, reopenedProvider.Snapshot(job.ID), err)
	}
}

func TestFakeJobProviderSnapshotPreservesLogsArtifactsAndCounters(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	if err := f.provider.Advance(job.ID, job.Version, "succeeded", []fakejob.LogLine{{Stream: "stdout", Text: "done"}}, []fakejob.Artifact{{Name: "result.json", SHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Bytes: 42}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.provider.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := fakejob.NewProviderFromSnapshot(snapshot, []byte("read-token"), []byte("control-token"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Snapshot(job.ID)
	if got.Status != "succeeded" || got.Version <= job.Version || reopened.JobCount() != 1 {
		t.Fatalf("job=%+v count=%d", got, reopened.JobCount())
	}
	roundTrip, err := reopened.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(roundTrip.Jobs) != 1 || len(roundTrip.Jobs[0].Logs) != 1 || len(roundTrip.Jobs[0].Artifacts) != 1 {
		t.Fatalf("snapshot=%+v", roundTrip)
	}
}

func TestFakeJobProviderSnapshotRejectsTampering(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	_ = submit(t, f)
	snapshot, _ := f.provider.ExportSnapshot()
	cases := []fakejob.ProviderSnapshot{snapshot, snapshot, snapshot}
	cases[0].SchemaVersion = 99
	cases[1].Jobs[0].Job.Version = 0
	cases[2].Jobs[0].OperationID = "operation:missing"
	for index, candidate := range cases {
		if _, err := fakejob.NewProviderFromSnapshot(candidate, []byte("read-token"), []byte("control-token")); err == nil {
			t.Fatalf("tampered snapshot %d accepted", index)
		}
	}
}
