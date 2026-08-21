package numpyproducer

import (
	"encoding/json"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const LineageSchemaVersion = "pysolate.numpy-producer-prepared-lineage.v1"

var ErrLineage = errors.New("invalid numpy producer prepared lineage")

type PreparedLineage struct {
	SchemaVersion   string                                          `json:"schema_version"`
	AdmissionSHA256 string                                          `json:"admission_sha256"`
	Decision        preparedregion.PreparedRegionDecision           `json:"decision"`
	Capsule         preparedregion.PreparedRegionCapsule            `json:"capsule"`
	Patch           preparedregion.PreparedRegionPatch              `json:"patch"`
	Selection       preparedregion.PreparedRegionExecutionSelection `json:"selection"`
	IdentitySHA256  string                                          `json:"identity_sha256"`
}

type lineageIdentity struct {
	SchemaVersion   string `json:"schema_version"`
	AdmissionSHA256 string `json:"admission_sha256"`
	DecisionSHA256  string `json:"decision_sha256"`
	CapsuleSHA256   string `json:"capsule_sha256"`
	PatchSHA256     string `json:"patch_sha256"`
	SelectionSHA256 string `json:"selection_sha256"`
}

func SealPreparedLineage(admission Admission, declaration Declaration, plan numpycodec.MaterializationPlan, finalAnalysis semantic.Analysis) ([]byte, PreparedLineage, error) {
	if admission.Validate() != nil || !declaration.valid() || admission.DeclarationSHA256 != declaration.IdentitySHA256 ||
		plan.FinalSourceSHA256 == "" || plan.ConsumerBindingSHA256 == "" || plan.InputsSHA256 == "" || plan.RequestSHA256 == "" ||
		finalAnalysis.Validate() != nil || finalAnalysis.SourceSHA256 != plan.FinalSourceSHA256 ||
		finalAnalysis.ArtifactSHA256 != admission.ArtifactSHA256 || finalAnalysis.ExecutionProfileSHA256 != admission.ExecutionProfileSHA256 ||
		finalAnalysis.ImportClosureSHA256 != admission.ImportClosureSHA256 || finalAnalysis.CapabilityPlanSHA256 != admission.CapabilityPlanSHA256 {
		return nil, PreparedLineage{}, ErrLineage
	}
	_, liveInsSHA256, err := preparedregion.SealPreparedRegionLiveIns(map[string]json.RawMessage{})
	if err != nil {
		return nil, PreparedLineage{}, err
	}
	span := preparedregion.SourceSpan{
		StartLine: admission.RegionSpan.StartLine, StartColumn: admission.RegionSpan.StartColumn,
		EndLine: admission.RegionSpan.EndLine, EndColumn: admission.RegionSpan.EndColumn,
	}
	binding := preparedregion.PreparedRegionBinding{
		SourceSHA256: admission.SourceSHA256, ASTSHA256: admission.ASTSHA256, AnalysisSHA256: admission.AnalysisSHA256,
		RegionID: admission.DeclarationSHA256, RegionSpan: span, RegionSourceSHA256: admission.SourceSHA256,
		LiveInsSHA256: liveInsSHA256, EnvironmentSHA256: Digest([]byte("pysolate.numpy-producer.environment.v1\x00" + admission.IdentitySHA256 + "\x00" + admission.InputsSHA256)),
		ExecutionProfileSHA256: admission.ExecutionProfileSHA256, ImportClosureSHA256: admission.ImportClosureSHA256,
		CapabilityPlanSHA256: admission.CapabilityPlanSHA256,
		PassConfigSHA256:     admission.PassConfigSHA256,
		Codec:                preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "admitted",
	}
	_, decision, err := preparedregion.SealPreparedRegionDecision(binding)
	if err != nil {
		return nil, PreparedLineage{}, err
	}
	_, capsule, err := preparedregion.SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`true`))
	if err != nil {
		return nil, PreparedLineage{}, err
	}
	patchBinding := preparedregion.PreparedRegionPatchBinding{
		DecisionSHA256: decision.IdentitySHA256, FinalSourceSHA256: plan.FinalSourceSHA256,
		FinalASTSHA256: admission.ASTSHA256, DerivedASTSHA256: finalAnalysis.ASTSHA256,
		RegionID: admission.DeclarationSHA256, RegionSpan: span, OutputName: "admitted",
	}
	_, patch, err := preparedregion.SealPreparedRegionPatch(patchBinding)
	if err != nil {
		return nil, PreparedLineage{}, err
	}
	_, selection, err := preparedregion.SealPreparedRegionExecutionSelection(decision, capsule, patch)
	if err != nil {
		return nil, PreparedLineage{}, err
	}
	identity := lineageIdentity{
		SchemaVersion: LineageSchemaVersion, AdmissionSHA256: admission.IdentitySHA256,
		DecisionSHA256: decision.IdentitySHA256, CapsuleSHA256: capsule.IdentitySHA256,
		PatchSHA256: patch.IdentitySHA256, SelectionSHA256: selection.IdentitySHA256,
	}
	lineage := PreparedLineage{
		SchemaVersion: identity.SchemaVersion, AdmissionSHA256: identity.AdmissionSHA256,
		Decision: decision, Capsule: capsule, Patch: patch, Selection: selection, IdentitySHA256: digestJSON(identity),
	}
	raw, err := json.Marshal(lineage)
	return raw, lineage, err
}

func (lineage PreparedLineage) Validate(admission Admission) error {
	if admission.Validate() != nil || lineage.SchemaVersion != LineageSchemaVersion || lineage.AdmissionSHA256 != admission.IdentitySHA256 ||
		lineage.Capsule.ValidateDecision(lineage.Decision) != nil || lineage.Patch.ValidateDecision(lineage.Decision) != nil ||
		lineage.Selection.Validate(lineage.Decision, lineage.Capsule, lineage.Patch) != nil {
		return ErrLineage
	}
	identity := lineageIdentity{
		SchemaVersion: lineage.SchemaVersion, AdmissionSHA256: lineage.AdmissionSHA256,
		DecisionSHA256: lineage.Decision.IdentitySHA256, CapsuleSHA256: lineage.Capsule.IdentitySHA256,
		PatchSHA256: lineage.Patch.IdentitySHA256, SelectionSHA256: lineage.Selection.IdentitySHA256,
	}
	if lineage.IdentitySHA256 != digestJSON(identity) {
		return ErrLineage
	}
	return nil
}
