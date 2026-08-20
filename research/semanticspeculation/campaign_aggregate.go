package semanticspeculation

import "errors"

var ErrInvalidMatchedTrials = errors.New("invalid matched semantic-speculation trials")

type PerfectEffectOracleEstimate struct {
	StudyID                string `json:"study_id"`
	PreregistrationSHA256  string `json:"preregistration_sha256"`
	CaseMatrixSHA256       string `json:"case_matrix_sha256"`
	CaseID                 string `json:"case_id"`
	TrialIndex             uint32 `json:"trial_index"`
	SourceSHA256           string `json:"source_sha256"`
	SourceScheduleSHA256   string `json:"source_schedule_sha256"`
	InputsSHA256           string `json:"inputs_sha256"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ManifestSHA256         string `json:"manifest_sha256"`
	ImportInventorySHA256  string `json:"import_inventory_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	PrivacySHA256          string `json:"privacy_sha256"`
	ElapsedNanos           uint64 `json:"elapsed_nanos"`
}

type MatchedCaseAggregate struct {
	CaseID                            string `json:"case_id"`
	TrialIndex                        uint32 `json:"trial_index"`
	SerialElapsedNanos                uint64 `json:"serial_elapsed_nanos"`
	EagerElapsedNanos                 uint64 `json:"eager_elapsed_nanos"`
	SemanticElapsedNanos              uint64 `json:"semantic_elapsed_nanos"`
	OracleElapsedNanos                uint64 `json:"oracle_elapsed_nanos"`
	SemanticVersusSerialNanos         uint64 `json:"semantic_versus_serial_nanos"`
	FalseConservativeNanos            uint64 `json:"false_conservative_nanos"`
	SafeOverlapReadyBeforeFinalize    uint32 `json:"safe_overlap_ready_before_finalize"`
	OrphanedPhysicalAttempts          uint32 `json:"orphaned_physical_attempts"`
	PhysicalResultBytes               uint64 `json:"physical_result_bytes"`
	ProviderCostUnits                 uint64 `json:"provider_cost_units"`
	OracleExcludedFromAchievedSpeedup bool   `json:"oracle_excluded_from_achieved_speedup"`
}

func NewPerfectEffectOracleEstimate(bound TrialRecord, elapsedNanos uint64) (PerfectEffectOracleEstimate, error) {
	if validateTrialRecord(bound, true) != nil || elapsedNanos == 0 {
		return PerfectEffectOracleEstimate{}, ErrInvalidMatchedTrials
	}
	return PerfectEffectOracleEstimate{
		StudyID: bound.StudyID, PreregistrationSHA256: bound.PreregistrationSHA256,
		CaseMatrixSHA256: bound.CaseMatrixSHA256, CaseID: bound.CaseID, TrialIndex: bound.TrialIndex,
		SourceSHA256: bound.SourceSHA256, SourceScheduleSHA256: bound.SourceScheduleSHA256, InputsSHA256: bound.InputsSHA256,
		ArtifactSHA256: bound.ArtifactSHA256, ManifestSHA256: bound.ManifestSHA256, ImportInventorySHA256: bound.ImportInventorySHA256,
		ExecutionProfileSHA256: bound.ExecutionProfileSHA256, CapabilityPlanSHA256: bound.CapabilityPlanSHA256,
		PrivacySHA256: bound.PrivacySHA256, ElapsedNanos: elapsedNanos,
	}, nil
}

