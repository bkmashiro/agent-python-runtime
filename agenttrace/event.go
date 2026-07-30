package agenttrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const EventVersion = "agent-trace-event/v1"

type EventType string

const (
	EventRunStarted          EventType = "agent.run.started"
	EventRunCompleted        EventType = "agent.run.completed"
	EventLLMRequestStarted   EventType = "llm.request.started"
	EventLLMResponseReceived EventType = "llm.response.received"
	EventLLMOutputObserved   EventType = "llm.output.observed"
	EventRoutingDecided      EventType = "routing.decided"
	EventDirectToolStarted   EventType = "tool.direct.started"
	EventDirectToolCompleted EventType = "tool.direct.completed"
	EventRuntimeStarted      EventType = "runtime.invocation.started"
	EventRuntimeCompleted    EventType = "runtime.invocation.completed"
	EventCheckpointCreated   EventType = "agent.checkpoint.created"
	EventFinalStateObserved  EventType = "agent.final_state.observed"
)

type Mode string

const (
	ModeOff        Mode = "off"
	ModeBestEffort Mode = "best-effort"
	ModeRequired   Mode = "required"
)

var (
	ErrInvalidEvent     = errors.New("invalid agent trace event")
	ErrSensitivePayload = errors.New("portable trace payload contains a sensitive body field")
	ErrAppend           = errors.New("append agent trace event")
	ErrIntegrity        = errors.New("agent trace integrity failure")
	ErrConflict         = errors.New("agent trace append conflict")
	ErrInvalidPlugin    = errors.New("invalid agent trace plugin")
)

