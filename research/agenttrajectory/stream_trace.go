package agenttrajectory

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const (
	RawCandidateStreamSchemaVersion     = "pysolate.private-observed-candidate-stream.v1"
	ObservedStreamEvidenceSchemaVersion = "pysolate.observed-stream-opportunity.v1"
)

var observedTravelCallRE = regexp.MustCompile(`travel\.(weather|rail|attractions)\s*\(`)

type StreamChunk struct {
	ElapsedNS       uint64 `json:"elapsed_ns"`
	Content         string `json:"content,omitempty"`
	Reasoning       string `json:"reasoning,omitempty"`
	FinishReason    string `json:"finish_reason,omitempty"`
	ProviderPayload []byte `json:"provider_payload,omitempty"`
}

type ObservedNetworkTrace struct {
	DNSStartNS          uint64 `json:"dns_start_ns,omitempty"`
	DNSDoneNS           uint64 `json:"dns_done_ns,omitempty"`
	ConnectStartNS      uint64 `json:"connect_start_ns,omitempty"`
	ConnectDoneNS       uint64 `json:"connect_done_ns,omitempty"`
	TLSStartNS          uint64 `json:"tls_start_ns,omitempty"`
	TLSDoneNS           uint64 `json:"tls_done_ns,omitempty"`
	GotConnNS           uint64 `json:"got_conn_ns,omitempty"`
	WroteRequestNS      uint64 `json:"wrote_request_ns,omitempty"`
	FirstResponseByteNS uint64 `json:"first_response_byte_ns,omitempty"`
	ConnectionReused    bool   `json:"connection_reused"`
}

type RawCandidateStream struct {
	SchemaVersion    string               `json:"schema_version"`
	CandidateID      string               `json:"candidate_id"`
	Model            string               `json:"model"`
	ResponseID       string               `json:"response_id"`
	StartedAt        string               `json:"started_at"`
	HeadersElapsedNS uint64               `json:"headers_elapsed_ns"`
	DoneElapsedNS    uint64               `json:"done_elapsed_ns"`
	Chunks           []StreamChunk        `json:"chunks"`
	Content          string               `json:"content"`
	Usage            TokenUsage           `json:"usage"`
	RawRequest       json.RawMessage      `json:"raw_request"`
	Network          ObservedNetworkTrace `json:"network"`
}

type ObservedStatement struct {
	Index    int    `json:"index"`
	Source   string `json:"source"`
	ClosedNS uint64 `json:"closed_ns"`
}

type ObservedToolCall struct {
	Capability string `json:"capability"`
	Statement  int    `json:"statement"`
	EligibleNS uint64 `json:"eligible_ns"`
	LatencyNS  uint64 `json:"latency_ns"`
	ReadyNS    uint64 `json:"ready_ns"`
}

type ObservedStreamEvidence struct {
	SchemaVersion            string               `json:"schema_version"`
	CandidateID              string               `json:"candidate_id"`
	Model                    string               `json:"model"`
	TraceSHA256              string               `json:"trace_sha256"`
	HeadersElapsedNS         uint64               `json:"headers_elapsed_ns"`
	Network                  ObservedNetworkTrace `json:"network"`
	FirstSSEEventNS          uint64               `json:"first_sse_event_ns"`
	FirstReasoningNS         uint64               `json:"first_reasoning_ns"`
	FirstContentNS           uint64               `json:"first_content_ns"`
	SourceCompleteNS         uint64               `json:"source_complete_ns"`
	StreamCompleteNS         uint64               `json:"stream_complete_ns"`
	EligibleWindowNS         uint64               `json:"eligible_window_ns"`
	Statements               []ObservedStatement  `json:"statements"`
	ToolCalls                []ObservedToolCall   `json:"tool_calls"`
	NativeSequentialReadyNS  uint64               `json:"native_sequential_ready_ns"`
	NativeParallelReadyNS    uint64               `json:"native_parallel_ready_ns"`
	PrefixPreDispatchReadyNS uint64               `json:"prefix_pre_dispatch_ready_ns"`
	SavingsVsSequentialNS    uint64               `json:"savings_vs_sequential_ns"`
	SavingsVsParallelNS      uint64               `json:"savings_vs_parallel_ns"`
}

