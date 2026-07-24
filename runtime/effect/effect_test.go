package effect_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/effect"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
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

type effectIDs struct{ next int }

func (ids *effectIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_%d", prefix, ids.next), nil
}

func TestIrreversibleOutboxRequiresExactApprovalAndCommitsOnce(t *testing.T) {
	now := time.Unix(1900, 0).UTC()
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &effectIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run_outbox", CatalogDigest: effect.Digest("catalog"), Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(transaction.ProposeRequest{TransactionID: tx.ID, ToolID: "mail.send", HandlerVersion: "v1", EffectClass: transaction.EffectIrreversible, Policy: transaction.PolicyUserApprovalRequired, PolicyVersion: "policy_v1", ArgumentDigest: effect.Digest("args")})
	if err != nil {
		t.Fatal(err)
	}
	outbox := effect.NewOutbox()
	if _, err := outbox.PrepareIrreversible(coordinator, operation.ID, "user@example.com", "body"); !errors.Is(err, effect.ErrRecipientDenied) {
		t.Fatalf("real recipient err=%v", err)
	}
	staged, err := outbox.PrepareIrreversible(coordinator, operation.ID, "user@example.invalid", "body")
	if err != nil || staged.ManifestDigest != operation.ManifestDigest {
		t.Fatalf("staged=%+v err=%v", staged, err)
	}
	if _, err := outbox.PrepareIrreversible(coordinator, operation.ID, "user@example.invalid", "changed"); !errors.Is(err, effect.ErrConflict) {
		t.Fatalf("changed staged arguments err=%v", err)
	}
	if outbox.CommittedCount() != 0 {
		t.Fatal("prepare-only outbox produced an irreversible event")
	}
	if _, exists := reflect.TypeOf(outbox).MethodByName("CommitDispatch"); exists {
		t.Fatal("outbox exposes executable irreversible dispatch without provider contract")
	}
	if _, exists := reflect.TypeOf(outbox).MethodByName("Rollback"); exists {
		t.Fatal("irreversible outbox exposes Rollback")
	}
	if _, exists := reflect.TypeOf(outbox).MethodByName("Commit"); exists {
		t.Fatal("outbox exposes caller-asserted authority commit")
	}
}
