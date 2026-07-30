package runtime

import (
	"errors"
	"regexp"
)

var (
	ErrInvalidInvocationRef = errors.New("invalid Host invocation reference")
	sha256DigestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// InvocationRef is optional Host-owned correlation metadata for one physical
// Runtime attempt. It is not part of RunRequest and carries no authority.
type InvocationRef struct {
	AgentRunID        string `json:"agent_run_id"`
	TurnSeq           uint32 `json:"turn_seq"`
	OutputItemSeq     uint32 `json:"output_item_seq"`
	SegmentSeq        uint32 `json:"segment_seq"`
	InvocationID      string `json:"invocation_id"`
	InvocationAttempt uint32 `json:"invocation_attempt"`
	ExecutionID       string `json:"execution_id"`
}

// ExecutionRef is authored by the Host after decoding the exact RunRequest.
type ExecutionRef struct {
	InvocationRef
	ExecutedCodeSHA256 string `json:"executed_code_sha256"`
}

func (ref InvocationRef) Validate() error {
	if !boundedIdentifier(ref.AgentRunID, 128) || !boundedIdentifier(ref.InvocationID, 128) ||
		!boundedIdentifier(ref.ExecutionID, 128) || ref.InvocationAttempt == 0 {
		return ErrInvalidInvocationRef
	}
	return nil
}

func (ref ExecutionRef) Validate() error {
	if ref.InvocationRef.Validate() != nil || !sha256DigestPattern.MatchString(ref.ExecutedCodeSHA256) {
		return ErrInvalidInvocationRef
	}
	return nil
}

func boundedIdentifier(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}
