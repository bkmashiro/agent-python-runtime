package agentic

import (
	"errors"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type Condition string

const (
	ConditionDirect Condition = "direct"
	ConditionPython Condition = "python"
	ConditionHybrid Condition = "hybrid"
)

var (
	ErrBudgetClosed   = errors.New("agentic trial budget is closed")
	ErrBudgetExceeded = errors.New("agentic trial budget exceeded")
	ErrUsageMissing   = errors.New("provider usage is missing or invalid")
)

type TrialLimits struct {
	MaxProviderCalls       uint32 `json:"max_provider_calls"`
	MaxToolCalls           uint32 `json:"max_tool_calls"`
	MaxPythonRuns          uint32 `json:"max_python_runs"`
	MaxInputTokens         uint64 `json:"max_input_tokens"`
	MaxOutputTokens        uint64 `json:"max_output_tokens"`
	MaxTotalTokens         uint64 `json:"max_total_tokens"`
	MaxOutputTokensPerCall uint64 `json:"max_output_tokens_per_call"`
}

func (limits TrialLimits) valid() bool {
	return limits.MaxProviderCalls > 0 && limits.MaxProviderCalls <= 64 &&
		limits.MaxToolCalls > 0 && limits.MaxToolCalls <= maxFunctionCalls &&
		limits.MaxPythonRuns <= limits.MaxToolCalls &&
		limits.MaxInputTokens > 0 && limits.MaxOutputTokens > 0 && limits.MaxTotalTokens > 0 &&
		limits.MaxOutputTokensPerCall > 0 && limits.MaxOutputTokensPerCall <= maxDirectOutputTokens
}

func (condition Condition) valid() bool {
	return condition == ConditionDirect || condition == ConditionPython || condition == ConditionHybrid
}

type ExchangeEvidence struct {
	StatusCode     int            `json:"status_code"`
	RequestDigest  string         `json:"request_digest"`
	ResponseDigest string         `json:"response_digest"`
	Usage          provider.Usage `json:"usage"`
}
