package trajectory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

const EvidenceSchemaVersion = "pysolate.causal-evidence.v1"

const evidenceHashDomain = "pysolate.causal-evidence.v1\x00"

const MaxEvidenceEvents = 100_000

var evidenceIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{7,127}$`)

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
	EventTraceStarted      EvidenceType = "trace.started"
	EventTraceEnded        EvidenceType = "trace.ended"
	EventAuthoritySnapshot EvidenceType = "authority.snapshot"
	EventEffectTransition  EvidenceType = "effect.transition"
	EventWorkspaceTerminal EvidenceType = "workspace.terminal"
	EventExecutionAttempt  EvidenceType = "execution.attempt"
	EventModelContext      EvidenceType = "model.context"
	EventSourceDocument    EvidenceType = "source.document"
	EventSourceOccurrence  EvidenceType = "source.occurrence"
	EventSourceDecision    EvidenceType = "source.decision"
	EventExecutedLine      EvidenceType = "source.executed_line"
	EventSubagentContext   EvidenceType = "subagent.context"
	EventSubagentRuntime   EvidenceType = "subagent.runtime"
	EventSubagentWorkspace EvidenceType = "subagent.workspace"
	EventEvidenceTruncated EvidenceType = "evidence.truncated"
)

func (eventType EvidenceType) valid() bool {
	switch eventType {
	case EventTraceStarted, EventTraceEnded, EventAuthoritySnapshot, EventEffectTransition,
		EventWorkspaceTerminal, EventExecutionAttempt, EventModelContext,
		EventSourceDocument, EventSourceOccurrence, EventSourceDecision, EventExecutedLine,
		EventSubagentContext, EventSubagentRuntime, EventSubagentWorkspace, EventEvidenceTruncated:
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

type ClaimLevel string

const ClaimSourceBound ClaimLevel = "source_bound"

type SourceDocumentPayload struct {
	DocumentID   string       `json:"document_id"`
	SourceSHA256 string       `json:"source_sha256"`
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
	ChildID                  string `json:"child_id"`
	FreshRunID               string `json:"fresh_run_id"`
	PreparedImageSHA256      string `json:"prepared_image_sha256"`
	ChildPlanSHA256          string `json:"child_plan_sha256"`
	ParentLiveStateInherited bool   `json:"parent_live_state_inherited"`
}

type SubagentWorkspacePayload struct {
	ChildID          string `json:"child_id"`
	BaseRootSHA256   string `json:"base_root_sha256"`
	ResultRootSHA256 string `json:"result_root_sha256"`
	ChangedEntries   uint32 `json:"changed_entries"`
	ChangedBytes     uint64 `json:"changed_bytes"`
	Disposition      string `json:"disposition"`
}

type TruncationPayload struct {
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	DroppedEvents uint64 `json:"dropped_events"`
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
	if builder == nil || len(builder.events) >= MaxEvidenceEvents || !input.Type.valid() || !evidenceIdentifier.MatchString(input.ActorID) {
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
	if input.Body != nil {
		if input.Type.productionEligible() || builder.store == nil || input.Body.Kind == "" || input.Body.SHA256 == "" {
			return EvidenceEvent{}, errors.New("invalid causal evidence body")
		}
		if _, err := builder.store.Get(*input.Body); err != nil {
			return EvidenceEvent{}, errors.New("unresolved causal evidence body")
		}
	}
	event := EvidenceEvent{
		SchemaVersion: EvidenceSchemaVersion, Ordinal: uint64(len(builder.events) + 1),
		OccurredNanos: input.OccurredNanos, Type: input.Type, ActorID: input.ActorID,
		ParentEventIDs: parents, Payload: payload, Body: cloneEvidenceRef(input.Body),
	}
	event.EventID = evidenceEventID(builder.header.HeaderSHA256, event)
	builder.events = append(builder.events, cloneEvidenceEvent(event))
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
	if builder == nil || (builder.overflow && !builder.truncated) || !profile.valid() ||
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
	if err := ValidateEvidenceExport(exported); err != nil {
		return Export{}, err
	}
	return exported, nil
}

func EncodeEvidenceExport(exported Export) ([]byte, error) {
	if err := ValidateEvidenceExport(exported); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		return nil, errors.New("encode causal evidence export")
	}
	return encoded, nil
}

func DecodeEvidenceExport(raw []byte) (Export, error) {
	if len(raw) == 0 || len(raw) > 16<<20 {
		return Export{}, errors.New("invalid causal evidence export")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var exported Export
	if decoder.Decode(&exported) != nil || decoder.Decode(&struct{}{}) == nil {
		return Export{}, errors.New("invalid causal evidence export")
	}
	if err := ValidateEvidenceExport(exported); err != nil {
		return Export{}, err
	}
	canonical, err := json.Marshal(exported)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Export{}, errors.New("noncanonical causal evidence export")
	}
	return exported, nil
}

func ValidateEvidenceExport(exported Export) error {
	if exported.SchemaVersion != EvidenceSchemaVersion || !exported.Profile.valid() ||
		exported.TraceID != exported.Header.TraceID || exported.HeaderSHA256 != exported.Header.HeaderSHA256 ||
		!validEvidenceHeaderWithSeal(exported.Header) || exported.Header.HeaderSHA256 != evidenceHeaderIdentity(exported.Header) ||
		(exported.Privacy != labstore.PrivacyPrivate && exported.Privacy != labstore.PrivacyPortable) ||
		(exported.Profile == ProfileProductionRollback && exported.Privacy != labstore.PrivacyPortable) ||
		len(exported.Events) > MaxEvidenceEvents || exported.SealSHA256 != evidenceExportSeal(exported) {
		return errors.New("invalid causal evidence export")
	}
	prior := make(map[string]EvidenceEvent, len(exported.Events))
	var lastOrdinal uint64
	var truncated bool
	var terminalComplete bool
	for _, event := range exported.Events {
		if event.SchemaVersion != EvidenceSchemaVersion || event.Ordinal <= lastOrdinal || !event.Type.valid() ||
			!evidenceIdentifier.MatchString(event.ActorID) || event.EventID != evidenceEventID(exported.HeaderSHA256, event) {
			return errors.New("invalid causal evidence event")
		}
		if (exported.Profile == ProfileProductionRollback && (!event.Type.productionEligible() || event.Body != nil)) ||
			(exported.Privacy == labstore.PrivacyPortable && event.Body != nil) {
			return errors.New("portable evidence leaked private telemetry")
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
		if event.Type == EventEvidenceTruncated {
			truncated = true
		}
		if value, ok := decoded.(*TraceEndedPayload); ok {
			terminalComplete = value.EvidenceComplete
		}
		if err := validateEvidenceRelations(event.Type, dereferenceEvidencePayload(decoded), event.ParentEventIDs, prior); err != nil {
			return err
		}
		prior[event.EventID] = event
		lastOrdinal = event.Ordinal
	}
	if truncated && terminalComplete {
		return errors.New("truncated evidence claimed complete")
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
	case EventWorkspaceTerminal:
		target = &WorkspaceTerminalPayload{}
	case EventExecutionAttempt:
		target = &ExecutionAttemptPayload{}
	case EventModelContext:
		target = &ModelContextPayload{}
	case EventSourceDocument:
		target = &SourceDocumentPayload{}
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
	case EventEvidenceTruncated:
		target = &TruncationPayload{}
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
	case *WorkspaceTerminalPayload:
		return *typed
	case *ExecutionAttemptPayload:
		return *typed
	case *ModelContextPayload:
		return *typed
	case *SourceDocumentPayload:
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
	case *TruncationPayload:
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
	case WorkspaceTerminalPayload:
		if kind != EventWorkspaceTerminal || !validDigest(value.BaseWorkspaceSHA256) ||
			(value.ResultWorkspaceSHA256 != "" && !validDigest(value.ResultWorkspaceSHA256)) ||
			(value.Disposition != "published" && value.Disposition != "discarded" && value.Disposition != "unchanged" && value.Disposition != "conflict") {
			return errors.New("invalid workspace terminal payload")
		}
	case ExecutionAttemptPayload:
		if kind != EventExecutionAttempt || !evidenceIdentifier.MatchString(value.RunID) || !evidenceIdentifier.MatchString(value.AttemptID) ||
			(value.PreparedImageSHA256 != "" && !validDigest(value.PreparedImageSHA256)) ||
			(value.Status != "started" && value.Status != "completed" && value.Status != "failed") {
			return errors.New("invalid execution attempt payload")
		}
	case ModelContextPayload:
		if kind != EventModelContext || !validDigest(value.ContextSHA256) || !validDigest(value.BriefSHA256) || !value.Availability.valid() {
			return errors.New("invalid model context payload")
		}
	case SourceDocumentPayload:
		if kind != EventSourceDocument || !validDigest(value.DocumentID) || !validDigest(value.SourceSHA256) || !value.Availability.valid() {
			return errors.New("invalid source document payload")
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
			(!value.Admitted && (len(value.Reasons) == 0 || value.ReceiptID != "")) {
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
		if kind != EventSubagentRuntime || !evidenceIdentifier.MatchString(value.ChildID) || !evidenceIdentifier.MatchString(value.FreshRunID) ||
			!validDigest(value.PreparedImageSHA256) || !validDigest(value.ChildPlanSHA256) || value.ParentLiveStateInherited {
			return errors.New("invalid subagent runtime payload")
		}
	case SubagentWorkspacePayload:
		if kind != EventSubagentWorkspace || !evidenceIdentifier.MatchString(value.ChildID) || !validDigest(value.BaseRootSHA256) || !validDigest(value.ResultRootSHA256) ||
			(value.Disposition != "selected" && value.Disposition != "discarded") {
			return errors.New("invalid subagent workspace payload")
		}
	case TruncationPayload:
		if kind != EventEvidenceTruncated || !evidenceIdentifier.MatchString(value.Scope) || !evidenceIdentifier.MatchString(value.Reason) || value.DroppedEvents == 0 {
			return errors.New("invalid evidence truncation payload")
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
	case SourceOccurrencePayload:
		parent, err := parentPayload(EventSourceDocument)
		document, ok := parent.(*SourceDocumentPayload)
		if err != nil || !ok || document.DocumentID != value.DocumentID || document.SourceSHA256 != value.SourceSHA256 {
			return errors.New("source occurrence is not bound to its document")
		}
	case SourceDecisionPayload:
		var occurrence *SourceOccurrencePayload
		var effect *EffectTransitionPayload
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
			default:
				return errors.New("invalid source decision parent")
			}
		}
		if occurrence == nil || occurrence.OccurrenceID != value.OccurrenceID || occurrence.DynamicOccurrence != value.DynamicOccurrence {
			return errors.New("source decision is not bound to its occurrence")
		}
		if value.Admitted {
			if effect == nil || effect.State != EffectCommitted || effect.ReceiptID != value.ReceiptID {
				return errors.New("admitted source decision is not bound to its receipt")
			}
		} else if effect != nil {
			return errors.New("rejected source decision cannot bind an effect")
		}
	case SubagentRuntimePayload:
		parent, err := parentPayload(EventSubagentContext)
		context, ok := parent.(*SubagentContextPayload)
		if err != nil || !ok || context.ChildID != value.ChildID {
			return errors.New("subagent runtime is not bound to child context")
		}
	case SubagentWorkspacePayload:
		parent, err := parentPayload(EventSubagentRuntime)
		runtime, ok := parent.(*SubagentRuntimePayload)
		if err != nil || !ok || runtime.ChildID != value.ChildID {
			return errors.New("subagent workspace is not bound to child runtime")
		}
	}
	_ = kind
	return nil
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
	case EffectCommitted:
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
