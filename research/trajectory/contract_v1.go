package trajectory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const EvidenceSchemaVersion = "pysolate.causal-evidence.v1"

const evidenceHashDomain = "pysolate.causal-evidence.v1\x00"

const MaxEvidenceEvents = 100_000

var (
	evidenceIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{7,127}$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Profile string

const (
	ProfileProductionRollback Profile = "production_rollback"
	ProfileExperimentFull     Profile = "experiment_full"
)

func (profile Profile) valid() bool {
	return profile == ProfileProductionRollback || profile == ProfileExperimentFull
}

type Availability string

const (
	Available   Availability = "available"
	Unavailable Availability = "unavailable"
	NotRecorded Availability = "not_recorded"
	Truncated   Availability = "truncated"
)

func (availability Availability) valid() bool {
	return availability == Available || availability == Unavailable || availability == NotRecorded || availability == Truncated
}

type EvidenceType string

const (
	EventTraceStarted       EvidenceType = "trace.started"
	EventTraceEnded         EvidenceType = "trace.ended"
	EventAuthoritySnapshot  EvidenceType = "authority.snapshot"
	EventEffectTransition   EvidenceType = "effect.transition"
	EventToolDecision       EvidenceType = "tool.decision"
	EventWorkspaceTerminal  EvidenceType = "workspace.terminal"
	EventExecutionAttempt   EvidenceType = "execution.attempt"
	EventModelContext       EvidenceType = "model.context"
	EventModelBody          EvidenceType = "model.body"
	EventModelOutput        EvidenceType = "model.output"
	EventSourceDocument     EvidenceType = "source.document"
	EventSourceBody         EvidenceType = "source.body"
	EventSourceOccurrence   EvidenceType = "source.occurrence"
	EventSourceDecision     EvidenceType = "source.decision"
	EventExecutedLine       EvidenceType = "source.executed_line"
	EventSubagentContext    EvidenceType = "subagent.context"
	EventSubagentRuntime    EvidenceType = "subagent.runtime"
	EventSubagentWorkspace  EvidenceType = "subagent.workspace"
	EventWorkspaceFile      EvidenceType = "workspace.file"
	EventEvidenceTruncated  EvidenceType = "evidence.truncated"
	EventRuntimeObservation EvidenceType = "runtime.observation"
	EventResourceSample     EvidenceType = "resource.sample"
)

func (eventType EvidenceType) valid() bool {
	switch eventType {
	case EventTraceStarted, EventTraceEnded, EventAuthoritySnapshot, EventEffectTransition, EventToolDecision,
		EventWorkspaceTerminal, EventExecutionAttempt, EventModelContext, EventModelBody, EventModelOutput,
		EventSourceDocument, EventSourceBody, EventSourceOccurrence, EventSourceDecision, EventExecutedLine,
		EventSubagentContext, EventSubagentRuntime, EventSubagentWorkspace, EventWorkspaceFile, EventEvidenceTruncated,
		EventRuntimeObservation, EventResourceSample:
		return true
	default:
		return false
	}
}

func (eventType EvidenceType) productionEligible() bool {
	switch eventType {
	case EventTraceStarted, EventTraceEnded, EventAuthoritySnapshot, EventEffectTransition,
		EventWorkspaceTerminal, EventExecutionAttempt, EventEvidenceTruncated:
		return true
	default:
		return false
	}
}

type EffectState string

const (
	EffectIntent                 EffectState = "intent"
	EffectStarted                EffectState = "started"
	EffectCommitted              EffectState = "committed"
	EffectCompensated            EffectState = "compensated"
	EffectCleanupOnly            EffectState = "cleanup_only"
	EffectAmbiguous              EffectState = "ambiguous"
	EffectReconciliationRequired EffectState = "reconciliation_required"
	EffectDenied                 EffectState = "denied"
	EffectFailed                 EffectState = "failed"
	EffectTimedOut               EffectState = "timed_out"
)

type TraceHeader struct {
	SchemaVersion   string `json:"schema_version"`
	TraceID         string `json:"trace_id"`
	SourceCommit    string `json:"source_commit"`
	RootExecutionID string `json:"root_execution_id"`
	HeaderSHA256    string `json:"header_sha256"`
}

type TraceStartedPayload struct {
	Status string `json:"status"`
}

type TraceEndedPayload struct {
	EvidenceComplete bool   `json:"evidence_complete"`
	Status           string `json:"status"`
}

type AuthoritySnapshotPayload struct {
	RunID                string `json:"run_id"`
	CapabilityPlanSHA256 string `json:"capability_plan_sha256"`
	PolicySHA256         string `json:"policy_sha256"`
	FreshnessSHA256      string `json:"freshness_sha256"`
	GrantsSHA256         string `json:"grants_sha256"`
}

type EffectTransitionPayload struct {
	CallID               string      `json:"call_id"`
	State                EffectState `json:"state"`
	ReceiptID            string      `json:"receipt_id,omitempty"`
	Compensator          string      `json:"compensator,omitempty"`
	ReconciliationReason string      `json:"reconciliation_reason,omitempty"`
}

type ToolDecisionPayload struct {
	ApprovalDisposition  string `json:"approval_disposition"`
	ApprovalRequestID    string `json:"approval_request_id,omitempty"`
	ArgumentsSHA256      string `json:"arguments_sha256"`
	BrokerOutcome        string `json:"broker_outcome"`
	CallID               string `json:"call_id"`
	Capability           string `json:"capability"`
	CapabilityPlanSHA256 string `json:"capability_plan_sha256"`
	Mechanism            string `json:"mechanism"`
	OperationIndex       uint32 `json:"operation_index"`
	ParentCallID         string `json:"parent_call_id,omitempty"`
	ReceiptID            string `json:"receipt_id"`
	ResultSHA256         string `json:"result_sha256,omitempty"`
	RunID                string `json:"run_id"`
}

type WorkspaceTerminalPayload struct {
	BaseWorkspaceSHA256   string `json:"base_workspace_sha256"`
	ResultWorkspaceSHA256 string `json:"result_workspace_sha256,omitempty"`
	Disposition           string `json:"disposition"`
}

type ExecutionAttemptPayload struct {
	RunID               string `json:"run_id"`
	AttemptID           string `json:"attempt_id"`
	PreparedImageSHA256 string `json:"prepared_image_sha256,omitempty"`
	Status              string `json:"status"`
}

type ModelContextPayload struct {
	ContextSHA256 string       `json:"context_sha256"`
	BriefSHA256   string       `json:"brief_sha256"`
	Availability  Availability `json:"availability"`
}

type ModelOutputPayload struct {
	Availability Availability `json:"availability"`
	OutputSHA256 string       `json:"output_sha256,omitempty"`
}

type ClaimLevel string

const ClaimSourceBound ClaimLevel = "source_bound"

type SourceDocumentPayload struct {
	DocumentID   string       `json:"document_id"`
	SourceSHA256 string       `json:"source_sha256"`
	Availability Availability `json:"availability"`
}

type SourceBodyPayload struct {
	DocumentID   string       `json:"document_id"`
	SourceSHA256 string       `json:"source_sha256"`
	DisplayPath  string       `json:"display_path"`
	Availability Availability `json:"availability"`
}

type SourceOccurrencePayload struct {
	DocumentID        string `json:"document_id"`
	SourceSHA256      string `json:"source_sha256"`
	OccurrenceID      string `json:"occurrence_id"`
	StartLine         uint32 `json:"start_line"`
	StartColumn       uint32 `json:"start_column"`
	EndLine           uint32 `json:"end_line"`
	EndColumn         uint32 `json:"end_column"`
	Capability        string `json:"capability"`
	DynamicOccurrence uint32 `json:"dynamic_occurrence"`
}

type SourceDecisionPayload struct {
	DecisionID           string     `json:"decision_id"`
	CapabilityPlanSHA256 string     `json:"capability_plan_sha256"`
	OccurrenceID         string     `json:"occurrence_id"`
	DynamicOccurrence    uint32     `json:"dynamic_occurrence"`
	ClaimLevel           ClaimLevel `json:"claim_level"`
	Admitted             bool       `json:"admitted"`
	Reasons              []string   `json:"reasons,omitempty"`
	ReceiptID            string     `json:"receipt_id,omitempty"`
}

type ExecutedLinePayload struct {
	SourceSHA256      string       `json:"source_sha256"`
	Availability      Availability `json:"availability"`
	Instrumentation   string       `json:"instrumentation,omitempty"`
	InstructionOffset uint32       `json:"instruction_offset,omitempty"`
	StartLine         uint32       `json:"start_line,omitempty"`
	StartColumn       uint32       `json:"start_column,omitempty"`
	EndLine           uint32       `json:"end_line,omitempty"`
	EndColumn         uint32       `json:"end_column,omitempty"`
}

type SubagentContextPayload struct {
	ChildID       string       `json:"child_id"`
	ContextSHA256 string       `json:"context_sha256"`
	BriefSHA256   string       `json:"brief_sha256"`
	Availability  Availability `json:"availability"`
}

type SubagentRuntimePayload struct {
	BriefSHA256               string `json:"brief_sha256"`
	ChildID                   string `json:"child_id"`
	ChildPlanSHA256           string `json:"child_plan_sha256"`
	ContextSHA256             string `json:"context_sha256"`
	Depth                     uint32 `json:"depth"`
	DescriptorSHA256          string `json:"descriptor_sha256"`
	ExecutionProfileSHA256    string `json:"execution_profile_sha256"`
	FreshRunID                string `json:"fresh_run_id"`
	InputsSHA256              string `json:"inputs_sha256"`
	ParentLineageSHA256       string `json:"parent_lineage_sha256"`
	ParentLiveStateInherited  bool   `json:"parent_live_state_inherited"`
	ParentStreamEpoch         string `json:"parent_stream_epoch"`
	ParentWorkspaceRootSHA256 string `json:"parent_workspace_root_sha256"`
	PreparedImageSHA256       string `json:"prepared_image_sha256"`
	PrivacyPartition          string `json:"privacy_partition"`
	SourceOccurrence          string `json:"source_occurrence"`
	SourceSHA256              string `json:"source_sha256"`
}

type SubagentWorkspacePayload struct {
	ChildID          string `json:"child_id"`
	BaseRootSHA256   string `json:"base_root_sha256"`
	ResultRootSHA256 string `json:"result_root_sha256"`
	WorkspaceSHA256  string `json:"workspace_sha256"`
	Depth            uint32 `json:"depth"`
	ChangedEntries   uint32 `json:"changed_entries"`
	ChangedBytes     uint64 `json:"changed_bytes"`
	Disposition      string `json:"disposition"`
}

type WorkspaceFilePayload struct {
	WorkspaceSHA256 string       `json:"workspace_sha256"`
	Path            string       `json:"path"`
	ContentSHA256   string       `json:"content_sha256"`
	Availability    Availability `json:"availability"`
}

type TruncationPayload struct {
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	DroppedEvents uint64 `json:"dropped_events"`
}

type RuntimeObservationPayload struct {
	ObservationType   string  `json:"observation_type"`
	Sequence          uint32  `json:"sequence"`
	ParentSequence    *uint32 `json:"parent_sequence,omitempty"`
	ObservationSHA256 string  `json:"observation_sha256"`
}

type ResourceSamplePayload struct {
	Scope                  string       `json:"scope"`
	WallNanos              uint64       `json:"wall_nanos"`
	ProcessCPUNanos        uint64       `json:"process_cpu_nanos"`
	ProcessCPUAvailability Availability `json:"process_cpu_availability"`
	PeakRSSBytes           uint64       `json:"peak_rss_bytes"`
	PeakRSSAvailability    Availability `json:"peak_rss_availability"`
}

type EvidenceLimits struct {
	MaxEvents       uint32
	MaxParents      uint32
	MaxPayloadBytes uint32
}

func (limits EvidenceLimits) normalized() (EvidenceLimits, error) {
	if limits.MaxEvents == 0 {
		limits.MaxEvents = 4096
	}
	if limits.MaxParents == 0 {
		limits.MaxParents = 32
	}
	if limits.MaxPayloadBytes == 0 {
		limits.MaxPayloadBytes = 64 << 10
	}
	if limits.MaxEvents >= MaxEvidenceEvents || limits.MaxParents > 256 || limits.MaxPayloadBytes > 1<<20 {
		return EvidenceLimits{}, errors.New("invalid causal evidence limits")
	}
	return limits, nil
}

type EvidenceInput struct {
	Type           EvidenceType
	ActorID        string
	OccurredNanos  uint64
	ParentEventIDs []string
	Payload        any
	Body           *labstore.Ref
}

type EvidenceEvent struct {
	SchemaVersion  string          `json:"schema_version"`
	Ordinal        uint64          `json:"ordinal"`
	EventID        string          `json:"event_id"`
	OccurredNanos  uint64          `json:"occurred_nanos"`
	Type           EvidenceType    `json:"type"`
	ActorID        string          `json:"actor_id"`
	ParentEventIDs []string        `json:"parent_event_ids,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Body           *labstore.Ref   `json:"body,omitempty"`
}

type Export struct {
	SchemaVersion string           `json:"schema_version"`
	Profile       Profile          `json:"profile"`
	Privacy       labstore.Privacy `json:"privacy"`
	TraceID       string           `json:"trace_id"`
	HeaderSHA256  string           `json:"header_sha256"`
	Header        TraceHeader      `json:"header"`
	Events        []EvidenceEvent  `json:"events"`
	SealSHA256    string           `json:"seal_sha256"`
}

type Builder struct {
	header    TraceHeader
	events    []EvidenceEvent
	store     *labstore.Store
	limits    EvidenceLimits
	overflow  bool
	truncated bool
	ended     bool
}

func NewBuilder(header TraceHeader) (*Builder, error) {
	return NewBoundedBuilder(header, nil, EvidenceLimits{})
}

func NewStoredBuilder(header TraceHeader, store *labstore.Store) (*Builder, error) {
	return NewBoundedBuilder(header, store, EvidenceLimits{})
}

func NewBoundedBuilder(header TraceHeader, store *labstore.Store, limits EvidenceLimits) (*Builder, error) {
	limits, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	header.SchemaVersion = EvidenceSchemaVersion
	header.HeaderSHA256 = ""
	if !validEvidenceHeader(header) {
		return nil, errors.New("invalid causal evidence header")
	}
	header.HeaderSHA256 = evidenceHash("header", header)
	return &Builder{header: header, store: store, limits: limits}, nil
}

func (builder *Builder) Append(input EvidenceInput) (EvidenceEvent, error) {
	if builder == nil || builder.ended || len(builder.events) >= MaxEvidenceEvents || !input.Type.valid() || !evidenceIdentifier.MatchString(input.ActorID) {
		return EvidenceEvent{}, errors.New("invalid causal evidence event")
	}
	terminalException := input.Type == EventEvidenceTruncated || input.Type == EventTraceEnded
	if builder.truncated && !terminalException {
		return EvidenceEvent{}, errors.New("causal evidence is terminally truncated")
	}
	if uint32(len(builder.events)) >= builder.limits.MaxEvents && !terminalException {
		builder.overflow = true
		return EvidenceEvent{}, errors.New("causal evidence event limit exceeded")
	}
	if uint32(len(input.ParentEventIDs)) > builder.limits.MaxParents {
		return EvidenceEvent{}, errors.New("causal evidence parent limit exceeded")
	}
	prior := make(map[string]EvidenceEvent, len(builder.events))
	for _, event := range builder.events {
		prior[event.EventID] = event
	}
	parents := append([]string(nil), input.ParentEventIDs...)
	sort.Strings(parents)
	for index, parent := range parents {
		if _, ok := prior[parent]; !ok || (index > 0 && parents[index-1] == parent) {
			return EvidenceEvent{}, errors.New("invalid causal evidence parent")
		}
	}
	payload, err := encodeEvidencePayload(input.Type, input.Payload)
	if err != nil || uint32(len(payload)) > builder.limits.MaxPayloadBytes {
		return EvidenceEvent{}, errors.New("causal evidence payload limit exceeded")
	}
	if err := validateEvidenceRelations(input.Type, input.Payload, parents, prior); err != nil {
		return EvidenceEvent{}, err
	}
	if (input.Type == EventModelBody || input.Type == EventSourceBody || input.Type == EventWorkspaceFile) && input.Body == nil {
		return EvidenceEvent{}, errors.New("private body evidence requires a body reference")
	}
	if input.Body != nil {
		bodyAllowed := input.Type == EventModelBody || input.Type == EventSourceBody || input.Type == EventWorkspaceFile || input.Type == EventRuntimeObservation
		if input.Type.productionEligible() || !bodyAllowed || builder.store == nil || input.Body.Kind == "" || input.Body.SHA256 == "" {
			return EvidenceEvent{}, errors.New("invalid causal evidence body")
		}
		object, err := builder.store.Get(*input.Body)
		if err != nil {
			return EvidenceEvent{}, errors.New("unresolved causal evidence body")
		}
		if object.Privacy != labstore.PrivacyPrivate {
			return EvidenceEvent{}, errors.New("causal evidence body is not private")
		}
		if err := validateEvidenceBodyBinding(input.Type, payload, object); err != nil {
			return EvidenceEvent{}, err
		}
	}
	event := EvidenceEvent{
		SchemaVersion: EvidenceSchemaVersion, Ordinal: uint64(len(builder.events) + 1),
		OccurredNanos: input.OccurredNanos, Type: input.Type, ActorID: input.ActorID,
		ParentEventIDs: parents, Payload: payload, Body: cloneEvidenceRef(input.Body),
	}
	event.EventID = evidenceEventID(builder.header.HeaderSHA256, event)
	builder.events = append(builder.events, cloneEvidenceEvent(event))
	if input.Type == EventTraceEnded {
		builder.ended = true
	}
	return cloneEvidenceEvent(event), nil
}

func (builder *Builder) MarkTruncated(actorID string, payload TruncationPayload) (EvidenceEvent, error) {
	if builder == nil || builder.truncated {
		return EvidenceEvent{}, errors.New("invalid causal evidence truncation")
	}
	builder.truncated = true
	event, err := builder.Append(EvidenceInput{Type: EventEvidenceTruncated, ActorID: actorID, Payload: payload})
	if err != nil {
		builder.truncated = false
		return EvidenceEvent{}, err
	}
	return event, nil
}

func (builder *Builder) Export(profile Profile, privacy labstore.Privacy) (Export, error) {
	if builder == nil || !builder.ended || (builder.overflow && !builder.truncated) || !profile.valid() ||
		(privacy != labstore.PrivacyPrivate && privacy != labstore.PrivacyPortable) ||
		(profile == ProfileProductionRollback && privacy != labstore.PrivacyPortable) {
		return Export{}, errors.New("invalid causal evidence export profile")
	}
	exported := Export{
		SchemaVersion: EvidenceSchemaVersion, Profile: profile, Privacy: privacy,
		TraceID: builder.header.TraceID, HeaderSHA256: builder.header.HeaderSHA256,
		Header: builder.header,
	}
	for _, event := range builder.events {
		if profile == ProfileProductionRollback && !event.Type.productionEligible() {
			continue
		}
		if privacy == labstore.PrivacyPortable && event.Body != nil {
			continue
		}
		if profile == ProfileProductionRollback && event.Body != nil {
			return Export{}, errors.New("production evidence contains a body")
		}
		exported.Events = append(exported.Events, cloneEvidenceEvent(event))
	}
	exported.SealSHA256 = evidenceExportSeal(exported)
	if privacy == labstore.PrivacyPrivate {
		if err := ValidateEvidenceExportWithStore(exported, builder.store); err != nil {
			return Export{}, err
		}
	} else if err := ValidateEvidenceExport(exported); err != nil {
		return Export{}, err
	}
	return exported, nil
}

func EncodeEvidenceExport(exported Export) ([]byte, error) {
	return encodeEvidenceExport(exported, nil)
}

func EncodeEvidenceExportWithStore(exported Export, store *labstore.Store) ([]byte, error) {
	return encodeEvidenceExport(exported, store)
}

func encodeEvidenceExport(exported Export, store *labstore.Store) ([]byte, error) {
	if err := validateEvidenceExport(exported, store); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(exported)
	if err != nil || len(encoded) > 16<<20 {
		return nil, errors.New("encode causal evidence export")
	}
	return encoded, nil
}

func DecodeEvidenceExport(raw []byte) (Export, error) {
	return decodeEvidenceExport(raw, nil)
}

func DecodeEvidenceExportWithStore(raw []byte, store *labstore.Store) (Export, error) {
	return decodeEvidenceExport(raw, store)
}

func decodeEvidenceExport(raw []byte, store *labstore.Store) (Export, error) {
	if len(raw) == 0 || len(raw) > 16<<20 {
		return Export{}, errors.New("invalid causal evidence export")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var exported Export
	if decoder.Decode(&exported) != nil || decoder.Decode(&struct{}{}) == nil {
		return Export{}, errors.New("invalid causal evidence export")
	}
	if err := validateEvidenceExport(exported, store); err != nil {
		return Export{}, err
	}
	canonical, err := json.Marshal(exported)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Export{}, errors.New("noncanonical causal evidence export")
	}
	return exported, nil
}

func ValidateEvidenceExport(exported Export) error {
	return validateEvidenceExport(exported, nil)
}

func ValidateEvidenceExportWithStore(exported Export, store *labstore.Store) error {
	return validateEvidenceExport(exported, store)
}

func validateEvidenceExport(exported Export, store *labstore.Store) error {
	if exported.SchemaVersion != EvidenceSchemaVersion || !exported.Profile.valid() ||
		exported.TraceID != exported.Header.TraceID || exported.HeaderSHA256 != exported.Header.HeaderSHA256 ||
		!validEvidenceHeaderWithSeal(exported.Header) || exported.Header.HeaderSHA256 != evidenceHeaderIdentity(exported.Header) ||
		(exported.Privacy != labstore.PrivacyPrivate && exported.Privacy != labstore.PrivacyPortable) ||
		(exported.Profile == ProfileProductionRollback && exported.Privacy != labstore.PrivacyPortable) ||
		len(exported.Events) == 0 || len(exported.Events) >= MaxEvidenceEvents || exported.SealSHA256 != evidenceExportSeal(exported) {
		return errors.New("invalid causal evidence export")
	}
	prior := make(map[string]EvidenceEvent, len(exported.Events))
	var lastOrdinal uint64
	var truncated bool
	var terminalComplete bool
	var traceStarted uint32
	var traceEnded uint32
	effectStates := make(map[string]EffectState)
	for eventIndex, event := range exported.Events {
		if event.SchemaVersion != EvidenceSchemaVersion || event.Ordinal <= lastOrdinal || !event.Type.valid() ||
			len(event.ParentEventIDs) > 256 || len(event.Payload) == 0 || len(event.Payload) > 1<<20 ||
			(truncated && event.Type != EventTraceEnded) ||
			!evidenceIdentifier.MatchString(event.ActorID) || event.EventID != evidenceEventID(exported.HeaderSHA256, event) {
			return errors.New("invalid causal evidence event")
		}
		if (exported.Profile == ProfileProductionRollback && (!event.Type.productionEligible() || event.Body != nil)) ||
			(exported.Privacy == labstore.PrivacyPortable && event.Body != nil) {
			return errors.New("portable evidence leaked private telemetry")
		}
		if event.Type == EventModelBody || event.Type == EventSourceBody || event.Type == EventWorkspaceFile {
			if event.Body == nil {
				return errors.New("private body evidence is missing its body")
			}
		}
		if event.Body != nil {
			if store == nil {
				return errors.New("private causal evidence body is unresolved")
			}
			object, err := store.Get(*event.Body)
			if err != nil || object.Privacy != labstore.PrivacyPrivate || validateEvidenceBodyBinding(event.Type, event.Payload, object) != nil {
				return errors.New("private causal evidence body binding is invalid")
			}
		}
		parents := append([]string(nil), event.ParentEventIDs...)
		if !sort.StringsAreSorted(parents) {
			return errors.New("noncanonical causal evidence parents")
		}
		for index, parent := range parents {
			if _, ok := prior[parent]; !ok || (index > 0 && parents[index-1] == parent) {
				return errors.New("invalid causal evidence parent")
			}
		}
		decoded, err := decodeEvidencePayload(event.Type, event.Payload)
		if err != nil {
			return err
		}
		if event.Type == EventTraceStarted {
			traceStarted++
		}
		if event.Type == EventTraceEnded {
			traceEnded++
		}
		if event.Type == EventEvidenceTruncated {
			if eventIndex != len(exported.Events)-2 {
				return errors.New("truncation evidence is not terminal")
			}
			truncated = true
		}
		if value, ok := decoded.(*TraceEndedPayload); ok {
			terminalComplete = value.EvidenceComplete
		}
		switch value := decoded.(type) {
		case *ExecutionAttemptPayload:
			if value.RunID != exported.Header.RootExecutionID {
				return errors.New("execution attempt is not bound to root execution")
			}
		case *AuthoritySnapshotPayload:
			if value.RunID != exported.Header.RootExecutionID {
				return errors.New("authority snapshot is not bound to root execution")
			}
		case *EffectTransitionPayload:
			effectStates[value.CallID] = value.State
		}
		if err := validateEvidenceRelations(event.Type, dereferenceEvidencePayload(decoded), event.ParentEventIDs, prior); err != nil {
			return err
		}
		prior[event.EventID] = event
		lastOrdinal = event.Ordinal
	}
	if traceStarted != 1 || traceEnded != 1 || exported.Events[len(exported.Events)-1].Type != EventTraceEnded {
		return errors.New("invalid causal evidence lifecycle")
	}
	if truncated && terminalComplete {
		return errors.New("truncated evidence claimed complete")
	}
	if terminalComplete {
		for _, state := range effectStates {
			if state == EffectIntent || state == EffectStarted || state == EffectAmbiguous {
				return errors.New("complete evidence has unterminated effect")
			}
		}
	}
	return nil
}

func validEvidenceHeader(header TraceHeader) bool {
	return header.SchemaVersion == EvidenceSchemaVersion && evidenceIdentifier.MatchString(header.TraceID) &&
		evidenceIdentifier.MatchString(header.RootExecutionID) && commitPattern.MatchString(header.SourceCommit) && header.HeaderSHA256 == ""
}

func validEvidenceHeaderWithSeal(header TraceHeader) bool {
	claimed := header.HeaderSHA256
	header.HeaderSHA256 = ""
	return validEvidenceHeader(header) && digestPattern.MatchString(claimed)
}

func evidenceHeaderIdentity(header TraceHeader) string {
	header.HeaderSHA256 = ""
	return evidenceHash("header", header)
}

func encodeEvidencePayload(kind EvidenceType, payload any) (json.RawMessage, error) {
	if err := validateTypedEvidencePayload(kind, payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("invalid causal evidence payload")
	}
	return encoded, nil
}

func decodeEvidencePayload(kind EvidenceType, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("invalid causal evidence payload")
	}
	var target any
	switch kind {
	case EventTraceStarted:
		target = &TraceStartedPayload{}
	case EventTraceEnded:
		target = &TraceEndedPayload{}
	case EventAuthoritySnapshot:
		target = &AuthoritySnapshotPayload{}
	case EventEffectTransition:
		target = &EffectTransitionPayload{}
	case EventToolDecision:
		target = &ToolDecisionPayload{}
	case EventWorkspaceTerminal:
		target = &WorkspaceTerminalPayload{}
	case EventExecutionAttempt:
		target = &ExecutionAttemptPayload{}
	case EventModelContext, EventModelBody:
		target = &ModelContextPayload{}
	case EventModelOutput:
		target = &ModelOutputPayload{}
	case EventSourceDocument:
		target = &SourceDocumentPayload{}
	case EventSourceBody:
		target = &SourceBodyPayload{}
	case EventSourceOccurrence:
		target = &SourceOccurrencePayload{}
	case EventSourceDecision:
		target = &SourceDecisionPayload{}
	case EventExecutedLine:
		target = &ExecutedLinePayload{}
	case EventSubagentContext:
		target = &SubagentContextPayload{}
	case EventSubagentRuntime:
		target = &SubagentRuntimePayload{}
	case EventSubagentWorkspace:
		target = &SubagentWorkspacePayload{}
	case EventWorkspaceFile:
		target = &WorkspaceFilePayload{}
	case EventEvidenceTruncated:
		target = &TruncationPayload{}
	case EventRuntimeObservation:
		target = &RuntimeObservationPayload{}
	case EventResourceSample:
		target = &ResourceSamplePayload{}
	default:
		return nil, errors.New("invalid causal evidence payload")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) == nil {
		return nil, errors.New("invalid causal evidence payload")
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, raw) || validateTypedEvidencePayload(kind, dereferenceEvidencePayload(target)) != nil {
		return nil, errors.New("invalid causal evidence payload")
	}
	return target, nil
}

func dereferenceEvidencePayload(value any) any {
	switch typed := value.(type) {
	case *TraceStartedPayload:
		return *typed
	case *TraceEndedPayload:
		return *typed
	case *AuthoritySnapshotPayload:
		return *typed
	case *EffectTransitionPayload:
		return *typed
	case *ToolDecisionPayload:
		return *typed
	case *WorkspaceTerminalPayload:
		return *typed
	case *ExecutionAttemptPayload:
		return *typed
	case *ModelContextPayload:
		return *typed
	case *ModelOutputPayload:
		return *typed
	case *SourceDocumentPayload:
		return *typed
	case *SourceBodyPayload:
		return *typed
	case *SourceOccurrencePayload:
		return *typed
	case *SourceDecisionPayload:
		return *typed
	case *ExecutedLinePayload:
		return *typed
	case *SubagentContextPayload:
		return *typed
	case *SubagentRuntimePayload:
		return *typed
	case *SubagentWorkspacePayload:
		return *typed
	case *WorkspaceFilePayload:
		return *typed
	case *TruncationPayload:
		return *typed
	case *RuntimeObservationPayload:
		return *typed
	case *ResourceSamplePayload:
		return *typed
	default:
		return nil
	}
}

func validateTypedEvidencePayload(kind EvidenceType, payload any) error {
	validDigest := func(value string) bool { return digestPattern.MatchString(value) }
	switch value := payload.(type) {
	case TraceStartedPayload:
		if kind != EventTraceStarted || value.Status != "running" {
			return errors.New("invalid trace start payload")
		}
	case TraceEndedPayload:
		if kind != EventTraceEnded || (value.Status != "completed" && value.Status != "failed") {
			return errors.New("invalid trace end payload")
		}
	case AuthoritySnapshotPayload:
		if kind != EventAuthoritySnapshot || !evidenceIdentifier.MatchString(value.RunID) || !validDigest(value.CapabilityPlanSHA256) ||
			!validDigest(value.PolicySHA256) || !validDigest(value.FreshnessSHA256) || !validDigest(value.GrantsSHA256) {
			return errors.New("invalid authority snapshot payload")
		}
	case EffectTransitionPayload:
		if kind != EventEffectTransition || !evidenceIdentifier.MatchString(value.CallID) || !validEffectTransition(value) {
			return errors.New("invalid effect transition payload")
		}
	case ToolDecisionPayload:
		if kind != EventToolDecision || !evidenceIdentifier.MatchString(value.CallID) || !evidenceIdentifier.MatchString(value.RunID) || !validDigest(value.ArgumentsSHA256) ||
			!evidenceIdentifier.MatchString(value.Capability) || !validDigest(value.CapabilityPlanSHA256) || !evidenceIdentifier.MatchString(value.ReceiptID) ||
			(value.ApprovalRequestID != "" && !evidenceIdentifier.MatchString(value.ApprovalRequestID)) || (value.ParentCallID != "" && !evidenceIdentifier.MatchString(value.ParentCallID)) ||
			(value.Mechanism != "direct" && value.Mechanism != "programmatic") ||
			(value.BrokerOutcome != "ok" && value.BrokerOutcome != "denied" && value.BrokerOutcome != "error" && value.BrokerOutcome != "timeout" && value.BrokerOutcome != "ambiguous") ||
			(value.ApprovalDisposition != "not_required" && value.ApprovalDisposition != "approved" && value.ApprovalDisposition != "denied" && value.ApprovalDisposition != "not_recorded") ||
			(value.BrokerOutcome == "ok" && !validDigest(value.ResultSHA256)) || (value.BrokerOutcome != "ok" && value.ResultSHA256 != "") {
			return errors.New("invalid tool decision payload")
		}
	case WorkspaceTerminalPayload:
		if kind != EventWorkspaceTerminal || !validDigest(value.BaseWorkspaceSHA256) ||
			(value.ResultWorkspaceSHA256 != "" && !validDigest(value.ResultWorkspaceSHA256)) ||
			(value.Disposition != "published" && value.Disposition != "discarded" && value.Disposition != "unchanged" && value.Disposition != "conflict" && value.Disposition != "finalized") {
			return errors.New("invalid workspace terminal payload")
		}
	case ExecutionAttemptPayload:
		if kind != EventExecutionAttempt || !evidenceIdentifier.MatchString(value.RunID) || !evidenceIdentifier.MatchString(value.AttemptID) ||
			(value.PreparedImageSHA256 != "" && !validDigest(value.PreparedImageSHA256)) ||
			(value.Status != "started" && value.Status != "completed" && value.Status != "failed") {
			return errors.New("invalid execution attempt payload")
		}
	case ModelContextPayload:
		if (kind != EventModelContext && kind != EventModelBody) || !validDigest(value.ContextSHA256) || !validDigest(value.BriefSHA256) || !value.Availability.valid() {
			return errors.New("invalid model context payload")
		}
	case ModelOutputPayload:
		if kind != EventModelOutput || !value.Availability.valid() ||
			(value.Availability == Available && !validDigest(value.OutputSHA256)) || (value.Availability != Available && value.OutputSHA256 != "") {
			return errors.New("invalid model output payload")
		}
	case SourceDocumentPayload:
		if kind != EventSourceDocument || !validDigest(value.DocumentID) || !validDigest(value.SourceSHA256) || !value.Availability.valid() {
			return errors.New("invalid source document payload")
		}
	case SourceBodyPayload:
		if kind != EventSourceBody || !validDigest(value.DocumentID) || !validDigest(value.SourceSHA256) || value.Availability != Available || !validPrivateEvidencePath(value.DisplayPath) {
			return errors.New("invalid source body payload")
		}
	case SourceOccurrencePayload:
		if kind != EventSourceOccurrence || !validDigest(value.DocumentID) || !validDigest(value.SourceSHA256) || !validDigest(value.OccurrenceID) ||
			value.StartLine == 0 || value.EndLine < value.StartLine || (value.EndLine == value.StartLine && value.EndColumn < value.StartColumn) ||
			!evidenceIdentifier.MatchString(value.Capability) || value.DynamicOccurrence == 0 {
			return errors.New("invalid source occurrence payload")
		}
	case SourceDecisionPayload:
		if kind != EventSourceDecision || !validDigest(value.DecisionID) || !validDigest(value.CapabilityPlanSHA256) || !validDigest(value.OccurrenceID) ||
			value.DynamicOccurrence == 0 || value.ClaimLevel != ClaimSourceBound || !stableReasons(value.Reasons) ||
			(value.Admitted && (len(value.Reasons) != 0 || !evidenceIdentifier.MatchString(value.ReceiptID))) ||
			(!value.Admitted && (len(value.Reasons) == 0 || (value.ReceiptID != "" && !evidenceIdentifier.MatchString(value.ReceiptID)))) {
			return errors.New("invalid source decision payload")
		}
	case ExecutedLinePayload:
		if kind != EventExecutedLine || !validDigest(value.SourceSHA256) || !value.Availability.valid() ||
			(value.Availability == Available && (value.Instrumentation != "sys.monitoring" || value.StartLine == 0 || value.EndLine < value.StartLine ||
				(value.EndLine == value.StartLine && value.EndColumn < value.StartColumn))) ||
			(value.Availability != Available && (value.Instrumentation != "" || value.InstructionOffset != 0 || value.StartLine != 0 || value.StartColumn != 0 || value.EndLine != 0 || value.EndColumn != 0)) {
			return errors.New("invalid executed line payload")
		}
	case SubagentContextPayload:
		if kind != EventSubagentContext || !evidenceIdentifier.MatchString(value.ChildID) || !validDigest(value.ContextSHA256) || !validDigest(value.BriefSHA256) || !value.Availability.valid() {
			return errors.New("invalid subagent context payload")
		}
	case SubagentRuntimePayload:
		descriptor := subagent.Descriptor{
			SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: value.ChildID, ParentStreamEpoch: value.ParentStreamEpoch,
			ParentLineageSHA256: value.ParentLineageSHA256, SourceOccurrence: value.SourceOccurrence, SourceSHA256: value.SourceSHA256,
			InputsSHA256: value.InputsSHA256, ArtifactSHA256: value.PreparedImageSHA256, ExecutionProfileSHA256: value.ExecutionProfileSHA256,
			ChildPlanSHA256: value.ChildPlanSHA256, PrivacyPartition: value.PrivacyPartition, Depth: value.Depth,
		}
		descriptorSHA256, _, descriptorErr := descriptor.Identity()
		if kind != EventSubagentRuntime || !evidenceIdentifier.MatchString(value.FreshRunID) || !validDigest(value.BriefSHA256) ||
			!validDigest(value.ContextSHA256) || !validDigest(value.ParentWorkspaceRootSHA256) || value.ParentLineageSHA256 != value.ParentWorkspaceRootSHA256 ||
			value.ParentLiveStateInherited || descriptorErr != nil || descriptorSHA256 != value.DescriptorSHA256 {
			return errors.New("invalid subagent runtime payload")
		}
	case SubagentWorkspacePayload:
		root := workspace.Root{
			SchemaVersion: workspace.RootSchemaVersion, IdentitySHA256: value.ResultRootSHA256, WorkspaceSHA256: value.WorkspaceSHA256,
			ParentIdentitySHA256: value.BaseRootSHA256, Depth: value.Depth, ChangedEntries: value.ChangedEntries, ChangedBytes: value.ChangedBytes,
		}
		document, rootErr := root.IdentityDocument()
		digest := sha256.Sum256(document)
		if kind != EventSubagentWorkspace || !evidenceIdentifier.MatchString(value.ChildID) || !validDigest(value.BaseRootSHA256) || !validDigest(value.WorkspaceSHA256) ||
			rootErr != nil || "sha256:"+hex.EncodeToString(digest[:]) != value.ResultRootSHA256 || value.Depth == 0 ||
			(value.Disposition != "selected" && value.Disposition != "discarded") {
			return errors.New("invalid subagent workspace payload")
		}
	case WorkspaceFilePayload:
		if kind != EventWorkspaceFile || !validDigest(value.WorkspaceSHA256) || !validDigest(value.ContentSHA256) || value.Availability != Available || !validPrivateEvidencePath(value.Path) {
			return errors.New("invalid workspace file payload")
		}
	case TruncationPayload:
		if kind != EventEvidenceTruncated || !evidenceIdentifier.MatchString(value.Scope) || !evidenceIdentifier.MatchString(value.Reason) || value.DroppedEvents == 0 {
			return errors.New("invalid evidence truncation payload")
		}
	case RuntimeObservationPayload:
		if kind != EventRuntimeObservation || !evidenceIdentifier.MatchString(value.ObservationType) || value.Sequence == 0 ||
			(value.ParentSequence != nil && (*value.ParentSequence == 0 || *value.ParentSequence >= value.Sequence)) || !validDigest(value.ObservationSHA256) {
			return errors.New("invalid runtime observation payload")
		}
	case ResourceSamplePayload:
		if kind != EventResourceSample || !evidenceIdentifier.MatchString(value.Scope) || value.WallNanos == 0 ||
			!value.ProcessCPUAvailability.valid() || !value.PeakRSSAvailability.valid() ||
			(value.ProcessCPUAvailability == Available && value.ProcessCPUNanos == 0) ||
			(value.ProcessCPUAvailability != Available && value.ProcessCPUNanos != 0) ||
			(value.PeakRSSAvailability == Available && value.PeakRSSBytes == 0) ||
			(value.PeakRSSAvailability != Available && value.PeakRSSBytes != 0) {
			return errors.New("invalid resource sample payload")
		}
	default:
		return errors.New("invalid causal evidence payload type")
	}
	return nil
}

func validateEvidenceRelations(kind EvidenceType, payload any, parents []string, prior map[string]EvidenceEvent) error {
	parentPayload := func(want EvidenceType) (any, error) {
		if len(parents) != 1 {
			return nil, errors.New("invalid typed causal relation")
		}
		parent, ok := prior[parents[0]]
		if !ok || parent.Type != want {
			return nil, errors.New("invalid typed causal relation")
		}
		return decodeEvidencePayload(parent.Type, parent.Payload)
	}
	switch value := payload.(type) {
	case EffectTransitionPayload:
		if len(parents) != 1 {
			return errors.New("effect transition requires one causal predecessor")
		}
		parentEvent, ok := prior[parents[0]]
		if !ok {
			return errors.New("effect transition predecessor unavailable")
		}
		if value.State == EffectIntent {
			if parentEvent.Type != EventAuthoritySnapshot {
				return errors.New("effect intent is not bound to Host authority")
			}
			break
		}
		if value.State == EffectDenied && parentEvent.Type == EventAuthoritySnapshot {
			break
		}
		if parentEvent.Type != EventEffectTransition {
			return errors.New("effect transition is not bound to its lifecycle")
		}
		decoded, err := decodeEvidencePayload(parentEvent.Type, parentEvent.Payload)
		priorEffect, ok := decoded.(*EffectTransitionPayload)
		if err != nil || !ok || priorEffect.CallID != value.CallID {
			return errors.New("effect transition call identity mismatch")
		}
		validPrior := false
		switch value.State {
		case EffectStarted:
			validPrior = priorEffect.State == EffectIntent
		case EffectCommitted:
			validPrior = priorEffect.State == EffectIntent || priorEffect.State == EffectStarted
		case EffectFailed, EffectTimedOut:
			validPrior = priorEffect.State == EffectIntent || priorEffect.State == EffectStarted
		case EffectAmbiguous:
			validPrior = priorEffect.State == EffectStarted
		case EffectDenied:
			validPrior = priorEffect.State == EffectIntent
		case EffectReconciliationRequired:
			validPrior = priorEffect.State == EffectIntent || priorEffect.State == EffectStarted || priorEffect.State == EffectFailed || priorEffect.State == EffectTimedOut || priorEffect.State == EffectAmbiguous
		case EffectCompensated:
			validPrior = priorEffect.State == EffectCommitted
		case EffectCleanupOnly:
			validPrior = priorEffect.State == EffectStarted || priorEffect.State == EffectFailed || priorEffect.State == EffectTimedOut
		}
		if !validPrior {
			return errors.New("invalid effect lifecycle transition")
		}
	case ToolDecisionPayload:
		var effect *EffectTransitionPayload
		var authority *AuthoritySnapshotPayload
		for _, parentID := range parents {
			parent := prior[parentID]
			decoded, err := decodeEvidencePayload(parent.Type, parent.Payload)
			if err != nil {
				return errors.New("invalid tool decision parent")
			}
			switch typed := decoded.(type) {
			case *EffectTransitionPayload:
				effect = typed
			case *AuthoritySnapshotPayload:
				authority = typed
			default:
				return errors.New("invalid tool decision parent")
			}
		}
		if len(parents) != 2 || effect == nil || authority == nil || effect.CallID != value.CallID || effect.ReceiptID != value.ReceiptID || authority.RunID != value.RunID || authority.CapabilityPlanSHA256 != value.CapabilityPlanSHA256 {
			return errors.New("tool decision authority/effect identity mismatch")
		}
	case SourceBodyPayload:
		parent, err := parentPayload(EventSourceDocument)
		document, ok := parent.(*SourceDocumentPayload)
		if err != nil || !ok || document.DocumentID != value.DocumentID || document.SourceSHA256 != value.SourceSHA256 {
			return errors.New("source body is not bound to its document")
		}
	case SourceOccurrencePayload:
		parent, err := parentPayload(EventSourceDocument)
		document, ok := parent.(*SourceDocumentPayload)
		if err != nil || !ok || document.DocumentID != value.DocumentID || document.SourceSHA256 != value.SourceSHA256 {
			return errors.New("source occurrence is not bound to its document")
		}
	case SourceDecisionPayload:
		var occurrence *SourceOccurrencePayload
		var effect *EffectTransitionPayload
		var tool *ToolDecisionPayload
		var authority *AuthoritySnapshotPayload
		for _, parentID := range parents {
			parent := prior[parentID]
			decoded, err := decodeEvidencePayload(parent.Type, parent.Payload)
			if err != nil {
				return errors.New("invalid source decision parent")
			}
			switch typed := decoded.(type) {
			case *SourceOccurrencePayload:
				if occurrence != nil {
					return errors.New("duplicate source occurrence parent")
				}
				occurrence = typed
			case *EffectTransitionPayload:
				if effect != nil {
					return errors.New("duplicate effect receipt parent")
				}
				effect = typed
			case *ToolDecisionPayload:
				if tool != nil {
					return errors.New("duplicate tool decision parent")
				}
				tool = typed
			case *AuthoritySnapshotPayload:
				if authority != nil {
					return errors.New("duplicate authority parent")
				}
				authority = typed
			default:
				return errors.New("invalid source decision parent")
			}
		}
		if occurrence == nil || occurrence.OccurrenceID != value.OccurrenceID || occurrence.DynamicOccurrence != value.DynamicOccurrence {
			return errors.New("source decision occurrence identity mismatch")
		}
		if value.ReceiptID == "" {
			if value.Admitted || len(parents) != 2 || authority == nil || effect != nil || tool != nil || authority.CapabilityPlanSHA256 != value.CapabilityPlanSHA256 {
				return errors.New("rejected source decision is not bound to authority")
			}
		} else {
			if len(parents) != 3 || authority != nil || effect == nil || tool == nil || occurrence.Capability != tool.Capability || value.CapabilityPlanSHA256 != tool.CapabilityPlanSHA256 || value.ReceiptID != effect.ReceiptID || value.ReceiptID != tool.ReceiptID {
				return errors.New("source decision authority/tool/occurrence identity mismatch")
			}
			projectedReceipt := receipt.Receipt{
				ReceiptID: value.ReceiptID, RunID: tool.RunID, CapabilityPlanSHA256: tool.CapabilityPlanSHA256,
				CallID: tool.CallID, ParentCallID: tool.ParentCallID, ApprovalRequestID: tool.ApprovalRequestID,
				Capability: tool.Capability, OperationIndex: tool.OperationIndex,
				RequestSHA256: strings.TrimPrefix(tool.ArgumentsSHA256, "sha256:"), Outcome: tool.BrokerOutcome,
			}
			if tool.ResultSHA256 != "" {
				projectedReceipt.ResponseSHA256 = strings.TrimPrefix(tool.ResultSHA256, "sha256:")
			}
			projectedReceipt.Source = &receipt.SourceBinding{
				SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound,
				DocumentID: occurrence.DocumentID, SourceSHA256: occurrence.SourceSHA256, OccurrenceID: occurrence.OccurrenceID,
				Capability: occurrence.Capability, DynamicOccurrence: occurrence.DynamicOccurrence,
				StartLine: occurrence.StartLine, StartColumn: occurrence.StartColumn, EndLine: occurrence.EndLine, EndColumn: occurrence.EndColumn,
			}
			if !receipt.ValidIdentity(projectedReceipt) {
				return errors.New("source decision receipt identity mismatch")
			}
			if value.Admitted {
				if effect.State != EffectCommitted || tool.BrokerOutcome != "ok" {
					return errors.New("admitted source decision is not bound to committed tool receipt")
				}
			} else if effect.State != EffectDenied || tool.BrokerOutcome != "denied" {
				return errors.New("rejected source decision is not bound to denied tool receipt")
			}
		}
	case SubagentRuntimePayload:
		var childContext *SubagentContextPayload
		var source *SourceDocumentPayload
		for _, parentID := range parents {
			parent := prior[parentID]
			decoded, err := decodeEvidencePayload(parent.Type, parent.Payload)
			if err != nil {
				return errors.New("invalid subagent runtime parent")
			}
			switch typed := decoded.(type) {
			case *SubagentContextPayload:
				childContext = typed
			case *SourceDocumentPayload:
				source = typed
			default:
				return errors.New("invalid subagent runtime parent")
			}
		}
		if len(parents) != 2 || childContext == nil || source == nil || childContext.ChildID != value.ChildID || childContext.ContextSHA256 != value.ContextSHA256 || childContext.BriefSHA256 != value.BriefSHA256 || childContext.Availability != Available || source.SourceSHA256 != value.SourceSHA256 || source.Availability != Available {
			return errors.New("subagent runtime context/source identity mismatch")
		}
	case SubagentWorkspacePayload:
		parent, err := parentPayload(EventSubagentRuntime)
		runtime, ok := parent.(*SubagentRuntimePayload)
		if err != nil || !ok || runtime.ChildID != value.ChildID || runtime.ParentWorkspaceRootSHA256 != value.BaseRootSHA256 {
			return errors.New("subagent workspace is not bound to child runtime/base root")
		}
	case WorkspaceFilePayload:
		parent, err := parentPayload(EventSubagentWorkspace)
		workspace, ok := parent.(*SubagentWorkspacePayload)
		if err != nil || !ok || workspace.ResultRootSHA256 != value.WorkspaceSHA256 || workspace.Disposition != "selected" {
			return errors.New("workspace file is not bound to selected child root")
		}
	}
	_ = kind
	return nil
}

func validateEvidenceBodyBinding(kind EvidenceType, raw json.RawMessage, object labstore.Object) error {
	payload, err := decodeEvidencePayload(kind, raw)
	if err != nil {
		return errors.New("invalid causal evidence body payload")
	}
	payload = dereferenceEvidencePayload(payload)
	bodySHA256 := fmt.Sprintf("sha256:%x", sha256.Sum256(object.Body))
	switch value := payload.(type) {
	case ModelContextPayload:
		if kind != EventModelBody || object.Kind != labstore.KindMetadataEvent {
			return errors.New("model body kind mismatch")
		}
		var modelBody struct {
			Brief   string `json:"brief"`
			Context string `json:"context"`
		}
		decoder := json.NewDecoder(bytes.NewReader(object.Body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&modelBody) != nil || decoder.Decode(&struct{}{}) == nil || modelBody.Brief == "" || modelBody.Context == "" ||
			fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(modelBody.Brief))) != value.BriefSHA256 ||
			fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(modelBody.Context))) != value.ContextSHA256 {
			return errors.New("model body digest mismatch")
		}
	case SourceBodyPayload:
		if object.Kind != labstore.KindCode || bodySHA256 != value.SourceSHA256 {
			return errors.New("source body digest mismatch")
		}
	case WorkspaceFilePayload:
		if object.Kind != labstore.KindFile || bodySHA256 != value.ContentSHA256 {
			return errors.New("workspace body digest mismatch")
		}
	case RuntimeObservationPayload:
		if object.Kind != labstore.KindMetadataEvent || bodySHA256 != value.ObservationSHA256 {
			return errors.New("runtime observation body digest mismatch")
		}
	default:
		return errors.New("invalid causal evidence body payload")
	}
	return nil
}

func validPrivateEvidencePath(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.Contains(value, "\\") && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func stableReasons(reasons []string) bool {
	for index, reason := range reasons {
		if !evidenceIdentifier.MatchString(reason) || (index > 0 && reasons[index-1] >= reason) {
			return false
		}
	}
	return true
}

func validEffectTransition(value EffectTransitionPayload) bool {
	if value.ReceiptID != "" && !evidenceIdentifier.MatchString(value.ReceiptID) {
		return false
	}
	switch value.State {
	case EffectIntent, EffectStarted, EffectCleanupOnly:
		return value.Compensator == "" && value.ReconciliationReason == ""
	case EffectCommitted, EffectDenied, EffectFailed, EffectTimedOut:
		return value.ReceiptID != "" && value.Compensator == "" && value.ReconciliationReason == ""
	case EffectCompensated:
		return evidenceIdentifier.MatchString(value.Compensator) && value.ReconciliationReason == ""
	case EffectAmbiguous, EffectReconciliationRequired:
		return value.Compensator == "" && evidenceIdentifier.MatchString(value.ReconciliationReason)
	default:
		return false
	}
}

func evidenceEventID(headerSHA string, event EvidenceEvent) string {
	event.EventID = ""
	return evidenceHash("event\x00"+headerSHA, event)
}

func evidenceExportSeal(exported Export) string {
	exported.SealSHA256 = ""
	return evidenceHash("export", exported)
}

func evidenceHash(label string, value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(append([]byte(evidenceHashDomain+label+"\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneEvidenceEvent(event EvidenceEvent) EvidenceEvent {
	event.ParentEventIDs = append([]string(nil), event.ParentEventIDs...)
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	event.Body = cloneEvidenceRef(event.Body)
	return event
}

func cloneEvidenceRef(ref *labstore.Ref) *labstore.Ref {
	if ref == nil {
		return nil
	}
	copy := *ref
	return &copy
}

func (event EvidenceEvent) String() string {
	return fmt.Sprintf("%s[%d]@%s", event.Type, event.Ordinal, event.EventID)
}
