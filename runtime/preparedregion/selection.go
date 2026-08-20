package preparedregion

const (
	PreparedRegionExecutionSelectionSchemaVersion = "pysolate.prepared-region-execution-selection.v1"
	PreparedRegionExecutionModeDerived            = "derived"
)

type PreparedRegionExecutionSelection struct {
	SchemaVersion     string `json:"schema_version"`
	Mode              string `json:"mode"`
	DecisionSHA256    string `json:"decision_sha256"`
	CapsuleSHA256     string `json:"capsule_sha256"`
	PatchSHA256       string `json:"patch_sha256"`
	FinalSourceSHA256 string `json:"final_source_sha256"`
	DerivedASTSHA256  string `json:"derived_ast_sha256"`
	IdentitySHA256    string `json:"identity_sha256"`
}

type preparedRegionExecutionSelectionIdentity struct {
	SchemaVersion     string `json:"schema_version"`
	Mode              string `json:"mode"`
	DecisionSHA256    string `json:"decision_sha256"`
	CapsuleSHA256     string `json:"capsule_sha256"`
	PatchSHA256       string `json:"patch_sha256"`
	FinalSourceSHA256 string `json:"final_source_sha256"`
	DerivedASTSHA256  string `json:"derived_ast_sha256"`
}

func SealPreparedRegionExecutionSelection(decision PreparedRegionDecision, capsule PreparedRegionCapsule, patch PreparedRegionPatch) ([]byte, PreparedRegionExecutionSelection, error) {
	if capsule.ValidateDecision(decision) != nil || patch.ValidateDecision(decision) != nil {
		return nil, PreparedRegionExecutionSelection{}, ErrInvalidPreparedRegion
	}
	identity := preparedRegionExecutionSelectionIdentity{
		SchemaVersion:     PreparedRegionExecutionSelectionSchemaVersion,
		Mode:              PreparedRegionExecutionModeDerived,
		DecisionSHA256:    decision.IdentitySHA256,
		CapsuleSHA256:     capsule.IdentitySHA256,
		PatchSHA256:       patch.IdentitySHA256,
		FinalSourceSHA256: patch.FinalSourceSHA256,
		DerivedASTSHA256:  patch.DerivedASTSHA256,
	}
	selection := PreparedRegionExecutionSelection{
		SchemaVersion: identity.SchemaVersion, Mode: identity.Mode,
		DecisionSHA256: identity.DecisionSHA256, CapsuleSHA256: identity.CapsuleSHA256,
		PatchSHA256: identity.PatchSHA256, FinalSourceSHA256: identity.FinalSourceSHA256,
		DerivedASTSHA256: identity.DerivedASTSHA256, IdentitySHA256: preparedRegionDigest(identity),
	}
	raw, err := preparedRegionCanonicalJSON(selection)
	return raw, selection, err
}

func DecodePreparedRegionExecutionSelection(raw []byte) (PreparedRegionExecutionSelection, error) {
	var selection PreparedRegionExecutionSelection
	if preparedRegionDecode(raw, &selection) != nil || !selection.valid() {
		return PreparedRegionExecutionSelection{}, ErrInvalidPreparedRegion
	}
	return selection, nil
}

func (selection PreparedRegionExecutionSelection) Validate(decision PreparedRegionDecision, capsule PreparedRegionCapsule, patch PreparedRegionPatch) error {
	if !selection.valid() || capsule.ValidateDecision(decision) != nil || patch.ValidateDecision(decision) != nil ||
		selection.DecisionSHA256 != decision.IdentitySHA256 || selection.CapsuleSHA256 != capsule.IdentitySHA256 ||
		selection.PatchSHA256 != patch.IdentitySHA256 || selection.FinalSourceSHA256 != patch.FinalSourceSHA256 ||
		selection.DerivedASTSHA256 != patch.DerivedASTSHA256 {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func (selection PreparedRegionExecutionSelection) valid() bool {
	if selection.SchemaVersion != PreparedRegionExecutionSelectionSchemaVersion || selection.Mode != PreparedRegionExecutionModeDerived {
		return false
	}
	for _, digest := range []string{selection.DecisionSHA256, selection.CapsuleSHA256, selection.PatchSHA256, selection.FinalSourceSHA256, selection.DerivedASTSHA256, selection.IdentitySHA256} {
		if !digestPattern.MatchString(digest) {
			return false
		}
	}
	identity := preparedRegionExecutionSelectionIdentity{
		SchemaVersion: selection.SchemaVersion, Mode: selection.Mode,
		DecisionSHA256: selection.DecisionSHA256, CapsuleSHA256: selection.CapsuleSHA256,
		PatchSHA256: selection.PatchSHA256, FinalSourceSHA256: selection.FinalSourceSHA256,
		DerivedASTSHA256: selection.DerivedASTSHA256,
	}
	return selection.IdentitySHA256 == preparedRegionDigest(identity)
}
