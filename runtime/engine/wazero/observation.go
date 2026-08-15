package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

var ErrObservationEvidenceInvalid = errors.New("required Runtime observation evidence is invalid")

type observationLifecycle struct {
	session      *observe.Session
	parent       *uint32
	active       bool
	started      bool
	terminal     bool
	evidenceLost bool
}

func (lifecycle *observationLifecycle) start(ctx context.Context, payload observe.ExecutionStartedPayload) error {
	if lifecycle.session == nil {
		return nil
	}
	lifecycle.active = lifecycle.session.Mode() != observe.Off
	event, err := appendObservation(ctx, lifecycle.session, observe.EventExecutionStarted, nil, payload)
	if err != nil {
		lifecycle.evidenceLost = true
		return errors.Join(ErrObservationEvidenceInvalid, err)
	}
	lifecycle.started = event.Sequence != 0
	if lifecycle.started {
		lifecycle.parent = sequencePointer(event.Sequence)
	}
	return nil
}

func (lifecycle *observationLifecycle) capabilityCalls(ctx context.Context, receipts []receipt.Receipt) error {
	if !lifecycle.started {
		return nil
	}
	for _, call := range receipts {
		payload := observe.CapabilityCallPayload{
			ArgumentsSHA256: prefixedReceiptDigest(call.RequestSHA256), Capability: call.Capability,
			CapabilityPlanSHA256: call.CapabilityPlanSHA256, OperationIndex: call.OperationIndex, Outcome: call.Outcome,
			ReceiptID: call.ReceiptID,
		}
		if call.ResponseSHA256 != "" {
			payload.ResultSHA256 = prefixedReceiptDigest(call.ResponseSHA256)
		}
		if call.Source != nil {
			payload.Source = &observe.SourceBindingPayload{
				ClaimLevel: call.Source.ClaimLevel, Capability: call.Source.Capability, DocumentID: call.Source.DocumentID,
				DynamicOccurrence: call.Source.DynamicOccurrence, EndColumn: call.Source.EndColumn, EndLine: call.Source.EndLine,
				OccurrenceID: call.Source.OccurrenceID, SchemaVersion: call.Source.SchemaVersion, SourceSHA256: call.Source.SourceSHA256,
				StartColumn: call.Source.StartColumn, StartLine: call.Source.StartLine,
			}
		}
		event, err := appendObservation(ctx, lifecycle.session, observe.EventCapabilityCall, lifecycle.parent, payload)
		if err != nil {
			lifecycle.evidenceLost = true
			return errors.Join(ErrObservationEvidenceInvalid, err)
		}
		if event.Sequence != 0 {
			lifecycle.parent = sequencePointer(event.Sequence)
		}
	}
	return nil
}

func (lifecycle *observationLifecycle) capabilityPlan(ctx context.Context, identity string) error {
	if !lifecycle.started {
		return nil
	}
	event, err := appendObservation(ctx, lifecycle.session, observe.EventCapabilityPlan, lifecycle.parent, observe.CapabilityPlanBoundPayload{
		CapabilityPlanSHA256: identity,
	})
	if err != nil {
		lifecycle.evidenceLost = true
		return errors.Join(ErrObservationEvidenceInvalid, err)
	}
	if event.Sequence != 0 {
		lifecycle.parent = sequencePointer(event.Sequence)
	}
	return nil
}

func (lifecycle *observationLifecycle) complete(ctx context.Context, resultSHA256 string) error {
	if !lifecycle.active || lifecycle.terminal {
		return nil
	}
	payload := observe.ExecutionCompletedPayload{
		EvidenceComplete: !lifecycle.evidenceLost && !lifecycle.session.Incomplete(), ResultSHA256: resultSHA256, Status: "ok",
	}
	_, err := appendObservation(ctx, lifecycle.session, observe.EventExecutionCompleted, lifecycle.parent, payload)
	lifecycle.terminal = err == nil
	if err != nil {
		lifecycle.evidenceLost = true
		return errors.Join(ErrObservationEvidenceInvalid, err)
	}
	return nil
}

