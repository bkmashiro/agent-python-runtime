package semanticspeculation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var ErrPhase5DuplicateTrial = errors.New("duplicate phase 5 trial")

// Phase5ExecutionInput is deliberately narrower than a campaign coordinate: an
// execution adapter sees source shape only, never case IDs, expected outcomes,
// profile labels, treatment labels, or trial indices.
type Phase5ExecutionInput struct {
	Source           string
	FocusRegionIndex uint32
	OutputName       string
}

type Phase5CapacityKind string

const (
	Phase5AnalyzerCapacity Phase5CapacityKind = "analyzer"
	Phase5ScratchCapacity  Phase5CapacityKind = "scratch"
	Phase5FinalCapacity    Phase5CapacityKind = "final"
)

type Phase5FinalizationGap interface {
	Wait(context.Context) error
}

type Phase5ExecutionSnapshot struct {
	DecisionSHA256               string
	PatchSHA256                  string
	CapsuleSHA256                string
	SelectionSHA256              string
	DerivedASTSHA256             string
	ActualDisposition            string
	ActualOutcome                string
	ResultSHA256                 string
	ErrorClass                   string
	ErrorMessageSHA256           string
	TracebackSHA256              string
	LogsSHA256                   string
	AnalyzerSessionCount         uint32
	AnalyzerRuntimeInitCount     uint32
	ScratchGuestExecutions       uint32
	ScratchRuntimeInitCount      uint32
	FormalGuestExecutions        uint32
	FinalRuntimeInitCount        uint32
	HelperClaimCount             uint32
	CapsuleConsumedCount         uint32
	CapsuleRejectedClaimCount    uint32
	CapsuleDiscardedCount        uint32
	CapsuleBytes                 uint32
	LogicalCallCount             uint32
	OrphanedPhysicalCount        uint32
	DiscardedCapacityBytes       uint64
	AuthorityTerminalDisposition string
	WorkspaceTerminalDisposition string
}

type Phase5CoordinateOperations interface {
	Provision(context.Context, Phase5CapacityKind) error
	BeginFinalizationGap(context.Context, time.Duration) (Phase5FinalizationGap, error)
	Analyze(context.Context, Phase5ExecutionInput) error
	EmitPatch(context.Context, Phase5ExecutionInput) error
	ExecuteScratch(context.Context, Phase5ExecutionInput) error
	SealCapsule(context.Context) error
	ValidateSelection(context.Context, Phase5ExecutionInput) error
	CompileDerived(context.Context, Phase5ExecutionInput) error
	ExecuteOriginal(context.Context, Phase5ExecutionInput) error
	ExecuteDerived(context.Context, Phase5ExecutionInput) error
	Teardown(context.Context) error
	Snapshot() Phase5ExecutionSnapshot
}

type Phase5RunnerConfig struct {
	HarnessIdentity string
	Clock           func() time.Time
	ResidentBytes   func() uint64
}

