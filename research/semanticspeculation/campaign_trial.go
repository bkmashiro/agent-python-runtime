package semanticspeculation

import "errors"

type TrialBindings struct {
	ArtifactSHA256         string
	ManifestSHA256         string
	ImportInventorySHA256  string
	ExecutionProfileSHA256 string
	CapabilityPlanSHA256   string
	PrivacySHA256          string
}

func BuildScheduledTrialRecord(
	fixture SyntheticCase,
	treatment string,
	trialIndex uint32,
	bindings TrialBindings,
	result ScheduledTreatmentResult,
) (TrialRecord, error) {
	if fixture.Validate() != nil || !validTreatment(treatment) || treatment == "perfect_effect_oracle" || trialIndex == 0 || trialIndex > 5 {
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
