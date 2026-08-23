package wazero

import (
	"encoding/json"
	"errors"
	"sort"
)

const PreparedFamilyAcceptanceReportV1 = "pysolate.prepared-family-acceptance.v1"

var ErrPreparedFamilyAcceptanceReport = errors.New("invalid prepared family acceptance report")

// PreparedFamilyAcceptanceReport is a compact body-free correctness report.
// It carries no ndarray bytes, response body, Broker payload, credential, path,
// backing handle, or scheduler policy.
type PreparedFamilyAcceptanceReport struct {
	SchemaVersion          string                      `json:"schema_version"`
	SourceTreeSHA256       string                      `json:"source_tree_sha256"`
	ArtifactSHA256         string                      `json:"artifact_sha256"`
	ExecutionProfileSHA256 string                      `json:"execution_profile_sha256"`
	InputSHA256            string                      `json:"input_sha256"`
	FamilySHA256           string                      `json:"family_sha256"`
	PhysicalDisposition    PreparedPhysicalDisposition `json:"physical_disposition"`
	Created                uint32                      `json:"created"`
	Terminal               uint32                      `json:"terminal"`
	SelectedRootSHA256     string                      `json:"selected_root_sha256,omitempty"`
	Members                []PreparedMemberRecord      `json:"members"`
}

// EncodePreparedFamilyAcceptanceReport validates and canonically encodes one
// family snapshot. The caller supplies source/artifact/profile identities from
// its verified Host build and may omit selectedRootSHA256 before selection.
func EncodePreparedFamilyAcceptanceReport(
	sourceTreeSHA256 string,
	artifactSHA256 string,
	executionProfileSHA256 string,
	state PreparedFamilyState,
	records []PreparedMemberRecord,
	selectedRootSHA256 string,
) ([]byte, error) {
	if !validPreparedDigest(sourceTreeSHA256) || !validPreparedDigest(artifactSHA256) || !validPreparedDigest(executionProfileSHA256) ||
		!validPreparedDigest(state.InputSHA256) || !validPreparedDigest(state.FamilySHA256) || state.Active != 0 || state.Created != uint32(len(records)) || state.Terminal != uint32(len(records)) ||
		(state.Disposition != PreparedDispositionPrivateCopy && state.Disposition != PreparedDispositionPrivateCOW && state.Disposition != PreparedDispositionOrdinaryFresh) ||
		(selectedRootSHA256 != "" && !validPreparedDigest(selectedRootSHA256)) {
		return nil, ErrPreparedFamilyAcceptanceReport
	}
	members := append([]PreparedMemberRecord(nil), records...)
	sort.Slice(members, func(left, right int) bool { return members[left].MemberID < members[right].MemberID })
	seen := make(map[uint64]struct{}, len(members))
	selectedObserved := selectedRootSHA256 == ""
	for _, record := range members {
		if record.Validate() != nil || record.FamilySHA256 != state.FamilySHA256 || record.InputSHA256 != state.InputSHA256 || record.PhysicalDisposition != state.Disposition {
			return nil, ErrPreparedFamilyAcceptanceReport
		}
		if _, duplicate := seen[record.MemberID]; duplicate {
			return nil, ErrPreparedFamilyAcceptanceReport
		}
		seen[record.MemberID] = struct{}{}
		if record.FinalWorkspaceSHA256 == selectedRootSHA256 {
			selectedObserved = true
		}
	}
	if !selectedObserved {
		return nil, ErrPreparedFamilyAcceptanceReport
	}
	report := PreparedFamilyAcceptanceReport{
		SchemaVersion:    PreparedFamilyAcceptanceReportV1,
		SourceTreeSHA256: sourceTreeSHA256, ArtifactSHA256: artifactSHA256, ExecutionProfileSHA256: executionProfileSHA256,
		InputSHA256: state.InputSHA256, FamilySHA256: state.FamilySHA256, PhysicalDisposition: state.Disposition,
		Created: state.Created, Terminal: state.Terminal, SelectedRootSHA256: selectedRootSHA256, Members: members,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return nil, ErrPreparedFamilyAcceptanceReport
	}
	return encoded, nil
}
