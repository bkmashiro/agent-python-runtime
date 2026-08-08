package claimmanifest

import (
	"encoding/json"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

type completedExecution struct {
	InvocationID       string `json:"invocation_id"`
	InvocationAttempt  uint32 `json:"invocation_attempt"`
	ExecutionID        string `json:"execution_id"`
	ExecutedCodeSHA256 string `json:"executed_code_sha256"`
	TurnSeq            uint32 `json:"turn_seq"`
	OutputItemSeq      uint32 `json:"output_item_seq"`
	SegmentSeq         uint32 `json:"segment_seq"`
}

// FromMetadataPlayback adapts an integrity-checked metadata trace and a
// Host-authored ExecutionRef. Metadata deliberately cannot qualify R1 or R2.
func FromMetadataPlayback(ref runtimeconfig.ExecutionRef, playback agenttrace.Playback) (Manifest, error) {
	if ref.Validate() != nil || playback.AgentRunID != ref.AgentRunID {
		return Manifest{}, ErrExecutionNotObserved
	}
	playbackDigest, err := playback.IntegrityDigest()
	if err != nil {
		return Manifest{}, err
	}
	completedEventID := ""
	for _, event := range playback.Events {
		if event.EventType != agenttrace.EventRuntimeCompleted || event.AgentRunID != ref.AgentRunID {
			continue
		}
		observed, ok := decodeCompletedExecution(event.Payload)
		if !ok || !matches(ref, observed) {
			continue
		}
		completedEventID = event.EventID
		break
	}
	if completedEventID == "" {
		return Manifest{}, ErrExecutionNotObserved
	}

	artifact := statusClaim(ClaimArtifact, ref.ExecutionID, StatusVerified)
	artifact.Evidence = []Evidence{{Kind: EvidenceExecutedCodeDigest, Ref: ref.ExecutedCodeSHA256}}
	base := statusClaim(ClaimBase, ref.ExecutionID, StatusInsufficient)
	authority := statusClaim(ClaimAuthority, ref.ExecutionID, StatusInsufficient)
	execution := statusClaim(ClaimExecution, ref.ExecutionID, StatusVerified, ClaimArtifact, ClaimBase, ClaimAuthority)
	execution.Evidence = []Evidence{{Kind: EvidenceTraceIntegrity, Ref: playbackDigest + "#" + completedEventID}}
	effect := statusClaim(ClaimEffect, ref.ExecutionID, StatusInsufficient, ClaimAuthority, ClaimExecution)
	outcome := statusClaim(ClaimOutcome, ref.ExecutionID, StatusInsufficient, ClaimExecution, ClaimEffect)

	manifest := Manifest{
		Version: Version, Source: "metadata-only", ExecutionRef: ref, PlaybackDigest: playbackDigest,
		Qualification: QualificationStructuralOnly,
		Claims:        []Claim{artifact, base, authority, execution, effect, outcome},
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func decodeCompletedExecution(payload []byte) (completedExecution, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return completedExecution{}, false
	}
	required := map[string]struct{}{
		"invocation_id": {}, "invocation_attempt": {}, "execution_id": {}, "executed_code_sha256": {},
		"status": {}, "turn_seq": {}, "output_item_seq": {}, "segment_seq": {},
	}
	for key := range fields {
		if _, ok := required[key]; ok || key == "result_digest" {
			continue
		}
		return completedExecution{}, false
	}
	for key := range required {
		if _, ok := fields[key]; !ok {
			return completedExecution{}, false
		}
	}
	var observed completedExecution
	if json.Unmarshal(payload, &observed) != nil {
		return completedExecution{}, false
	}
	return observed, true
}

func matches(ref runtimeconfig.ExecutionRef, observed completedExecution) bool {
	return observed.InvocationID == ref.InvocationID && observed.InvocationAttempt == ref.InvocationAttempt &&
		observed.ExecutionID == ref.ExecutionID && observed.ExecutedCodeSHA256 == ref.ExecutedCodeSHA256 &&
		observed.TurnSeq == ref.TurnSeq && observed.OutputItemSeq == ref.OutputItemSeq && observed.SegmentSeq == ref.SegmentSeq
}