func ProjectObservedCandidateStream(raw RawCandidateStream, latencies map[string]time.Duration) (ObservedStreamEvidence, error) {
	if raw.SchemaVersion != RawCandidateStreamSchemaVersion || !validCandidateID(raw.CandidateID) || raw.Model == "" || raw.ResponseID == "" || len(raw.Chunks) == 0 || strings.TrimSpace(raw.Content) == "" {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	candidate, err := normalizeCandidateResponse(raw.Content)
	if err != nil || candidate.CandidateID != raw.CandidateID {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	encodedSource, err := json.Marshal(candidate.PythonSource)
	if err != nil {
		return ObservedStreamEvidence{}, err
	}
	sourceOffset := bytes.Index([]byte(raw.Content), encodedSource)
	if sourceOffset < 0 {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	lines := strings.Split(candidate.PythonSource, "\n")
	if len(lines) < 4 {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	statements := make([]ObservedStatement, 0, len(lines))
	calls := make([]ObservedToolCall, 0, 3)
	for index := range lines {
		prefix := strings.Join(lines[:index+1], "\n")
		encodedPrefix, marshalErr := json.Marshal(prefix)
		if marshalErr != nil || len(encodedPrefix) < 2 {
			return ObservedStreamEvidence{}, ErrInvalidContract
		}
		contentEnd := sourceOffset + len(encodedPrefix) - 1
		closedNS, ok := contentArrival(raw.Chunks, contentEnd)
		if !ok {
			return ObservedStreamEvidence{}, ErrInvalidContract
		}
		statements = append(statements, ObservedStatement{Index: index + 1, Source: lines[index], ClosedNS: closedNS})
		match := observedTravelCallRE.FindStringSubmatch(lines[index])
		if len(match) == 2 {
			latency, exists := latencies[match[1]]
			if !exists || latency <= 0 {
				return ObservedStreamEvidence{}, ErrInvalidContract
			}
			calls = append(calls, ObservedToolCall{Capability: match[1], Statement: index + 1, EligibleNS: closedNS, LatencyNS: uint64(latency), ReadyNS: closedNS + uint64(latency)})
		}
	}
	if len(calls) != 3 {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	sourceComplete := statements[len(statements)-1].ClosedNS
	streamComplete := raw.DoneElapsedNS
	if streamComplete == 0 {
		streamComplete = raw.Chunks[len(raw.Chunks)-1].ElapsedNS
	}
	if streamComplete < sourceComplete {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	var firstEvent, firstReasoning, firstContent, sumLatency, maxLatency, predispatchReady uint64
	for _, chunk := range raw.Chunks {
		if firstEvent == 0 {
			firstEvent = chunk.ElapsedNS
		}
		if firstReasoning == 0 && chunk.Reasoning != "" {
			firstReasoning = chunk.ElapsedNS
		}
		if firstContent == 0 && chunk.Content != "" {
			firstContent = chunk.ElapsedNS
		}
	}
	if firstContent == 0 {
		return ObservedStreamEvidence{}, ErrInvalidContract
	}
	for _, call := range calls {
		sumLatency += call.LatencyNS
		if call.LatencyNS > maxLatency {
			maxLatency = call.LatencyNS
		}
		if call.ReadyNS > predispatchReady {
			predispatchReady = call.ReadyNS
		}
	}
	if predispatchReady < sourceComplete {
		predispatchReady = sourceComplete
	}
	firstEligible := calls[0].EligibleNS
	for _, call := range calls[1:] {
		if call.EligibleNS < firstEligible {
			firstEligible = call.EligibleNS
		}
	}
	sequentialReady := sourceComplete + sumLatency
	parallelReady := sourceComplete + maxLatency
	encodedRaw, err := json.Marshal(raw)
	if err != nil {
		return ObservedStreamEvidence{}, err
	}
	return ObservedStreamEvidence{
		SchemaVersion: ObservedStreamEvidenceSchemaVersion, CandidateID: raw.CandidateID, Model: raw.Model,
		TraceSHA256: digestBytes(encodedRaw), HeadersElapsedNS: raw.HeadersElapsedNS, Network: raw.Network,
		FirstSSEEventNS: firstEvent, FirstReasoningNS: firstReasoning, FirstContentNS: firstContent,
		SourceCompleteNS: sourceComplete, StreamCompleteNS: streamComplete, EligibleWindowNS: sourceComplete - firstEligible,
		Statements: statements, ToolCalls: calls, NativeSequentialReadyNS: sequentialReady, NativeParallelReadyNS: parallelReady,
		PrefixPreDispatchReadyNS: predispatchReady, SavingsVsSequentialNS: sequentialReady - predispatchReady,
		SavingsVsParallelNS: parallelReady - predispatchReady,
	}, nil
}

func contentArrival(chunks []StreamChunk, contentEnd int) (uint64, bool) {
	if contentEnd <= 0 {
		return 0, false
	}
	seen := 0
	var previous uint64
	for _, chunk := range chunks {
		if chunk.ElapsedNS < previous {
			return 0, false
		}
		previous = chunk.ElapsedNS
		seen += len(chunk.Content)
		if seen >= contentEnd {
			return chunk.ElapsedNS, true
		}
	}
	return 0, false
}
