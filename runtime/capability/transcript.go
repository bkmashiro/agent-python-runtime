package capability

import (
	"context"
	"encoding/json"
	"sort"
)

// TransportEvidence is bounded, non-secret attribution metadata returned with
// one capturable Host call. It is not Agent-authored and never contains an
// endpoint or headers.
type TransportEvidence struct {
	Kind       string `json:"kind"`
	Status     uint16 `json:"status"`
	MediaType  string `json:"media_type"`
	BodyBytes  uint32 `json:"body_bytes"`
	BodySHA256 string `json:"body_sha256"`
}

// EvidenceHandler returns evidence in the same call frame as its result so
// concurrent calls cannot race through shared "last result" state.
type EvidenceHandler interface {
	Handler
	CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, TransportEvidence, error)
}

type TranscriptEntry struct {
	OperationIndex  uint32            `json:"operation_index"`
	Capability      string            `json:"capability"`
	Arguments       json.RawMessage   `json:"arguments"`
	ArgumentsSHA256 string            `json:"arguments_sha256"`
	Result          json.RawMessage   `json:"result"`
	ResultSHA256    string            `json:"result_sha256"`
	Evidence        TransportEvidence `json:"transport_evidence"`
}

func validTransportEvidence(evidence TransportEvidence) bool {
	return evidence.Kind == "http" && evidence.Status >= 100 && evidence.Status <= 599 &&
		len(evidence.MediaType) > 0 && len(evidence.MediaType) <= 128 && evidence.BodyBytes <= maxCallBytes &&
		validSHA256Identity(evidence.BodySHA256)
}

func cloneTranscript(entries []TranscriptEntry) []TranscriptEntry {
	cloned := make([]TranscriptEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Arguments = append(json.RawMessage(nil), entry.Arguments...)
		cloned[index].Result = append(json.RawMessage(nil), entry.Result...)
	}
	sort.Slice(cloned, func(left, right int) bool { return cloned[left].OperationIndex < cloned[right].OperationIndex })
	return cloned
}
