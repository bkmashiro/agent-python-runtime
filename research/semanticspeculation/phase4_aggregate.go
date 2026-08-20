package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"
)

const Phase4CampaignReportSchemaVersion = "pysolate.semantic-speculation-phase4-campaign-report.v1"

type Phase4ProfileGate struct {
	Profile             string `json:"profile"`
	EligibleCoordinates uint32 `json:"eligible_coordinates"`
	PassingCoordinates  uint32 `json:"passing_coordinates"`
	EconomicsPassed     bool   `json:"economics_passed"`
}

type Phase4CoordinateAggregate struct {
	Profile             string `json:"profile"`
	CaseID              string `json:"case_id"`
	MedianSerialNanos   uint64 `json:"median_serial_nanos"`
	MedianSemanticNanos uint64 `json:"median_semantic_nanos"`
	MedianSavingNanos   int64  `json:"median_saving_nanos"`
	ReadyTrials         uint32 `json:"ready_trials"`
	Eligible            bool   `json:"eligible"`
	Passed              bool   `json:"passed"`
}

type Phase4CampaignReport struct {
	SchemaVersion           string                      `json:"schema_version"`
	StudyID                 string                      `json:"study_id"`
	MatrixIdentity          string                      `json:"matrix_identity"`
	PreregistrationIdentity string                      `json:"preregistration_identity"`
	RecordCount             uint32                      `json:"record_count"`
	MatchedCells            uint32                      `json:"matched_cells"`
	MechanismPassed         bool                        `json:"mechanism_passed"`
	EconomicsPassed         bool                        `json:"economics_passed"`
	ProfileGates            []Phase4ProfileGate         `json:"profile_gates"`
	Coordinates             []Phase4CoordinateAggregate `json:"coordinates"`
}

func DecodePhase4TrialRecord(raw []byte) (Phase4TrialRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Phase4TrialRecord
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF || validatePhase4TrialRecord(value) != nil {
		return Phase4TrialRecord{}, errors.New("invalid phase 4 trial record")
	}
	canonical, _ := json.Marshal(value)
	canonical = append(canonical, '\n')
	if !bytes.Equal(canonical, raw) {
		return Phase4TrialRecord{}, errors.New("noncanonical phase 4 trial record")
	}
	return value, nil
}