func RunPhase5Coordinate(ctx context.Context, coordinate Phase5CampaignCoordinate, config Phase5RunnerConfig, operations Phase5CoordinateOperations) ([]byte, error) {
	if ctx == nil || operations == nil || !digestPattern.MatchString(config.HarnessIdentity) || config.Clock == nil || config.ResidentBytes == nil || !phase5CampaignContains(coordinate) {
		return nil, errors.New("invalid phase 5 coordinate runner input")
	}
	candidate, found := phase5CaseByID(coordinate.CaseID)
	if !found || !candidate.EconomicsEligible {
		return nil, errors.New("phase 5 runner accepts frozen economics cases only")
	}
	recorder, err := NewPhase5StageRecorder(config.Clock)
	if err != nil {
		return nil, err
	}
	input := Phase5ExecutionInput{Source: candidate.Source, FocusRegionIndex: candidate.FocusRegionIndex, OutputName: candidate.OutputName}
	provisionDisposition := Phase5StageMeasured
	if coordinate.Profile == "preprovisioned_equivalent_capacity" {
		provisionDisposition = Phase5StagePreclock
	}
	runStage := func(name, disposition string, operation func() error) error {
		token, startErr := recorder.Start(name, disposition)
		if startErr != nil {
			return startErr
		}
		operationErr := operation()
		return errors.Join(operationErr, recorder.End(token))
	}
	provision := func(kind Phase5CapacityKind) error {
		stage := "final_guest_provision"
		switch kind {
		case Phase5AnalyzerCapacity:
			stage = "analyzer_provision"
		case Phase5ScratchCapacity:
			stage = "scratch_guest_provision"
		case Phase5FinalCapacity:
		default:
			return errors.New("invalid phase 5 capacity kind")
		}
		return runStage(stage, provisionDisposition, func() error { return operations.Provision(ctx, kind) })
	}
	kinds := []Phase5CapacityKind{Phase5FinalCapacity}
	if coordinate.Treatment == "prepared_region_derived" {
		kinds = []Phase5CapacityKind{Phase5AnalyzerCapacity, Phase5ScratchCapacity, Phase5FinalCapacity}
	}
	if provisionDisposition == Phase5StagePreclock {
		for _, kind := range kinds {
			if err := provision(kind); err != nil {
				return nil, errors.Join(err, operations.Teardown(ctx))
			}
		}
	}
	if err := recorder.MarkCriticalPathStart(); err != nil {
		return nil, errors.Join(err, operations.Teardown(ctx))
	}
	gapToken, err := recorder.Start("finalization_gap", Phase5StageMeasured)
	if err != nil {
		return nil, errors.Join(err, operations.Teardown(ctx))
	}
	gap, err := operations.BeginFinalizationGap(ctx, time.Duration(candidate.FinalizationGapMillis)*time.Millisecond)
	if err != nil || gap == nil {
		return nil, errors.Join(err, recorder.End(gapToken), operations.Teardown(ctx))
	}
	gapOpen := true
	drainGap := func() error {
		if !gapOpen {
			return nil
		}
		gapOpen = false
		return errors.Join(gap.Wait(ctx), recorder.End(gapToken))
	}
	cleanupFailure := func(cause error) ([]byte, error) {
		gapErr := drainGap()
		teardownErr := runStage("teardown", Phase5StageMeasured, func() error { return operations.Teardown(ctx) })
		return nil, errors.Join(cause, gapErr, teardownErr)
	}
	if provisionDisposition == Phase5StageMeasured {
		for _, kind := range kinds {
			if err := provision(kind); err != nil {
				return cleanupFailure(err)
			}
		}
	}
	if coordinate.Treatment == "prepared_region_derived" {
		steps := []struct {
			name string
			call func() error
		}{
			{name: "analysis", call: func() error { return operations.Analyze(ctx, input) }},
			{name: "patch_emission", call: func() error { return operations.EmitPatch(ctx, input) }},
			{name: "scratch_execution", call: func() error { return operations.ExecuteScratch(ctx, input) }},
			{name: "capsule_seal_transport", call: func() error { return operations.SealCapsule(ctx) }},
		}
		for _, step := range steps {
			if err := runStage(step.name, Phase5StageMeasured, step.call); err != nil {
				return cleanupFailure(err)
			}
		}
	}
	if err := drainGap(); err != nil {
		return cleanupFailure(err)
	}
	if coordinate.Treatment == "prepared_region_derived" {
		if err := runStage("final_selection_validation", Phase5StageMeasured, func() error { return operations.ValidateSelection(ctx, input) }); err != nil {
			return cleanupFailure(err)
		}
		if err := runStage("final_patch_compile_load", Phase5StageMeasured, func() error { return operations.CompileDerived(ctx, input) }); err != nil {
			return cleanupFailure(err)
		}
		if err := runStage("final_execution", Phase5StageMeasured, func() error { return operations.ExecuteDerived(ctx, input) }); err != nil {
			return cleanupFailure(err)
		}
	} else {
		if err := runStage("final_execution", Phase5StageMeasured, func() error { return operations.ExecuteOriginal(ctx, input) }); err != nil {
			return cleanupFailure(err)
		}
	}
	if err := runStage("teardown", Phase5StageMeasured, func() error { return operations.Teardown(ctx) }); err != nil {
		return nil, err
	}
	timeline, err := recorder.Finalize()
	if err != nil {
		return nil, err
	}
	snapshot := operations.Snapshot()
	record := Phase5TrialRecord{
		SchemaVersion: Phase5TrialRecordSchemaVersion, StudyID: Phase5StudyID, CaseMatrixIdentity: Phase5CaseMatrixIdentity, PreregistrationIdentity: Phase5PreregistrationIdentity,
		HarnessIdentity: config.HarnessIdentity, GuestArtifactSHA256: Phase5GuestArtifactSHA256, RunID: phase5OpaqueRunID(coordinate), Profile: coordinate.Profile, CaseID: coordinate.CaseID, Treatment: coordinate.Treatment, TrialIndex: coordinate.TrialIndex,
		SourceSHA256: candidate.SourceSHA256, RegionSourceSHA256: candidate.RegionSourceSHA256, DecisionSHA256: snapshot.DecisionSHA256, PatchSHA256: snapshot.PatchSHA256, CapsuleSHA256: snapshot.CapsuleSHA256, SelectionSHA256: snapshot.SelectionSHA256, DerivedASTSHA256: snapshot.DerivedASTSHA256,
		ExpectedDisposition: candidate.ExpectedDisposition, ExpectedOutcome: candidate.ExpectedOutcome, ActualDisposition: snapshot.ActualDisposition, ActualOutcome: snapshot.ActualOutcome, ResultSHA256: snapshot.ResultSHA256, ErrorClass: snapshot.ErrorClass, ErrorMessageSHA256: snapshot.ErrorMessageSHA256, TracebackSHA256: snapshot.TracebackSHA256, LogsSHA256: snapshot.LogsSHA256,
		CriticalPathStartedOffsetNanos: timeline.CriticalPathStartedOffsetNanos, TrialEndedOffsetNanos: timeline.TrialEndedOffsetNanos, TotalCriticalPathNanos: timeline.TotalCriticalPathNanos, UnattributedCriticalPathNanos: timeline.UnattributedCriticalPathNanos, Stages: timeline.Stages,
		AnalyzerSessionCount: snapshot.AnalyzerSessionCount, AnalyzerRuntimeInitCount: snapshot.AnalyzerRuntimeInitCount, ScratchGuestExecutions: snapshot.ScratchGuestExecutions, ScratchRuntimeInitCount: snapshot.ScratchRuntimeInitCount, FormalGuestExecutions: snapshot.FormalGuestExecutions, FinalRuntimeInitCount: snapshot.FinalRuntimeInitCount,
		HelperClaimCount: snapshot.HelperClaimCount, CapsuleConsumedCount: snapshot.CapsuleConsumedCount, CapsuleRejectedClaimCount: snapshot.CapsuleRejectedClaimCount, CapsuleDiscardedCount: snapshot.CapsuleDiscardedCount, CapsuleBytes: snapshot.CapsuleBytes, LogicalCallCount: snapshot.LogicalCallCount, OrphanedPhysicalCount: snapshot.OrphanedPhysicalCount,
		PeakResidentMemoryBytes: config.ResidentBytes(), DiscardedCapacityBytes: snapshot.DiscardedCapacityBytes, AuthorityTerminalDisposition: snapshot.AuthorityTerminalDisposition, WorkspaceTerminalDisposition: snapshot.WorkspaceTerminalDisposition,
	}
	return EncodePhase5TrialRecord(record)
}

