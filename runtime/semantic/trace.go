package semantic

import "sort"

const (
	ObservableTraceSchemaVersion = "pysolate.observable-trace.v0"
	maxObservableTraceEvents     = 1024
	maxTracePredecessors         = 256
)

type EventKind string
type EventSurface string
type EventStatus string
type TerminalClass string
type DivergenceClass string

type TraceEffectClass string

const (
	TraceEffectPure           TraceEffectClass = "pure"
	TraceEffectWorkspaceRead  TraceEffectClass = "workspace_read"
	TraceEffectWorkspaceWrite TraceEffectClass = "workspace_write"
	TraceEffectExternalRead   TraceEffectClass = "external_read"
	TraceEffectExternalWrite  TraceEffectClass = "external_write"
	TraceEffectUnknown        TraceEffectClass = "unknown"
)

const (
	EventRunStart               EventKind = "run_start"
	EventCapabilityAttempt      EventKind = "capability_attempt"
	EventCapabilityObservation  EventKind = "capability_observation"
	EventWorkspaceRead          EventKind = "workspace_read"
	EventWorkspaceWrite         EventKind = "workspace_write"
	EventExternalEffectIntent   EventKind = "external_effect_intent"
	EventExternalEffectAttempt  EventKind = "external_effect_attempt"
	EventExternalEffectTerminal EventKind = "external_effect_terminal"
	EventResult                 EventKind = "result"
	EventRaise                  EventKind = "raise"
	EventCancel                 EventKind = "cancel"
	EventTimeout                EventKind = "timeout"
	EventTrap                   EventKind = "trap"
	EventRunTerminal            EventKind = "run_terminal"

	SurfaceLogical             EventSurface = "logical"
	SurfaceSpeculativePhysical EventSurface = "speculative_physical"

	EventReady     EventStatus = "ready"
	EventAttempted EventStatus = "attempted"
	EventSucceeded EventStatus = "succeeded"
	EventFailed    EventStatus = "failed"
	EventAmbiguous EventStatus = "ambiguous"
	EventConsumed  EventStatus = "consumed"
	EventCancelled EventStatus = "cancelled"
	EventLate      EventStatus = "late"
	EventOrphaned  EventStatus = "orphaned"

	TerminalResult    TerminalClass = "result"
	TerminalException TerminalClass = "exception"
	TerminalCancelled TerminalClass = "cancelled"
	TerminalTimeout   TerminalClass = "timeout"
	TerminalTrap      TerminalClass = "trap"
	TerminalOOM       TerminalClass = "oom"

	DivergenceNone                         DivergenceClass = ""
	DivergenceTerminalClassMismatch        DivergenceClass = "terminal_class_mismatch"
	DivergenceResultMismatch               DivergenceClass = "result_mismatch"
	DivergenceExceptionMismatch            DivergenceClass = "exception_mismatch"
	DivergenceMissingEffectEvent           DivergenceClass = "missing_effect_event"
	DivergenceExtraEffectEvent             DivergenceClass = "extra_effect_event"
	DivergenceEffectArgumentMismatch       DivergenceClass = "effect_argument_mismatch"
	DivergenceRequiredOrderInversion       DivergenceClass = "required_order_inversion"
	DivergenceWorkspaceStateMismatch       DivergenceClass = "workspace_state_mismatch"
	DivergenceFreshnessContextMismatch     DivergenceClass = "freshness_context_mismatch"
	DivergenceAuthorityBindingMismatch     DivergenceClass = "authority_binding_mismatch"
	DivergenceCancellationBoundaryMismatch DivergenceClass = "cancellation_boundary_mismatch"
	DivergenceTerminalDispositionMismatch  DivergenceClass = "terminal_disposition_mismatch"
	DivergencePostEffectReplay             DivergenceClass = "post_effect_replay"
	DivergenceTraceUnclassifiable          DivergenceClass = "trace_unclassifiable"
)

type TraceEvent struct {
	ID                   string
	Kind                 EventKind
	Surface              EventSurface
	Capability           string
	EffectClass          TraceEffectClass
	ArgumentsSHA256      string
	ResourceSHA256       string
	ResultSHA256         string
	FreshnessSHA256      string
	AuthoritySHA256      string
	Status               EventStatus
	Predecessors         []string
	QualifiedSpeculation bool
	QualificationSHA256  string
	ClaimedLogicalID     string
}

type WorkspaceTrace struct {
	StartSHA256 string
	FinalSHA256 string
	Disposition string
}

type TerminalTrace struct {
	Class                TerminalClass
	ValueSHA256          string
	ExceptionType        string
	ExceptionSHA256      string
	Cancellation         string
	Ambiguity            string
	Reconciliation       string
	WorkspaceDisposition string
	EffectDisposition    string
	PhysicalStarted      bool
	EffectMayHaveStarted bool
	PostEffectReplay     bool
}

