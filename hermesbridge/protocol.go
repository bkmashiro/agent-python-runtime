package hermesbridge

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const (
	ProtocolVersion  = "hermes-python-runtime-bridge/v1"
	OperationExecute = "execute"
	MaxFrameBytes    = 1 << 20
	MaxCodeBytes     = 64 << 10
)

var (
	ErrInvalidRequest = errors.New("invalid Hermes bridge request")
	ErrFrameTooLarge  = errors.New("Hermes bridge frame exceeds configured bound")
)

type InvocationCoordinates struct {
	AgentRunID        string `json:"agent_run_id"`
	TurnSeq           uint32 `json:"turn_seq"`
	OutputItemSeq     uint32 `json:"output_item_seq"`
	SegmentSeq        uint32 `json:"segment_seq"`
	InvocationID      string `json:"invocation_id"`
	InvocationAttempt uint32 `json:"invocation_attempt"`
}

type ExecuteRequest struct {
	Version      string                          `json:"version"`
	Operation    string                          `json:"operation"`
	RequestID    string                          `json:"request_id"`
	Invocation   InvocationCoordinates           `json:"invocation"`
	Code         string                          `json:"code"`
	Inputs       json.RawMessage                 `json:"inputs"`
	OutputSchema json.RawMessage                 `json:"output_schema,omitempty"`
	Requirements []runtimeconfig.RequiredFeature `json:"requirements,omitempty"`
}

func DecodeExecuteRequest(payload []byte) (ExecuteRequest, error) {
	if len(payload) == 0 || len(payload) > MaxFrameBytes || rejectDuplicateJSON(payload) != nil {
		return ExecuteRequest{}, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request ExecuteRequest
	if err := decoder.Decode(&request); err != nil {
		return ExecuteRequest{}, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ExecuteRequest{}, ErrInvalidRequest
	}
	if err := request.Validate(); err != nil {
		return ExecuteRequest{}, err
	}
	return request, nil
}

func (request ExecuteRequest) Validate() error {
	if request.Version != ProtocolVersion || request.Operation != OperationExecute ||
		!boundedIdentifier(request.RequestID, 128) || !utf8.ValidString(request.Code) ||
		strings.TrimSpace(request.Code) == "" || strings.ContainsRune(request.Code, 0) || len([]byte(request.Code)) > MaxCodeBytes ||
		len(request.Inputs) == 0 || !json.Valid(request.Inputs) {
		return ErrInvalidRequest
	}
	if len(request.OutputSchema) != 0 {
		if !json.Valid(request.OutputSchema) {
			return ErrInvalidRequest
		}
		var schema any
		if json.Unmarshal(request.OutputSchema, &schema) != nil {
			return ErrInvalidRequest
		}
		if _, ok := schema.(map[string]any); !ok {
			return ErrInvalidRequest
		}
	}
	if runtimeconfig.ValidateRunRequirements(request.Requirements) != nil {
		return ErrInvalidRequest
	}
	ref := runtimeconfig.InvocationRef{
		AgentRunID: request.Invocation.AgentRunID, TurnSeq: request.Invocation.TurnSeq,
		OutputItemSeq: request.Invocation.OutputItemSeq, SegmentSeq: request.Invocation.SegmentSeq,
		InvocationID: request.Invocation.InvocationID, InvocationAttempt: request.Invocation.InvocationAttempt,
		ExecutionID: "validation-placeholder",
	}
	if ref.Validate() != nil {
		return ErrInvalidRequest
	}
	return nil
}

func ReadFrame(reader io.Reader, maximum uint32) ([]byte, error) {
	if maximum == 0 {
		return nil, ErrFrameTooLarge
	}
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return nil, fmt.Errorf("read Hermes bridge frame header: %w", err)
	}
	if size == 0 || size > maximum {
		return nil, ErrFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read Hermes bridge frame: %w", err)
	}
	return payload, nil
}

func WriteFrame(writer io.Writer, payload []byte, maximum uint32) error {
	if len(payload) == 0 || maximum == 0 || uint64(len(payload)) > uint64(maximum) {
		return ErrFrameTooLarge
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(payload))); err != nil {
		return fmt.Errorf("write Hermes bridge frame header: %w", err)
	}
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("write Hermes bridge frame: %w", err)
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

func rejectDuplicateJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 4096 {
		return ErrInvalidRequest
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return ErrInvalidRequest
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidRequest
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
