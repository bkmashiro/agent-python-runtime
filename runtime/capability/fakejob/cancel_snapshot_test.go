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

func TestCancelControllerSnapshotReopensAwaitingApproval(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	staged, _ := controller.Prepare(context.Background(), job.ID, job.Version)
	snapshot, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := fakejob.NewCancelControllerFromSnapshot(f.coordinator, f.adapter, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	credential := transaction.CommitCredential{Token: "4444444444444444444444444444444444444444444444444444444444444444"}
	if err := reopened.RegisterApproval(credential, staged.OperationID, "approval:reopen", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Commit(context.Background(), credential, staged.OperationID); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.Snapshot(job.ID); got.Status != "canceled" {
		t.Fatalf("job=%+v", got)
	}
}

func TestCancelControllerSnapshotReconcilesAmbiguousAfterReopen(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	staged, _ := controller.Prepare(context.Background(), job.ID, job.Version)
	credential := transaction.CommitCredential{Token: "5555555555555555555555555555555555555555555555555555555555555555"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:reopen-ambiguous", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	f.provider.SetAmbiguousNextCancel()
	_, _ = controller.Commit(context.Background(), credential, staged.OperationID)
	snapshot, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), credential.Token) {
		t.Fatal("snapshot leaked approval token")
	}
	reopened, err := fakejob.NewCancelControllerFromSnapshot(f.coordinator, f.adapter, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := reopened.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.JobID != job.ID {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	committedSnapshot, err := reopened.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := fakejob.NewCancelControllerFromSnapshot(f.coordinator, f.adapter, committedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := committed.Commit(context.Background(), credential, staged.OperationID)
	if err != nil || replayed != receipt {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
}

func TestCancelControllerSnapshotRejectsTampering(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	_, _ = controller.Prepare(context.Background(), job.ID, job.Version)
	snapshot, _ := controller.Snapshot()
	cases := []fakejob.CancelControllerSnapshot{snapshot, snapshot, snapshot}
	cases[0].SchemaVersion = 99
	cases[1].TransactionID = "tx:other"
	cases[2].Stages[0].ManifestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for index, candidate := range cases {
		if _, err := fakejob.NewCancelControllerFromSnapshot(f.coordinator, f.adapter, candidate); err == nil {
			t.Fatalf("tampered snapshot %d accepted", index)
		}
	}
}