func phase5CaseByID(id string) (Phase5Case, bool) {
	for _, candidate := range Phase5Cases() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Phase5Case{}, false
}

type Phase5JSONLSink struct {
	mu              sync.Mutex
	writer          io.Writer
	harnessIdentity string
	seen            map[string]struct{}
}

func NewPhase5JSONLSink(writer io.Writer, harnessIdentity string) (*Phase5JSONLSink, error) {
	if writer == nil || !digestPattern.MatchString(harnessIdentity) {
		return nil, errors.New("invalid phase 5 JSONL sink")
	}
	return &Phase5JSONLSink{writer: writer, harnessIdentity: harnessIdentity, seen: map[string]struct{}{}}, nil
}

func (sink *Phase5JSONLSink) Append(raw []byte) error {
	if sink == nil {
		return errors.New("nil phase 5 JSONL sink")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	record, err := DecodePhase5TrialRecord(raw, sink.harnessIdentity)
	if err != nil {
		return err
	}
	if _, duplicate := sink.seen[record.RunID]; duplicate {
		return ErrPhase5DuplicateTrial
	}
	line := append(append([]byte(nil), raw...), '\n')
	written, err := sink.writer.Write(line)
	if err != nil || written != len(line) {
		return errors.Join(err, fmt.Errorf("short phase 5 JSONL write: %d/%d", written, len(line)))
	}
	sink.seen[record.RunID] = struct{}{}
	return nil
}