type Event struct {
	Version          string          `json:"version"`
	EventID          string          `json:"event_id"`
	AgentRunID       string          `json:"agent_run_id"`
	Sequence         uint64          `json:"sequence"`
	EventType        EventType       `json:"event_type"`
	ObservedAt       time.Time       `json:"observed_at"`
	ParentEventID    string          `json:"parent_event_id,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	PayloadDigest    string          `json:"payload_digest"`
	StateFingerprint string          `json:"state_fingerprint,omitempty"`
}

type Sink interface {
	Append(context.Context, Event) error
}

type Plugin struct {
	Mode Mode
	Sink Sink
}

type Recorder struct {
	mu         sync.Mutex
	mode       Mode
	sink       Sink
	agentRunID string
	clock      func() time.Time
	sequence   uint64
	dropped    uint64
}

type pluginContextKey struct{}

func WithPlugin(ctx context.Context, plugin Plugin) (context.Context, error) {
	if ctx == nil || plugin.validate() != nil {
		return nil, ErrInvalidPlugin
	}
	return context.WithValue(ctx, pluginContextKey{}, plugin), nil
}

func PluginFromContext(ctx context.Context) (Plugin, bool) {
	if ctx == nil {
		return Plugin{}, false
	}
	plugin, ok := ctx.Value(pluginContextKey{}).(Plugin)
	return plugin, ok && plugin.validate() == nil
}

func (plugin Plugin) validate() error {
	switch plugin.Mode {
	case ModeOff:
		return nil
	case ModeBestEffort, ModeRequired:
		if plugin.Sink == nil {
			return ErrInvalidPlugin
		}
		return nil
	default:
		return ErrInvalidPlugin
	}
}

func (plugin Plugin) Begin(agentRunID string, clock func() time.Time) (*Recorder, error) {
	if plugin.validate() != nil || !boundedIdentifier(agentRunID, 128) {
		return nil, ErrInvalidPlugin
	}
	if clock == nil {
		clock = time.Now
	}
	return &Recorder{mode: plugin.Mode, sink: plugin.Sink, agentRunID: agentRunID, clock: clock}, nil
}

func (recorder *Recorder) Record(ctx context.Context, eventType EventType, parentEventID string, payload json.RawMessage, stateFingerprint string) (Event, error) {
	if recorder == nil || !validEventType(eventType) || (parentEventID != "" && !boundedIdentifier(parentEventID, 160)) ||
		(stateFingerprint != "" && !validDigest(stateFingerprint)) {
		return Event{}, ErrInvalidEvent
	}
	canonical, err := canonicalMetadataPayload(payload)
	if err != nil {
		return Event{}, err
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	nextSequence := recorder.sequence + 1
	event := Event{
		Version: EventVersion, AgentRunID: recorder.agentRunID, Sequence: nextSequence, EventType: eventType,
		ObservedAt: recorder.clock().UTC(), ParentEventID: parentEventID, Payload: canonical,
		PayloadDigest: digest(canonical), StateFingerprint: stateFingerprint,
	}
	event.EventID = eventIdentity(event)
	if recorder.mode == ModeOff {
		recorder.sequence = nextSequence
		return event, nil
	}
	if err := recorder.sink.Append(ctx, event); err != nil {
		recorder.dropped++
		if recorder.mode == ModeRequired {
			return Event{}, fmt.Errorf("%w: %v", ErrAppend, err)
		}
		return Event{}, nil
	}
	recorder.sequence = nextSequence
	return event, nil
}

func (recorder *Recorder) Dropped() uint64 {
	if recorder == nil {
		return 0
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.dropped
}

func (event Event) Validate() error {
	if event.Version != EventVersion || !boundedIdentifier(event.EventID, 160) || !boundedIdentifier(event.AgentRunID, 128) ||
		event.Sequence == 0 || !validEventType(event.EventType) || event.ObservedAt.IsZero() ||
		(event.ParentEventID != "" && !boundedIdentifier(event.ParentEventID, 160)) ||
		(event.StateFingerprint != "" && !validDigest(event.StateFingerprint)) {
		return ErrInvalidEvent
	}
	canonical, err := canonicalMetadataPayload(event.Payload)
	if err != nil || !bytes.Equal(canonical, event.Payload) || digest(canonical) != event.PayloadDigest || eventIdentity(event) != event.EventID {
		return ErrIntegrity
	}
	return nil
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventRunStarted, EventRunCompleted, EventLLMRequestStarted, EventLLMResponseReceived, EventLLMOutputObserved,
		EventRoutingDecided, EventDirectToolStarted, EventDirectToolCompleted, EventRuntimeStarted, EventRuntimeCompleted,
		EventCheckpointCreated, EventFinalStateObserved:
		return true
	default:
		return false
	}
}

var sensitivePayloadKeys = map[string]struct{}{
	"prompt": {}, "developer_prompt": {}, "code": {}, "content": {}, "arguments": {}, "observation": {},
	"request_body": {}, "response_body": {}, "tool_surface": {}, "raw": {},
}

func canonicalMetadataPayload(payload json.RawMessage) ([]byte, error) {
	if len(payload) == 0 || len(payload) > 64*1024 {
		return nil, ErrInvalidEvent
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var document any
	if decoder.Decode(&document) != nil {
		return nil, ErrInvalidEvent
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidEvent
	}
	root, ok := document.(map[string]any)
	if !ok {
		return nil, ErrInvalidEvent
	}
	if containsSensitiveKey(root) {
		return nil, ErrSensitivePayload
	}
	canonical, err := json.Marshal(root)
	if err != nil || len(canonical) > 64*1024 {
		return nil, ErrInvalidEvent
	}
	return canonical, nil
}

func containsSensitiveKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, blocked := sensitivePayloadKeys[strings.ToLower(key)]; blocked || containsSensitiveKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsSensitiveKey(nested) {
				return true
			}
		}
	}
	return false
}

func eventIdentity(event Event) string {
	identity := fmt.Sprintf("%s\n%s\n%d\n%s\n%s\n%s\n%s", event.Version, event.AgentRunID, event.Sequence,
		event.EventType, event.ParentEventID, event.PayloadDigest, event.StateFingerprint)
	sum := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("evt_%x", sum[:16])
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func validDigest(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
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

type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

func NewMemorySink() *MemorySink { return &MemorySink{} }

func (sink *MemorySink) Append(_ context.Context, event Event) error {
	if sink == nil || event.Validate() != nil {
		return ErrIntegrity
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, cloneEvent(event))
	return nil
}

func (sink *MemorySink) Events() []Event {
	if sink == nil {
		return nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events := make([]Event, len(sink.events))
	for index, event := range sink.events {
		events[index] = cloneEvent(event)
	}
	return events
}

func cloneEvent(event Event) Event {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}
