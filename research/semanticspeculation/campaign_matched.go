package semanticspeculation

import (
	"context"
	"errors"
)

var ErrInvalidMatchedCampaign = errors.New("invalid matched semantic-speculation campaign")

var achievedTreatmentOrder = [...]string{"serial_whole_file", "eager_style_gate", "semantic_pre_dispatch"}

type MatchedTreatmentFactory func(treatment string, trialIndex uint32) (ScheduledTreatment, error)
type PerfectEffectEstimator func(serial TrialRecord) (uint64, error)

type MatchedCampaignResult struct {
	Records   []TrialRecord               `json:"records"`
	Oracle    PerfectEffectOracleEstimate `json:"oracle"`
	Aggregate MatchedCaseAggregate        `json:"aggregate"`
}

// RunMatchedCaseCampaign executes the three achieved lanes against one frozen
// case and trial index. The factory receives the lane and trial index, but never
// the case identity, expected outcome, source, inputs, or treatment hints. Each
// adapter receives only canonical inputs and scheduled source chunks through the
// ScheduledTreatment interface.
func RunMatchedCaseCampaign(
	ctx context.Context,
	fixture SyntheticCase,
	trialIndex uint32,
	bindings TrialBindings,
	factory MatchedTreatmentFactory,
	estimator PerfectEffectEstimator,
) (MatchedCampaignResult, error) {
	if ctx == nil || !isFrozenPhase3Case(fixture) || trialIndex == 0 || trialIndex > 5 || factory == nil || estimator == nil {
		return MatchedCampaignResult{}, ErrInvalidMatchedCampaign
	}
	records := make([]TrialRecord, 0, len(achievedTreatmentOrder))
	for _, treatmentName := range achievedTreatmentOrder {
		treatment, err := factory(treatmentName, trialIndex)
		if err != nil || treatment == nil {
			return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
		}
		result, err := RunScheduledTreatment(ctx, fixture, treatment)
		if err != nil {
			return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
		}
		record, err := BuildScheduledTrialRecord(fixture, treatmentName, trialIndex, bindings, result)
		if err != nil || !recordMatchesFrozenExpectation(record, fixture) {
			return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
		}
		records = append(records, record)
	}

	// Aggregate once without an oracle-equivalent shortcut: only after all three
	// achieved records satisfy frozen expectations may analysis-only estimation run.
	serial := records[0]
	oracleElapsed, err := estimator(serial)
	if err != nil || oracleElapsed == 0 {
		return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
	}
	oracle, err := NewPerfectEffectOracleEstimate(serial, oracleElapsed)
	if err != nil {
		return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
	}
	aggregate, err := AggregateMatchedTrials(records, oracle)
	if err != nil {
		return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
	}
	return MatchedCampaignResult{Records: records, Oracle: oracle, Aggregate: aggregate}, nil
}

func recordMatchesFrozenExpectation(record TrialRecord, fixture SyntheticCase) bool {
	if record.CaseID != fixture.ID || record.FinalProgramOutcome != fixture.ExpectedOutcome || record.LogicalCalls != fixture.ExpectedLogicalCalls {
		return false
	}
	if fixture.ExpectedLogicalCalls == 0 && record.AuthorityDisposition != "unchanged" {
		return false
	}
	if fixture.ExpectedLogicalCalls > 0 && record.AuthorityDisposition != "read_consumed" {
		return false
	}
	if fixture.ExpectedOutcome == "success" {
		return record.WorkspaceDisposition == "published"
	}
	return record.WorkspaceDisposition == "discarded"
}
