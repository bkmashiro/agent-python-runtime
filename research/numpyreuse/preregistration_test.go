package numpyreuse

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestFrozenCasesCoverEconomicsAndAdversarialBoundary(t *testing.T) {
	matrix := NewCaseMatrix()
	classes := map[string]int{}
	compute := map[string]bool{}
	payloads := map[string]bool{}
	gaps := map[uint32]bool{}
	consumers := map[uint32]bool{}
	for _, candidate := range matrix.Cases {
		classes[candidate.Class]++
		compute[candidate.ComputeClass] = true
		payloads[candidate.PayloadClass] = true
		gaps[candidate.LeadGapMillis] = true
		consumers[candidate.Consumers] = true
		if candidate.SourceSHA256 != digest([]byte(candidate.Source)) {
			t.Fatalf("source identity drift for %s", candidate.ID)
		}
		command := exec.Command("python3", "-c", "import ast,sys; ast.parse(sys.stdin.read())")
		command.Stdin = bytes.NewBufferString(candidate.Source)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("case %s syntax: %v: %s", candidate.ID, err, output)
		}
	}
	for _, required := range []string{"import_only", "elementwise", "reduction", "matrix"} {
		if !compute[required] {
			t.Fatalf("missing compute class %s: %v", required, compute)
		}
	}
	for _, required := range []string{"small", "medium", "large"} {
		if !payloads[required] {
			t.Fatalf("missing payload class %s: %v", required, payloads)
		}
	}
	for _, required := range []uint32{0, 10_000, 45_000} {
		if !gaps[required] {
			t.Fatalf("missing lead gap %d: %v", required, gaps)
		}
	}
	for _, required := range []uint32{1, 2, 4} {
		if !consumers[required] {
			t.Fatalf("missing consumer count %d: %v", required, consumers)
		}
	}
	if classes["economics"] != 10 || classes["adversarial"] != 8 || len(matrix.Cases) != 18 {
		t.Fatalf("classes=%v cases=%d", classes, len(matrix.Cases))
	}
}

func TestCampaignScheduleIsCompleteSeededAndDeterministic(t *testing.T) {
	first, second := CampaignCoordinates(), CampaignCoordinates()
	if len(first) != 240 || len(second) != 240 {
		t.Fatalf("coordinates=%d/%d", len(first), len(second))
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if !bytes.Equal(left, right) {
		t.Fatal("schedule is nondeterministic")
	}
	seen := map[CampaignCoordinate]bool{}
	for _, coordinate := range first {
		if seen[coordinate] {
			t.Fatalf("duplicate coordinate %+v", coordinate)
		}
		seen[coordinate] = true
	}
	if first[0] == (CampaignCoordinate{Platform: "darwin_arm64", Profile: "cold_end_to_end", CaseID: "numpy_import_small_gap0_c1", Treatment: "original_recompute", TrialIndex: 1}) {
		t.Fatal("schedule was not shuffled")
	}
}

func TestContractsSeparateMechanismFromEconomicsAndPermitMixedResults(t *testing.T) {
	matrix, err := SealCaseMatrix(NewCaseMatrix())
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := SealPreregistration(NewPreregistration(matrix.Identity))
	if err != nil {
		t.Fatal(err)
	}
	if !prereg.MechanismGate.RequireAllAdversarialControls || !prereg.MechanismGate.RequireFreshProducerAndConsumers || !prereg.MechanismGate.RequireNoAuthorityExpansion || !prereg.MechanismGate.RequireNoReplay {
		t.Fatalf("mechanism gate=%+v", prereg.MechanismGate)
	}
	if prereg.EconomicsInterpretation != "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure" || prereg.RequireUniversalPositiveEconomics {
		t.Fatalf("economics policy=%q universal=%v", prereg.EconomicsInterpretation, prereg.RequireUniversalPositiveEconomics)
	}
	if prereg.ParentP5RMechanismEvidenceSHA256 != ParentP5RMechanismEvidenceSHA256 {
		t.Fatalf("parent=%s", prereg.ParentP5RMechanismEvidenceSHA256)
	}
}

func TestCheckedInContractsAreCanonicalStrictAndObservationFree(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/numpy-result-reuse-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := DecodeCaseMatrix(matrixRaw)
	if err != nil || matrix.Identity != CaseMatrixIdentity {
		t.Fatalf("matrix identity=%s err=%v", matrix.Identity, err)
	}
	preregRaw, err := os.ReadFile("../../docs/evidence/numpy-result-reuse-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := DecodePreregistration(preregRaw)
	if err != nil || prereg.Identity != PreregistrationIdentity || prereg.CaseMatrixIdentity != matrix.Identity {
		t.Fatalf("prereg identity=%s err=%v", prereg.Identity, err)
	}
	for _, forbidden := range [][]byte{[]byte(`"observed_`), []byte(`"trial_records"`), []byte(`"gate_passed"`), []byte(`"median_nanos"`), []byte(`"observed_speedup"`)} {
		if bytes.Contains(matrixRaw, forbidden) || bytes.Contains(preregRaw, forbidden) {
			t.Fatalf("frozen input contains observation field %q", forbidden)
		}
	}
	unknown := append([]byte(nil), matrixRaw[:len(matrixRaw)-1]...)
	unknown = append(unknown, []byte(`,"observed_result":42}`)...)
	if _, err := DecodeCaseMatrix(unknown); err == nil {
		t.Fatal("matrix accepted observation field")
	}
}

func TestIndependentPreregistrationReviewerPassesWithoutTimingSamples(t *testing.T) {
	command := exec.Command("python3", "../../scripts/review-numpy-result-reuse-preregistration.py")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent review: %v: %s", err, output)
	}
	var report struct {
		Status                string `json:"status"`
		CoordinateCount       int    `json:"campaign_coordinate_count"`
		TimingSamplesObserved int    `json:"timing_samples_observed"`
	}
	if json.Unmarshal(output, &report) != nil || report.Status != "pass" || report.CoordinateCount != 240 || report.TimingSamplesObserved != 0 {
		t.Fatalf("independent report=%s", output)
	}
}
