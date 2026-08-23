package sourceboundpasses

import (
	"bytes"
	"testing"
)

func TestBuildAuthoredWorkloadEvidenceIsBodyFreeAndCountsFrozenCases(t *testing.T) {
	preregistration := AuthoredWorkloadPreregistrationV2()
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
		Preregistration: preregistration, PreregistrationSHA256: expectedAuthoredWorkloadPreregistrationSHA256(t),
		ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256:       digestWorkloadSource("artifact"), ArtifactManifestSHA256: digestWorkloadSource("manifest"),
		HarnessSourceCommit: "89abcdef0123456789abcdef0123456789abcdef", CapabilityPlanSHA256: digestWorkloadSource("plan"),
		ExecutionProfileSHA256: digestWorkloadSource("profile"), Rows: rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Counts.Cases != 7 || evidence.Counts.SemanticAdmitted != 3 || evidence.Counts.SemanticRejected != 4 || evidence.PerformanceComparisonSupported {
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

func TestAuthoredWorkloadEvidenceValidationIsStructuralNotArtifactAuthenticity(t *testing.T) {
	preregistration := AuthoredWorkloadPreregistrationV2()
	rows := make([]AuthoredWorkloadEvidenceInput, len(preregistration.Cases))
	for index, item := range preregistration.Cases {
		rows[index] = AuthoredWorkloadEvidenceInput{
			ID: item.ID, SourceSHA256: item.SourceSHA256,
			ASTSHA256: digestWorkloadSource(item.ID + "-ast"), AnalyzerSHA256: digestWorkloadSource("analyzer"),
		}
	}
	value, err := BuildAuthoredWorkloadEvidence(AuthoredWorkloadEvidenceBuild{
		Preregistration: preregistration, PreregistrationSHA256: expectedAuthoredWorkloadPreregistrationSHA256(t),
		ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256:       digestWorkloadSource("artifact"), ArtifactManifestSHA256: digestWorkloadSource("manifest"),
		HarnessSourceCommit: "89abcdef0123456789abcdef0123456789abcdef", CapabilityPlanSHA256: digestWorkloadSource("plan"),
		ExecutionProfileSHA256: digestWorkloadSource("profile"), Rows: rows,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Validation catches malformed or internally inconsistent data. Git history and
	// the checked-in generator establish which valid artifact the project endorses.
	value.NaturalCorpus.StructurallyEligible = 1
	value.ClaimBoundary = []string{"synthetic positive fixture only"}
	value.Rows = append([]AuthoredWorkloadEvidenceRow(nil), value.Rows...)
	value.Rows[0].Category = "synthetic_positive_control"
	value.IdentitySHA256 = authoredEvidenceIdentity(value)
	if _, err := EncodeAuthoredWorkloadEvidence(value); err != nil {
		t.Fatalf("structurally valid alternate artifact rejected: %v", err)
	}
}

func expectedAuthoredWorkloadPreregistrationSHA256(t *testing.T) string {
	t.Helper()
	raw, err := EncodeAuthoredWorkloadPreregistration(AuthoredWorkloadPreregistrationV2())
	if err != nil {
		t.Fatal(err)
	}
	return digestWorkloadSource(string(raw) + "\n")
}
