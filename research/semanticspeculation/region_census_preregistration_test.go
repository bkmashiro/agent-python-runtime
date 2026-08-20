package semanticspeculation

import (
	"bytes"
	"os"
	"testing"
)

func TestCheckedInPhase4RegionContractsAreCanonicalFrozenAndStrict(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-region-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := DecodePhase4RegionCaseMatrix(matrixRaw)
	if err != nil || len(matrix.Cases) != 12 || len(matrix.PilotCaseIDs) != 1 {
		t.Fatalf("matrix=%+v err=%v", matrix, err)
	}
	preregRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-region-census-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := DecodePhase4RegionPreregistration(preregRaw)
	if err != nil || prereg.MatrixIdentity != Phase4RegionMatrixIdentity || prereg.OpportunityGate.RequiredPositiveNonPilotCases != 3 {
		t.Fatalf("preregistration=%+v err=%v", prereg, err)
	}

	positive := 0
	controls := map[string]bool{}
	for _, candidate := range matrix.Cases {
		if candidate.ExpectedLocalReusable && candidate.ID != phase4RegionPilotCaseID {
			positive++
		}
		for _, tag := range candidate.RequiredControlTags {
			controls[tag] = true
		}
	}
	for _, required := range []string{"effect_after_focus", "effect_before_focus", "effects_before_and_after_focus", "unknown_call", "heap_mutation", "may_raise", "alias_identity", "opaque_control", "non_json_transport"} {
		if !controls[required] {
			t.Fatalf("missing control %q", required)
		}
	}
	if positive != 3 {
		t.Fatalf("non-pilot positives=%d", positive)
	}
}

func TestPhase4RegionContractsRejectMutationAndUnknownFields(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-region-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Replace(matrixRaw, []byte("seed = 40"), []byte("seed = 41"), 1)
	if _, err := DecodePhase4RegionCaseMatrix(mutated); err == nil {
		t.Fatal("matrix accepted source mutation")
	}
	unknown := append([]byte(nil), matrixRaw[:len(matrixRaw)-2]...)
	unknown = append(unknown, []byte(`,"observed_result":"forbidden"}\n`)...)
	if _, err := DecodePhase4RegionCaseMatrix(unknown); err == nil {
		t.Fatal("matrix accepted post-freeze observed result")
	}

	preregRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-region-census-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePhase4RegionPreregistration(append(preregRaw, '\n')); err == nil {
		t.Fatal("preregistration accepted trailing data")
	}
}
