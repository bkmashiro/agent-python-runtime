// Package observe defines the bounded Host-authored Runtime observation
// contract. It deliberately contains no storage or Runtime policy.
package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	SchemaVersion = "pysolate.runtime-observation.v1"

	EventExecutionStarted   = "execution.started"
	EventExecutionCompleted = "execution.completed"
	EventExecutionFailed    = "execution.failed"
	EventCapabilityPlan     = "capability.plan_bound"
	EventCapabilityCall     = "capability.call"
	EventWorkspaceFinalized = "workspace.finalized"

	// MaxPayloadBytes bounds metadata carried by one event. Large or private
	// bodies belong in a separately protected content store and are referenced
	// from portable observation metadata instead of being embedded here.
	MaxPayloadBytes = 16 << 10
	// MaxEventsPerExecution bounds one in-memory Session independently of the
	// configured Recorder implementation.
	MaxEventsPerExecution = 1024
	MaxEncodedEventBytes  = MaxPayloadBytes + 2048
)

const maxJSONNodes = 2048

var (
	ErrInvalidSession      = errors.New("invalid observation session")
	ErrInvalidEvent        = errors.New("invalid observation event")
	ErrInvalidCausalParent = errors.New("invalid observation causal parent")
	ErrEventLimitExceeded  = errors.New("observation event limit exceeded")

	identifier     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	capabilityName = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$`)
	digest         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Mode string

const (
	Off        Mode = "off"
	BestEffort Mode = "best_effort"
	Required   Mode = "required"
)

func (mode Mode) Valid() bool {
	return mode == Off || mode == BestEffort || mode == Required
}

// Event is one canonical, Host-authored observation envelope. Sequence is
// one-based. ParentSequence, when present, always names an earlier successfully
// appended event from the same execution Session.
type Event struct {
	SchemaVersion  string          `json:"schema_version"`
	ExecutionID    string          `json:"execution_id"`
	Sequence       uint32          `json:"sequence"`
	ParentSequence *uint32         `json:"parent_sequence,omitempty"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
}

// Recorder is an optional sink selected and owned by the Host. Append must
// return only after it has either accepted the event or rejected it.
type Recorder interface {
	Append(context.Context, Event) error
}

type Session struct {
	mu         sync.Mutex
	mode       Mode
	recorder   Recorder
	execution  string
	sequence   uint32
	incomplete bool
}

func NewSession(mode Mode, recorder Recorder, executionID string) (*Session, error) {
	if !identifier.MatchString(executionID) || !mode.Valid() || (mode != Off && recorder == nil) {
		return nil, ErrInvalidSession
	}
	return &Session{mode: mode, recorder: recorder, execution: executionID}, nil
}

// ExecutionID returns the immutable Host execution identity bound to this
// session. It is useful for checking the correlation context before Guest
// startup.
func (session *Session) ExecutionID() string {
	if session == nil {
		return ""
	}
	return session.execution
}

func (session *Session) Mode() Mode {
	if session == nil {
		return ""
	}
	return session.mode
}

