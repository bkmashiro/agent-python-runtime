package sourceboundpasses

import (
	"bytes"
	"testing"
)

func TestBuildAuthoredWorkloadEvidenceIsBodyFreeAndCountsFrozenCases(t *testing.T) {
	preregistration := AuthoredWorkloadPreregistrationV1()
	rows := make([]AuthoredWorkloadEvidenceInput, len(preregistration.Cases))
	for index, item := range preregistration.Cases {
		rows[index] = AuthoredWorkloadEvidenceInput{
			ID: item.ID, SourceSHA256: item.SourceSHA256, ASTSHA256: digestWorkloadSource(item.ID + "-ast"),
			AnalyzerSHA256: digestWorkloadSource("analyzer"), CandidateRegions: uint32(index + 1),
			LocallyReusableRegions: uint32(index), CallSites: 1,
			SemanticAdmitted: uint32(index % 2), SemanticRejected: uint32(1 - index%2),
		}
	}
	evidence, err := BuildAuthoredWorkloadEvidence(AuthoredWorkloadEvidenceBuild{
		Preregistration: preregistration, PreregistrationSHA256: digestWorkloadSource("preregistration"),
		ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256:       digestWorkloadSource("artifact"), ArtifactManifestSHA256: digestWorkloadSource("manifest"),
		HarnessSourceCommit: "89abcdef0123456789abcdef0123456789abcdef", CapabilityPlanSHA256: digestWorkloadSource("plan"),
		ExecutionProfileSHA256: digestWorkloadSource("profile"), Rows: rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Counts.Cases != 6 || evidence.Counts.SemanticAdmitted != 3 || evidence.Counts.SemanticRejected != 3 || evidence.PerformanceComparisonSupported {
		t.Fatalf("counts=%+v performance=%t", evidence.Counts, evidence.PerformanceComparisonSupported)
	}
	raw, err := EncodeAuthoredWorkloadEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("workspace.read_text"), []byte("source_body"), []byte("result_body"), []byte("alpha.py")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("evidence leaked body marker %q", forbidden)
		}
	}
	mutated := evidence
	mutated.Rows = append([]AuthoredWorkloadEvidenceRow(nil), evidence.Rows...)
	mutated.Rows[0].SourceSHA256 = digestWorkloadSource("drift")
	if _, err := EncodeAuthoredWorkloadEvidence(mutated); err == nil {
		t.Fatal("accepted source identity drift")
	}
}
