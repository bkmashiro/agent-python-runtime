package semanticspeculation

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestPhase3CampaignCoordinatesCoverFrozenGridInSeededOrder(t *testing.T) {
	coordinates := Phase3CampaignCoordinates()
	if len(coordinates) != 35 {
		t.Fatalf("coordinates=%d", len(coordinates))
	}
	seen := make(map[CampaignCoordinate]struct{}, len(coordinates))
	for _, coordinate := range coordinates {
		if _, ok := FrozenPhase3Case(coordinate.CaseID); !ok || coordinate.TrialIndex == 0 || coordinate.TrialIndex > 5 {
			t.Fatalf("coordinate=%+v", coordinate)
		}
		if _, exists := seen[coordinate]; exists {
			t.Fatalf("duplicate=%+v", coordinate)
		}
		seen[coordinate] = struct{}{}
	}
}

func TestCampaignEvidenceManifestCanonicalRoundTripAndOrderBinding(t *testing.T) {
	refs := fakeCampaignEvidenceReferences()
	manifest, err := SealCampaignEvidenceManifest("0123456789abcdef0123456789abcdef01234567", matchedTestBindings(), refs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeCampaignEvidenceManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCampaignEvidenceManifest(encoded)
	if err != nil || decoded.Identity != manifest.Identity || !bytes.Equal(encoded, mustEncodeManifest(t, decoded)) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	manifest.Files[0], manifest.Files[1] = manifest.Files[1], manifest.Files[0]
	if _, err := EncodeCampaignEvidenceManifest(manifest); err == nil {
		t.Fatal("reordered campaign coordinates were accepted")
	}
}

func TestCampaignEvidenceManifestRejectsIncompleteGrid(t *testing.T) {
	refs := fakeCampaignEvidenceReferences()
	if _, err := SealCampaignEvidenceManifest("0123456789abcdef0123456789abcdef01234567", matchedTestBindings(), refs[:34]); err == nil {
		t.Fatal("incomplete campaign grid was accepted")
	}
}

func fakeCampaignEvidenceReferences() []MatchedCaseEvidenceReference {
	coordinates := Phase3CampaignCoordinates()
	refs := make([]MatchedCaseEvidenceReference, len(coordinates))
	for index, coordinate := range coordinates {
		refs[index] = MatchedCaseEvidenceReference{
			FileName: matchedEvidenceFileName(coordinate.CaseID, coordinate.TrialIndex), CaseID: coordinate.CaseID, TrialIndex: coordinate.TrialIndex,
			Identity: syntheticDigest([]byte("identity:" + coordinate.CaseID)), SHA256: syntheticDigest([]byte("file:" + coordinate.CaseID)), SizeBytes: uint64(index + 1),
		}
	}
	return refs
}

func mustEncodeManifest(t *testing.T, value CampaignEvidenceManifest) []byte {
	t.Helper()
	encoded, err := EncodeCampaignEvidenceManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestCampaignEvidenceManifestWriterIsPrivateAndNonOverwriting(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := SealCampaignEvidenceManifest("0123456789abcdef0123456789abcdef01234567", matchedTestBindings(), fakeCampaignEvidenceReferences())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := WriteCampaignEvidenceManifestFile(root, manifest)
	if err != nil || ref.Identity != manifest.Identity || ref.FileName != campaignManifestFileName || ref.SizeBytes == 0 {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}
	if _, err := WriteCampaignEvidenceManifestFile(root, manifest); !errors.Is(err, ErrEvidenceFileExists) {
		t.Fatalf("overwrite err=%v", err)
	}
	if err := VerifyCampaignEvidenceFiles(root, manifest); !errors.Is(err, ErrInvalidCampaignEvidenceManifest) {
		t.Fatalf("missing evidence files err=%v", err)
	}
}
