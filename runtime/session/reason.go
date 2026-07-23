package session

import (
	"errors"
	"fmt"
)

var ErrInvalidUnsupportedReason = errors.New("invalid unsupported session reason")

type Operation string

const (
	OperationCapture Operation = "capture"
	OperationResume  Operation = "resume"
)

type ReasonCode string

const (
	ReasonNotQuiescent            ReasonCode = "not_quiescent"
	ReasonMutableStateUnsupported ReasonCode = "mutable_state_unsupported"
	ReasonExternalResourceActive  ReasonCode = "external_resource_active"
	ReasonArtifactMismatch        ReasonCode = "artifact_mismatch"
	ReasonBaseMismatch            ReasonCode = "base_mismatch"
	ReasonRuntimeMismatch         ReasonCode = "runtime_mismatch"
	ReasonArchitectureMismatch    ReasonCode = "architecture_mismatch"
	ReasonCapsuleInvalid          ReasonCode = "capsule_invalid"
	ReasonLeaseConflict           ReasonCode = "lease_conflict"
)

type StateClass string

const (
	StateClassLinearMemory   StateClass = "linear_memory"
	StateClassMutableGlobal  StateClass = "mutable_global"
	StateClassTable          StateClass = "table"
	StateClassWASIResource   StateClass = "wasi_resource"
	StateClassHostState      StateClass = "host_state"
	StateClassExternalHandle StateClass = "external_handle"
)

type UnsupportedReason struct {
	SchemaVersion int        `json:"schema_version"`
	Operation     Operation  `json:"operation"`
	Code          ReasonCode `json:"code"`
	StateClass    StateClass `json:"state_class,omitempty"`
}

func (reason UnsupportedReason) Validate() error {
	if reason.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema version", ErrInvalidUnsupportedReason)
	}
	if !validOperation(reason.Operation) {
		return fmt.Errorf("%w: unsupported operation %q", ErrInvalidUnsupportedReason, reason.Operation)
	}
	if !validReasonForOperation(reason.Code, reason.Operation) {
		return fmt.Errorf("%w: reason %q is invalid for %q", ErrInvalidUnsupportedReason, reason.Code, reason.Operation)
	}
	if reason.Code == ReasonMutableStateUnsupported {
		if !validStateClass(reason.StateClass) {
			return fmt.Errorf("%w: mutable state reason requires a known state class", ErrInvalidUnsupportedReason)
		}
	} else if reason.StateClass != "" {
		return fmt.Errorf("%w: state class is only valid for mutable state", ErrInvalidUnsupportedReason)
	}
	return nil
}

func validOperation(operation Operation) bool {
	return operation == OperationCapture || operation == OperationResume
}

func validReasonForOperation(code ReasonCode, operation Operation) bool {
	switch operation {
	case OperationCapture:
		switch code {
		case ReasonNotQuiescent, ReasonMutableStateUnsupported, ReasonExternalResourceActive, ReasonLeaseConflict:
			return true
		}
	case OperationResume:
		switch code {
		case ReasonMutableStateUnsupported, ReasonArtifactMismatch, ReasonBaseMismatch, ReasonRuntimeMismatch,
			ReasonArchitectureMismatch, ReasonCapsuleInvalid, ReasonLeaseConflict:
			return true
		}
	}
	return false
}

func validStateClass(stateClass StateClass) bool {
	switch stateClass {
	case StateClassLinearMemory, StateClassMutableGlobal, StateClassTable, StateClassWASIResource,
		StateClassHostState, StateClassExternalHandle:
		return true
	default:
		return false
	}
}
