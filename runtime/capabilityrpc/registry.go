package capabilityrpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const (
	SchemaVersion = "pysolate.capability-rpc.v1"
	maxFrameBytes = 1 << 20
)

var (
	ErrInvalidChannel       = errors.New("invalid capability RPC channel")
	ErrChannelExists        = errors.New("capability RPC channel already exists")
	ErrChannelDenied        = errors.New("capability RPC channel denied")
	ErrInvalidRequest       = errors.New("invalid capability RPC request")
	ErrCallIdentityMismatch = errors.New("capability RPC call identity mismatch")
	identityPattern         = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	digestPattern           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Transport string

const TransportUnixHTTP Transport = "unix_http"

func (transport Transport) valid() bool { return transport == TransportUnixHTTP }

type Request struct {
	SchemaVersion string          `json:"schema_version"`
	ChannelID     string          `json:"channel_id"`
	InvocationID  string          `json:"invocation_id"`
	ExecutionID   string          `json:"execution_id"`
	PlanSHA256    string          `json:"plan_sha256"`
	Call          json.RawMessage `json:"call"`
}

type Status string

const (
	StatusCompleted Status = "completed"
	StatusAmbiguous Status = "ambiguous"
)

type Response struct {
	SchemaVersion  string          `json:"schema_version"`
	Status         Status          `json:"status"`
	CallID         string          `json:"call_id"`
	Replayed       bool            `json:"replayed"`
	BrokerResponse json.RawMessage `json:"broker_response,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
}

type ChannelConfig struct {
	ID              string
	Credential      string
	InvocationID    string
	ExecutionID     string
	Transport       Transport
	ExpiresAt       time.Time
	MaxRequestBytes int
	Broker          *capability.Broker
}

type callRecord struct {
	digest   string
	inFlight bool
	response json.RawMessage
}

type channel struct {
	config  ChannelConfig
	revoked bool
	calls   map[string]*callRecord
}

type Registry struct {
	mu       sync.Mutex
	channels map[string]*channel
	now      func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{channels: make(map[string]*channel), now: time.Now}
}

func (registry *Registry) Open(config ChannelConfig) error {
	if registry == nil || !identityPattern.MatchString(config.ID) || len(config.Credential) < 16 || len(config.Credential) > 256 ||
		!identityPattern.MatchString(config.InvocationID) || !identityPattern.MatchString(config.ExecutionID) || !config.Transport.valid() ||
		config.ExpiresAt.IsZero() || !config.ExpiresAt.After(time.Now()) || config.MaxRequestBytes <= 0 || config.MaxRequestBytes > maxFrameBytes ||
		config.Broker == nil || config.Broker.RunIdentity() != config.ExecutionID || !digestPattern.MatchString(config.Broker.CapabilityPlanSHA256()) {
		return ErrInvalidChannel
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.channels == nil {
		registry.channels = make(map[string]*channel)
	}
	if _, exists := registry.channels[config.ID]; exists {
		return ErrChannelExists
	}
	registry.channels[config.ID] = &channel{config: config, calls: make(map[string]*callRecord)}
	return nil
}

func (registry *Registry) Revoke(channelID string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if opened := registry.channels[channelID]; opened != nil {
		opened.revoked = true
	}
}

func (registry *Registry) Dispatch(ctx context.Context, credential string, raw []byte) (Response, error) {
	request, err := DecodeRequest(raw, maxFrameBytes)
	if err != nil || registry == nil || ctx == nil {
		return Response{}, ErrInvalidRequest
	}

	registry.mu.Lock()
	opened := registry.channels[request.ChannelID]
	if opened == nil || opened.revoked || !opened.config.ExpiresAt.After(registry.now()) || len(raw) > opened.config.MaxRequestBytes ||
		subtle.ConstantTimeCompare([]byte(credential), []byte(opened.config.Credential)) != 1 ||
		request.InvocationID != opened.config.InvocationID || request.ExecutionID != opened.config.ExecutionID ||
		request.PlanSHA256 != opened.config.Broker.CapabilityPlanSHA256() {
		registry.mu.Unlock()
		return Response{}, ErrChannelDenied
	}
	callID, callDigest, err := callIdentity(request.Call)
	if err != nil {
		registry.mu.Unlock()
		return Response{}, ErrInvalidRequest
	}
	if previous := opened.calls[callID]; previous != nil {
		if previous.digest != callDigest {
			registry.mu.Unlock()
			return Response{}, ErrCallIdentityMismatch
		}
		if previous.inFlight {
			registry.mu.Unlock()
			return Response{SchemaVersion: SchemaVersion, Status: StatusAmbiguous, CallID: callID, ErrorCode: "call_in_flight"}, nil
		}
		response := append(json.RawMessage(nil), previous.response...)
		registry.mu.Unlock()
		return Response{SchemaVersion: SchemaVersion, Status: StatusCompleted, CallID: callID, Replayed: true, BrokerResponse: response}, nil
	}
	record := &callRecord{digest: callDigest, inFlight: true}
	opened.calls[callID] = record
	broker := opened.config.Broker
	registry.mu.Unlock()

	brokerResponse, callErr := broker.Call(ctx, request.Call)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if callErr != nil {
		return Response{SchemaVersion: SchemaVersion, Status: StatusAmbiguous, CallID: callID, ErrorCode: "dispatch_outcome_unknown"}, nil
	}
	record.inFlight = false
	record.response = append(json.RawMessage(nil), brokerResponse...)
	return Response{SchemaVersion: SchemaVersion, Status: StatusCompleted, CallID: callID, BrokerResponse: append(json.RawMessage(nil), brokerResponse...)}, nil
}

func DecodeRequest(raw []byte, maximum int) (Request, error) {
	if maximum <= 0 || maximum > maxFrameBytes || len(raw) == 0 || len(raw) > maximum || rejectDuplicateJSON(raw) != nil {
		return Request{}, ErrInvalidRequest
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 6 {
		return Request{}, ErrInvalidRequest
	}
	for _, key := range []string{"schema_version", "channel_id", "invocation_id", "execution_id", "plan_sha256", "call"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Request{}, ErrInvalidRequest
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var request Request
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || request.SchemaVersion != SchemaVersion ||
		!identityPattern.MatchString(request.ChannelID) || !identityPattern.MatchString(request.InvocationID) ||
		!identityPattern.MatchString(request.ExecutionID) || !digestPattern.MatchString(request.PlanSHA256) || len(request.Call) == 0 {
		return Request{}, ErrInvalidRequest
	}
	if _, _, err := callIdentity(request.Call); err != nil {
		return Request{}, ErrInvalidRequest
	}
	return request, nil
}

func callIdentity(raw []byte) (string, string, error) {
	if len(raw) == 0 || len(raw) > maxFrameBytes || rejectDuplicateJSON(raw) != nil {
		return "", "", ErrInvalidRequest
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		return "", "", ErrInvalidRequest
	}
	for _, key := range []string{"call_id", "capability", "arguments"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", "", ErrInvalidRequest
		}
	}
	var call struct {
		CallID     string          `json:"call_id"`
		Capability string          `json:"capability"`
		Arguments  json.RawMessage `json:"arguments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&call) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || !identityPattern.MatchString(call.CallID) || call.Capability == "" || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		return "", "", ErrInvalidRequest
	}
	digest := sha256.Sum256(raw)
	return call.CallID, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrInvalidRequest
		}
		return err
	}
	return nil
}

func scanValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidRequest
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidRequest
			}
			seen[key] = struct{}{}
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}
