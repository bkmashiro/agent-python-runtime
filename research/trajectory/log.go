// Package trajectory records the complete model-visible and model-emitted
// development history as a private, append-only, hash-chained session log.
package trajectory

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

const SchemaVersion = "pysolate.agent-trajectory.v0"

const hashDomain = "pysolate.agent-trajectory.event.v0\x00"

var (
	identifierPattern = regexp.MustCompile(`^(session|turn|step|event|call|run|logical|physical|span|agent)-[0-9a-z][0-9a-z-]{7,127}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type EventType string

const (
	EventSessionStart       EventType = "session.start"
	EventSessionEnd         EventType = "session.end"
	EventTurnStart          EventType = "turn.start"
	EventTurnEnd            EventType = "turn.end"
	EventContext            EventType = "context.inject"
	EventUserMessage        EventType = "user.message"
	EventRequestHeader      EventType = "request.header"
	EventModelRequest       EventType = "model.request"
	EventAssistantChunk     EventType = "assistant.chunk"
	EventAssistantReasoning EventType = "assistant.reasoning"
	EventAssistantOutput    EventType = "assistant.output"
	EventToolCall           EventType = "tool.call"
	EventToolResult         EventType = "tool.result"
	EventSubagentDispatch   EventType = "subagent.dispatch"
	EventSubagentResult     EventType = "subagent.result"
	EventRuntime            EventType = "runtime.event"
	EventWorkspace          EventType = "workspace.change"
)

type Source string

const (
	SourceSystem    Source = "system"
	SourceDeveloper Source = "developer"
	SourceUser      Source = "user"
	SourceMemory    Source = "memory"
	SourceSkill     Source = "skill"
	SourceHarness   Source = "harness"
	SourceModel     Source = "model"
	SourceTool      Source = "tool"
	SourceSubagent  Source = "subagent"
	SourceRuntime   Source = "runtime"
	SourceWorkspace Source = "workspace"
)

type TokenUsage struct {
	Input      uint64 `json:"input,omitempty"`
	Output     uint64 `json:"output,omitempty"`
	Reasoning  uint64 `json:"reasoning,omitempty"`
	CacheRead  uint64 `json:"cache_read,omitempty"`
	CacheWrite uint64 `json:"cache_write,omitempty"`
}

type SessionHeader struct {
	SchemaVersion string `json:"schema_version"`
	SessionID     string `json:"session_id"`
	SourceCommit  string `json:"source_commit"`
	HeaderSHA256  string `json:"header_sha256"`
}

type Event struct {
	Sequence            uint64        `json:"sequence"`
	EventID             string        `json:"event_id"`
	PreviousSHA256      string        `json:"previous_sha256"`
	SHA256              string        `json:"sha256"`
	OccurredMillis      uint64        `json:"occurred_millis"`
	Type                EventType     `json:"type"`
	Source              Source        `json:"source"`
	ActorID             string        `json:"actor_id,omitempty"`
	ParentEventID       string        `json:"parent_event_id,omitempty"`
	TurnID              string        `json:"turn_id,omitempty"`
	StepID              string        `json:"step_id,omitempty"`
	ModelVisible        bool          `json:"model_visible"`
	ContextEventIDs     []string      `json:"context_event_ids,omitempty"`
	SourceEventIDs      []string      `json:"source_event_ids,omitempty"`
	Body                *labstore.Ref `json:"body,omitempty"`
	ContentType         string        `json:"content_type,omitempty"`
	Provider            string        `json:"provider,omitempty"`
	Model               string        `json:"model,omitempty"`
	FinishReason        string        `json:"finish_reason,omitempty"`
	ToolCallID          string        `json:"tool_call_id,omitempty"`
	ToolName            string        `json:"tool_name,omitempty"`
	ChildSessionID      string        `json:"child_session_id,omitempty"`
	RunID               string        `json:"run_id,omitempty"`
	LogicalRequestID    string        `json:"logical_request_id,omitempty"`
	PhysicalExecutionID string        `json:"physical_execution_id,omitempty"`
	SpanID              string        `json:"span_id,omitempty"`
	Status              string        `json:"status,omitempty"`
	DurationNanos       uint64        `json:"duration_nanos,omitempty"`
	Usage               *TokenUsage   `json:"usage,omitempty"`
}

type EventInput struct {
	OccurredMillis      uint64
	Type                EventType
	Source              Source
	ActorID             string
	ParentEventID       string
	TurnID              string
	StepID              string
	ModelVisible        bool
	ContextEventIDs     []string
	SourceEventIDs      []string
	Body                []byte
	BodyKind            labstore.Kind
	ContentType         string
	Provider            string
	Model               string
	FinishReason        string
	ToolCallID          string
	ToolName            string
	ChildSessionID      string
	RunID               string
	LogicalRequestID    string
	PhysicalExecutionID string
	SpanID              string
	Status              string
	DurationNanos       uint64
	Usage               *TokenUsage
}

type ContextItem struct {
	EventID string `json:"event_id"`
	Source  Source `json:"source"`
	Body    string `json:"body"`
}

type ExportEvent struct {
	Event
	BodyText string `json:"body_text,omitempty"`
}

type PrivateExport struct {
	SchemaVersion string           `json:"schema_version"`
	Privacy       labstore.Privacy `json:"privacy"`
	Session       SessionHeader    `json:"session"`
	Events        []ExportEvent    `json:"events"`
	SealSHA256    string           `json:"seal_sha256,omitempty"`
}

type record struct {
	Kind   string         `json:"kind"`
	Header *SessionHeader `json:"header,omitempty"`
	Event  *Event         `json:"event,omitempty"`
}

type Log struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	store    *labstore.Store
	header   SessionHeader
	events   []Event
	closed   bool
	terminal bool
}

func Create(path string, store *labstore.Store, header SessionHeader) (*Log, error) {
	if store == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("trajectory path and store are required")
	}
	header.SchemaVersion = SchemaVersion
	header.HeaderSHA256 = ""
	if !identifierPattern.MatchString(header.SessionID) || !strings.HasPrefix(header.SessionID, "session-") || !commitPattern.MatchString(header.SourceCommit) {
		return nil, errors.New("invalid trajectory header")
	}
	header.HeaderSHA256 = hashValue("header", header)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	log := &Log{path: path, file: file, store: store, header: header}
	if err := log.writeRecord(record{Kind: "header", Header: &header}); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return log, nil
}

func Open(path string, store *labstore.Store) (*Log, error) {
	if store == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("trajectory path and store are required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("trajectory log must be a private regular file")
	}
	read, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(read)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var header SessionHeader
	var events []Event
	line := 0
	for scanner.Scan() {
		line++
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		var value record
		if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			_ = read.Close()
			return nil, fmt.Errorf("invalid trajectory record at line %d", line)
		}
		if line == 1 && value.Kind == "header" && value.Header != nil && value.Event == nil {
			header = *value.Header
			continue
		}
		if line == 1 || value.Kind != "event" || value.Event == nil || value.Header != nil {
			_ = read.Close()
			return nil, fmt.Errorf("invalid trajectory record at line %d", line)
		}
		events = append(events, *value.Event)
	}
	if err := errors.Join(scanner.Err(), read.Close()); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	terminal := len(events) > 0 && events[len(events)-1].Type == EventSessionEnd
	log := &Log{path: path, file: file, store: store, header: header, events: events, terminal: terminal}
	if err := log.Validate(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return log, nil
}

func (log *Log) Append(input EventInput) (Event, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return Event{}, errors.New("trajectory log is closed")
	}
	if log.terminal {
		return Event{}, errors.New("trajectory session is terminal")
	}
	event := Event{
		Sequence: uint64(len(log.events) + 1), EventID: fmt.Sprintf("event-%016x", len(log.events)+1),
		OccurredMillis: input.OccurredMillis, Type: input.Type, Source: input.Source, ActorID: input.ActorID,
		ParentEventID: input.ParentEventID, TurnID: input.TurnID, StepID: input.StepID, ModelVisible: input.ModelVisible,
		ContextEventIDs: append([]string(nil), input.ContextEventIDs...), SourceEventIDs: append([]string(nil), input.SourceEventIDs...), ContentType: input.ContentType,
		Provider: input.Provider, Model: input.Model, FinishReason: input.FinishReason, ToolCallID: input.ToolCallID,
		ToolName: input.ToolName, ChildSessionID: input.ChildSessionID, RunID: input.RunID,
		LogicalRequestID: input.LogicalRequestID, PhysicalExecutionID: input.PhysicalExecutionID, SpanID: input.SpanID,
		Status: input.Status, DurationNanos: input.DurationNanos,
	}
	if input.Usage != nil {
		usage := *input.Usage
		event.Usage = &usage
	}
	if len(input.Body) != 0 {
		if input.BodyKind == "" {
			return Event{}, errors.New("trajectory body kind is required")
		}
		ref, _, err := log.store.Put(input.BodyKind, input.Body, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
		if err != nil {
			return Event{}, err
		}
		event.Body = &ref
	}
	if len(log.events) == 0 {
		event.PreviousSHA256 = log.header.HeaderSHA256
	} else {
		event.PreviousSHA256 = log.events[len(log.events)-1].SHA256
	}
	if err := validateNext(log.header, log.events, event, log.store); err != nil {
		return Event{}, err
	}
	event.SHA256 = hashEvent(event)
	if err := log.writeRecord(record{Kind: "event", Event: &event}); err != nil {
		return Event{}, err
	}
	log.events = append(log.events, event)
	log.terminal = event.Type == EventSessionEnd
	return event, nil
}

func (log *Log) Validate() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.header.SchemaVersion != SchemaVersion || !identifierPattern.MatchString(log.header.SessionID) || !commitPattern.MatchString(log.header.SourceCommit) {
		return errors.New("invalid trajectory header")
	}
	header := log.header
	header.HeaderSHA256 = ""
	if log.header.HeaderSHA256 != hashValue("header", header) {
		return errors.New("trajectory header seal mismatch")
	}
	prior := make([]Event, 0, len(log.events))
	for _, event := range log.events {
		if err := validateNext(log.header, prior, event, log.store); err != nil {
			return err
		}
		if event.SHA256 != hashEvent(event) {
			return fmt.Errorf("trajectory event seal mismatch at sequence %d", event.Sequence)
		}
		prior = append(prior, event)
	}
	return nil
}

func (log *Log) ModelContext(requestEventID string) ([]ContextItem, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	byID := eventIndex(log.events)
	request, ok := byID[requestEventID]
	if !ok || request.Type != EventModelRequest {
		return nil, errors.New("model request event not found")
	}
	items := make([]ContextItem, 0, len(request.ContextEventIDs))
	for _, id := range request.ContextEventIDs {
		event := byID[id]
		if event.Body == nil {
			return nil, errors.New("model context body is unavailable")
		}
		object, err := log.store.Get(*event.Body)
		if err != nil {
			return nil, err
		}
		items = append(items, ContextItem{EventID: id, Source: event.Source, Body: string(object.Body)})
	}
	return items, nil
}

func (log *Log) ExportPrivate() (PrivateExport, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	exported := PrivateExport{SchemaVersion: SchemaVersion, Privacy: "private", Session: log.header, Events: make([]ExportEvent, 0, len(log.events))}
	for _, event := range log.events {
		item := ExportEvent{Event: event}
		if event.Body != nil {
			object, err := log.store.Get(*event.Body)
			if err != nil {
				return PrivateExport{}, err
			}
			item.BodyText = string(object.Body)
		}
		exported.Events = append(exported.Events, item)
	}
	exported.SealSHA256 = hashValue("private-export", exported)
	return exported, nil
}

// ValidateExport verifies the complete materialized private browser export,
// including body bytes that are deliberately outside the append-only event hash.
func ValidateExport(exported PrivateExport) error {
	if exported.SchemaVersion != SchemaVersion || exported.Privacy != labstore.PrivacyPrivate || !digestPattern.MatchString(exported.SealSHA256) {
		return fmt.Errorf("%w: malformed private export", labstore.ErrInvalid)
	}
	claimed := exported.SealSHA256
	exported.SealSHA256 = ""
	if hashValue("private-export", exported) != claimed {
		return fmt.Errorf("%w: private export seal mismatch", labstore.ErrInvalid)
	}
	return nil
}

func (log *Log) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return nil
	}
	log.closed = true
	return log.file.Close()
}

func (log *Log) writeRecord(value record) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	offset, err := log.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := log.file.Write(encoded); err != nil {
		return log.rollbackRecord(offset, err)
	}
	if err := log.file.Sync(); err != nil {
		return log.rollbackRecord(offset, err)
	}
	return nil
}

func (log *Log) rollbackRecord(offset int64, cause error) error {
	truncateErr := log.file.Truncate(offset)
	_, seekErr := log.file.Seek(0, io.SeekEnd)
	syncErr := log.file.Sync()
	return errors.Join(cause, truncateErr, seekErr, syncErr)
}

func validateNext(header SessionHeader, prior []Event, event Event, store *labstore.Store) error {
	if event.Sequence != uint64(len(prior)+1) || event.EventID != fmt.Sprintf("event-%016x", event.Sequence) || !validEventType(event.Type) || !validSource(event.Source) {
		return errors.New("invalid trajectory event identity")
	}
	expectedPrevious := header.HeaderSHA256
	if len(prior) != 0 {
		expectedPrevious = prior[len(prior)-1].SHA256
	}
	if event.PreviousSHA256 != expectedPrevious || !digestPattern.MatchString(event.PreviousSHA256) {
		return errors.New("trajectory hash chain mismatch")
	}
	byID := eventIndex(prior)
	if event.ParentEventID != "" {
		if _, ok := byID[event.ParentEventID]; !ok {
			return errors.New("trajectory parent event is unavailable")
		}
	}
	seenSource := map[string]bool{}
	for _, id := range event.SourceEventIDs {
		if _, ok := byID[id]; !ok || seenSource[id] {
			return errors.New("trajectory source event is unavailable or duplicated")
		}
		seenSource[id] = true
	}
	if event.TurnID != "" && (!identifierPattern.MatchString(event.TurnID) || !strings.HasPrefix(event.TurnID, "turn-")) {
		return errors.New("invalid trajectory turn")
	}
	if event.StepID != "" && (!identifierPattern.MatchString(event.StepID) || !strings.HasPrefix(event.StepID, "step-")) {
		return errors.New("invalid trajectory step")
	}
	if event.Body != nil {
		object, err := store.Get(*event.Body)
		if err != nil || object.Privacy != labstore.PrivacyPrivate {
			return errors.New("trajectory body is unavailable or not private")
		}
	} else if event.ModelVisible || requiresBody(event.Type) {
		return errors.New("trajectory event requires a body")
	}
	if event.Type == EventModelRequest {
		if len(event.ContextEventIDs) == 0 || event.Provider == "" || event.Model == "" {
			return errors.New("model request is incomplete")
		}
		seen := map[string]bool{}
		for _, id := range event.ContextEventIDs {
			context, ok := byID[id]
			if !ok || context.Body == nil || seen[id] {
				return errors.New("model request context is invalid")
			}
			seen[id] = true
		}
	} else if len(event.ContextEventIDs) != 0 {
		return errors.New("only model requests may carry context IDs")
	}
	if event.Type == EventToolCall {
		if event.ToolCallID == "" || event.ToolName == "" || !strings.HasPrefix(event.ToolCallID, "call-") {
			return errors.New("tool call is incomplete")
		}
	}
	if event.Type == EventAssistantReasoning || event.Type == EventAssistantOutput || event.Type == EventToolCall {
		if len(event.SourceEventIDs) == 0 {
			return errors.New("assembled model event has no raw chunk citations")
		}
		for _, id := range event.SourceEventIDs {
			if byID[id].Type != EventAssistantChunk {
				return errors.New("assembled model event cites a non-chunk source")
			}
		}
	}
	if event.Type == EventToolResult || event.Type == EventRuntime || event.Type == EventWorkspace {
		call, ok := findToolCall(prior, event.ToolCallID)
		if !ok || (event.ToolName != "" && event.ToolName != call.ToolName) {
			return errors.New("tool-linked event has no matching call")
		}
		if event.Type == EventToolResult && !seenSource[call.EventID] {
			return errors.New("tool result does not cite its call event")
		}
	}
	if event.Type == EventRuntime && (event.ToolCallID == "" || event.RunID == "" || (event.LogicalRequestID == "") != (event.PhysicalExecutionID == "")) {
		return errors.New("runtime event has incomplete execution identity")
	}
	return nil
}

func requiresBody(kind EventType) bool {
	switch kind {
	case EventContext, EventUserMessage, EventRequestHeader, EventAssistantChunk, EventAssistantReasoning, EventAssistantOutput, EventToolCall, EventToolResult, EventRuntime, EventWorkspace:
		return true
	default:
		return false
	}
}

func validEventType(value EventType) bool {
	switch value {
	case EventSessionStart, EventSessionEnd, EventTurnStart, EventTurnEnd, EventContext, EventUserMessage, EventRequestHeader, EventModelRequest, EventAssistantChunk, EventAssistantReasoning, EventAssistantOutput, EventToolCall, EventToolResult, EventSubagentDispatch, EventSubagentResult, EventRuntime, EventWorkspace:
		return true
	default:
		return false
	}
}

func validSource(value Source) bool {
	switch value {
	case SourceSystem, SourceDeveloper, SourceUser, SourceMemory, SourceSkill, SourceHarness, SourceModel, SourceTool, SourceSubagent, SourceRuntime, SourceWorkspace:
		return true
	default:
		return false
	}
}

func eventIndex(events []Event) map[string]Event {
	result := make(map[string]Event, len(events))
	for _, event := range events {
		result[event.EventID] = event
	}
	return result
}

func findToolCall(events []Event, callID string) (Event, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == EventToolCall && events[index].ToolCallID == callID {
			return events[index], true
		}
	}
	return Event{}, false
}

func hashEvent(event Event) string {
	event.SHA256 = ""
	return hashValue("event", event)
}

func hashValue(label string, value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(append([]byte(hashDomain+label+"\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:])
}
