package runtime

import (
	"errors"
	"sort"
)

const MechanismEvidenceSchemaVersion = "pysolate.mechanisms.v2"

type MechanismName string
type MechanismDisposition string
type MechanismReason string

const (
	MechanismApprovalSuspension  MechanismName = "approval_suspension"
	MechanismColdIOContinuation  MechanismName = "cold_io_continuation"
	MechanismStreaming           MechanismName = "streaming"
	MechanismStagedObservation   MechanismName = "staged_observation"
	MechanismPrivateWorkspace    MechanismName = "private_workspace"
	MechanismProgrammaticTools   MechanismName = "programmatic_tool_calling"
	MechanismImmutableBranches   MechanismName = "immutable_branches"
	MechanismChildFanout         MechanismName = "child_fanout"
	MechanismFunctionCache       MechanismName = "function_cache"
	MechanismSingleFlight        MechanismName = "single_flight"
	MechanismFreshReevaluation   MechanismName = "fresh_reevaluation"
	MechanismPreparedRuntime     MechanismName = "prepared_runtime"
	MechanismMemoryCOW           MechanismName = "memory_cow"
	MechanismSemanticAnalysis    MechanismName = "semantic_analysis"
	MechanismSemanticPreDispatch MechanismName = "semantic_pre_dispatch"
	MechanismSemanticReuse       MechanismName = "semantic_reuse"
	MechanismSplitPhaseCalls     MechanismName = "split_phase_calls"
	MechanismValueSlots          MechanismName = "value_slots"

	MechanismOff      MechanismDisposition = "off"
	MechanismSelected MechanismDisposition = "selected"
	MechanismFallback MechanismDisposition = "fallback"

	MechanismReasonUnavailable MechanismReason = "unavailable"
)

var (
	ErrInvalidMechanismSet      = errors.New("invalid mechanism set")
	ErrInvalidMechanismEvidence = errors.New("invalid mechanism evidence")
	ErrMechanismDisabled        = errors.New("optional mechanism is disabled")
)

var mechanismNames = []MechanismName{
	MechanismApprovalSuspension,
	MechanismChildFanout,
	MechanismColdIOContinuation,
	MechanismFreshReevaluation,
	MechanismFunctionCache,
	MechanismImmutableBranches,
	MechanismMemoryCOW,
	MechanismPreparedRuntime,
	MechanismPrivateWorkspace,
	MechanismProgrammaticTools,
	MechanismSemanticAnalysis,
	MechanismSemanticPreDispatch,
	MechanismSemanticReuse,
	MechanismSingleFlight,
	MechanismSplitPhaseCalls,
	MechanismStagedObservation,
	MechanismStreaming,
	MechanismValueSlots,
}

// MechanismSet is an internal Host-owned feature set. Zero value means ordinary
// fresh execution with every optional mechanism disabled.
type MechanismSet struct {
	ApprovalSuspension      bool
	Streaming               bool
	StagedObservation       bool
	PrivateWorkspace        bool
	ProgrammaticToolCalling bool
	ImmutableBranches       bool
	ChildFanout             bool
	FunctionCache           bool
	SingleFlight            bool
	FreshReevaluation       bool
	PreparedRuntime         bool
	MemoryCOW               bool
	ColdIOContinuation      bool
	SemanticAnalysis        bool
	SemanticPreDispatch     bool
	SemanticReuse           bool
	SplitPhaseCalls         bool `json:"SplitPhaseCalls,omitempty"`
	ValueSlots              bool `json:"ValueSlots,omitempty"`
}

func (set MechanismSet) Validate() error {
	if set.Streaming && !set.PrivateWorkspace {
		return ErrInvalidMechanismSet
	}
	if set.StagedObservation && !set.Streaming && !set.SemanticPreDispatch && !set.SplitPhaseCalls {
		return ErrInvalidMechanismSet
	}
	if set.SemanticPreDispatch && (!set.SemanticAnalysis || !set.StagedObservation) {
		return ErrInvalidMechanismSet
	}
	if set.SplitPhaseCalls && set.SemanticPreDispatch {
		return ErrInvalidMechanismSet
	}
	if set.ChildFanout && (!set.Streaming || !set.ImmutableBranches) {
		return ErrInvalidMechanismSet
	}
	if set.FunctionCache && !set.ImmutableBranches {
		return ErrInvalidMechanismSet
	}
	if set.FreshReevaluation && !set.FunctionCache {
		return ErrInvalidMechanismSet
	}
	if set.MemoryCOW && !set.PreparedRuntime {
		return ErrInvalidMechanismSet
	}
	if set.ColdIOContinuation && !set.MemoryCOW {
		return ErrInvalidMechanismSet
	}
	if set.SemanticReuse && (!set.SemanticAnalysis || (!set.FunctionCache && !set.SingleFlight)) {
		return ErrInvalidMechanismSet
	}
	return nil
}

func (set MechanismSet) Enabled() []MechanismName {
	names := make([]MechanismName, 0, len(mechanismNames))
	for _, name := range mechanismNames {
		if set.enabled(name) {
			names = append(names, name)
		}
	}
	return names
}

