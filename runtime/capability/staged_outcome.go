package capability

import (
	"encoding/json"
	"errors"
)

var (
	ErrPreDispatchUnavailable    = errors.New("pre-dispatch capability is unavailable")
	ErrPreDispatchAlreadyStarted = errors.New("pre-dispatch physical call already started")
	ErrPreDispatchInvalidResult  = errors.New("pre-dispatch result is outside the capability contract")
)

type StagedCapabilityOutcome struct {
	Result              json.RawMessage `json:"result,omitempty"`
	ErrorCode           string          `json:"error_code,omitempty"`
	PhysicalResultBytes uint64          `json:"-"`
}

func (outcome StagedCapabilityOutcome) Validate() error {
	hasResult, hasError := len(outcome.Result) != 0, outcome.ErrorCode != ""
	if hasResult == hasError || (hasError && outcome.ErrorCode != "handler_error" && outcome.ErrorCode != "invalid_result" && outcome.ErrorCode != PLMProviderOutcomeUncertainCode) {
		return ErrPreDispatchUnavailable
	}
	return nil
}