func (session *Session) Incomplete() bool {
	if session == nil {
		return true
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.incomplete
}

// Append validates and copies the payload before calling the Host Recorder.
// Recorder calls are serialized so a failed append never consumes a sequence
// number and concurrent successful calls remain gap-free. Required propagates
// sink failure; BestEffort records the evidence loss and returns no Event; Off
// performs no validation or sink work and returns the zero Event.
func (session *Session) Append(ctx context.Context, kind string, parent *uint32, payload json.RawMessage) (Event, error) {
	if session == nil {
		return Event{}, ErrInvalidSession
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.mode == Off {
		return Event{}, nil
	}
	if ctx == nil {
		return Event{}, ErrInvalidEvent
	}
	canonicalPayload, err := validatePayload(kind, payload)
	if err != nil {
		return Event{}, err
	}
	if session.incomplete && terminalClaimsComplete(kind, canonicalPayload) {
		return Event{}, ErrInvalidEvent
	}
	if parent != nil && (*parent == 0 || *parent > session.sequence) {
		return Event{}, ErrInvalidCausalParent
	}
	if session.sequence >= MaxEventsPerExecution {
		return Event{}, ErrEventLimitExceeded
	}
	next := session.sequence + 1
	event := Event{
		SchemaVersion:  SchemaVersion,
		ExecutionID:    session.execution,
		Sequence:       next,
		ParentSequence: cloneParent(parent),
		Type:           kind,
		Payload:        canonicalPayload,
	}
	// Give the Recorder its own deep copy. A Recorder may retain or mutate the
	// value without aliasing the Event returned to the caller.
	if err := session.recorder.Append(ctx, cloneEvent(event)); err != nil {
		if session.mode == Required {
			return Event{}, fmt.Errorf("append required observation event: %w", err)
		}
		session.incomplete = true
		return Event{}, nil
	}
	session.sequence = next
	return cloneEvent(event), nil
}

// Encode returns the canonical byte representation of one independently valid
// event envelope.
func Encode(event Event) ([]byte, error) {
	canonical, err := validateEvent(event)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(canonical)
	if err != nil || len(encoded) > MaxEncodedEventBytes {
		return nil, ErrInvalidEvent
	}
	return encoded, nil
}

// Decode accepts only the exact-key, duplicate-free, canonical v1 envelope.
// Cross-event parent existence is enforced by Session; a standalone envelope
// can only prove that its parent sequence precedes it.
func Decode(raw []byte) (Event, error) {
	if len(raw) == 0 || len(raw) > MaxEncodedEventBytes || !utf8.Valid(raw) {
		return Event{}, ErrInvalidEvent
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return Event{}, ErrInvalidEvent
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil ||
		!hasRequiredAndOnlyExactKeys(fields,
			[]string{"schema_version", "execution_id", "sequence", "type", "payload"},
			"parent_sequence") || containsNull(fields) {
		return Event{}, ErrInvalidEvent
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var event Event
	if decoder.Decode(&event) != nil {
		return Event{}, ErrInvalidEvent
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Event{}, ErrInvalidEvent
	}
	encoded, err := Encode(event)
	if err != nil || !bytes.Equal(encoded, raw) {
		return Event{}, ErrInvalidEvent
	}
	return cloneEvent(event), nil
}

func validateEvent(event Event) (Event, error) {
	if event.SchemaVersion != SchemaVersion || !identifier.MatchString(event.ExecutionID) ||
		event.Sequence == 0 || event.Sequence > MaxEventsPerExecution {
		return Event{}, ErrInvalidEvent
	}
	if event.ParentSequence != nil && (*event.ParentSequence == 0 || *event.ParentSequence >= event.Sequence) {
		return Event{}, ErrInvalidCausalParent
	}
	payload, err := validatePayload(event.Type, event.Payload)
	if err != nil {
		return Event{}, err
	}
	event.Payload = payload
	event.ParentSequence = cloneParent(event.ParentSequence)
	return event, nil
}

func cloneEvent(event Event) Event {
	event.ParentSequence = cloneParent(event.ParentSequence)
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}

func cloneParent(parent *uint32) *uint32 {
	if parent == nil {
		return nil
	}
	value := *parent
	return &value
}

type ExecutionStartedPayload struct {
	ArtifactSHA256             string `json:"artifact_sha256,omitempty"`
	CapabilityPlanSHA256       string `json:"capability_plan_sha256,omitempty"`
	DeterministicProfileSHA256 string `json:"deterministic_profile_sha256,omitempty"`
	ExecutedCodeSHA256         string `json:"executed_code_sha256"`
	ExecutionProfileSHA256     string `json:"execution_profile_sha256,omitempty"`
}

type ExecutionCompletedPayload struct {
	EvidenceComplete bool   `json:"evidence_complete"`
	ResultSHA256     string `json:"result_sha256"`
	Status           string `json:"status"`
}

type ExecutionFailedPayload struct {
	ErrorClass       string `json:"error_class"`
	EvidenceComplete bool   `json:"evidence_complete"`
	ResultSHA256     string `json:"result_sha256,omitempty"`
	Status           string `json:"status"`
}

type CapabilityCallPayload struct {
	ArgumentsSHA256      string                `json:"arguments_sha256"`
	Capability           string                `json:"capability"`
	CapabilityPlanSHA256 string                `json:"capability_plan_sha256,omitempty"`
	OperationIndex       uint32                `json:"operation_index"`
	Outcome              string                `json:"outcome"`
	ReceiptID            string                `json:"receipt_id,omitempty"`
	ResultSHA256         string                `json:"result_sha256,omitempty"`
	Source               *SourceBindingPayload `json:"source,omitempty"`
}

// SourceBindingPayload is field-ordered for canonical nested JSON encoding.
type SourceBindingPayload struct {
	Capability        string `json:"capability"`
	ClaimLevel        string `json:"claim_level"`
	DocumentID        string `json:"document_id"`
	DynamicOccurrence uint32 `json:"dynamic_occurrence"`
	EndColumn         uint32 `json:"end_column"`
	EndLine           uint32 `json:"end_line"`
	OccurrenceID      string `json:"occurrence_id"`
	SchemaVersion     string `json:"schema_version"`
	SourceSHA256      string `json:"source_sha256"`
	StartColumn       uint32 `json:"start_column"`
	StartLine         uint32 `json:"start_line"`
}

type CapabilityPlanBoundPayload struct {
	CapabilityPlanSHA256 string `json:"capability_plan_sha256"`
}

type WorkspaceFinalizedPayload struct {
	Changes                []WorkspaceChange `json:"changes"`
	ChangesTruncated       bool              `json:"changes_truncated"`
	EntryCount             uint32            `json:"entry_count"`
	FinalTreeSHA256        string            `json:"final_tree_sha256"`
	FinalWorkspaceSHA256   string            `json:"final_workspace_sha256"`
	InitialWorkspaceSHA256 string            `json:"initial_workspace_sha256"`
	SyscallOrderAvailable  bool              `json:"syscall_order_available"`
	TotalBytes             uint64            `json:"total_bytes"`
}

type WorkspaceChange struct {
	AfterBytes       uint64 `json:"after_bytes"`
	AfterExecutable  bool   `json:"after_executable"`
	AfterSHA256      string `json:"after_sha256,omitempty"`
	BeforeBytes      uint64 `json:"before_bytes"`
	BeforeExecutable bool   `json:"before_executable"`
	BeforeSHA256     string `json:"before_sha256,omitempty"`
	Kind             string `json:"kind"`
	Path             string `json:"path"`
}

func validatePayload(kind string, raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes || !utf8.Valid(raw) || !validKind(kind) {
		return nil, ErrInvalidEvent
	}
	canonical, fields, err := canonicalJSONObject(raw)
	if err != nil || !bytes.Equal(canonical, raw) || containsNull(fields) {
		return nil, ErrInvalidEvent
	}
	switch kind {
	case EventExecutionStarted:
		if !hasRequiredAndOnlyExactKeys(fields, []string{"executed_code_sha256"},
			"artifact_sha256", "capability_plan_sha256", "deterministic_profile_sha256", "execution_profile_sha256") {
			return nil, ErrInvalidEvent
		}
		var payload ExecutionStartedPayload
		if json.Unmarshal(raw, &payload) != nil || !digest.MatchString(payload.ExecutedCodeSHA256) ||
			!optionalDigestField(fields, "artifact_sha256", payload.ArtifactSHA256) ||
			!optionalDigestField(fields, "capability_plan_sha256", payload.CapabilityPlanSHA256) ||
			!optionalDigestField(fields, "deterministic_profile_sha256", payload.DeterministicProfileSHA256) ||
			!optionalDigestField(fields, "execution_profile_sha256", payload.ExecutionProfileSHA256) {
			return nil, ErrInvalidEvent
		}
	case EventExecutionCompleted:
		if !hasRequiredAndOnlyExactKeys(fields, []string{"evidence_complete", "result_sha256", "status"}) {
			return nil, ErrInvalidEvent
		}
		var payload ExecutionCompletedPayload
		if json.Unmarshal(raw, &payload) != nil || payload.Status != "ok" || !digest.MatchString(payload.ResultSHA256) {
			return nil, ErrInvalidEvent
		}
	case EventExecutionFailed:
		if !hasRequiredAndOnlyExactKeys(fields, []string{"error_class", "evidence_complete", "status"}, "result_sha256") {
			return nil, ErrInvalidEvent
		}
		var payload ExecutionFailedPayload
		if json.Unmarshal(raw, &payload) != nil || payload.Status != "error" || !identifier.MatchString(payload.ErrorClass) ||
			!optionalDigestField(fields, "result_sha256", payload.ResultSHA256) {
			return nil, ErrInvalidEvent
		}
	case EventCapabilityCall:
		if !hasRequiredAndOnlyExactKeys(fields,
			[]string{"arguments_sha256", "capability", "operation_index", "outcome"}, "capability_plan_sha256", "receipt_id", "result_sha256", "source") {
			return nil, ErrInvalidEvent
		}
		var payload CapabilityCallPayload
		if json.Unmarshal(raw, &payload) != nil || !digest.MatchString(payload.ArgumentsSHA256) || !optionalDigestField(fields, "capability_plan_sha256", payload.CapabilityPlanSHA256) ||
			!validCapabilityName(payload.Capability) || !validOutcome(payload.Outcome) || !optionalIdentifierField(fields, "receipt_id", payload.ReceiptID) ||
			!optionalDigestField(fields, "result_sha256", payload.ResultSHA256) ||
			(payload.ResultSHA256 != "" && payload.Outcome != "ok") || !optionalSourceField(fields, payload.Source) ||
			(payload.Source != nil && payload.ReceiptID == "") {
			return nil, ErrInvalidEvent
		}
	case EventCapabilityPlan:
		if !hasRequiredAndOnlyExactKeys(fields, []string{"capability_plan_sha256"}) {
			return nil, ErrInvalidEvent
		}
		var payload CapabilityPlanBoundPayload
		if json.Unmarshal(raw, &payload) != nil || !digest.MatchString(payload.CapabilityPlanSHA256) {
			return nil, ErrInvalidEvent
		}
	case EventWorkspaceFinalized:
		if !hasRequiredAndOnlyExactKeys(fields, []string{
			"changes", "changes_truncated", "entry_count", "final_tree_sha256", "final_workspace_sha256", "initial_workspace_sha256", "syscall_order_available", "total_bytes",
		}) {
			return nil, ErrInvalidEvent
		}
		var payload WorkspaceFinalizedPayload
		if json.Unmarshal(raw, &payload) != nil || !digest.MatchString(payload.InitialWorkspaceSHA256) ||
			!digest.MatchString(payload.FinalWorkspaceSHA256) || !digest.MatchString(payload.FinalTreeSHA256) ||
			payload.SyscallOrderAvailable || len(payload.Changes) > 128 || !validWorkspaceChanges(fields["changes"], payload.Changes) {
			return nil, ErrInvalidEvent
		}
	}
	return append(json.RawMessage(nil), canonical...), nil
}

func validWorkspaceChanges(raw json.RawMessage, changes []WorkspaceChange) bool {
	var documents []map[string]json.RawMessage
	if json.Unmarshal(raw, &documents) != nil || len(documents) != len(changes) {
		return false
	}
	previous := ""
	for index, change := range changes {
		if !hasRequiredAndOnlyExactKeys(documents[index], []string{
			"after_bytes", "after_executable", "before_bytes", "before_executable", "kind", "path",
		}, "after_sha256", "before_sha256") || containsNull(documents[index]) {
			return false
		}
		if change.Path == "" || len(change.Path) > 4096 || !utf8.ValidString(change.Path) || strings.Contains(change.Path, "\\") ||
			strings.ContainsRune(change.Path, 0) || strings.HasPrefix(change.Path, "/") || change.Path == "." || change.Path == ".." ||
			strings.HasPrefix(change.Path, "../") || strings.Contains(change.Path, "/../") || (previous != "" && previous >= change.Path) {
			return false
		}
		switch change.Kind {
		case "added":
			if change.BeforeSHA256 != "" || change.BeforeBytes != 0 || change.BeforeExecutable || !digest.MatchString(change.AfterSHA256) {
				return false
			}
		case "removed":
			if !digest.MatchString(change.BeforeSHA256) || change.AfterSHA256 != "" || change.AfterBytes != 0 || change.AfterExecutable {
				return false
			}
		case "modified":
			if !digest.MatchString(change.BeforeSHA256) || !digest.MatchString(change.AfterSHA256) ||
				(change.BeforeSHA256 == change.AfterSHA256 && change.BeforeBytes == change.AfterBytes && change.BeforeExecutable == change.AfterExecutable) {
				return false
			}
		default:
			return false
		}
		previous = change.Path
	}
	return true
}

func validKind(kind string) bool {
	return kind == EventExecutionStarted || kind == EventExecutionCompleted || kind == EventExecutionFailed ||
		kind == EventCapabilityPlan || kind == EventCapabilityCall || kind == EventWorkspaceFinalized
}

func validOutcome(outcome string) bool {
	return outcome == "ok" || outcome == "denied" || outcome == "error" || outcome == "timeout" || outcome == "ambiguous"
}

func terminalClaimsComplete(kind string, raw []byte) bool {
	switch kind {
	case EventExecutionCompleted:
		var payload ExecutionCompletedPayload
		return json.Unmarshal(raw, &payload) == nil && payload.EvidenceComplete
	case EventExecutionFailed:
		var payload ExecutionFailedPayload
		return json.Unmarshal(raw, &payload) == nil && payload.EvidenceComplete
	default:
		return false
	}
}

func validCapabilityName(value string) bool {
	return len(value) <= 128 && capabilityName.MatchString(value)
}

func optionalDigestField(fields map[string]json.RawMessage, name, value string) bool {
	_, present := fields[name]
	if !present {
		return value == ""
	}
	return digest.MatchString(value)
}

func optionalIdentifierField(fields map[string]json.RawMessage, name, value string) bool {
	_, present := fields[name]
	if !present {
		return value == ""
	}
	return identifier.MatchString(value)
}

func optionalSourceField(fields map[string]json.RawMessage, source *SourceBindingPayload) bool {
	_, present := fields["source"]
	if !present {
		return source == nil
	}
	return source != nil && source.SchemaVersion == "pysolate.source-binding.v0" && source.ClaimLevel == "source_bound" &&
		digest.MatchString(source.DocumentID) && digest.MatchString(source.SourceSHA256) && digest.MatchString(source.OccurrenceID) &&
		validCapabilityName(source.Capability) && source.DynamicOccurrence > 0 && source.StartLine > 0 && source.EndLine >= source.StartLine &&
		(source.EndLine != source.StartLine || source.EndColumn >= source.StartColumn)
}

func canonicalJSONObject(raw []byte) (json.RawMessage, map[string]json.RawMessage, error) {
	if err := rejectDuplicateJSON(raw); err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if decoder.Decode(&document) != nil {
		return nil, nil, ErrInvalidEvent
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, ErrInvalidEvent
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, nil, ErrInvalidEvent
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, nil, ErrInvalidEvent
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, nil, ErrInvalidEvent
	}
	return canonical, fields, nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidEvent
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxJSONNodes {
		return ErrInvalidEvent
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
				return ErrInvalidEvent
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidEvent
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalidEvent
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalidEvent
		}
	default:
		return ErrInvalidEvent
	}
	return nil
}

func hasRequiredAndOnlyExactKeys(values map[string]json.RawMessage, required []string, optional ...string) bool {
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = struct{}{}
		if _, present := values[key]; !present {
			return false
		}
	}
	for _, key := range optional {
		allowed[key] = struct{}{}
	}
	for key := range values {
		if _, allowed := allowed[key]; !allowed {
			return false
		}
	}
	return true
}

func containsNull(values map[string]json.RawMessage) bool {
	for _, value := range values {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return true
		}
	}
	return false
}
