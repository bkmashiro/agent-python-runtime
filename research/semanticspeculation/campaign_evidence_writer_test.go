package semanticspeculation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteMatchedCaseEvidenceFileWritesPrivateCanonicalArtifactOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence, err := SealMatchedCaseEvidence(matchedCampaignFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := WriteMatchedCaseEvidenceFile(root, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if ref.CaseID != "pure_local" || ref.TrialIndex != 1 || ref.Identity != evidence.Identity || ref.SHA256 == "" || ref.SizeBytes == 0 {
		t.Fatalf("ref=%+v", ref)
	}
	info, err := os.Stat(filepath.Join(root, ref.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Size() != int64(ref.SizeBytes) {
		t.Fatalf("mode=%o size=%d ref=%+v", info.Mode().Perm(), info.Size(), ref)
	}
	raw, err := os.ReadFile(filepath.Join(root, ref.FileName))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchedCaseEvidence(raw)
	if err != nil || decoded.Identity != evidence.Identity {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := WriteMatchedCaseEvidenceFile(root, evidence); !errors.Is(err, ErrEvidenceFileExists) {
		t.Fatalf("overwrite err=%v", err)
	}
}

func TestWriteMatchedCaseEvidenceFileRejectsNonPrivateRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	evidence, err := SealMatchedCaseEvidence(matchedCampaignFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMatchedCaseEvidenceFile(root, evidence); !errors.Is(err, ErrEvidenceRootNotPrivate) {
		t.Fatalf("err=%v", err)
	}
}