func AggregatePhase4Campaign(records []Phase4TrialRecord) (Phase4CampaignReport, error) {
	if len(records) != 360 {
		return Phase4CampaignReport{}, errors.New("incomplete phase 4 campaign")
	}
	expected := Phase4CampaignCoordinates()
	seen := map[Phase4CampaignCoordinate]Phase4TrialRecord{}
	for _, record := range records {
		key := Phase4CampaignCoordinate{Profile: record.Profile, CaseID: record.CaseID, Treatment: record.Treatment, TrialIndex: record.TrialIndex}
		if _, ok := seen[key]; ok {
			return Phase4CampaignReport{}, errors.New("duplicate phase 4 trial")
		}
		seen[key] = record
	}
	for _, coordinate := range expected {
		if _, ok := seen[coordinate]; !ok {
			return Phase4CampaignReport{}, errors.New("missing phase 4 trial")
		}
	}
	matrixRows := Phase4SyntheticCoordinates()
	byID := map[string]Phase4SyntheticCoordinate{}
	for _, row := range matrixRows {
		byID[row.Fixture.ID] = row
	}
	mechanism := true
	matched := uint32(0)
	for _, profile := range phase4Profiles {
		for _, row := range matrixRows {
			for trial := uint32(1); trial <= Phase4TrialsPerTreatment; trial++ {
				serial := seen[Phase4CampaignCoordinate{profile, row.Fixture.ID, "serial_whole_file", trial}]
				eager := seen[Phase4CampaignCoordinate{profile, row.Fixture.ID, "eager_style_gate", trial}]
				semantic := seen[Phase4CampaignCoordinate{profile, row.Fixture.ID, "semantic_pre_dispatch", trial}]
				matched++
				if !samePhase4Outcome(serial, eager) || !samePhase4Outcome(serial, semantic) || semantic.AnalyzerSessionCount != 1 || semantic.FormalGuestExecutions != 1 || semantic.OrphanedPhysicalCount != 0 || semantic.AnalyzerInvocations > uint32(len(row.CandidatePrefixIndices)) {
					mechanism = false
				}
				if profile == "preprovisioned_equivalent_capacity" && (semantic.PreparedOrCOWHitCount != 1 || semantic.PreparedOrCOWFallbackCount != 0 || semantic.ProvisioningNanos == 0) {
					mechanism = false
				}
				if profile == "cold_end_to_end" && (semantic.PreparedOrCOWHitCount != 0 || semantic.ProvisioningNanos != 0) {
					mechanism = false
				}
			}
		}
	}
	aggregates := []Phase4CoordinateAggregate{}
	profileGates := []Phase4ProfileGate{}
	economics := true
	for _, profile := range phase4Profiles {
		gate := Phase4ProfileGate{Profile: profile}
		for _, row := range matrixRows {
			serial, semanticNanos := []uint64{}, []uint64{}
			ready := uint32(0)
			for trial := uint32(1); trial <= Phase4TrialsPerTreatment; trial++ {
				serial = append(serial, seen[Phase4CampaignCoordinate{profile, row.Fixture.ID, "serial_whole_file", trial}].TotalElapsedNanos)
				record := seen[Phase4CampaignCoordinate{profile, row.Fixture.ID, "semantic_pre_dispatch", trial}]
				semanticNanos = append(semanticNanos, record.TotalElapsedNanos)
				if record.ReadyBeforeFinalize > 0 {
					ready++
				}
			}
			sort.Slice(serial, func(i, j int) bool { return serial[i] < serial[j] })
			sort.Slice(semanticNanos, func(i, j int) bool { return semanticNanos[i] < semanticNanos[j] })
			serialMedian, semanticMedian := serial[2], semanticNanos[2]
			saving := int64(serialMedian) - int64(semanticMedian)
			eligible := row.ExpectedPreDispatch == "admit_consumed" && row.Fixture.Chunks[len(row.Fixture.Chunks)-1].ReleaseAfterMilliseconds-row.Fixture.Chunks[row.CandidatePrefixIndices[len(row.CandidatePrefixIndices)-1]-1].ReleaseAfterMilliseconds >= 3000 && row.PhysicalDelayMillis >= 2500
			passed := eligible && saving >= 100000000 && ready >= 4
			if eligible {
				gate.EligibleCoordinates++
				if passed {
					gate.PassingCoordinates++
				}
			}
			aggregates = append(aggregates, Phase4CoordinateAggregate{profile, row.Fixture.ID, serialMedian, semanticMedian, saving, ready, eligible, passed})
		}
		gate.EconomicsPassed = gate.PassingCoordinates >= 1
		if !gate.EconomicsPassed {
			economics = false
		}
		profileGates = append(profileGates, gate)
	}
	return Phase4CampaignReport{Phase4CampaignReportSchemaVersion, Phase4StudyID, Phase4ExtensionMatrixIdentity, Phase4PreregistrationIdentity, uint32(len(records)), matched, mechanism, economics, profileGates, aggregates}, nil
}

func validatePhase4TrialRecord(r Phase4TrialRecord) error {
	if r.SchemaVersion != Phase4TrialRecordSchemaVersion || r.ExecutionTimeoutNanos != uint64(30*time.Second) || r.TotalElapsedNanos == 0 || r.Profile == "" || r.CaseID == "" || r.Treatment == "" || r.TrialIndex == 0 {
		return errors.New("invalid phase 4 trial")
	}
	return nil
}
func samePhase4Outcome(a, b Phase4TrialRecord) bool {
	return a.FinalProgramOutcome == b.FinalProgramOutcome && a.ResultSHA256 == b.ResultSHA256 && a.ErrorClass == b.ErrorClass && a.LogicalCallCount == b.LogicalCallCount && a.AuthorityTerminalDisposition == b.AuthorityTerminalDisposition && a.WorkspaceTerminalDisposition == b.WorkspaceTerminalDisposition
}
