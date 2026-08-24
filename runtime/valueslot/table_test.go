package valueslot_test

import (
	"bytes"
	"errors"
	"testing"

	preparedfixture "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
)

func TestCanonicalNumpySumProducerAttestsPayloadAndFreshRuns(t *testing.T) {
	fixture := preparedfixture.CanonicalFixture()
	template, err := valueslot.NewCanonicalNumpyInt64SumTable(fixture, "private-cohort")
	if err != nil || !template.IsCanonicalNumpyInt64Sum() {
		t.Fatalf("template=%v attested=%t", err, template != nil && template.IsCanonicalNumpyInt64Sum())
	}
	first, err := template.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	second, err := template.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	for index, table := range []*valueslot.Table{first, second} {
		payload, strategy, claimErr := table.Claim("slot-numpy-sum-v1")
		if claimErr != nil || string(payload) != valueslot.CanonicalNumpyInt64Sum || strategy != valueslot.StrategyInlineJSON {
			t.Fatalf("run=%d payload=%q strategy=%s err=%v", index, payload, strategy, claimErr)
		}
		if err := table.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := template.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNumpySumRejectsDriftAndGenericMetadataForgery(t *testing.T) {
	fixture := preparedfixture.CanonicalFixture()
	fixture[len(fixture)-1] ^= 1
	if _, err := valueslot.NewCanonicalNumpyInt64SumTable(fixture, "private-cohort"); !errors.Is(err, valueslot.ErrInvalidObject) {
		t.Fatalf("drift error=%v", err)
	}
	object, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("0"), "numpy-int64-sum-v1", valueslot.CanonicalNumpyInt64FileSHA256, "private-cohort")
	if err != nil {
		t.Fatal(err)
	}
	forged, err := valueslot.NewTable([]valueslot.Entry{{
		Spec: valueslot.SlotSpec{
			ID: "slot-numpy-sum-v1", SourceOccurrence: "line-4:result",
			ProducerIdentity: "numpy-int64-sum-v1", InputIdentity: valueslot.CanonicalNumpyInt64FileSHA256,
			Kind: valueslot.KindJSONScalar, MaxBytes: 32, PrivacyPartition: "private-cohort",
			ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
		},
		Object: object, Strategy: valueslot.StrategyInlineJSON,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if forged.IsCanonicalNumpyInt64Sum() {
		t.Fatal("generic metadata forged an adapter proof")
	}
	if err := forged.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInlineJSONScalarIsCanonicalAndSingleUse(t *testing.T) {
	spec := scalarSpec()
	object, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("42"), "producer-v1", "input-v1", "run-private")
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{Spec: spec, Object: object, Strategy: valueslot.StrategyInlineJSON}})
	if err != nil {
		t.Fatal(err)
	}
	payload, strategy, err := table.Claim("slot-scalar")
	if err != nil || string(payload) != "42" || strategy != valueslot.StrategyInlineJSON {
		t.Fatalf("payload=%q strategy=%s err=%v", payload, strategy, err)
	}
	if _, _, err := table.Claim("slot-scalar"); !errors.Is(err, valueslot.ErrAlreadyClaimed) {
		t.Fatalf("second claim err=%v", err)
	}
	evidence := table.Evidence()
	if evidence.Ready != 1 || evidence.Claims != 1 || evidence.CopiedBytes != 2 || evidence.Rejected != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestPreparedObjectRejectsNonCanonicalOrUnsupportedScalar(t *testing.T) {
	for _, payload := range [][]byte{[]byte("01"), []byte("1.0"), []byte(`"text"`), []byte("9223372036854775808"), []byte(" true ")} {
		if _, err := valueslot.NewPreparedObject(valueslot.KindJSONScalar, payload, "producer-v1", "input-v1", "run-private"); !errors.Is(err, valueslot.ErrInvalidObject) {
			t.Fatalf("payload=%q err=%v", payload, err)
		}
	}
}

func TestImmutableBytesClaimsArePrivateCopies(t *testing.T) {
	spec := valueslot.SlotSpec{
		ID: "slot-bytes", SourceOccurrence: "source-line-2", ProducerIdentity: "producer-v1", InputIdentity: "input-v1",
		Kind: valueslot.KindImmutableBytes, MaxBytes: 16, PrivacyPartition: "run-private", ClaimPolicy: valueslot.ClaimPrivateCopy, MaxClaims: 2,
	}
	object, err := valueslot.NewPreparedObject(valueslot.KindImmutableBytes, []byte("immutable"), "producer-v1", "input-v1", "run-private")
	if err != nil {
		t.Fatal(err)
	}
	table, err := valueslot.NewTable([]valueslot.Entry{{Spec: spec, Object: object, Strategy: valueslot.StrategyPrivateCopy}})
	if err != nil {
		t.Fatal(err)
	}
	first, strategy, err := table.Claim("slot-bytes")
	if err != nil || strategy != valueslot.StrategyPrivateCopy {
		t.Fatalf("strategy=%s err=%v", strategy, err)
	}
	first[0] = 'X'
	second, _, err := table.Claim("slot-bytes")
	if err != nil || string(second) != "immutable" || bytes.Equal(first, second) {
		t.Fatalf("first=%q second=%q err=%v", first, second, err)
	}
	if _, _, err := table.Claim("slot-bytes"); !errors.Is(err, valueslot.ErrAlreadyClaimed) {
		t.Fatalf("third claim err=%v", err)
	}
}

func TestFreshTablesCanShareOneImmutablePhysicalObject(t *testing.T) {
	object, err := valueslot.NewPreparedObject(valueslot.KindImmutableBytes, []byte("shared"), "producer-v1", "input-v1", "private-cohort-a")
	if err != nil {
		t.Fatal(err)
	}
	newConsumer := func(occurrence string) *valueslot.Table {
		t.Helper()
		spec := valueslot.SlotSpec{
			ID: "slot-shared", SourceOccurrence: occurrence, ProducerIdentity: "producer-v1", InputIdentity: "input-v1",
			Kind: valueslot.KindImmutableBytes, MaxBytes: 16, PrivacyPartition: "private-cohort-a", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
		}
		table, createErr := valueslot.NewTable([]valueslot.Entry{{Spec: spec, Object: object, Strategy: valueslot.StrategyPrivateCopy}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return table
	}
	firstTable, secondTable := newConsumer("agent-a-line-1"), newConsumer("agent-b-line-1")
	if object.ConsumerCount() != 2 || object.CanEvict() {
		t.Fatalf("consumers=%d can_evict=%t", object.ConsumerCount(), object.CanEvict())
	}
	first, _, firstErr := firstTable.Claim("slot-shared")
	second, _, secondErr := secondTable.Claim("slot-shared")
	if firstErr != nil || secondErr != nil || string(first) != "shared" || string(second) != "shared" {
		t.Fatalf("first=%q second=%q firstErr=%v secondErr=%v", first, second, firstErr, secondErr)
	}
	first[0] = 'X'
	if string(second) != "shared" || firstTable.BackingIdentity("slot-shared") != secondTable.BackingIdentity("slot-shared") {
		t.Fatalf("first=%q second=%q identities=%q/%q", first, second, firstTable.BackingIdentity("slot-shared"), secondTable.BackingIdentity("slot-shared"))
	}
	if firstTable.Close() != nil || object.ConsumerCount() != 1 || secondTable.Close() != nil || !object.CanEvict() {
		t.Fatalf("consumers=%d can_evict=%t", object.ConsumerCount(), object.CanEvict())
	}
}

func TestPreparedObjectCannotCrossPrivacyPartition(t *testing.T) {
	object, _ := valueslot.NewPreparedObject(valueslot.KindImmutableBytes, []byte("shared"), "producer-v1", "input-v1", "private-cohort-a")
	spec := valueslot.SlotSpec{
		ID: "slot-shared", SourceOccurrence: "agent-b-line-1", ProducerIdentity: "producer-v1", InputIdentity: "input-v1",
		Kind: valueslot.KindImmutableBytes, MaxBytes: 16, PrivacyPartition: "private-cohort-b", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
	}
	if _, err := valueslot.NewTable([]valueslot.Entry{{Spec: spec, Object: object, Strategy: valueslot.StrategyPrivateCopy}}); !errors.Is(err, valueslot.ErrInvalidEntry) {
		t.Fatalf("cross-partition err=%v", err)
	}
}

func TestCloseDiscardsUnclaimedSlotsAndRejectsFurtherClaims(t *testing.T) {
	object, _ := valueslot.NewPreparedObject(valueslot.KindJSONScalar, []byte("true"), "producer-v1", "input-v1", "run-private")
	table, err := valueslot.NewTable([]valueslot.Entry{{Spec: scalarSpec(), Object: object, Strategy: valueslot.StrategyInlineJSON}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := table.Claim("slot-scalar"); !errors.Is(err, valueslot.ErrClosed) {
		t.Fatalf("closed claim err=%v", err)
	}
	evidence := table.Evidence()
	if evidence.Discarded != 1 || evidence.Claims != 0 || evidence.Rejected != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func scalarSpec() valueslot.SlotSpec {
	return valueslot.SlotSpec{
		ID: "slot-scalar", SourceOccurrence: "source-line-1", ProducerIdentity: "producer-v1", InputIdentity: "input-v1",
		Kind: valueslot.KindJSONScalar, MaxBytes: 32, PrivacyPartition: "run-private", ClaimPolicy: valueslot.ClaimSingleUse, MaxClaims: 1,
	}
}
