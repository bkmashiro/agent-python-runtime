package semanticspeculation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrEvidenceRootNotPrivate = errors.New("semantic-speculation evidence root is not private")
	ErrEvidenceFileExists     = errors.New("semantic-speculation evidence file already exists")
	ErrEvidenceWriteInvalid   = errors.New("semantic-speculation evidence write is invalid")
)

type MatchedCaseEvidenceReference struct {
	FileName   string `json:"file_name"`
	CaseID     string `json:"case_id"`
	TrialIndex uint32 `json:"trial_index"`
	Identity   string `json:"identity"`
	SHA256     string `json:"sha256"`
	SizeBytes  uint64 `json:"size_bytes"`
}

func matchedEvidenceFileName(caseID string, trialIndex uint32) string {
	return fmt.Sprintf("%s-trial-%02d.json", caseID, trialIndex)
}

// WriteMatchedCaseEvidenceFile persists one immutable body-free matched-case
// envelope. The root must already exist as a private non-symlink directory. A
// pre-existing destination is never replaced, including a partial prior write.
func WriteMatchedCaseEvidenceFile(root string, evidence MatchedCaseEvidence) (MatchedCaseEvidenceReference, error) {
	encoded, err := EncodeMatchedCaseEvidence(evidence)
	if err != nil || root == "" || len(evidence.Records) != 3 {
		return MatchedCaseEvidenceReference{}, ErrEvidenceWriteInvalid
	}
	if err := validatePrivateEvidenceRoot(root); err != nil {
		return MatchedCaseEvidenceReference{}, err
	}
	caseID := evidence.Records[0].CaseID
	trialIndex := evidence.Records[0].TrialIndex
	if !identifierPattern.MatchString(caseID) || trialIndex == 0 || trialIndex > 5 {
		return MatchedCaseEvidenceReference{}, ErrEvidenceWriteInvalid
	}
	fileName := matchedEvidenceFileName(caseID, trialIndex)
	path := filepath.Join(root, fileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return MatchedCaseEvidenceReference{}, ErrEvidenceFileExists
	}
	if err != nil {
		return MatchedCaseEvidenceReference{}, err
	}
	writeOK := false
	defer func() {
		if !writeOK {
			_ = file.Close()
		}
	}()
	written, err := file.Write(encoded)
	if err != nil || written != len(encoded) {
		return MatchedCaseEvidenceReference{}, errors.Join(ErrEvidenceWriteInvalid, err)
	}
	if err := file.Sync(); err != nil {
		return MatchedCaseEvidenceReference{}, err
	}
	if err := file.Close(); err != nil {
		return MatchedCaseEvidenceReference{}, err
	}
	writeOK = true
	persisted, err := os.ReadFile(path)
	if err != nil {
		return MatchedCaseEvidenceReference{}, err
	}
	decoded, err := DecodeMatchedCaseEvidence(persisted)
	if err != nil || decoded.Identity != evidence.Identity {
		return MatchedCaseEvidenceReference{}, ErrEvidenceWriteInvalid
	}
	digest := sha256.Sum256(persisted)
	return MatchedCaseEvidenceReference{
		FileName: fileName, CaseID: caseID, TrialIndex: trialIndex, Identity: evidence.Identity,
		SHA256: "sha256:" + hex.EncodeToString(digest[:]), SizeBytes: uint64(len(persisted)),
	}, nil
}
