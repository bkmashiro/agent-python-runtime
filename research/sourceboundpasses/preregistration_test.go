package sourceboundpasses

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func TestPreregistrationFreezesStagesOutcomesBoundsAndControls(t *testing.T) {
	value := PreregistrationV1()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value.Stages, []Stage{StagePrefixOverlay, StageHybridPreparePatch, StageWholeProgramPatch, StageMultiProgramPatch}) {
		t.Fatalf("stages=%v", value.Stages)
	}
	if !reflect.DeepEqual(value.Outcomes, []Outcome{OutcomeApplied, OutcomeDiscarded, OutcomePreparedAwaitingFinal, OutcomeRejected}) {
		t.Fatalf("outcomes=%v", value.Outcomes)
	}
	if value.Bounds.MaxPasses != 16 || value.Bounds.MaxASTNodes != 8192 || value.Bounds.MaxSourceBytes != 1<<20 || value.Bounds.MaxPreparationBytes != 8<<20 || value.Bounds.MaxReanalyses != 16 {
		t.Fatalf("bounds=%+v", value.Bounds)
	}
	wantComparators := []ComparatorField{
		ComparatorOriginalSourceSHA256, ComparatorDerivedSourceSHA256, ComparatorOriginalASTSHA256,
		ComparatorDerivedASTSHA256, ComparatorPassOrder, ComparatorLogicalEvents,
		ComparatorPhysicalEvents, ComparatorResultSHA256, ComparatorExceptionClass,
		ComparatorExceptionOrder, ComparatorWorkspaceDisposition, ComparatorRejectionReason,
	}
	if !reflect.DeepEqual(value.ComparatorFields, wantComparators) {
		t.Fatalf("comparators=%v", value.ComparatorFields)
	}
	wantForbidden := []ForbiddenClaim{
		ForbiddenPresealExecution, ForbiddenCapabilityAmplification, ForbiddenPostEffectReplay,
		ForbiddenGenericRollback, ForbiddenUnmatchedPerformance,
	}
	if !reflect.DeepEqual(value.ForbiddenClaims, wantForbidden) {
		t.Fatalf("forbidden=%v", value.ForbiddenClaims)
	}
	wantCases := []string{
		"branch_not_taken", "cancellation", "earlier_exception", "external_write", "freshness_drift", "invalid_final_suffix",
		"mutable_alias", "plan_drift", "positive_hybrid_preparation", "positive_prefix_overlay", "positive_pure_scalar_patch",
		"privacy_drift", "unsupported_syntax", "workspace_drift", "zero_iteration",
	}
	if len(value.Cases) != len(wantCases) {
		t.Fatalf("cases=%d", len(value.Cases))
	}
	for index, want := range wantCases {
		if value.Cases[index].ID != want {
			t.Fatalf("case[%d]=%s", index, value.Cases[index].ID)
		}
	}
}

func TestPreregistrationEncodingIsCanonicalBodyFreeAndCheckedIn(t *testing.T) {
	value := PreregistrationV1()
	raw, err := EncodePreregistration(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("source_body"), []byte("result_body"), []byte("workspace_body"), []byte("private-data")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("encoding leaked %q", forbidden)
		}
	}
	decoded, err := DecodePreregistration(raw)
	if err != nil || decoded.IdentitySHA256 != value.IdentitySHA256 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	checkedIn, err := os.ReadFile("../../docs/evidence/source-bound-pass-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(checkedIn, append(raw, '\n')) {
		t.Fatal("checked-in preregistration is not canonical")
	}
}

func TestPreregistrationRejectsUnknownFieldsAndIdentityDrift(t *testing.T) {
	raw, err := EncodePreregistration(PreregistrationV1())
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), raw[:len(raw)-1]...)
	mutated = append(mutated, []byte(`,"unknown":true}`)...)
	if _, err := DecodePreregistration(mutated); err == nil {
		t.Fatal("accepted unknown field")
	}
	mutated = append([]byte(nil), raw...)
	needle := []byte(`"max_passes":16`)
	if !bytes.Contains(mutated, needle) {
		t.Fatal("missing bound encoding")
	}
	mutated = bytes.Replace(mutated, needle, []byte(`"max_passes":15`), 1)
	if _, err := DecodePreregistration(mutated); err == nil {
		t.Fatal("accepted identity drift")
	}
}