func (lifecycle *observationLifecycle) workspace(ctx context.Context, initial, final workspace.Snapshot) error {
	if !lifecycle.started || lifecycle.terminal {
		return nil
	}
	changes, truncated := workspaceChanges(initial.Entries, final.Entries, 128)
	payload := observe.WorkspaceFinalizedPayload{
		Changes: changes, ChangesTruncated: truncated, EntryCount: final.Info.EntryCount,
		FinalTreeSHA256: final.Info.TreeSHA256, FinalWorkspaceSHA256: final.Info.WorkspaceSHA256,
		InitialWorkspaceSHA256: initial.Info.WorkspaceSHA256, SyscallOrderAvailable: false, TotalBytes: final.Info.TotalBytes,
	}
	event, err := appendObservation(ctx, lifecycle.session, observe.EventWorkspaceFinalized, lifecycle.parent, payload)
	if err != nil {
		lifecycle.evidenceLost = true
		return errors.Join(ErrObservationEvidenceInvalid, err)
	}
	if event.Sequence != 0 {
		lifecycle.parent = sequencePointer(event.Sequence)
	}
	return nil
}

func (lifecycle *observationLifecycle) fail(ctx context.Context, class string) error {
	if !lifecycle.active || lifecycle.terminal {
		return nil
	}
	payload := observe.ExecutionFailedPayload{
		ErrorClass: class, EvidenceComplete: !lifecycle.evidenceLost && !lifecycle.session.Incomplete(), Status: "error",
	}
	_, err := appendObservation(ctx, lifecycle.session, observe.EventExecutionFailed, lifecycle.parent, payload)
	lifecycle.terminal = err == nil
	if err != nil {
		lifecycle.evidenceLost = true
		return errors.Join(ErrObservationEvidenceInvalid, err)
	}
	return nil
}

func appendObservation(ctx context.Context, session *observe.Session, kind string, parent *uint32, payload any) (observe.Event, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return observe.Event{}, err
	}
	return session.Append(ctx, kind, parent, encoded)
}

func sequencePointer(sequence uint32) *uint32 {
	value := sequence
	return &value
}

func prefixedReceiptDigest(value string) string {
	if len(value) == 64 {
		return "sha256:" + value
	}
	return value
}

func workspaceChanges(initial, final []workspace.SnapshotEntry, maximum int) ([]observe.WorkspaceChange, bool) {
	before := make(map[string]workspace.SnapshotEntry, len(initial))
	after := make(map[string]workspace.SnapshotEntry, len(final))
	paths := make(map[string]struct{}, len(initial)+len(final))
	for _, entry := range initial {
		if entry.Kind == "file" {
			before[entry.Path] = entry
			paths[entry.Path] = struct{}{}
		}
	}
	for _, entry := range final {
		if entry.Kind == "file" {
			after[entry.Path] = entry
			paths[entry.Path] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := make([]observe.WorkspaceChange, 0)
	truncated := false
	for _, path := range ordered {
		old, existed := before[path]
		current, exists := after[path]
		var change observe.WorkspaceChange
		switch {
		case !existed && exists:
			change = observe.WorkspaceChange{Kind: "added", Path: path, AfterBytes: current.Size, AfterExecutable: current.Executable, AfterSHA256: current.SHA256}
		case existed && !exists:
			change = observe.WorkspaceChange{Kind: "removed", Path: path, BeforeBytes: old.Size, BeforeExecutable: old.Executable, BeforeSHA256: old.SHA256}
		case existed && exists && (old.SHA256 != current.SHA256 || old.Size != current.Size || old.Executable != current.Executable):
			change = observe.WorkspaceChange{
				Kind: "modified", Path: path, BeforeBytes: old.Size, BeforeExecutable: old.Executable, BeforeSHA256: old.SHA256,
				AfterBytes: current.Size, AfterExecutable: current.Executable, AfterSHA256: current.SHA256,
			}
		default:
			continue
		}
		if len(changes) >= maximum {
			truncated = true
			continue
		}
		changes = append(changes, change)
	}
	return changes, truncated
}
