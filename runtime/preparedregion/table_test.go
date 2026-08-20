package preparedregion

import (
	"encoding/json"
	"errors"
	"testing"
)

func preparedRegionFixture(t *testing.T, payload string) (PreparedRegionDecision, PreparedRegionCapsule) {
	t.Helper()
	_, decision, err := SealPreparedRegionDecision(validPreparedRegionBinding())
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	return decision, capsule
}

func TestPreparedRegionTableClaimsPinnedCapsuleExactlyOnce(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `42`)
	table, err := NewPreparedRegionTable([]PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := table.Claim(decision.IdentitySHA256)
	if err != nil || string(payload) != `42` {
		t.Fatalf("claim=(%s,%v)", payload, err)
	}
	if _, err := table.Claim(decision.IdentitySHA256); !errors.Is(err, ErrPreparedRegionConsumed) {
		t.Fatalf("second claim error=%v", err)
	}
	evidence := table.Evidence()
	if evidence.Ready != 0 || evidence.Consumed != 1 || evidence.Claims != 1 || evidence.RejectedClaims != 1 || evidence.PayloadBytes != 2 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestPreparedRegionTableRejectsMissingMismatchedAndUnreadyClaims(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `true`)
	otherBinding := validPreparedRegionBinding()
	otherBinding.SourceSHA256 = testDigestB
	_, otherDecision, err := SealPreparedRegionDecision(otherBinding)
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		entries []PreparedRegionEntry
		claim   string
		want    error
	}{
		"missing":    {nil, decision.IdentitySHA256, ErrPreparedRegionMissing},
		"mismatched": {[]PreparedRegionEntry{{Decision: otherDecision, Capsule: capsule}}, otherDecision.IdentitySHA256, ErrInvalidPreparedRegion},
		"unready":    {[]PreparedRegionEntry{{Decision: decision}}, decision.IdentitySHA256, ErrPreparedRegionUnready},
	} {
		t.Run(name, func(t *testing.T) {
			table, tableErr := NewPreparedRegionTable(test.entries)
			if name == "mismatched" {
				if !errors.Is(tableErr, test.want) {
					t.Fatalf("table error=%v want=%v", tableErr, test.want)
				}
				return
			}
			if tableErr != nil {
				t.Fatal(tableErr)
			}
			if _, claimErr := table.Claim(test.claim); !errors.Is(claimErr, test.want) {
				t.Fatalf("claim error=%v want=%v", claimErr, test.want)
			}
		})
	}
}

func TestPreparedRegionTableValidateReadyDoesNotConsumeAndBindsExactCapsule(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `42`)
	table, err := NewPreparedRegionTable([]PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.ValidateReady(decision, capsule); err != nil {
		t.Fatal(err)
	}
	if evidence := table.Evidence(); evidence.Ready != 1 || evidence.Consumed != 0 || evidence.Claims != 0 {
		t.Fatalf("preselection consumed capsule: %+v", evidence)
	}
	_, otherCapsule, err := SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`43`))
	if err != nil {
		t.Fatal(err)
	}
	if err := table.ValidateReady(decision, otherCapsule); err == nil {
		t.Fatal("preselection accepted a different capsule")
	}
	payload, err := table.Claim(decision.IdentitySHA256)
	if err != nil || string(payload) != "42" {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
}

func TestPreparedRegionTablePublishesLateIntoEmptyTableExactlyOnce(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `42`)
	table, err := NewPreparedRegionTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Publish(decision, capsule); err != nil {
		t.Fatal(err)
	}
	if err := table.Publish(decision, capsule); !errors.Is(err, ErrPreparedRegionAlreadyPublished) {
		t.Fatalf("second publish error=%v", err)
	}
	if err := table.ValidateReady(decision, capsule); err != nil {
		t.Fatal(err)
	}
	payload, err := table.Claim(decision.IdentitySHA256)
	if err != nil || string(payload) != "42" {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
	if evidence := table.Evidence(); evidence.Ready != 0 || evidence.Consumed != 1 || evidence.Claims != 1 || evidence.RejectedClaims != 0 || evidence.PayloadBytes != 2 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestPreparedRegionTableLatePublishRejectsClosedAndInvalidInputs(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `true`)
	table, err := NewPreparedRegionTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	tampered := capsule
	tampered.PayloadSHA256 = testDigestB
	if err := table.Publish(decision, tampered); !errors.Is(err, ErrInvalidPreparedRegion) {
		t.Fatalf("tampered publish error=%v", err)
	}
	if err := table.Close(); err != nil {
		t.Fatal(err)
	}
	if err := table.Publish(decision, capsule); !errors.Is(err, ErrPreparedRegionClosed) {
		t.Fatalf("closed publish error=%v", err)
	}
}

func TestPreparedRegionTableCloseDiscardsReadyEntries(t *testing.T) {
	decision, capsule := preparedRegionFixture(t, `-7`)
	table, err := NewPreparedRegionTable([]PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := table.Claim(decision.IdentitySHA256); !errors.Is(err, ErrPreparedRegionClosed) {
		t.Fatalf("claim after close error=%v", err)
	}
	evidence := table.Evidence()
	if evidence.Ready != 0 || evidence.Discarded != 1 || evidence.Consumed != 0 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if err := table.Close(); err != nil {
		t.Fatalf("second close=%v", err)
	}
}
