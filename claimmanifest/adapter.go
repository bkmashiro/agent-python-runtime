package claimmanifest

import (
	"bytes"
	"encoding/json"
	"io"

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
	Status             string `json:"status"`
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
		if completedEventID != "" {
			return Manifest{}, ErrAmbiguousExecutionObservation
		}
		completedEventID = event.EventID
	}
	if completedEventID == "" {
		return Manifest{}, ErrExecutionNotObserved
	}

	manifest := Manifest{
		Version: Version, Source: "metadata-only", ExecutionRef: ref,
		PlaybackDigest: playbackDigest, CompletedEventID: completedEventID,
		Qualification: QualificationStructuralOnly,
		Claims:        metadataClaims(ref, playbackDigest, completedEventID),
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func decodeCompletedExecution(payload []byte) (completedExecution, bool) {
	fields, ok := decodeStrictObject(payload)
	if !ok {
		return completedExecution{}, false
	}
	required := map[string]struct{}{
		"invocation_id": {}, "invocation_attempt": {}, "execution_id": {}, "executed_code_sha256": {},
		"status": {}, "turn_seq": {}, "output_item_seq": {}, "segment_seq": {},
	}
	optional := map[string]struct{}{
		"result_digest": {}, "run_error": {}, "error_code": {}, "request_digest": {},
		"response_digest": {}, "capability_calls": {},
	}
	for key := range fields {
		if _, ok := required[key]; ok {
			continue
		}
		if _, ok := optional[key]; !ok {
			return completedExecution{}, false
		}
	}
	for key := range required {
		if _, ok := fields[key]; !ok {
			return completedExecution{}, false
		}
	}
	var observed completedExecution
	if !decodeField(fields, "invocation_id", &observed.InvocationID) ||
		!decodeField(fields, "invocation_attempt", &observed.InvocationAttempt) ||
		!decodeField(fields, "execution_id", &observed.ExecutionID) ||
		!decodeField(fields, "executed_code_sha256", &observed.ExecutedCodeSHA256) ||
		!decodeField(fields, "turn_seq", &observed.TurnSeq) ||
		!decodeField(fields, "output_item_seq", &observed.OutputItemSeq) ||
		!decodeField(fields, "segment_seq", &observed.SegmentSeq) ||
		!decodeField(fields, "status", &observed.Status) || observed.Status != "ok" ||
		!validCompletionMetadata(fields) {
		return completedExecution{}, false
	}
	return observed, true
}

func decodeStrictObject(payload []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, false
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, false
		}
		fields[key] = append(json.RawMessage(nil), raw...)
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if token, err = decoder.Token(); err != io.EOF || token != nil {
		return nil, false
	}
	return fields, true
}

func decodeField(fields map[string]json.RawMessage, key string, target any) bool {
	raw, ok := fields[key]
	return ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, target) == nil
}

func validCompletionMetadata(fields map[string]json.RawMessage) bool {
	if _, ok := fields["run_error"]; ok {
		var runError bool
		if !decodeField(fields, "run_error", &runError) || runError {
			return false
		}
	}
	if _, ok := fields["error_code"]; ok {
		var errorCode string
		if !decodeField(fields, "error_code", &errorCode) || errorCode != "" {
			return false
		}
	}
	for _, key := range []string{"result_digest", "request_digest", "response_digest"} {
		if _, ok := fields[key]; !ok {
			continue
		}
		var value string
		if !decodeField(fields, key, &value) || !digestPattern.MatchString(value) {
			return false
		}
	}
	if _, ok := fields["capability_calls"]; ok {
		var calls uint32
		if !decodeField(fields, "capability_calls", &calls) {
			return false
		}
	}
	return true
}

func matches(ref runtimeconfig.ExecutionRef, observed completedExecution) bool {
	return observed.InvocationID == ref.InvocationID && observed.InvocationAttempt == ref.InvocationAttempt &&
		observed.ExecutionID == ref.ExecutionID && observed.ExecutedCodeSHA256 == ref.ExecutedCodeSHA256 &&
		observed.TurnSeq == ref.TurnSeq && observed.OutputItemSeq == ref.OutputItemSeq && observed.SegmentSeq == ref.SegmentSeq
}
