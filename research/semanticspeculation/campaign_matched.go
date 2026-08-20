package semanticspeculation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

var ErrInvalidMatchedCampaign = errors.New("invalid matched semantic-speculation campaign")

var achievedTreatments = [...]string{"serial_whole_file", "eager_style_gate", "semantic_pre_dispatch"}

const phase3ShuffleSeed uint64 = 20260820

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
	order := matchedTreatmentOrder(fixture.ID, trialIndex)
	records := make([]TrialRecord, 0, len(order))
	for _, treatmentName := range order {
		treatment, err := factory(treatmentName, trialIndex)
		if err != nil || treatment == nil {
			return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
		}
		result, err := RunScheduledTreatment(ctx, fixture, treatment)
		if err != nil {
			return MatchedCampaignResult{}, errors.Join(ErrInvalidMatchedCampaign, err)
		}
		record, err := BuildScheduledTrialRecord(fixture, treatmentName, trialIndex, bindings, result)
		if err != nil {
			return MatchedCampaignResult{}, fmt.Errorf("%w: seal treatment %s: %v", ErrInvalidMatchedCampaign, treatmentName, err)
		}
		if !recordMatchesFrozenExpectation(record, fixture) {
			return MatchedCampaignResult{}, fmt.Errorf("%w: treatment %s outcome=%s logical_calls=%d authority=%s workspace=%s", ErrInvalidMatchedCampaign, treatmentName, record.FinalProgramOutcome, record.LogicalCalls, record.AuthorityDisposition, record.WorkspaceDisposition)
		}
		records = append(records, record)
	}

	// Aggregate once without an oracle-equivalent shortcut: only after all three
	// achieved records satisfy frozen expectations may analysis-only estimation run.
	var serial TrialRecord
	for _, record := range records {
		if record.Treatment == "serial_whole_file" {
			serial = record
			break
		}
	}
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

func matchedTreatmentOrder(caseID string, trialIndex uint32) []string {
	type rankedTreatment struct {
		name string
		rank [sha256.Size]byte
	}
	ranked := make([]rankedTreatment, 0, len(achievedTreatments))
	for _, name := range achievedTreatments {
		ranked = append(ranked, rankedTreatment{
			name: name,
			rank: sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d\x00%s", phase3ShuffleSeed, caseID, trialIndex, name))),
		})
	}
	sort.Slice(ranked, func(i, j int) bool { return bytes.Compare(ranked[i].rank[:], ranked[j].rank[:]) < 0 })
	order := make([]string, len(ranked))
	for index, treatment := range ranked {
		order[index] = treatment.name
	}
	return order
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
