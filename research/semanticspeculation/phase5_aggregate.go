package semanticspeculation

import (
	"errors"
	"math"
	"sort"
)

const Phase5CampaignReportSchemaVersion = "pysolate.semantic-speculation-phase5-campaign-report.v1"

type Phase5CoordinateAggregate struct {
	Profile              string `json:"profile"`
	CaseID               string `json:"case_id"`
	MatchedTrials        uint32 `json:"matched_trials"`
	PositiveSavingTrials uint32 `json:"positive_saving_trials"`
	MedianNetSavingNanos int64  `json:"median_net_saving_nanos"`
	Passed               bool   `json:"passed"`
}

type Phase5ProfileGate struct {
	Profile             string `json:"profile"`
	EligibleCoordinates uint32 `json:"eligible_coordinates"`
	PassingCoordinates  uint32 `json:"passing_coordinates"`
	EconomicsPassed     bool   `json:"economics_passed"`
}

type Phase5CampaignReport struct {
	SchemaVersion           string                      `json:"schema_version"`
	StudyID                 string                      `json:"study_id"`
	MatrixIdentity          string                      `json:"matrix_identity"`
	PreregistrationIdentity string                      `json:"preregistration_identity"`
	HarnessIdentity         string                      `json:"harness_identity"`
	RecordCount             uint32                      `json:"record_count"`
	MatchedCells            uint32                      `json:"matched_cells"`
	MechanismPassed         bool                        `json:"mechanism_passed"`
	EconomicsPassed         bool                        `json:"economics_passed"`
	ProfileGates            []Phase5ProfileGate         `json:"profile_gates"`
	Coordinates             []Phase5CoordinateAggregate `json:"coordinates"`
	NoGoAction              string                      `json:"no_go_action,omitempty"`
}

func AggregatePhase5Campaign(records []Phase5TrialRecord, harnessIdentity string) (Phase5CampaignReport, error) {
	if len(records) != len(Phase5CampaignCoordinates()) || !digestPattern.MatchString(harnessIdentity) {
		return Phase5CampaignReport{}, errors.New("incomplete phase 5 campaign")
	}
	seen := make(map[Phase5CampaignCoordinate]Phase5TrialRecord, len(records))
	for _, record := range records {
		if validatePhase5TrialRecord(record, harnessIdentity) != nil || record.TotalCriticalPathNanos > math.MaxInt64 {
			return Phase5CampaignReport{}, errors.New("invalid phase 5 campaign record")
		}
		key := Phase5CampaignCoordinate{Profile: record.Profile, CaseID: record.CaseID, Treatment: record.Treatment, TrialIndex: record.TrialIndex}
		if _, exists := seen[key]; exists {
			return Phase5CampaignReport{}, ErrPhase5DuplicateTrial
		}
		seen[key] = record
	}
	for _, coordinate := range Phase5CampaignCoordinates() {
		if _, exists := seen[coordinate]; !exists {
			return Phase5CampaignReport{}, errors.New("missing phase 5 campaign coordinate")
		}
	}

	report := Phase5CampaignReport{SchemaVersion: Phase5CampaignReportSchemaVersion, StudyID: Phase5StudyID, MatrixIdentity: Phase5CaseMatrixIdentity, PreregistrationIdentity: Phase5PreregistrationIdentity, HarnessIdentity: harnessIdentity, RecordCount: uint32(len(records)), MechanismPassed: true, EconomicsPassed: true}
	for _, profile := range phase5Profiles {
		gate := Phase5ProfileGate{Profile: profile}
		for _, candidate := range Phase5Cases() {
			if !candidate.EconomicsEligible {
				continue
			}
			savings := make([]int64, 0, Phase5TrialsPerTreatment)
			positive := uint32(0)
			for trial := uint32(1); trial <= Phase5TrialsPerTreatment; trial++ {
				original := seen[Phase5CampaignCoordinate{Profile: profile, CaseID: candidate.ID, Treatment: "original_unchanged", TrialIndex: trial}]
				derived := seen[Phase5CampaignCoordinate{Profile: profile, CaseID: candidate.ID, Treatment: "prepared_region_derived", TrialIndex: trial}]
				if !phase5OutcomesMatch(original, derived) {
					report.MechanismPassed = false
				}
				saving := int64(original.TotalCriticalPathNanos) - int64(derived.TotalCriticalPathNanos)
				savings = append(savings, saving)
				if saving > 0 {
					positive++
				}
				report.MatchedCells++
			}
			sort.Slice(savings, func(i, j int) bool { return savings[i] < savings[j] })
			median := savings[len(savings)/2]
			passed := positive >= 4 && median > 0
			gate.EligibleCoordinates++
			if passed {
				gate.PassingCoordinates++
			}
			report.Coordinates = append(report.Coordinates, Phase5CoordinateAggregate{Profile: profile, CaseID: candidate.ID, MatchedTrials: Phase5TrialsPerTreatment, PositiveSavingTrials: positive, MedianNetSavingNanos: median, Passed: passed})
		}
		gate.EconomicsPassed = gate.PassingCoordinates >= 2
		if !gate.EconomicsPassed {
			report.EconomicsPassed = false
		}
		report.ProfileGates = append(report.ProfileGates, gate)
	}
	if !report.MechanismPassed || !report.EconomicsPassed {
		report.NoGoAction = "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority"
	}
	return report, nil
}

func phase5OutcomesMatch(a, b Phase5TrialRecord) bool {
	return a.ActualDisposition == b.ActualDisposition && a.ActualOutcome == b.ActualOutcome && a.ResultSHA256 == b.ResultSHA256 && a.ErrorClass == b.ErrorClass && a.ErrorMessageSHA256 == b.ErrorMessageSHA256 && a.TracebackSHA256 == b.TracebackSHA256 && a.LogsSHA256 == b.LogsSHA256 && a.LogicalCallCount == b.LogicalCallCount && a.OrphanedPhysicalCount == b.OrphanedPhysicalCount && a.AuthorityTerminalDisposition == b.AuthorityTerminalDisposition && a.WorkspaceTerminalDisposition == b.WorkspaceTerminalDisposition
}
