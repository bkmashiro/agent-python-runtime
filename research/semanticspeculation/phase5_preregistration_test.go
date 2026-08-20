package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestPhase5FrozenCasesAreSyntacticallyValidBoundAndBalanced(t *testing.T) {
	matrix := NewPhase5CaseMatrix()
	classes := map[string]int{}
	eligible := 0
	for _, candidate := range matrix.Cases {
		classes[candidate.Class]++
		if candidate.EconomicsEligible {
			eligible++
		}
		if candidate.SourceSHA256 != syntheticDigest([]byte(candidate.Source)) {
			t.Fatalf("source identity drift for %s", candidate.ID)
		}
		command := exec.Command("python3", "-c", "import ast,sys; ast.parse(sys.stdin.read())")
		command.Stdin = bytes.NewBufferString(candidate.Source)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("case %s syntax: %v: %s", candidate.ID, err, output)
		}
	}
	if len(matrix.Cases) != 11 || eligible != 4 || classes["pilot_only"] != 1 || classes["positive"] != 4 || classes["negative_control"] != 2 || classes["adversarial"] != 4 {
		t.Fatalf("cases=%d eligible=%d classes=%v", len(matrix.Cases), eligible, classes)
	}
}

func TestPhase5CampaignScheduleIsCompleteSeededAndDeterministic(t *testing.T) {
	first, second := Phase5CampaignCoordinates(), Phase5CampaignCoordinates()
	if len(first) != 80 || len(second) != 80 {
		t.Fatalf("coordinates=%d/%d", len(first), len(second))
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if !bytes.Equal(left, right) {
		t.Fatal("phase 5 schedule is nondeterministic")
	}
	seen := map[Phase5CampaignCoordinate]bool{}
	caseCounts := map[string]int{}
	for _, coordinate := range first {
		if seen[coordinate] {
			t.Fatalf("duplicate coordinate %+v", coordinate)
		}
		seen[coordinate] = true
		caseCounts[coordinate.CaseID]++
	}
	for _, count := range caseCounts {
		if count != 20 {
			t.Fatalf("case count=%d want 20", count)
		}
	}
	if len(caseCounts) != 4 {
		t.Fatalf("eligible cases=%v", caseCounts)
	}
	unshuffled := Phase5CampaignCoordinate{Profile: "cold_end_to_end", CaseID: "scalar_add_16_gap0", Treatment: "original_unchanged", TrialIndex: 1}
	if first[0] == unshuffled {
		t.Fatal("phase 5 schedule was not shuffled")
	}
}

func TestPhase5ContractsFreezeLineageIndependentGatesAndNoGoAction(t *testing.T) {
	matrix, err := SealPhase5CaseMatrix(NewPhase5CaseMatrix())
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := SealPhase5Preregistration(NewPhase5Preregistration(matrix.Identity))
	if err != nil {
		t.Fatal(err)
	}
	if matrix.ParentPhase3MatrixIdentity != Phase3FrozenMatrixIdentity || matrix.ParentPhase4MatrixIdentity != Phase4ExtensionMatrixIdentity || matrix.ParentPhase4PreregIdentity != Phase4PreregistrationIdentity || prereg.CaseMatrixIdentity != matrix.Identity {
		t.Fatalf("matrix=%+v prereg=%+v", matrix, prereg)
	}
	if !prereg.MechanismGate.RequireAllExactControlsPass || !prereg.MechanismGate.RequireNoReplayOrRecomputation || prereg.EconomicsGate.MinimumPositiveTrialsPerCell != 4 || prereg.EconomicsGate.RequiredPassingCoordinatesPerProfile != 2 || !prereg.EconomicsGate.RequireBothProfilesPass || prereg.FailureAction != "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority" {
		t.Fatalf("preregistration gates=%+v/%+v action=%s", prereg.MechanismGate, prereg.EconomicsGate, prereg.FailureAction)
	}
}

func TestCheckedInPhase5ContractsAreCanonicalStrictAndObservationFree(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase5-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := DecodePhase5CaseMatrix(matrixRaw)
	if err != nil || matrix.Identity != Phase5CaseMatrixIdentity {
		t.Fatalf("matrix identity=%s err=%v", matrix.Identity, err)
	}
	preregRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase5-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := DecodePhase5Preregistration(preregRaw)
	if err != nil || prereg.Identity != Phase5PreregistrationIdentity || prereg.CaseMatrixIdentity != matrix.Identity {
		t.Fatalf("prereg=%+v err=%v", prereg, err)
	}
	for _, forbidden := range [][]byte{[]byte(`"observed_`), []byte(`"trial_records"`), []byte(`"gate_passed"`), []byte(`"median_nanos"`)} {
		if bytes.Contains(matrixRaw, forbidden) || bytes.Contains(preregRaw, forbidden) {
			t.Fatalf("frozen input contains observation field %q", forbidden)
		}
	}
	unknown := append([]byte(nil), matrixRaw[:len(matrixRaw)-1]...)
	unknown = append(unknown, []byte(`,"observed_result":42}`)...)
	if _, err := DecodePhase5CaseMatrix(unknown); err == nil {
		t.Fatal("matrix accepted observed result")
	}
	if _, err := DecodePhase5Preregistration(append(append([]byte(nil), preregRaw...), '\n')); err == nil {
		t.Fatal("preregistration accepted trailing data")
	}
}

func TestPhase5IndependentReviewScriptPassesWithoutTimingSamples(t *testing.T) {
	command := exec.Command("python3", "../../scripts/review-semantic-speculation-phase5-preregistration.py", "--matrix", "../../docs/evidence/semantic-speculation-phase5-case-matrix-v1.json", "--preregistration", "../../docs/evidence/semantic-speculation-phase5-preregistration-v1.json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent review: %v: %s", err, output)
	}
	var report struct {
		Status                string `json:"status"`
		CoordinateCount       int    `json:"campaign_coordinate_count"`
		TimingSamplesObserved int    `json:"timing_samples_observed"`
	}
	if json.Unmarshal(output, &report) != nil || report.Status != "pass" || report.CoordinateCount != 80 || report.TimingSamplesObserved != 0 {
		t.Fatalf("independent report=%s", output)
	}
}

func TestPhase5ContractsRejectScheduleSourceAndIdentityMutation(t *testing.T) {
	matrix, err := SealPhase5CaseMatrix(NewPhase5CaseMatrix())
	if err != nil {
		t.Fatal(err)
	}
	matrix.Cases[1].FinalizationGapMillis++
	if _, err := EncodePhase5CaseMatrix(matrix); err == nil {
		t.Fatal("matrix accepted timing mutation")
	}
	matrix, _ = SealPhase5CaseMatrix(NewPhase5CaseMatrix())
	matrix.Cases[1].Source += "# drift\n"
	if _, err := EncodePhase5CaseMatrix(matrix); err == nil {
		t.Fatal("matrix accepted source mutation")
	}
	prereg, err := SealPhase5Preregistration(NewPhase5Preregistration(Phase5CaseMatrixIdentity))
	if err != nil {
		t.Fatal(err)
	}
	prereg.EconomicsGate.RequiredPassingCoordinatesPerProfile = 1
	if _, err := EncodePhase5Preregistration(prereg); err == nil {
		t.Fatal("preregistration accepted gate mutation")
	}
}
