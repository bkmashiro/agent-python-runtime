package v1

import (
	"encoding/json"
	"errors"
)

var ErrInvalidComparison = errors.New("invalid evaluation comparison")

type experimentContract struct {
	SchemaVersion               string     `json:"schema_version"`
	EvaluationScenarios         []string   `json:"evaluation_scenarios"`
	RepeatsPerScenarioCondition int        `json:"repeats_per_scenario_condition"`
	Thresholds                  thresholds `json:"thresholds"`
}

type thresholds struct {
	TaskSuccessNoninferiorityPP        float64 `json:"task_success_noninferiority_pp"`
	CompoundRoundTripReductionFraction float64 `json:"compound_round_trip_reduction_fraction"`
	CompoundTokenReductionFraction     float64 `json:"compound_token_reduction_fraction"`
	SimpleUnnecessaryWorkflowRate      float64 `json:"simple_unnecessary_workflow_rate"`
}

type comparisonContract struct {
	SchemaVersion string `json:"schema_version"`
	Decision      string `json:"decision"`
	Aggregates    []struct {
		Condition               string  `json:"condition"`
		IncludedTrials          int     `json:"included_trials"`
		UnnecessaryWorkflowRate float64 `json:"unnecessary_workflow_rate"`
	} `json:"aggregates"`
	Completeness struct {
		PlannedTrials        int      `json:"planned_trials"`
		ObservedTrials       int      `json:"observed_trials"`
		IncludedTrials       int      `json:"included_trials"`
		MissingTrialIDs      []string `json:"missing_trial_ids"`
		DuplicateTrialIDs    []string `json:"duplicate_trial_ids"`
		UnregisteredTrialIDs []string `json:"unregistered_trial_ids"`
	} `json:"completeness"`
	Exclusions []struct {
		Preregistered bool `json:"preregistered"`
	} `json:"exclusions"`
	PairedContrasts []struct {
		Baseline     string  `json:"baseline"`
		Candidate    string  `json:"candidate"`
		Metric       string  `json:"metric"`
		CILower      float64 `json:"ci_lower"`
		PairedTrials int     `json:"paired_trials"`
	} `json:"paired_contrasts"`
	SafetyTotals struct {
		UnauthorizedExecutions            int `json:"unauthorized_executions"`
		IrreversiblePreapprovalExecutions int `json:"irreversible_preapproval_executions"`
		DuplicateCommittedEffects         int `json:"duplicate_committed_effects"`
		CompensationAsRollback            int `json:"compensation_as_rollback"`
		StrongerTerminalStates            int `json:"stronger_terminal_states"`
		StaleCatalogAcceptances           int `json:"stale_catalog_acceptances"`
		ForgedAuthorityAcceptances        int `json:"forged_authority_acceptances"`
	} `json:"safety_totals"`
}

func decodeDocument(data []byte, value any) error {
	if len(data) == 0 || !json.Valid(data) {
		return ErrInvalidComparison
	}
	return json.Unmarshal(data, value)
}

// ValidateComparison enforces the cross-document arithmetic and promotion
// rules that JSON Schema cannot express. Callers must schema-validate both
// documents before invoking this function.
func ValidateComparison(experimentJSON, comparisonJSON []byte) error {
	var experiment experimentContract
	var comparison comparisonContract
	if decodeDocument(experimentJSON, &experiment) != nil || decodeDocument(comparisonJSON, &comparison) != nil ||
		experiment.SchemaVersion != "evaluation-experiment/v1" || comparison.SchemaVersion != "evaluation-comparison/v1" ||
		len(experiment.EvaluationScenarios) == 0 || experiment.RepeatsPerScenarioCondition <= 0 {
		return ErrInvalidComparison
	}
	expectedPerCondition := len(experiment.EvaluationScenarios) * experiment.RepeatsPerScenarioCondition
	expectedTotal := expectedPerCondition * 3
	completeness := comparison.Completeness
	if completeness.PlannedTrials != expectedTotal || completeness.ObservedTrials != expectedTotal ||
		completeness.IncludedTrials+len(comparison.Exclusions) != completeness.ObservedTrials ||
		len(completeness.MissingTrialIDs) != 0 || len(completeness.DuplicateTrialIDs) != 0 || len(completeness.UnregisteredTrialIDs) != 0 {
		return ErrInvalidComparison
	}
	includedByCondition := make(map[string]int, 3)
	includedTotal := 0
	var hybridUnnecessary float64
	for _, aggregate := range comparison.Aggregates {
		if _, duplicate := includedByCondition[aggregate.Condition]; duplicate {
			return ErrInvalidComparison
		}
		includedByCondition[aggregate.Condition] = aggregate.IncludedTrials
		includedTotal += aggregate.IncludedTrials
		if aggregate.Condition == "hybrid" {
			hybridUnnecessary = aggregate.UnnecessaryWorkflowRate
		}
	}
	if len(includedByCondition) != 3 || includedTotal != completeness.IncludedTrials {
		return ErrInvalidComparison
	}
	for _, contrast := range comparison.PairedContrasts {
		if contrast.Baseline == contrast.Candidate || contrast.PairedTrials <= 0 ||
			contrast.PairedTrials > includedByCondition[contrast.Baseline] || contrast.PairedTrials > includedByCondition[contrast.Candidate] {
			return ErrInvalidComparison
		}
	}
	if comparison.Decision != "promote" {
		return nil
	}
	if len(comparison.Exclusions) != 0 || completeness.IncludedTrials != expectedTotal {
		return ErrInvalidComparison
	}
	for _, count := range includedByCondition {
		if count != expectedPerCondition {
			return ErrInvalidComparison
		}
	}
	for _, excluded := range comparison.Exclusions {
		if !excluded.Preregistered {
			return ErrInvalidComparison
		}
	}
	safety := comparison.SafetyTotals
	if safety.UnauthorizedExecutions != 0 || safety.IrreversiblePreapprovalExecutions != 0 ||
		safety.DuplicateCommittedEffects != 0 || safety.CompensationAsRollback != 0 ||
		safety.StrongerTerminalStates != 0 || safety.StaleCatalogAcceptances != 0 || safety.ForgedAuthorityAcceptances != 0 {
		return ErrInvalidComparison
	}
	var taskGate, roundTripGate, tokenGate bool
	for _, contrast := range comparison.PairedContrasts {
		if contrast.Baseline != "direct-only" || contrast.Candidate != "hybrid" || contrast.PairedTrials != expectedPerCondition {
			continue
		}
		switch contrast.Metric {
		case "task_success_difference_pp":
			taskGate = contrast.CILower >= experiment.Thresholds.TaskSuccessNoninferiorityPP
		case "compound_round_trip_reduction_fraction":
			roundTripGate = contrast.CILower >= experiment.Thresholds.CompoundRoundTripReductionFraction
		case "compound_token_reduction_fraction":
			tokenGate = contrast.CILower >= experiment.Thresholds.CompoundTokenReductionFraction
		}
	}
	if !taskGate || (!roundTripGate && !tokenGate) || hybridUnnecessary > experiment.Thresholds.SimpleUnnecessaryWorkflowRate {
		return ErrInvalidComparison
	}
	return nil
}
