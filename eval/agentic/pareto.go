package agentic

import "errors"

var ErrCounterfactualComparison = errors.New("invalid hybrid counterfactual comparison")

type RouteOutcome struct {
	OutcomeSuccess bool   `json:"outcome_success"`
	StrictPass     bool   `json:"strict_pass"`
	ProviderCalls  uint32 `json:"provider_calls"`
	TotalTokens    int64  `json:"total_tokens"`
}

type HybridCounterfactualComparison struct {
	OutcomeRegret      uint32 `json:"outcome_regret"`
	StrictRegret       uint32 `json:"strict_regret"`
	ProviderCallRegret uint32 `json:"provider_call_regret"`
	TokenRegret        int64  `json:"token_regret"`
	ParetoDominated    bool   `json:"pareto_dominated"`
	DominatingArm      string `json:"dominating_arm,omitempty"`
}

type HybridRegretReport struct {
	Tasks                   uint32 `json:"tasks"`
	OutcomeRegretTasks      uint32 `json:"outcome_regret_tasks"`
	StrictRegretTasks       uint32 `json:"strict_regret_tasks"`
	ParetoDominatedTasks    uint32 `json:"pareto_dominated_tasks"`
	TotalProviderCallRegret uint64 `json:"total_provider_call_regret"`
	TotalTokenRegret        int64  `json:"total_token_regret"`
}

func CompareHybridCounterfactuals(direct, python, hybrid RouteOutcome) (HybridCounterfactualComparison, error) {
	if !validRouteOutcome(direct) || !validRouteOutcome(python) || !validRouteOutcome(hybrid) {
		return HybridCounterfactualComparison{}, ErrCounterfactualComparison
	}
	result := HybridCounterfactualComparison{}
	if (direct.OutcomeSuccess || python.OutcomeSuccess) && !hybrid.OutcomeSuccess {
		result.OutcomeRegret = 1
	}
	if (direct.StrictPass || python.StrictPass) && !hybrid.StrictPass {
		result.StrictRegret = 1
	}
	eligible := []struct {
		name    string
		outcome RouteOutcome
	}{}
	for _, arm := range []struct {
		name    string
		outcome RouteOutcome
	}{{"direct", direct}, {"python", python}} {
		if arm.outcome.OutcomeSuccess && (!hybrid.StrictPass || arm.outcome.StrictPass) {
			eligible = append(eligible, arm)
		}
		if routeDominates(arm.outcome, hybrid) && !result.ParetoDominated {
			result.ParetoDominated = true
			result.DominatingArm = arm.name
		}
	}
	if len(eligible) > 0 {
		bestCalls := eligible[0].outcome.ProviderCalls
		bestTokens := eligible[0].outcome.TotalTokens
		for _, arm := range eligible[1:] {
			if arm.outcome.ProviderCalls < bestCalls {
				bestCalls = arm.outcome.ProviderCalls
			}
			if arm.outcome.TotalTokens < bestTokens {
				bestTokens = arm.outcome.TotalTokens
			}
		}
		if hybrid.ProviderCalls > bestCalls {
			result.ProviderCallRegret = hybrid.ProviderCalls - bestCalls
		}
		if hybrid.TotalTokens > bestTokens {
			result.TokenRegret = hybrid.TotalTokens - bestTokens
		}
	}
	return result, nil
}

func routeDominates(candidate, actual RouteOutcome) bool {
	noWorse := candidate.OutcomeSuccess && (!actual.OutcomeSuccess || candidate.OutcomeSuccess) && (!actual.StrictPass || candidate.StrictPass) &&
		candidate.ProviderCalls <= actual.ProviderCalls && candidate.TotalTokens <= actual.TotalTokens
	strictlyBetter := candidate.OutcomeSuccess != actual.OutcomeSuccess || candidate.StrictPass != actual.StrictPass ||
		candidate.ProviderCalls < actual.ProviderCalls || candidate.TotalTokens < actual.TotalTokens
	return noWorse && strictlyBetter
}

func validRouteOutcome(outcome RouteOutcome) bool {
	return outcome.TotalTokens >= 0 && (!outcome.StrictPass || outcome.OutcomeSuccess)
}

func AggregateHybridRegret(comparisons []HybridCounterfactualComparison) (HybridRegretReport, error) {
	if len(comparisons) == 0 {
		return HybridRegretReport{}, ErrCounterfactualComparison
	}
	report := HybridRegretReport{Tasks: uint32(len(comparisons))}
	for _, comparison := range comparisons {
		if comparison.TokenRegret < 0 || comparison.OutcomeRegret > 1 || comparison.StrictRegret > 1 ||
			(comparison.ParetoDominated != (comparison.DominatingArm != "")) ||
			(comparison.DominatingArm != "" && comparison.DominatingArm != "direct" && comparison.DominatingArm != "python") ||
			^uint64(0)-report.TotalProviderCallRegret < uint64(comparison.ProviderCallRegret) {
			return HybridRegretReport{}, ErrCounterfactualComparison
		}
		if comparison.OutcomeRegret != 0 {
			report.OutcomeRegretTasks++
		}
		if comparison.StrictRegret != 0 {
			report.StrictRegretTasks++
		}
		if comparison.ParetoDominated {
			report.ParetoDominatedTasks++
		}
		report.TotalProviderCallRegret += uint64(comparison.ProviderCallRegret)
		if comparison.TokenRegret > 0 && report.TotalTokenRegret > int64(^uint64(0)>>1)-comparison.TokenRegret {
			return HybridRegretReport{}, ErrCounterfactualComparison
		}
		report.TotalTokenRegret += comparison.TokenRegret
	}
	return report, nil
}
