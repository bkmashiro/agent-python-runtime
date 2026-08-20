package semanticspeculation

import (
	"bytes"
	"os"
	"testing"
)

func TestPhase3SyntheticCasesFreezeCompleteBodySafeMatrix(t *testing.T) {
	cases := Phase3SyntheticCases()
	wantIDs := []string{"branch_not_taken", "earlier_exception", "external_read_valid_suffix", "later_runtime_error", "later_syntax_error", "pure_local", "unknown_wrapper"}
	if len(cases) != len(wantIDs) {
		t.Fatalf("cases=%d", len(cases))
	}
	for index, fixture := range cases {
		if fixture.ID != wantIDs[index] {
			t.Fatalf("case[%d]=%s", index, fixture.ID)
		}
		if err := fixture.Validate(); err != nil {
			t.Fatalf("case %s: %v", fixture.ID, err)
		}
		if !bytes.Equal(fixture.Source(), bytes.Join(fixture.ChunkBodies(), nil)) {
			t.Fatalf("case %s source differs from chunks", fixture.ID)
		}
		projection := fixture.Projection()
		if projection.ID != fixture.ID || projection.SourceSHA256 == "" || projection.SourceScheduleSHA256 == "" || projection.InputsSHA256 == "" || len(projection.Chunks) != len(fixture.Chunks) {
			t.Fatalf("case %s projection=%+v", fixture.ID, projection)
		}
		encoded, err := EncodeSyntheticCaseProjection(projection)
		if err != nil || bytes.Contains(encoded, []byte("time.read")) || bytes.Contains(encoded, []byte("RuntimeError")) {
			t.Fatalf("case %s projection leaked source: %s err=%v", fixture.ID, encoded, err)
		}
	}
}

func TestSyntheticCaseMatrixRoundTripsWithoutBodies(t *testing.T) {
	matrix, err := NewSyntheticCaseMatrix()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealSyntheticCaseMatrix(matrix)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeSyntheticCaseMatrix(sealed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSyntheticCaseMatrix(raw)
	if err != nil || decoded.Identity != sealed.Identity || bytes.Contains(raw, []byte("time.read")) {
		t.Fatalf("decoded=%+v err=%v raw=%s", decoded, err, raw)
	}
	mutated := append([]byte(nil), raw[:len(raw)-1]...)
	mutated = append(mutated, []byte(`,"source_body":"private"}`)...)
	if _, err := DecodeSyntheticCaseMatrix(mutated); err == nil {
		t.Fatal("matrix accepted source body")
	}
}

func TestCheckedInSyntheticCaseMatrixIsCanonical(t *testing.T) {
	raw, err := os.ReadFile("../../docs/evidence/semantic-speculation-synthetic-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := DecodeSyntheticCaseMatrix(raw)
	if err != nil || matrix.Identity != SyntheticCaseMatrixIdentity {
		t.Fatalf("identity=%s err=%v", matrix.Identity, err)
	}
}

func TestSyntheticCaseRejectsScheduleAndSourceMutation(t *testing.T) {
	fixture := Phase3SyntheticCases()[2]
	mutated := fixture
	mutated.Chunks = append([]SyntheticChunk(nil), fixture.Chunks...)
	mutated.Chunks[0].ReleaseAfterMilliseconds++
	if mutated.Projection().SourceScheduleSHA256 == fixture.Projection().SourceScheduleSHA256 {
		t.Fatal("schedule mutation preserved identity")
	}
	mutated = fixture
	mutated.Chunks = append([]SyntheticChunk(nil), fixture.Chunks...)
	mutated.Chunks[0].Source += " "
	if mutated.Projection().SourceSHA256 == fixture.Projection().SourceSHA256 {
		t.Fatal("source mutation preserved identity")
	}
}
