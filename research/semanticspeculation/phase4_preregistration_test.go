package semanticspeculation

import (
	"bytes"
	"os"
	"testing"
)

func TestPhase4ExtensionMatrixIsFrozenAndBodyFree(t *testing.T) {
	sealed, err := SealPhase4ExtensionMatrix(NewPhase4ExtensionMatrix())
	if err != nil {
		t.Fatal(err)
	}
	for _, coordinate := range Phase4SyntheticCoordinates() {
		if err := coordinate.Fixture.Validate(); err != nil {
			t.Fatalf("fixture %s: %v", coordinate.Fixture.ID, err)
		}
	}
	if sealed.Identity != Phase4ExtensionMatrixIdentity || len(sealed.Coordinates) != 12 {
		t.Fatalf("identity=%s coordinates=%d", sealed.Identity, len(sealed.Coordinates))
	}
	raw, err := EncodePhase4ExtensionMatrix(sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, bodyFragment := range [][]byte{[]byte("time.read"), []byte("RuntimeError"), []byte("source_body")} {
		if bytes.Contains(raw, bodyFragment) {
			t.Fatalf("matrix leaked body fragment %q", bodyFragment)
		}
	}
	admittedShapes := map[string]bool{}
	controls := 0
	for _, coordinate := range sealed.Coordinates {
		if coordinate.ExpectedPreDispatch == "admit_consumed" {
			admittedShapes[coordinate.Shape] = true
		}
		if coordinate.ExpectedPreDispatch == "must_not_admit" {
			controls++
		}
	}
	if !admittedShapes["direct_read"] || !admittedShapes["local_then_read"] || controls < 4 {
		t.Fatalf("admitted=%v controls=%d", admittedShapes, controls)
	}
}

func TestPhase4PreregistrationFreezesIndependentGates(t *testing.T) {
	value := NewPhase4Preregistration("5a4576267db83234308757d4b4eb0fd58e59a2cf", Phase4ExtensionMatrixIdentity)
	sealed, err := SealPhase4Preregistration(value)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Identity != Phase4PreregistrationIdentity || sealed.ParentPhase3MatrixIdentity != SyntheticCaseMatrixIdentity ||
		sealed.EconomicsGate.MinimumMedianSavingNanos != 100_000_000 || sealed.EconomicsGate.MinimumReadyTrials != 4 {
		t.Fatalf("preregistration=%+v", sealed)
	}
}

func TestCheckedInPhase4ContractsAreCanonicalAndStrict(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-extension-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := DecodePhase4ExtensionMatrix(matrixRaw)
	if err != nil || matrix.Identity != Phase4ExtensionMatrixIdentity {
		t.Fatalf("matrix identity=%s err=%v", matrix.Identity, err)
	}
	preregRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-preregistration-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	prereg, err := DecodePhase4Preregistration(preregRaw)
	if err != nil || prereg.Identity != Phase4PreregistrationIdentity || prereg.ExtensionMatrixIdentity != matrix.Identity {
		t.Fatalf("prereg=%+v err=%v", prereg, err)
	}

	unknown := append([]byte(nil), matrixRaw[:len(matrixRaw)-1]...)
	unknown = append(unknown, []byte(`,"source_body":"forbidden"}`)...)
	if _, err := DecodePhase4ExtensionMatrix(unknown); err == nil {
		t.Fatal("matrix accepted unknown source body")
	}
	trailing := append(append([]byte(nil), preregRaw...), '\n')
	if _, err := DecodePhase4Preregistration(trailing); err == nil {
		t.Fatal("preregistration accepted non-canonical trailing data")
	}
}

func TestPhase4ContractsRejectIdentityAndScheduleMutation(t *testing.T) {
	matrix, err := SealPhase4ExtensionMatrix(NewPhase4ExtensionMatrix())
	if err != nil {
		t.Fatal(err)
	}
	matrix.Coordinates[0].PhysicalDelayMillis++
	if _, err := EncodePhase4ExtensionMatrix(matrix); err == nil {
		t.Fatal("matrix accepted mutation under frozen identity")
	}

	matrix, err = SealPhase4ExtensionMatrix(NewPhase4ExtensionMatrix())
	if err != nil {
		t.Fatal(err)
	}
	matrix.Coordinates[0].CandidatePrefixIndices = []uint32{2, 1}
	if _, err := SealPhase4ExtensionMatrix(matrix); err == nil {
		t.Fatal("matrix accepted unordered candidate prefixes")
	}
}