func (set MechanismSet) enabled(name MechanismName) bool {
	switch name {
	case MechanismApprovalSuspension:
		return set.ApprovalSuspension
	case MechanismColdIOContinuation:
		return set.ColdIOContinuation
	case MechanismStreaming:
		return set.Streaming
	case MechanismStagedObservation:
		return set.StagedObservation
	case MechanismPrivateWorkspace:
		return set.PrivateWorkspace
	case MechanismProgrammaticTools:
		return set.ProgrammaticToolCalling
	case MechanismImmutableBranches:
		return set.ImmutableBranches
	case MechanismChildFanout:
		return set.ChildFanout
	case MechanismFunctionCache:
		return set.FunctionCache
	case MechanismSingleFlight:
		return set.SingleFlight
	case MechanismFreshReevaluation:
		return set.FreshReevaluation
	case MechanismPreparedRuntime:
		return set.PreparedRuntime
	case MechanismMemoryCOW:
		return set.MemoryCOW
	case MechanismSemanticAnalysis:
		return set.SemanticAnalysis
	case MechanismSemanticPreDispatch:
		return set.SemanticPreDispatch
	case MechanismSemanticReuse:
		return set.SemanticReuse
	case MechanismSplitPhaseCalls:
		return set.SplitPhaseCalls
	case MechanismValueSlots:
		return set.ValueSlots
	default:
		return false
	}
}

func (set *MechanismSet) set(name MechanismName, enabled bool) {
	switch name {
	case MechanismApprovalSuspension:
		set.ApprovalSuspension = enabled
	case MechanismColdIOContinuation:
		set.ColdIOContinuation = enabled
	case MechanismStreaming:
		set.Streaming = enabled
	case MechanismStagedObservation:
		set.StagedObservation = enabled
	case MechanismPrivateWorkspace:
		set.PrivateWorkspace = enabled
	case MechanismProgrammaticTools:
		set.ProgrammaticToolCalling = enabled
	case MechanismImmutableBranches:
		set.ImmutableBranches = enabled
	case MechanismChildFanout:
		set.ChildFanout = enabled
	case MechanismFunctionCache:
		set.FunctionCache = enabled
	case MechanismSingleFlight:
		set.SingleFlight = enabled
	case MechanismFreshReevaluation:
		set.FreshReevaluation = enabled
	case MechanismPreparedRuntime:
		set.PreparedRuntime = enabled
	case MechanismMemoryCOW:
		set.MemoryCOW = enabled
	case MechanismSemanticAnalysis:
		set.SemanticAnalysis = enabled
	case MechanismSemanticPreDispatch:
		set.SemanticPreDispatch = enabled
	case MechanismSemanticReuse:
		set.SemanticReuse = enabled
	case MechanismSplitPhaseCalls:
		set.SplitPhaseCalls = enabled
	case MechanismValueSlots:
		set.ValueSlots = enabled
	}
}

type MechanismMode struct {
	Name        MechanismName        `json:"name"`
	Disposition MechanismDisposition `json:"disposition"`
	Reason      MechanismReason      `json:"reason,omitempty"`
}

type MechanismEvidence struct {
	SchemaVersion string          `json:"schema_version"`
	Mechanisms    []MechanismMode `json:"mechanisms"`
}

// ResolveMechanisms selects requested mechanisms available on this Host and
// records stable fallback reasons. It never changes capability grants or Plans.
func ResolveMechanisms(requested, available MechanismSet) (MechanismSet, MechanismEvidence, error) {
	if err := requested.Validate(); err != nil {
		return MechanismSet{}, MechanismEvidence{}, err
	}
	resolved := MechanismSet{}
	evidence := MechanismEvidence{SchemaVersion: MechanismEvidenceSchemaVersion, Mechanisms: make([]MechanismMode, 0, len(mechanismNames))}
	for _, name := range mechanismNames {
		mode := MechanismMode{Name: name, Disposition: MechanismOff}
		if requested.enabled(name) {
			if available.enabled(name) {
				resolved.set(name, true)
				mode.Disposition = MechanismSelected
			} else {
				mode.Disposition = MechanismFallback
				mode.Reason = MechanismReasonUnavailable
			}
		}
		evidence.Mechanisms = append(evidence.Mechanisms, mode)
	}
	if err := resolved.Validate(); err != nil {
		return MechanismSet{}, MechanismEvidence{}, err
	}
	return resolved, evidence, evidence.Validate()
}

func (evidence MechanismEvidence) Validate() error {
	if evidence.SchemaVersion != MechanismEvidenceSchemaVersion || len(evidence.Mechanisms) != len(mechanismNames) {
		return ErrInvalidMechanismEvidence
	}
	for index, mode := range evidence.Mechanisms {
		if mode.Name != mechanismNames[index] {
			return ErrInvalidMechanismEvidence
		}
		switch mode.Disposition {
		case MechanismOff, MechanismSelected:
			if mode.Reason != "" {
				return ErrInvalidMechanismEvidence
			}
		case MechanismFallback:
			if mode.Reason != MechanismReasonUnavailable {
				return ErrInvalidMechanismEvidence
			}
		default:
			return ErrInvalidMechanismEvidence
		}
	}
	return nil
}

func (evidence MechanismEvidence) Disposition(name MechanismName) MechanismDisposition {
	index := sort.Search(len(evidence.Mechanisms), func(index int) bool {
		return evidence.Mechanisms[index].Name >= name
	})
	if index < len(evidence.Mechanisms) && evidence.Mechanisms[index].Name == name {
		return evidence.Mechanisms[index].Disposition
	}
	return ""
}
