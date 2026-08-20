package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"errors"
)

type TrialBindings struct {
	ArtifactSHA256         string `json:"artifact_sha256"`
	ManifestSHA256         string `json:"manifest_sha256"`
	ImportInventorySHA256  string `json:"import_inventory_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	PrivacySHA256          string `json:"privacy_sha256"`
}

func BuildScheduledTrialRecord(
	fixture SyntheticCase,
	treatment string,
	trialIndex uint32,
	bindings TrialBindings,
	result ScheduledTreatmentResult,
) (TrialRecord, error) {
	if !isFrozenPhase3Case(fixture) || !validTreatment(treatment) || treatment == "perfect_effect_oracle" || trialIndex == 0 || trialIndex > 5 {
		return TrialRecord{}, errors.New("invalid scheduled trial")
	}
	comparatorIdentity := ""
	if treatment == "eager_style_gate" {
		comparatorIdentity = EagerStyleGateV1Identity
	}
	value := TrialRecord{
		SchemaVersion:            TrialSchemaVersion,
		StudyID:                  "semantic-speculation-v1",
		PreregistrationSHA256:    PreregistrationIdentity,
		CaseMatrixSHA256:         SyntheticCaseMatrixIdentity,
		CaseID:                   fixture.ID,
		Treatment:                treatment,
		ComparatorContractSHA256: comparatorIdentity,
		TrialIndex:               trialIndex,
		SourceSHA256:             fixture.SourceSHA256(),
		SourceScheduleSHA256:     fixture.SourceScheduleSHA256(),
		InputsSHA256:             fixture.InputsSHA256(),
		ArtifactSHA256:           bindings.ArtifactSHA256,
		ManifestSHA256:           bindings.ManifestSHA256,
		ImportInventorySHA256:    bindings.ImportInventorySHA256,
		ExecutionProfileSHA256:   bindings.ExecutionProfileSHA256,
		CapabilityPlanSHA256:     bindings.CapabilityPlanSHA256,
		PrivacySHA256:            bindings.PrivacySHA256,
		FinalProgramOutcome:      result.Outcome.FinalProgramOutcome,
		FinalPythonStarted:       result.Outcome.FinalPythonStarted,
		PrefixPythonExecutions:   result.Outcome.PrefixPythonExecutions,
		ResultSHA256:             result.Outcome.ResultSHA256,
		ErrorClass:               result.Outcome.ErrorClass,
		LogicalCalls:             result.Outcome.LogicalCalls,
		PhysicalAttempts:         result.Outcome.PhysicalAttempts,
		PhysicalResultBytes:      result.Outcome.PhysicalResultBytes,
		ProviderCostUnits:        result.Outcome.ProviderCostUnits,
		ReadyBeforeFinalize:      result.Outcome.ReadyBeforeFinalize,
		PhysicalDispositions:     result.Outcome.PhysicalDispositions,
		AuthorityDisposition:     result.Outcome.AuthorityDisposition,
		WorkspaceDisposition:     result.Outcome.WorkspaceDisposition,
		StartedNanos:             result.StartedNanos,
		FinalizedNanos:           result.FinalizeNanos,
		EndedNanos:               result.EndedNanos,
	}
	return SealTrialRecord(value)
}

func isFrozenPhase3Case(fixture SyntheticCase) bool {
	if fixture.Validate() != nil {
		return false
	}
	want, err := json.Marshal(fixture.Projection())
	if err != nil {
		return false
	}
	for _, frozen := range Phase3SyntheticCases() {
		if frozen.ID != fixture.ID {
			continue
		}
		got, marshalErr := json.Marshal(frozen.Projection())
		return marshalErr == nil && bytes.Equal(want, got)
	}
	return false
}