func AggregateMatchedTrials(records []TrialRecord, oracle PerfectEffectOracleEstimate) (MatchedCaseAggregate, error) {
	if len(records) != 3 {
		return MatchedCaseAggregate{}, ErrInvalidMatchedTrials
	}
	lanes := make(map[string]TrialRecord, 3)
	for _, record := range records {
		if validateTrialRecord(record, true) != nil || record.Treatment == "perfect_effect_oracle" {
			return MatchedCaseAggregate{}, ErrInvalidMatchedTrials
		}
		if _, exists := lanes[record.Treatment]; exists {
			return MatchedCaseAggregate{}, ErrInvalidMatchedTrials
		}
		lanes[record.Treatment] = record
	}
	serial, serialOK := lanes["serial_whole_file"]
	eager, eagerOK := lanes["eager_style_gate"]
	semantic, semanticOK := lanes["semantic_pre_dispatch"]
	if !serialOK || !eagerOK || !semanticOK || !sameTrialBindings(serial, eager) || !sameTrialBindings(serial, semantic) ||
		!sameProgramOutcome(serial, eager) || !sameProgramOutcome(serial, semantic) || !oracle.matches(serial) {
		return MatchedCaseAggregate{}, ErrInvalidMatchedTrials
	}
	serialElapsed := trialElapsedNanos(serial)
	eagerElapsed := trialElapsedNanos(eager)
	semanticElapsed := trialElapsedNanos(semantic)
	return MatchedCaseAggregate{
		CaseID: serial.CaseID, TrialIndex: serial.TrialIndex,
		SerialElapsedNanos: serialElapsed, EagerElapsedNanos: eagerElapsed,
		SemanticElapsedNanos: semanticElapsed, OracleElapsedNanos: oracle.ElapsedNanos,
		SemanticVersusSerialNanos:      positiveDelta(serialElapsed, semanticElapsed),
		FalseConservativeNanos:         positiveDelta(eagerElapsed, semanticElapsed),
		SafeOverlapReadyBeforeFinalize: semantic.ReadyBeforeFinalize,
		OrphanedPhysicalAttempts:       semantic.PhysicalDispositions.Orphaned,
		PhysicalResultBytes:            semantic.PhysicalResultBytes, ProviderCostUnits: semantic.ProviderCostUnits,
		OracleExcludedFromAchievedSpeedup: true,
	}, nil
}

func (oracle PerfectEffectOracleEstimate) matches(record TrialRecord) bool {
	return oracle.ElapsedNanos > 0 && oracle.StudyID == record.StudyID && oracle.PreregistrationSHA256 == record.PreregistrationSHA256 &&
		oracle.CaseMatrixSHA256 == record.CaseMatrixSHA256 && oracle.CaseID == record.CaseID && oracle.TrialIndex == record.TrialIndex &&
		oracle.SourceSHA256 == record.SourceSHA256 && oracle.SourceScheduleSHA256 == record.SourceScheduleSHA256 && oracle.InputsSHA256 == record.InputsSHA256 &&
		oracle.ArtifactSHA256 == record.ArtifactSHA256 && oracle.ManifestSHA256 == record.ManifestSHA256 && oracle.ImportInventorySHA256 == record.ImportInventorySHA256 &&
		oracle.ExecutionProfileSHA256 == record.ExecutionProfileSHA256 && oracle.CapabilityPlanSHA256 == record.CapabilityPlanSHA256 && oracle.PrivacySHA256 == record.PrivacySHA256
}

func sameTrialBindings(left, right TrialRecord) bool {
	return left.SchemaVersion == right.SchemaVersion && left.StudyID == right.StudyID &&
		left.PreregistrationSHA256 == right.PreregistrationSHA256 && left.CaseMatrixSHA256 == right.CaseMatrixSHA256 &&
		left.CaseID == right.CaseID && left.TrialIndex == right.TrialIndex && left.SourceSHA256 == right.SourceSHA256 &&
		left.SourceScheduleSHA256 == right.SourceScheduleSHA256 && left.InputsSHA256 == right.InputsSHA256 &&
		left.ArtifactSHA256 == right.ArtifactSHA256 && left.ManifestSHA256 == right.ManifestSHA256 &&
		left.ImportInventorySHA256 == right.ImportInventorySHA256 && left.ExecutionProfileSHA256 == right.ExecutionProfileSHA256 &&
		left.CapabilityPlanSHA256 == right.CapabilityPlanSHA256 && left.PrivacySHA256 == right.PrivacySHA256
}

func sameProgramOutcome(left, right TrialRecord) bool {
	return left.FinalProgramOutcome == right.FinalProgramOutcome && left.ResultSHA256 == right.ResultSHA256 &&
		left.ErrorClass == right.ErrorClass && left.LogicalCalls == right.LogicalCalls && left.AuthorityDisposition == right.AuthorityDisposition
}

func trialElapsedNanos(record TrialRecord) uint64 { return record.EndedNanos - record.StartedNanos }

func positiveDelta(left, right uint64) uint64 {
	if left > right {
		return left - right
	}
	return 0
}