type ObservableTrace struct {
	SchemaVersion       string
	FrozenContextSHA256 string
	Workspace           WorkspaceTrace
	Events              []TraceEvent
	Terminal            TerminalTrace
}

type TraceComparison struct {
	Equivalent bool
	Divergence DivergenceClass
}

func CompareObservableTraces(baseline, candidate ObservableTrace) TraceComparison {
	if !validObservableTrace(baseline) || !validObservableTrace(candidate) {
		return diverged(DivergenceTraceUnclassifiable)
	}
	if baseline.Terminal.PostEffectReplay || candidate.Terminal.PostEffectReplay {
		return diverged(DivergencePostEffectReplay)
	}
	if baseline.FrozenContextSHA256 != candidate.FrozenContextSHA256 {
		return diverged(DivergenceAuthorityBindingMismatch)
	}
	if baseline.Terminal.Class != candidate.Terminal.Class {
		return diverged(DivergenceTerminalClassMismatch)
	}
	switch baseline.Terminal.Class {
	case TerminalResult:
		if baseline.Terminal.ValueSHA256 != candidate.Terminal.ValueSHA256 {
			return diverged(DivergenceResultMismatch)
		}
	case TerminalException:
		if baseline.Terminal.ExceptionType != candidate.Terminal.ExceptionType ||
			baseline.Terminal.ExceptionSHA256 != candidate.Terminal.ExceptionSHA256 {
			return diverged(DivergenceExceptionMismatch)
		}
	}
	if baseline.Workspace != candidate.Workspace ||
		baseline.Terminal.WorkspaceDisposition != candidate.Terminal.WorkspaceDisposition {
		return diverged(DivergenceWorkspaceStateMismatch)
	}
	if baseline.Terminal.Cancellation != candidate.Terminal.Cancellation {
		return diverged(DivergenceCancellationBoundaryMismatch)
	}
	if baseline.Terminal.Ambiguity != candidate.Terminal.Ambiguity ||
		baseline.Terminal.Reconciliation != candidate.Terminal.Reconciliation ||
		baseline.Terminal.EffectDisposition != candidate.Terminal.EffectDisposition ||
		baseline.Terminal.EffectMayHaveStarted != candidate.Terminal.EffectMayHaveStarted {
		return diverged(DivergenceTerminalDispositionMismatch)
	}

	baselineLogical := logicalEvents(baseline.Events)
	candidateLogical := logicalEvents(candidate.Events)
	baselineOrder := logicalEventOrder(baseline.Events)
	candidateOrder := logicalEventOrder(candidate.Events)
	for _, id := range baselineOrder {
		expected := baselineLogical[id]
		actual, ok := candidateLogical[id]
		if !ok {
			return diverged(DivergenceMissingEffectEvent)
		}
		if expected.Kind != actual.Kind || expected.Capability != actual.Capability || expected.EffectClass != actual.EffectClass {
			return diverged(DivergenceMissingEffectEvent)
		}
		if expected.ArgumentsSHA256 != actual.ArgumentsSHA256 || expected.ResourceSHA256 != actual.ResourceSHA256 {
			return diverged(DivergenceEffectArgumentMismatch)
		}
		if expected.FreshnessSHA256 != actual.FreshnessSHA256 {
			return diverged(DivergenceFreshnessContextMismatch)
		}
		if expected.AuthoritySHA256 != actual.AuthoritySHA256 {
			return diverged(DivergenceAuthorityBindingMismatch)
		}
		if expected.ResultSHA256 != actual.ResultSHA256 {
			return diverged(DivergenceResultMismatch)
		}
		if expected.Status != actual.Status {
			return diverged(DivergenceTerminalDispositionMismatch)
		}
		if !sameSortedStrings(expected.Predecessors, actual.Predecessors) {
			return diverged(DivergenceRequiredOrderInversion)
		}
	}
	if len(candidateLogical) != len(baselineLogical) {
		return diverged(DivergenceExtraEffectEvent)
	}
	if !sameSortedStrings(baselineOrder, candidateOrder) {
		return diverged(DivergenceRequiredOrderInversion)
	}
	return TraceComparison{Equivalent: true}
}

