package hermesbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

const OperationObserve = "observe"

const maxObservePayloadBytes = 8 << 10

type ObserveRequest struct {
	Version          string               `json:"version"`
	Operation        string               `json:"operation"`
	RequestID        string               `json:"request_id"`
	AgentRunID       string               `json:"agent_run_id"`
	EventType        agenttrace.EventType `json:"event_type"`
	ParentEventID    string               `json:"parent_event_id,omitempty"`
	Payload          json.RawMessage      `json:"payload"`
	StateFingerprint string               `json:"state_fingerprint,omitempty"`
}

type ObserveResponse struct {
	Version   string       `json:"version"`
	RequestID string       `json:"request_id"`
	Status    string       `json:"status"`
	EventID   string       `json:"event_id,omitempty"`
	Sequence  uint64       `json:"sequence,omitempty"`
	Error     *BridgeError `json:"error,omitempty"`
}

func DecodeObserveRequest(payload []byte) (ObserveRequest, error) {
	if len(payload) == 0 || len(payload) > MaxFrameBytes || rejectDuplicateJSON(payload) != nil {
		return ObserveRequest{}, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request ObserveRequest
	if err := decoder.Decode(&request); err != nil {
		return ObserveRequest{}, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ObserveRequest{}, ErrInvalidRequest
	}
	if err := request.Validate(); err != nil {
		return ObserveRequest{}, err
	}
	return request, nil
}

func (request ObserveRequest) Validate() error {
	if request.Version != ProtocolVersion || request.Operation != OperationObserve ||
		!boundedIdentifier(request.RequestID, 128) || !boundedIdentifier(request.AgentRunID, 128) ||
		(request.ParentEventID != "" && !boundedIdentifier(request.ParentEventID, 160)) ||
		len(request.Payload) == 0 || len(request.Payload) > maxObservePayloadBytes ||
		!json.Valid(request.Payload) || rejectDuplicateJSON(request.Payload) != nil ||
		(request.StateFingerprint != "" && !validDigest(request.StateFingerprint)) {
		return ErrInvalidRequest
	}
	if request.StateFingerprint != "" && request.EventType != agenttrace.EventFinalStateObserved && request.EventType != agenttrace.EventRunCompleted {
		return ErrInvalidRequest
	}
	metadata, err := decodeMetadata(request.Payload)
	if err != nil || validateObserveMetadata(request.EventType, metadata) != nil {
		return ErrInvalidRequest
	}
	return nil
}

func decodeOperation(payload []byte) (string, error) {
	if len(payload) == 0 || len(payload) > MaxFrameBytes || rejectDuplicateJSON(payload) != nil {
		return "", ErrInvalidRequest
	}
	var envelope struct {
		Operation string `json:"operation"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return "", ErrInvalidRequest
	}
	return envelope.Operation, nil
}

func decodeMetadata(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var metadata map[string]any
	if err := decoder.Decode(&metadata); err != nil || metadata == nil {
		return nil, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidRequest
	}
	return metadata, nil
}

var observeKeys = map[agenttrace.EventType]map[string]string{
	agenttrace.EventRunStarted: {
		"source": "text", "platform": "text", "profile": "text",
	},
	agenttrace.EventRunCompleted: {
		"status": "identifier",
	},
	agenttrace.EventLLMResponseReceived: {
		"turn_id_digest": "digest", "api_request_id_digest": "digest", "api_call_count": "number",
		"duration_ms": "number", "finish_reason": "text", "model": "text", "provider": "text",
		"input_tokens": "number", "output_tokens": "number", "cached_tokens": "number", "total_tokens": "number",
		"assistant_content_chars": "number", "assistant_content_digest": "digest", "tool_call_count": "number",
		"tool_names": "text_list",
	},
	agenttrace.EventRoutingDecided: {
		"route": "route", "direct_tool_count": "number", "runtime_tool_count": "number",
	},
	agenttrace.EventDirectToolCompleted: {
		"tool_name": "text", "status": "identifier", "duration_ms": "number", "error_type": "text",
		"args_digest": "digest", "args_bytes": "number", "result_digest": "digest", "result_bytes": "number",
	},
	agenttrace.EventFinalStateObserved: {
		"status": "identifier", "content_digest": "digest", "content_chars": "number",
	},
}

var observeRequired = map[agenttrace.EventType][]string{
	agenttrace.EventRunStarted:          {"source", "profile"},
	agenttrace.EventRunCompleted:        {"status"},
	agenttrace.EventLLMResponseReceived: {"turn_id_digest", "api_request_id_digest", "api_call_count", "finish_reason", "assistant_content_chars", "assistant_content_digest", "tool_call_count"},
	agenttrace.EventRoutingDecided:      {"route", "direct_tool_count", "runtime_tool_count"},
	agenttrace.EventDirectToolCompleted: {"tool_name", "status", "args_digest", "args_bytes", "result_digest", "result_bytes"},
	agenttrace.EventFinalStateObserved:  {"status", "content_digest", "content_chars"},
}

func validateObserveMetadata(eventType agenttrace.EventType, metadata map[string]any) error {
	keys, ok := observeKeys[eventType]
	if !ok || len(metadata) > 24 {
		return ErrInvalidRequest
	}
	for _, required := range observeRequired[eventType] {
		if _, ok := metadata[required]; !ok {
			return ErrInvalidRequest
		}
	}
	for key, value := range metadata {
		kind, ok := keys[key]
		if !ok || validateMetadataValue(kind, value) != nil {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validateMetadataValue(kind string, value any) error {
	switch kind {
	case "digest":
		text, ok := value.(string)
		if !ok || !validDigest(text) {
			return ErrInvalidRequest
		}
	case "identifier":
		text, ok := value.(string)
		if !ok || !boundedIdentifier(text, 64) {
			return ErrInvalidRequest
		}
	case "text":
		text, ok := value.(string)
		if !ok || !boundedMetadataText(text, 128) {
			return ErrInvalidRequest
		}
	case "route":
		text, ok := value.(string)
		if !ok || (text != "direct" && text != "runtime" && text != "hybrid" && text != "none") {
			return ErrInvalidRequest
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return ErrInvalidRequest
		}
		integer, err := number.Int64()
		if err != nil || integer < 0 || integer > math.MaxInt32 {
			return ErrInvalidRequest
		}
	case "text_list":
		values, ok := value.([]any)
		if !ok || len(values) > 32 {
			return ErrInvalidRequest
		}
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !boundedMetadataText(text, 64) {
				return ErrInvalidRequest
			}
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func boundedMetadataText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) &&
		!strings.ContainsFunc(value, func(r rune) bool { return r < ' ' || r == 0x7f })
}
