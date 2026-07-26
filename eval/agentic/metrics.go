package agentic

import "errors"

var ErrTrialMetrics = errors.New("invalid agentic trial metrics")

type TrialMetrics struct {
	OutcomeSuccess    bool  `json:"outcome_success"`
	StrictPass        bool  `json:"strict_pass"`
	TraceExact        *bool `json:"trace_exact,omitempty"`
	FinalStateCorrect *bool `json:"final_state_correct,omitempty"`
	ExpectedCalls     *int  `json:"expected_calls,omitempty"`
	ActualCalls       int   `json:"actual_calls"`
	ExtraCalls        int   `json:"extra_calls"`
}

func DeriveTrialMetrics(result TrialResult) (TrialMetrics, error) {
	stateful := result.StatefulScore != nil
	stateless := result.StatelessScore != nil
	if stateful == stateless || result.ToolCalls < 0 {
		return TrialMetrics{}, ErrTrialMetrics
	}
	metrics := TrialMetrics{ActualCalls: result.ToolCalls}
	if stateless {
		metrics.OutcomeSuccess = result.ErrorCode == "" && result.StatelessScore.Passed
		metrics.StrictPass = metrics.OutcomeSuccess
		return metrics, nil
	}
	traceExact := result.StatefulScore.TracePassed
	finalStateCorrect := result.StatefulScore.FinalStatePassed
	expectedCalls := result.StatefulScore.ExpectedCalls
	metrics.TraceExact = &traceExact
	metrics.FinalStateCorrect = &finalStateCorrect
	metrics.ExpectedCalls = &expectedCalls
	if result.ToolCalls > expectedCalls {
		metrics.ExtraCalls = result.ToolCalls - expectedCalls
	}
	metrics.OutcomeSuccess = result.ErrorCode == "" && finalStateCorrect
	metrics.StrictPass = result.ErrorCode == "" && result.StatefulScore.Passed
	return metrics, nil
}

func trialMetricsEqual(left, right TrialMetrics) bool {
	return left.OutcomeSuccess == right.OutcomeSuccess && left.StrictPass == right.StrictPass &&
		optionalBoolEqual(left.TraceExact, right.TraceExact) && optionalBoolEqual(left.FinalStateCorrect, right.FinalStateCorrect) &&
		optionalIntEqual(left.ExpectedCalls, right.ExpectedCalls) && left.ActualCalls == right.ActualCalls && left.ExtraCalls == right.ExtraCalls
}

func optionalBoolEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func optionalIntEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
