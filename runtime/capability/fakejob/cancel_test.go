package fakejob_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakejob"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

func TestFakeJobCancelRequiresApprovalAndCommitsOnce(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, err := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := controller.Prepare(context.Background(), job.ID, job.Version)
	if err != nil || staged.OperationID == "" || staged.ManifestDigest == "" || staged.JobID != job.ID || staged.ExpectedVersion != job.Version {
		t.Fatalf("staged=%+v err=%v", staged, err)
	}
	credential := transaction.CommitCredential{Token: "1111111111111111111111111111111111111111111111111111111111111111"}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakejob.ErrCancelApprovalRequired) {
		t.Fatalf("unapproved err=%v", err)
	}
	if got := f.provider.Snapshot(job.ID); got.Status != "queued" {
		t.Fatalf("unapproved cancel mutated job: %+v", got)
	}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:cancel", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	receipt, err := controller.Commit(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.JobID != job.ID || receipt.ReceiptDigest == "" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	canceled := f.provider.Snapshot(job.ID)
	if canceled.Status != "canceled" || canceled.Version <= job.Version {
		t.Fatalf("canceled=%+v", canceled)
	}
	replayed, err := controller.Commit(context.Background(), credential, staged.OperationID)
	if err != nil || replayed != receipt || f.provider.Snapshot(job.ID).Version != canceled.Version {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err := f.coordinator.FinalizeWorkflow(f.tx.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFakeJobCancelRejectsVersionDriftWithoutOverwritingState(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	staged, _ := controller.Prepare(context.Background(), job.ID, job.Version)
	if err := f.provider.Advance(job.ID, job.Version, "running", nil, nil); err != nil {
		t.Fatal(err)
	}
	credential := transaction.CommitCredential{Token: "2222222222222222222222222222222222222222222222222222222222222222"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:drift", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakejob.ErrJobDrift) {
		t.Fatalf("drift err=%v", err)
	}
	if got := f.provider.Snapshot(job.ID); got.Status != "running" {
		t.Fatalf("drift overwritten: %+v", got)
	}
}

func TestFakeJobAmbiguousCancelReconcilesWithoutSecondMutation(t *testing.T) {
	f := newFixture(t, []byte("read-token"), []byte("control-token"))
	job := submit(t, f)
	controller, _ := fakejob.NewCancelController(f.coordinator, f.tx.ID, f.adapter)
	staged, _ := controller.Prepare(context.Background(), job.ID, job.Version)
	credential := transaction.CommitCredential{Token: "3333333333333333333333333333333333333333333333333333333333333333"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:ambiguous-cancel", "owner", time.Unix(1060, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	f.provider.SetAmbiguousNextCancel()
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakejob.ErrCancelReconciliation) {
		t.Fatalf("ambiguous err=%v", err)
	}
	canceled := f.provider.Snapshot(job.ID)
	if canceled.Status != "canceled" {
		t.Fatalf("job=%+v", canceled)
	}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakejob.ErrCancelReconciliation) || f.provider.Snapshot(job.ID).Version != canceled.Version {
		t.Fatalf("replay err=%v job=%+v", err, f.provider.Snapshot(job.ID))
	}
	receipt, err := controller.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.JobID != job.ID || f.provider.Snapshot(job.ID).Version != canceled.Version {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if _, err := f.coordinator.FinalizeWorkflow(f.tx.ID); err != nil {
		t.Fatal(err)
	}
}
