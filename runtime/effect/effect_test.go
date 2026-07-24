package effect_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/effect"
)

func TestConfigApplyRollbackIsIdempotentAndBlocksVersionDrift(t *testing.T) {
	store := effect.NewConfigStore(map[string]string{"feature": "off"})
	applied, err := store.Apply("apply_1", "feature", 1, "on")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.Apply("apply_1", "feature", 1, "on")
	if err != nil || replayed != applied {
		t.Fatalf("apply replay=%+v err=%v", replayed, err)
	}
	rolled, err := store.Rollback("rollback_1", applied.UndoToken)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.RestoredDigest != applied.BeforeDigest || store.Value("feature") != "off" {
		t.Fatalf("rollback did not restore projection: %+v value=%q", rolled, store.Value("feature"))
	}
	replayedRollback, err := store.Rollback("rollback_1", applied.UndoToken)
	if err != nil || replayedRollback != rolled {
		t.Fatalf("rollback replay=%+v err=%v", replayedRollback, err)
	}

	second, err := store.Apply("apply_2", "feature", rolled.Version, "on")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply("external_1", "feature", second.PostVersion, "newer"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rollback("rollback_2", second.UndoToken); !errors.Is(err, effect.ErrVersionConflict) {
		t.Fatalf("drift rollback err=%v", err)
	}
	if store.Value("feature") != "newer" {
		t.Fatal("conflicting rollback mutated newer value")
	}
	history := store.History()
	if len(history) != 4 || history[0].Kind != "apply" || history[1].Kind != "rollback" || history[0].PostDigest == "on" {
		t.Fatalf("config history=%+v", history)
	}
}

func TestReservationCompensationFailureIsBoundedAndInspectable(t *testing.T) {
	store := effect.NewReservationStore()
	reserved, err := store.Reserve("reserve_failure", "sku_2", 1)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.RecordCompensationFailure("comp_failed", reserved.ReservationID, "provider_timeout")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.ErrorCode != "provider_timeout" || !store.Active(reserved.ReservationID) {
		t.Fatalf("failed compensation=%+v active=%v", failed, store.Active(reserved.ReservationID))
	}
	if _, err := store.Compensate("comp_retry", reserved.ReservationID); err != nil {
		t.Fatal(err)
	}
	history := store.History()
	if len(history) != 3 || history[1].Kind != "compensation" || history[1].Status != "failed" || history[2].Status != "succeeded" {
		t.Fatalf("partial compensation history=%+v", history)
	}
}

func TestReservationCompensationIsDistinctAndPreservesOriginalHistory(t *testing.T) {
	store := effect.NewReservationStore()
	reserved, err := store.Reserve("reserve_1", "sku_1", 2)
	if err != nil {
		t.Fatal(err)
	}
	compensated, err := store.Compensate("comp_1", reserved.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.Compensate("comp_1", reserved.ReservationID)
	if err != nil || replayed != compensated {
		t.Fatalf("compensation replay=%+v err=%v", replayed, err)
	}
	if compensated.Kind != "compensation" || store.Active(reserved.ReservationID) {
		t.Fatalf("not compensated: %+v", compensated)
	}
	history := store.History()
	if len(history) != 2 || history[0].Kind != "reservation" || history[1].Kind != "compensation" {
		t.Fatalf("history=%+v", history)
	}
}

func TestIrreversibleOutboxRequiresExactApprovalAndCommitsOnce(t *testing.T) {
	outbox := effect.NewOutbox()
	if _, err := outbox.Prepare("prepare_bad", "user@example.com", "body"); !errors.Is(err, effect.ErrRecipientDenied) {
		t.Fatalf("real recipient err=%v", err)
	}
	staged, err := outbox.Prepare("prepare_1", "user@example.invalid", "body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.Prepare("prepare_1", "user@example.invalid", "changed"); !errors.Is(err, effect.ErrConflict) {
		t.Fatalf("changed staged arguments err=%v", err)
	}
	if _, err := outbox.Commit("commit_1", staged.ManifestDigest, false); !errors.Is(err, effect.ErrAuthorityDenied) {
		t.Fatalf("forged authority err=%v", err)
	}
	if _, err := outbox.Commit("commit_1", effect.Digest("changed"), true); !errors.Is(err, effect.ErrManifestMismatch) {
		t.Fatalf("changed manifest err=%v", err)
	}
	committed, err := outbox.Commit("commit_1", staged.ManifestDigest, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.Commit("commit_2", staged.ManifestDigest, true); !errors.Is(err, effect.ErrConflict) {
		t.Fatalf("commit identity drift err=%v", err)
	}
	second, err := outbox.Prepare("prepare_2", "user@example.invalid", "correction")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.Commit("commit_2", second.ManifestDigest, true); !errors.Is(err, effect.ErrConflict) {
		t.Fatalf("burned commit identity was reused err=%v", err)
	}
	if _, err := outbox.Commit("commit_3", second.ManifestDigest, true); err != nil {
		t.Fatal(err)
	}
	replayed, err := outbox.Commit("commit_1", staged.ManifestDigest, true)
	if err != nil || replayed != committed || outbox.CommittedCount() != 2 {
		t.Fatalf("commit replay=%+v count=%d err=%v", replayed, outbox.CommittedCount(), err)
	}
	if _, exists := reflect.TypeOf(outbox).MethodByName("Rollback"); exists {
		t.Fatal("irreversible outbox exposes Rollback")
	}
}
