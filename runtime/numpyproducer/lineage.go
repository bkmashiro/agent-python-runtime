package numpyproducer

import (
	"encoding/json"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const LineageSchemaVersion = "pysolate.numpy-producer-prepared-lineage.v1"

var ErrLineage = errors.New("invalid numpy producer prepared lineage")

type PreparedLineage struct {
	SchemaVersion         string                                          `json:"schema_version"`
	AdmissionSHA256       string                                          `json:"admission_sha256"`
	ConsumerBindingSHA256 string                                          `json:"consumer_binding_sha256"`
	ConsumerSourceSHA256  string                                          `json:"consumer_source_sha256"`
	FinalSourceSHA256     string                                          `json:"final_source_sha256"`
	InputsSHA256          string                                          `json:"inputs_sha256"`
	RequestSHA256         string                                          `json:"request_sha256"`
	Decision              preparedregion.PreparedRegionDecision           `json:"decision"`
	Capsule               preparedregion.PreparedRegionCapsule            `json:"capsule"`
	Patch                 preparedregion.PreparedRegionPatch              `json:"patch"`
	Selection             preparedregion.PreparedRegionExecutionSelection `json:"selection"`
	IdentitySHA256        string                                          `json:"identity_sha256"`
	provenance            publicationauth.Token
}

type lineageIdentity struct {
	SchemaVersion         string `json:"schema_version"`
	AdmissionSHA256       string `json:"admission_sha256"`
	ConsumerBindingSHA256 string `json:"consumer_binding_sha256"`
	ConsumerSourceSHA256  string `json:"consumer_source_sha256"`
	FinalSourceSHA256     string `json:"final_source_sha256"`
	InputsSHA256          string `json:"inputs_sha256"`
	RequestSHA256         string `json:"request_sha256"`
	DecisionSHA256        string `json:"decision_sha256"`
	CapsuleSHA256         string `json:"capsule_sha256"`
	PatchSHA256           string `json:"patch_sha256"`
	SelectionSHA256       string `json:"selection_sha256"`
}

func SealPreparedLineage(admission Admission, declaration Declaration, plan numpycodec.MaterializationPlan, verified semantic.VerifiedAnalysis) ([]byte, PreparedLineage, error) {
	analysis, err := verified.Analysis()
	if err != nil {
		return nil, PreparedLineage{}, ErrLineage
	}
	return sealPreparedLineageAnalysis(admission, declaration, plan, analysis)
}

func sealPreparedLineageAnalysis(admission Admission, declaration Declaration, plan numpycodec.MaterializationPlan, finalAnalysis semantic.Analysis) ([]byte, PreparedLineage, error) {
	if admission.Validate() != nil || !declaration.valid() || admission.DeclarationSHA256 != declaration.IdentitySHA256 ||
		plan.FinalSourceSHA256 == "" || plan.ConsumerBindingSHA256 == "" || plan.ConsumerSourceSHA256 == "" || plan.InputsSHA256 == "" ||
		plan.RequestSHA256 == "" || Digest(plan.Request) != plan.RequestSHA256 ||
		finalAnalysis.Validate() != nil || finalAnalysis.SourceSHA256 != plan.FinalSourceSHA256 ||
		finalAnalysis.ArtifactSHA256 != admission.ArtifactSHA256 || finalAnalysis.ExecutionProfileSHA256 != admission.ExecutionProfileSHA256 ||
		finalAnalysis.ImportClosureSHA256 != admission.ImportClosureSHA256 || finalAnalysis.CapabilityPlanSHA256 != admission.CapabilityPlanSHA256 {
		return nil, PreparedLineage{}, ErrLineage
	}
	binding, err := preparedBinding(admission)
	if err != nil {
		return nil, PreparedLineage{}, err
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
		RegionID: admission.DeclarationSHA256, RegionSpan: binding.RegionSpan, OutputName: "admitted",
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
		ConsumerBindingSHA256: plan.ConsumerBindingSHA256, ConsumerSourceSHA256: plan.ConsumerSourceSHA256, FinalSourceSHA256: plan.FinalSourceSHA256,
		InputsSHA256: plan.InputsSHA256, RequestSHA256: plan.RequestSHA256,
		DecisionSHA256: decision.IdentitySHA256, CapsuleSHA256: capsule.IdentitySHA256,
		PatchSHA256: patch.IdentitySHA256, SelectionSHA256: selection.IdentitySHA256,
	}
	lineage := PreparedLineage{
		SchemaVersion: identity.SchemaVersion, AdmissionSHA256: identity.AdmissionSHA256,
		ConsumerBindingSHA256: identity.ConsumerBindingSHA256, ConsumerSourceSHA256: identity.ConsumerSourceSHA256, FinalSourceSHA256: identity.FinalSourceSHA256,
		InputsSHA256: identity.InputsSHA256, RequestSHA256: identity.RequestSHA256,
		Decision: decision, Capsule: capsule, Patch: patch, Selection: selection, IdentitySHA256: digestJSON(identity),
	}
	lineage.provenance = publicationauth.Mint(lineage.IdentitySHA256)
	raw, err := json.Marshal(lineage)
	return raw, lineage, err
}

func (lineage PreparedLineage) Validate(admission Admission, plan numpycodec.MaterializationPlan) error {
	if admission.Validate() != nil || !lineage.provenance.Valid(lineage.IdentitySHA256) || lineage.SchemaVersion != LineageSchemaVersion || lineage.AdmissionSHA256 != admission.IdentitySHA256 ||
		lineage.ConsumerBindingSHA256 != plan.ConsumerBindingSHA256 || lineage.ConsumerSourceSHA256 != plan.ConsumerSourceSHA256 ||
		lineage.FinalSourceSHA256 != plan.FinalSourceSHA256 || Digest(plan.Request) != plan.RequestSHA256 ||
		lineage.InputsSHA256 != plan.InputsSHA256 || lineage.RequestSHA256 != plan.RequestSHA256 ||
		lineage.Capsule.ValidateDecision(lineage.Decision) != nil || lineage.Patch.ValidateDecision(lineage.Decision) != nil ||
		lineage.Selection.Validate(lineage.Decision, lineage.Capsule, lineage.Patch) != nil {
		return ErrLineage
	}
	expectedBinding, err := preparedBinding(admission)
	if err != nil || lineage.Decision.ValidateBinding(expectedBinding) != nil ||
		lineage.Patch.FinalSourceSHA256 != plan.FinalSourceSHA256 || lineage.Selection.FinalSourceSHA256 != plan.FinalSourceSHA256 {
		return ErrLineage
	}
	identity := lineageIdentity{
		SchemaVersion: lineage.SchemaVersion, AdmissionSHA256: lineage.AdmissionSHA256,
		ConsumerBindingSHA256: lineage.ConsumerBindingSHA256, ConsumerSourceSHA256: lineage.ConsumerSourceSHA256, FinalSourceSHA256: lineage.FinalSourceSHA256,
		InputsSHA256: lineage.InputsSHA256, RequestSHA256: lineage.RequestSHA256,
		DecisionSHA256: lineage.Decision.IdentitySHA256, CapsuleSHA256: lineage.Capsule.IdentitySHA256,
		PatchSHA256: lineage.Patch.IdentitySHA256, SelectionSHA256: lineage.Selection.IdentitySHA256,
	}
	if lineage.IdentitySHA256 != digestJSON(identity) {
		return ErrLineage
	}
	return nil
}

func preparedBinding(admission Admission) (preparedregion.PreparedRegionBinding, error) {
	_, liveInsSHA256, err := preparedregion.SealPreparedRegionLiveIns(map[string]json.RawMessage{})
	if err != nil {
		return preparedregion.PreparedRegionBinding{}, err
	}
	span := preparedregion.SourceSpan{
		StartLine: admission.RegionSpan.StartLine, StartColumn: admission.RegionSpan.StartColumn,
		EndLine: admission.RegionSpan.EndLine, EndColumn: admission.RegionSpan.EndColumn,
	}
	return preparedregion.PreparedRegionBinding{
		SourceSHA256: admission.SourceSHA256, ASTSHA256: admission.ASTSHA256, AnalysisSHA256: admission.AnalysisSHA256,
		RegionID: admission.DeclarationSHA256, RegionSpan: span, RegionSourceSHA256: admission.SourceSHA256,
		LiveInsSHA256: liveInsSHA256, EnvironmentSHA256: Digest([]byte("pysolate.numpy-producer.environment.v1\x00" + admission.IdentitySHA256 + "\x00" + admission.InputsSHA256)),
		ExecutionProfileSHA256: admission.ExecutionProfileSHA256, ImportClosureSHA256: admission.ImportClosureSHA256,
		CapabilityPlanSHA256: admission.CapabilityPlanSHA256, PassConfigSHA256: admission.PassConfigSHA256,
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "admitted",
	}, nil
}