func validObservableTrace(trace ObservableTrace) bool {
	if trace.SchemaVersion != ObservableTraceSchemaVersion || !digestPattern.MatchString(trace.FrozenContextSHA256) ||
		!validWorkspaceTrace(trace.Workspace) || !validTerminalTrace(trace.Terminal) ||
		len(trace.Events) == 0 || len(trace.Events) > maxObservableTraceEvents {
		return false
	}
	logical := make(map[string]TraceEvent, len(trace.Events))
	all := make(map[string]struct{}, len(trace.Events))
	positions := make(map[string]int, len(trace.Events))
	physicalStarted := false
	for eventIndex, event := range trace.Events {
		if !digestPattern.MatchString(event.ID) || !validEventKind(event.Kind) || !validEventSurface(event.Surface) ||
			!validEventStatus(event.Status) || len(event.Predecessors) > maxTracePredecessors ||
			!sort.StringsAreSorted(event.Predecessors) {
			return false
		}
		if _, exists := all[event.ID]; exists {
			return false
		}
		all[event.ID] = struct{}{}
		positions[event.ID] = eventIndex
		for index, predecessor := range event.Predecessors {
			if !digestPattern.MatchString(predecessor) || predecessor == event.ID ||
				(index > 0 && event.Predecessors[index-1] == predecessor) {
				return false
			}
		}
		if event.Surface == SurfaceLogical {
			if event.QualifiedSpeculation || event.QualificationSHA256 != "" || event.ClaimedLogicalID != "" ||
				!validLogicalStatus(event.Kind, event.Status) {
				return false
			}
			logical[event.ID] = event
		} else if !validSpeculativePhysical(event) {
			return false
		} else {
			physicalStarted = true
		}
		if !validEventPayload(event) {
			return false
		}
	}
	claimed := make(map[string]struct{})
	for eventIndex, event := range trace.Events {
		for _, predecessor := range event.Predecessors {
			if _, exists := logical[predecessor]; !exists || positions[predecessor] >= eventIndex {
				return false
			}
		}
		if event.Surface == SurfaceSpeculativePhysical && event.ClaimedLogicalID != "" {
			logicalEvent, exists := logical[event.ClaimedLogicalID]
			if !exists || !physicalClaimsLogical(event, logicalEvent) {
				return false
			}
			if _, duplicate := claimed[event.ClaimedLogicalID]; duplicate {
				return false
			}
			claimed[event.ClaimedLogicalID] = struct{}{}
		}
	}
	return trace.Terminal.PhysicalStarted == physicalStarted
}

func validWorkspaceTrace(workspace WorkspaceTrace) bool {
	return digestPattern.MatchString(workspace.StartSHA256) && digestPattern.MatchString(workspace.FinalSHA256) &&
		legalityTokenPattern.MatchString(workspace.Disposition)
}

func validTerminalTrace(terminal TerminalTrace) bool {
	if !validTerminalClass(terminal.Class) || !legalityTokenPattern.MatchString(terminal.WorkspaceDisposition) ||
		!legalityTokenPattern.MatchString(terminal.EffectDisposition) {
		return false
	}
	for _, optional := range []string{terminal.Cancellation, terminal.Ambiguity, terminal.Reconciliation} {
		if optional != "" && !legalityTokenPattern.MatchString(optional) {
			return false
		}
	}
	switch terminal.Class {
	case TerminalResult:
		return digestPattern.MatchString(terminal.ValueSHA256) && terminal.ExceptionType == "" && terminal.ExceptionSHA256 == ""
	case TerminalException:
		return terminal.ValueSHA256 == "" && legalityTokenPattern.MatchString(terminal.ExceptionType) && digestPattern.MatchString(terminal.ExceptionSHA256)
	default:
		return terminal.ValueSHA256 == "" && terminal.ExceptionType == "" && terminal.ExceptionSHA256 == ""
	}
}

func validSpeculativePhysical(event TraceEvent) bool {
	if !event.QualifiedSpeculation || !digestPattern.MatchString(event.QualificationSHA256) ||
		event.Kind != EventCapabilityObservation || len(event.Predecessors) != 0 ||
		(event.EffectClass != TraceEffectPure && event.EffectClass != TraceEffectWorkspaceRead &&
			event.EffectClass != TraceEffectExternalRead) {
		return false
	}
	if event.ClaimedLogicalID != "" {
		return event.Status == EventConsumed && digestPattern.MatchString(event.ClaimedLogicalID)
	}
	switch event.Status {
	case EventCancelled, EventLate, EventOrphaned:
		return true
	default:
		return false
	}
}

