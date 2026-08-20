package semanticspeculation

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakePhase5Gap struct {
	adapter *fakePhase5Operations
	start   time.Time
	gap     time.Duration
}

func (gap *fakePhase5Gap) Wait(context.Context) error {
	gap.adapter.call("wait_gap", 0)
	elapsed := gap.adapter.clock.Now().Sub(gap.start)
	if elapsed < gap.gap {
		gap.adapter.clock.Advance(gap.gap - elapsed)
	}
	return nil
}

type fakePhase5Operations struct {
	clock        *phase5FakeClock
	mu           sync.Mutex
	calls        []string
	derived      bool
	failAnalysis bool
	resultSHA    string
}

func (adapter *fakePhase5Operations) call(name string, duration time.Duration) {
	adapter.mu.Lock()
	adapter.calls = append(adapter.calls, name)
	adapter.mu.Unlock()
	adapter.clock.Advance(duration)
}
func (adapter *fakePhase5Operations) Provision(_ context.Context, kind Phase5CapacityKind) error {
	adapter.call("provision_"+string(kind), 10*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) BeginFinalizationGap(_ context.Context, gap time.Duration) (Phase5FinalizationGap, error) {
	adapter.call("begin_gap", 0)
	return &fakePhase5Gap{adapter: adapter, start: adapter.clock.Now(), gap: gap}, nil
}
func (adapter *fakePhase5Operations) Analyze(context.Context, Phase5ExecutionInput) error {
	adapter.call("analysis", 20*time.Nanosecond)
	if adapter.failAnalysis {
		return errors.New("fake analysis failure")
	}
	return nil
}
func (adapter *fakePhase5Operations) EmitPatch(context.Context, Phase5ExecutionInput) error {
	adapter.call("patch_emission", 5*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) ExecuteScratch(context.Context, Phase5ExecutionInput) error {
	adapter.call("scratch_execution", 30*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) SealCapsule(context.Context) error {
	adapter.call("capsule_seal_transport", 5*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) ValidateSelection(context.Context, Phase5ExecutionInput) error {
	adapter.call("final_selection_validation", 5*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) CompileDerived(context.Context, Phase5ExecutionInput) error {
	adapter.call("final_patch_compile_load", 7*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) ExecuteOriginal(context.Context, Phase5ExecutionInput) error {
	adapter.call("execute_original", 10*time.Nanosecond)
	adapter.derived = false
	adapter.resultSHA = syntheticDigest([]byte("65"))
	return nil
}
func (adapter *fakePhase5Operations) ExecuteDerived(context.Context, Phase5ExecutionInput) error {
	adapter.call("execute_derived", 10*time.Nanosecond)
	adapter.derived = true
	adapter.resultSHA = syntheticDigest([]byte("65"))
	return nil
}
func (adapter *fakePhase5Operations) Teardown(context.Context) error {
	adapter.call("teardown", 3*time.Nanosecond)
	return nil
}
func (adapter *fakePhase5Operations) Snapshot() Phase5ExecutionSnapshot {
	value := Phase5ExecutionSnapshot{
		ActualDisposition: "ready_consumed", ActualOutcome: "success", ResultSHA256: adapter.resultSHA, LogsSHA256: syntheticDigest(nil),
		FormalGuestExecutions: 1, FinalRuntimeInitCount: 1, AuthorityTerminalDisposition: "none", WorkspaceTerminalDisposition: "unmounted",
	}

	if adapter.derived {
		value.DecisionSHA256 = syntheticDigest([]byte("decision"))
		value.PatchSHA256 = syntheticDigest([]byte("patch"))
		value.CapsuleSHA256 = syntheticDigest([]byte("capsule"))
		value.SelectionSHA256 = syntheticDigest([]byte("selection"))
		value.DerivedASTSHA256 = syntheticDigest([]byte("derived-ast"))
		value.AnalyzerSessionCount = 1
		value.AnalyzerRuntimeInitCount = 1
		value.ScratchGuestExecutions = 1
		value.ScratchRuntimeInitCount = 1
		value.FormalGuestExecutions = 2
		value.HelperClaimCount = 1
		value.CapsuleConsumedCount = 1
		value.CapsuleBytes = 2
	}
	return value
}
func (adapter *fakePhase5Operations) Calls() []string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return append([]string(nil), adapter.calls...)
}

func TestPhase5ExecutionInputExposesNoCoordinateOrExpectedOutcome(t *testing.T) {
	typeOf := reflect.TypeOf(Phase5ExecutionInput{})
	fields := make([]string, 0, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		fields = append(fields, typeOf.Field(index).Name)
	}
	if expected := []string{"Source", "FocusRegionIndex", "OutputName"}; !reflect.DeepEqual(fields, expected) {
		t.Fatalf("execution input fields=%v", fields)
	}
}

func TestPhase5CoordinateRunnerEmitsCanonicalFourCellRecords(t *testing.T) {
	harness := syntheticDigest([]byte("phase5-fake-harness"))
	var output bytes.Buffer
	sink, err := NewPhase5JSONLSink(&output, harness)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range phase5Profiles {
		for _, treatment := range phase5Treatments {
			clock := newPhase5FakeClock()
			adapter := &fakePhase5Operations{clock: clock}
			coordinate := Phase5CampaignCoordinate{Profile: profile, CaseID: "scalar_add_64_gap250", Treatment: treatment, TrialIndex: 1}
			raw, err := RunPhase5Coordinate(context.Background(), coordinate, Phase5RunnerConfig{HarnessIdentity: harness, Clock: clock.Now, ResidentBytes: func() uint64 { return 4096 }}, adapter)
			if err != nil {
				t.Fatalf("%s/%s: %v", profile, treatment, err)
			}
			record, err := DecodePhase5TrialRecord(raw, harness)
			if err != nil {
				t.Fatal(err)
			}
			stages := map[string]Phase5StageObservation{}
			for _, stage := range record.Stages {
				stages[stage.Name] = stage
			}
			provisionDisposition := Phase5StageMeasured
			if profile == "preprovisioned_equivalent_capacity" {
				provisionDisposition = Phase5StagePreclock
			}
			if stages["final_guest_provision"].Disposition != provisionDisposition || stages["finalization_gap"].DurationNanos < 250_000_000 {
				t.Fatalf("%s/%s stages=%+v", profile, treatment, stages)
			}
			if treatment == "prepared_region_derived" {
				if stages["analyzer_provision"].Disposition != provisionDisposition || stages["scratch_guest_provision"].Disposition != provisionDisposition || record.HelperClaimCount != 1 {
					t.Fatalf("derived record=%+v", record)
				}
			} else if stages["analysis"].Disposition != Phase5StageNotApplicable || record.HelperClaimCount != 0 {
				t.Fatalf("original record=%+v", record)
			}
			expectedCalls := []string{"begin_gap", "provision_final", "wait_gap", "execute_original", "teardown"}
			if profile == "preprovisioned_equivalent_capacity" {
				expectedCalls = []string{"provision_final", "begin_gap", "wait_gap", "execute_original", "teardown"}
			}
			if treatment == "prepared_region_derived" {
				expectedCalls = []string{"begin_gap", "provision_analyzer", "provision_scratch", "provision_final", "analysis", "patch_emission", "scratch_execution", "capsule_seal_transport", "wait_gap", "final_selection_validation", "final_patch_compile_load", "execute_derived", "teardown"}
				if profile == "preprovisioned_equivalent_capacity" {
					expectedCalls = []string{"provision_analyzer", "provision_scratch", "provision_final", "begin_gap", "analysis", "patch_emission", "scratch_execution", "capsule_seal_transport", "wait_gap", "final_selection_validation", "final_patch_compile_load", "execute_derived", "teardown"}
				}
			}
			if calls := adapter.Calls(); !reflect.DeepEqual(calls, expectedCalls) {
				t.Fatalf("%s/%s calls=%v expected=%v", profile, treatment, calls, expectedCalls)
			}
			if err := sink.Append(raw); err != nil {
				t.Fatal(err)
			}
			if err := sink.Append(raw); !errors.Is(err, ErrPhase5DuplicateTrial) {
				t.Fatalf("duplicate err=%v", err)
			}
		}
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("JSONL lines=%d", len(lines))
	}
	for _, line := range lines {
		if _, err := DecodePhase5TrialRecord(line, harness); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhase5CoordinateRunnerDrainsGapAndTeardownOnDerivedFailure(t *testing.T) {
	clock := newPhase5FakeClock()
	adapter := &fakePhase5Operations{clock: clock, failAnalysis: true}
	coordinate := Phase5CampaignCoordinate{Profile: "cold_end_to_end", CaseID: "scalar_add_64_gap250", Treatment: "prepared_region_derived", TrialIndex: 1}
	_, err := RunPhase5Coordinate(context.Background(), coordinate, Phase5RunnerConfig{HarnessIdentity: syntheticDigest([]byte("harness")), Clock: clock.Now, ResidentBytes: func() uint64 { return 1 }}, adapter)
	if err == nil {
		t.Fatal("failed analysis produced a record")
	}
	calls := adapter.Calls()
	if calls[len(calls)-2] != "wait_gap" || calls[len(calls)-1] != "teardown" {
		t.Fatalf("failure lifecycle=%v", calls)
	}
}
