package semanticspeculation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

const campaignManifestFileName = "campaign-manifest.json"

type CampaignManifestFileReference struct {
	FileName  string `json:"file_name"`
	Identity  string `json:"identity"`
	SHA256    string `json:"sha256"`
	SizeBytes uint64 `json:"size_bytes"`
}

func WriteCampaignEvidenceManifestFile(root string, manifest CampaignEvidenceManifest) (CampaignManifestFileReference, error) {
	encoded, err := EncodeCampaignEvidenceManifest(manifest)
	if err != nil {
		return CampaignManifestFileReference{}, ErrEvidenceWriteInvalid
	}
	if err := validatePrivateEvidenceRoot(root); err != nil {
		return CampaignManifestFileReference{}, err
	}
	path := filepath.Join(root, campaignManifestFileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return CampaignManifestFileReference{}, ErrEvidenceFileExists
	}
	if err != nil {
		return CampaignManifestFileReference{}, err
	}
	removeOnFailure := true
	defer func() {
		_ = file.Close()
		if removeOnFailure {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return CampaignManifestFileReference{}, err
	}
	if err := file.Sync(); err != nil {
		return CampaignManifestFileReference{}, err
	}
	if err := file.Close(); err != nil {
		return CampaignManifestFileReference{}, err
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		return CampaignManifestFileReference{}, err
	}
	decoded, err := DecodeCampaignEvidenceManifest(persisted)
	if err != nil || decoded.Identity != manifest.Identity {
		return CampaignManifestFileReference{}, ErrEvidenceWriteInvalid
	}
	removeOnFailure = false
	digest := sha256.Sum256(persisted)
	return CampaignManifestFileReference{
		FileName: campaignManifestFileName, Identity: manifest.Identity,
		SHA256: "sha256:" + hex.EncodeToString(digest[:]), SizeBytes: uint64(len(persisted)),
	}, nil
}

func VerifyCampaignEvidenceFiles(root string, manifest CampaignEvidenceManifest) error {
	if validateCampaignEvidenceManifest(manifest, true) != nil {
		return ErrInvalidCampaignEvidenceManifest
	}
	if err := validatePrivateEvidenceRoot(root); err != nil {
		return err
	}
	for _, ref := range manifest.Files {
		path := filepath.Join(root, ref.FileName)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || uint64(info.Size()) != ref.SizeBytes {
			return ErrInvalidCampaignEvidenceManifest
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		if "sha256:"+hex.EncodeToString(digest[:]) != ref.SHA256 {
			return ErrInvalidCampaignEvidenceManifest
		}
		evidence, err := DecodeMatchedCaseEvidence(raw)
		if err != nil || evidence.Identity != ref.Identity || len(evidence.Records) != 3 ||
			evidence.Records[0].CaseID != ref.CaseID || evidence.Records[0].TrialIndex != ref.TrialIndex {
			return ErrInvalidCampaignEvidenceManifest
		}
		for _, record := range evidence.Records {
			if trialBindingsFromRecord(record) != manifest.Bindings {
				return ErrInvalidCampaignEvidenceManifest
			}
		}
	}
	return nil
}

func validatePrivateEvidenceRoot(root string) error {
	if root == "" {
		return ErrEvidenceRootNotPrivate
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return ErrEvidenceRootNotPrivate
	}
	return nil
}

func trialBindingsFromRecord(record TrialRecord) TrialBindings {
	return TrialBindings{
		ArtifactSHA256: record.ArtifactSHA256, ManifestSHA256: record.ManifestSHA256,
		ImportInventorySHA256: record.ImportInventorySHA256, ExecutionProfileSHA256: record.ExecutionProfileSHA256,
		CapabilityPlanSHA256: record.CapabilityPlanSHA256, PrivacySHA256: record.PrivacySHA256,
	}
}
