package agentic

import (
	"encoding/json"
)

type StatefulScore struct {
	Passed              bool   `json:"passed"`
	TracePassed         bool   `json:"trace_passed"`
	FinalStatePassed    bool   `json:"final_state_passed"`
	ExpectedCalls       int    `json:"expected_calls"`
	ActualCalls         int    `json:"actual_calls"`
	ExpectedStateDigest string `json:"expected_state_digest"`
	ActualStateDigest   string `json:"actual_state_digest"`
}

func ScoreStateful(task Task, actualTurns [][]StatefulCall, actualFS *GorillaFileSystem) (StatefulScore, error) {
	if task.Track != "stateful_local_tools" || actualFS == nil {
		return StatefulScore{}, ErrFileSystem
	}
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" {
		return StatefulScore{}, ErrDataset
	}
	expectedFS, err := NewGorillaFileSystem(task.Environment.InitialState)
	if err != nil {
		return StatefulScore{}, err
	}
	for _, turn := range oracle.Turns {
		for _, call := range turn {
			output, callErr := expectedFS.Call(call.Name, call.Arguments)
			if callErr != nil || outputContainsError(output) {
				return StatefulScore{}, ErrDataset
			}
		}
	}
	score := StatefulScore{
		TracePassed:         equivalentStatefulTrace(oracle.Turns, actualTurns),
		ExpectedCalls:       countStatefulCalls(oracle.Turns),
		ActualCalls:         countStatefulCalls(actualTurns),
		ExpectedStateDigest: expectedFS.Digest(),
		ActualStateDigest:   actualFS.Digest(),
	}
	score.FinalStatePassed = score.ExpectedStateDigest == score.ActualStateDigest
	score.Passed = score.TracePassed && score.FinalStatePassed
	return score, nil
}

func equivalentStatefulTrace(expected, actual [][]StatefulCall) bool {
	if len(expected) != len(actual) {
		return false
	}
	for turnIndex := range expected {
		if len(expected[turnIndex]) != len(actual[turnIndex]) {
			return false
		}
		for callIndex := range expected[turnIndex] {
			left, right := expected[turnIndex][callIndex], actual[turnIndex][callIndex]
			if left.Name != right.Name {
				return false
			}
			var leftArguments, rightArguments map[string]any
			if decodeUseNumber(left.Arguments, &leftArguments) != nil || decodeUseNumber(right.Arguments, &rightArguments) != nil ||
				!equivalentBFCL(leftArguments, rightArguments) {
				return false
			}
		}
	}
	return true
}

func countStatefulCalls(turns [][]StatefulCall) int {
	total := 0
	for _, turn := range turns {
		total += len(turn)
	}
	return total
}

func outputContainsError(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	_, exists := value["error"]
	return exists
}
