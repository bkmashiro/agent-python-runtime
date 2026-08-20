package semanticspeculation

import "testing"

func TestBuildScheduledTrialRecordBindsEveryFrozenIdentity(t *testing.T) {
	fixture := Phase3SyntheticCases()[2]
	result := ScheduledTreatmentResult{
		StartedNanos: 1, FinalizeNanos: 10, EndedNanos: 20,
		Outcome: TreatmentOutcome{
			FinalProgramOutcome: "success", FinalPythonStarted: true,
			ResultSHA256: syntheticDigest([]byte(`{"tail":"done","value":"weather"}`)),
			LogicalCalls: 1, PhysicalAttempts: 1, PhysicalResultBytes: 19, ProviderCostUnits: 1, ReadyBeforeFinalize: 1,
			PhysicalDispositions: PhysicalDispositions{Consumed: 1}, AuthorityDisposition: "read_consumed", WorkspaceDisposition: "untouched",
		},
	}
	bindings := TrialBindings{
		ArtifactSHA256: syntheticDigest([]byte("artifact")), ManifestSHA256: syntheticDigest([]byte("manifest")),
		ImportInventorySHA256: syntheticDigest([]byte("imports")), ExecutionProfileSHA256: syntheticDigest([]byte("profile")),
		CapabilityPlanSHA256: syntheticDigest([]byte("plan")), PrivacySHA256: syntheticDigest([]byte("privacy")),
	}
	sealed, err := BuildScheduledTrialRecord(fixture, "eager_style_gate", 1, bindings, result)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.SchemaVersion != TrialSchemaVersion || sealed.CaseMatrixSHA256 != SyntheticCaseMatrixIdentity ||
		sealed.ComparatorContractSHA256 != EagerStyleGateV1Identity || sealed.SourceSHA256 != fixture.SourceSHA256() ||
		sealed.SourceScheduleSHA256 != fixture.SourceScheduleSHA256() || sealed.InputsSHA256 != fixture.InputsSHA256() || sealed.Identity == "" {
		t.Fatalf("trial=%+v", sealed)
	}
}