func validEventPayload(event TraceEvent) bool {
	requireDigest := func(value string) bool { return digestPattern.MatchString(value) }
	switch event.Kind {
	case EventCapabilityAttempt, EventCapabilityObservation, EventExternalEffectIntent,
		EventExternalEffectAttempt, EventExternalEffectTerminal:
		return capabilityPattern.MatchString(event.Capability) && validTraceEffectClass(event.EffectClass) && requireDigest(event.ArgumentsSHA256) &&
			requireDigest(event.ResourceSHA256) && requireDigest(event.FreshnessSHA256) && requireDigest(event.AuthoritySHA256) &&
			(event.ResultSHA256 == "" || requireDigest(event.ResultSHA256))
	case EventWorkspaceRead, EventWorkspaceWrite:
		expected := TraceEffectWorkspaceRead
		if event.Kind == EventWorkspaceWrite {
			expected = TraceEffectWorkspaceWrite
		}
		freshnessValid := event.FreshnessSHA256 == ""
		if event.Kind == EventWorkspaceRead {
			freshnessValid = requireDigest(event.FreshnessSHA256)
		}
		return event.Capability == "" && event.EffectClass == expected && event.ArgumentsSHA256 == "" &&
			requireDigest(event.ResourceSHA256) && requireDigest(event.AuthoritySHA256) && freshnessValid &&
			(event.ResultSHA256 == "" || requireDigest(event.ResultSHA256))
	case EventResult, EventRaise:
		return event.Capability == "" && event.EffectClass == "" && event.ArgumentsSHA256 == "" &&
			event.ResourceSHA256 == "" && requireDigest(event.ResultSHA256) && event.FreshnessSHA256 == "" &&
			event.AuthoritySHA256 == ""
	default:
		return event.Capability == "" && event.EffectClass == "" && event.ArgumentsSHA256 == "" && event.ResourceSHA256 == "" &&
			event.ResultSHA256 == "" && event.FreshnessSHA256 == "" && event.AuthoritySHA256 == ""
	}
}

func physicalClaimsLogical(physical, logical TraceEvent) bool {
	return physical.Kind == logical.Kind && physical.Capability == logical.Capability &&
		physical.EffectClass == logical.EffectClass && physical.ArgumentsSHA256 == logical.ArgumentsSHA256 &&
		physical.ResourceSHA256 == logical.ResourceSHA256 && physical.ResultSHA256 == logical.ResultSHA256 &&
		physical.FreshnessSHA256 == logical.FreshnessSHA256 && physical.AuthoritySHA256 == logical.AuthoritySHA256
}

func validLogicalStatus(kind EventKind, status EventStatus) bool {
	switch kind {
	case EventRunStart:
		return status == EventAttempted
	case EventCapabilityAttempt, EventExternalEffectIntent, EventExternalEffectAttempt,
		EventWorkspaceRead, EventWorkspaceWrite:
		return status == EventAttempted || status == EventSucceeded || status == EventFailed || status == EventAmbiguous
	case EventCapabilityObservation, EventExternalEffectTerminal, EventResult, EventRaise, EventRunTerminal:
		return status == EventSucceeded || status == EventFailed || status == EventAmbiguous
	case EventCancel:
		return status == EventCancelled
	case EventTimeout, EventTrap:
		return status == EventFailed
	default:
		return false
	}
}

func logicalEvents(events []TraceEvent) map[string]TraceEvent {
	result := make(map[string]TraceEvent, len(events))
	for _, event := range events {
		if event.Surface == SurfaceLogical {
			result[event.ID] = event
		}
	}
	return result
}

func logicalEventOrder(events []TraceEvent) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		if event.Surface == SurfaceLogical {
			result = append(result, event.ID)
		}
	}
	return result
}

func sameSortedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validTraceEffectClass(class TraceEffectClass) bool {
	switch class {
	case TraceEffectPure, TraceEffectWorkspaceRead, TraceEffectWorkspaceWrite,
		TraceEffectExternalRead, TraceEffectExternalWrite:
		return true
	default:
		return false
	}
}

func validEventKind(kind EventKind) bool {
	switch kind {
	case EventRunStart, EventCapabilityAttempt, EventCapabilityObservation, EventWorkspaceRead, EventWorkspaceWrite,
		EventExternalEffectIntent, EventExternalEffectAttempt, EventExternalEffectTerminal, EventResult, EventRaise,
		EventCancel, EventTimeout, EventTrap, EventRunTerminal:
		return true
	default:
		return false
	}
}

func validEventSurface(surface EventSurface) bool {
	return surface == SurfaceLogical || surface == SurfaceSpeculativePhysical
}

func validEventStatus(status EventStatus) bool {
	switch status {
	case EventReady, EventAttempted, EventSucceeded, EventFailed, EventAmbiguous, EventConsumed, EventCancelled, EventLate, EventOrphaned:
		return true
	default:
		return false
	}
}

func validTerminalClass(class TerminalClass) bool {
	switch class {
	case TerminalResult, TerminalException, TerminalCancelled, TerminalTimeout, TerminalTrap, TerminalOOM:
		return true
	default:
		return false
	}
}

func diverged(class DivergenceClass) TraceComparison {
	return TraceComparison{Divergence: class}
}
