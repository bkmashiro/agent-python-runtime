package passpipeline_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/passpipeline"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

func TestCurrentPassesRouteThroughTypedStagesWithoutChangingRegistrationIdentity(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	overlayEntry, err := passpipeline.CurrentEntry(overlay, true)
	if err != nil || overlayEntry.Stage != passpipeline.StagePrefixOverlay {
		t.Fatalf("overlay entry=%+v err=%v", overlayEntry, err)
	}
	patchEntry, err := passpipeline.CurrentEntry(patch, true)
	if err != nil || patchEntry.Stage != passpipeline.StageWholeProgramPatch {
		t.Fatalf("patch entry=%+v err=%v", patchEntry, err)
	}
	pipeline, err := passpipeline.New([]passpipeline.Entry{overlayEntry, patchEntry}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if pipeline.AllOff() {
		t.Fatal("enabled current passes reported all-off")
	}
	firstInput := input(overlay, passpipeline.OutcomeApplied, "")
	firstInput.LogicalEvents = 1
	firstInput.PhysicalEvents = 1
	firstInput.WorkspaceDisposition = "not_owned"
	first, err := pipeline.RecordPrefixOverlay(firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := input(patch, passpipeline.OutcomeApplied, "")
	secondInput.DerivedSourceSHA256 = digest('d')
	secondInput.DerivedASTSHA256 = digest('e')
	secondInput.Usage.DerivedSourceBytes = secondInput.Usage.OriginalSourceBytes + 12
	secondInput.Usage.DerivedASTNodes = secondInput.Usage.OriginalASTNodes + 3
	second, err := pipeline.RecordWholeProgramPatch(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stage != passpipeline.StagePrefixOverlay || second.Stage != passpipeline.StageWholeProgramPatch ||
		first.RegistrationSHA256 != overlay.IdentitySHA256() || second.RegistrationSHA256 != patch.IdentitySHA256() ||
		first.PassOrder != 1 || second.PassOrder != 2 || first.Outcome != passpipeline.OutcomeApplied || second.Outcome != passpipeline.OutcomeApplied {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	raw, err := json.Marshal(pipeline.Records())
	if err != nil || !bytes.Contains(raw, []byte(`"schema_version":"pysolate.stage-aware-pass-outcome.v2"`)) ||
		!bytes.Contains(raw, []byte(`"registration_sha256"`)) || !bytes.Contains(raw, []byte(`"logical_events":1`)) ||
		!bytes.Contains(raw, []byte(`"workspace_disposition":"not_owned"`)) || bytes.Contains(raw, []byte("source_body")) || bytes.Contains(raw, []byte("workspace_body")) {
		t.Fatalf("body-safe outcome encoding=%s err=%v", raw, err)
	}
}

func TestAnalyzerFreePassStagesRecordWithoutInventingSourceAnalysis(t *testing.T) {
	future, err := passregistration.CapabilityFutureProjectionDefinition().Register("", digest('b'))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := passregistration.PreparedValueBindingDefinition().Register("", digest('c'))
	if err != nil {
		t.Fatal(err)
	}
	runtimePass, err := passregistration.RuntimeOptimizationDefinitions()[0].Register("", digest('a'))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := passpipeline.New([]passpipeline.Entry{
		{Registration: future, Stage: passpipeline.StagePlanProjection, Enabled: true},
		{Registration: prepared, Stage: passpipeline.StageRunBinding, Enabled: true},
		{Registration: runtimePass, Stage: passpipeline.StageRuntimeLowering, Enabled: true},
	}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	projection, err := pipeline.RecordPlanProjection(passpipeline.RecordInput{
		PassName: future.Name(), Outcome: passpipeline.OutcomeApplied,
		Bindings: map[passregistration.Binding]string{
			passregistration.CapabilityPlanSHA256: digest('d'),
			passregistration.PassConfigSHA256:     future.ConfigSHA256(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := pipeline.RecordRunBinding(passpipeline.RecordInput{
		PassName: prepared.Name(), Outcome: passpipeline.OutcomeApplied,
		Bindings: map[passregistration.Binding]string{
			passregistration.RegionID:          digest('e'),
			passregistration.RunIdentitySHA256: digest('f'),
			passregistration.PassConfigSHA256:  prepared.ConfigSHA256(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	lowering, err := pipeline.RecordRuntimeLowering(passpipeline.RecordInput{
		PassName: runtimePass.Name(), Outcome: passpipeline.OutcomeApplied,
		Bindings: map[passregistration.Binding]string{
			passregistration.RuntimeConfigSHA256: digest('b'),
			passregistration.PassConfigSHA256:    runtimePass.ConfigSHA256(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Stage != passpipeline.StagePlanProjection || binding.Stage != passpipeline.StageRunBinding ||
		lowering.Stage != passpipeline.StageRuntimeLowering || projection.OriginalSourceSHA256 != "" ||
		binding.OriginalASTSHA256 != "" || lowering.OriginalSourceSHA256 != "" {
		t.Fatalf("projection=%+v binding=%+v lowering=%+v", projection, binding, lowering)
	}
	secondRun := passpipeline.RecordInput{
		PassName: prepared.Name(), Outcome: passpipeline.OutcomeApplied,
		Bindings: map[passregistration.Binding]string{
			passregistration.RegionID:          digest('e'),
			passregistration.RunIdentitySHA256: digest('a'),
			passregistration.PassConfigSHA256:  prepared.ConfigSHA256(),
		},
	}
	if _, err := pipeline.RecordRunBinding(secondRun); err != nil {
		t.Fatalf("fresh Run binding collided with prior Run: %v", err)
	}
}

func TestAllOffAndPerPassControlsFailClosed(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	allOff, err := passpipeline.New([]passpipeline.Entry{
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay},
		{Registration: patch, Stage: passpipeline.StageWholeProgramPatch},
	}, passpipeline.DefaultLimits())
	if err != nil || !allOff.AllOff() {
		t.Fatalf("allOff=%v err=%v", allOff.AllOff(), err)
	}
	if _, err := allOff.RecordPrefixOverlay(input(overlay, passpipeline.OutcomeApplied, "")); !errors.Is(err, passpipeline.ErrAllOff) || len(allOff.Records()) != 0 {
		t.Fatalf("record err=%v records=%+v", err, allOff.Records())
	}

	mixed, err := passpipeline.New([]passpipeline.Entry{
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true},
		{Registration: patch, Stage: passpipeline.StageWholeProgramPatch},
	}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := mixed.RecordWholeProgramPatch(input(patch, passpipeline.OutcomeApplied, ""))
	if err != nil || rejected.Outcome != passpipeline.OutcomeRejected || rejected.RejectionReason != passpipeline.RejectPassDisabled {
		t.Fatalf("rejected=%+v err=%v", rejected, err)
	}
}

func TestStageSpecificEntryPointsRejectConsumerAndOutcomeConfusion(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	pipeline, err := passpipeline.New([]passpipeline.Entry{
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true},
		{Registration: patch, Stage: passpipeline.StageWholeProgramPatch, Enabled: true},
	}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.RecordWholeProgramPatch(input(overlay, passpipeline.OutcomeApplied, "")); !errors.Is(err, passpipeline.ErrStageMismatch) {
		t.Fatalf("overlay entered patch stage: %v", err)
	}
	prepared := input(patch, passpipeline.OutcomePreparedAwaitingFinal, "")
	if _, err := pipeline.RecordWholeProgramPatch(prepared); !errors.Is(err, passpipeline.ErrInvalidOutcome) {
		t.Fatalf("whole patch accepted awaiting-final: %v", err)
	}
	if _, err := pipeline.RecordPrefixOverlay(input(overlay, passpipeline.OutcomeRejected, "free form secret reason")); !errors.Is(err, passpipeline.ErrInvalidRecord) {
		t.Fatalf("accepted free-form reason: %v", err)
	}
}

func TestPipelineRejectsOutcomeFieldsThatContradictDisposition(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	pipeline, err := passpipeline.New([]passpipeline.Entry{
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true},
		{Registration: patch, Stage: passpipeline.StageWholeProgramPatch, Enabled: true},
	}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	overlayDerived := input(overlay, passpipeline.OutcomeApplied, "")
	overlayDerived.DerivedSourceSHA256 = digest('d')
	overlayDerived.DerivedASTSHA256 = digest('e')
	if _, err := pipeline.RecordPrefixOverlay(overlayDerived); !errors.Is(err, passpipeline.ErrInvalidRecord) {
		t.Fatalf("overlay derived source err=%v", err)
	}

	for name, mutate := range map[string]func(*passpipeline.RecordInput){
		"rejected result": func(value *passpipeline.RecordInput) {
			value.ResultSHA256 = digest('f')
		},
		"rejected logical work": func(value *passpipeline.RecordInput) {
			value.LogicalEvents = 7
			value.PhysicalEvents = 9
		},
		"rejected preparation": func(value *passpipeline.RecordInput) {
			value.Usage.PreparationBytes = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := input(patch, passpipeline.OutcomeRejected, "not_admitted")
			mutate(&value)
			if _, err := pipeline.RecordWholeProgramPatch(value); !errors.Is(err, passpipeline.ErrInvalidRecord) {
				t.Fatalf("err=%v", err)
			}
		})
	}

	discarded := input(overlay, passpipeline.OutcomeDiscarded, "orphaned")
	discarded.LogicalEvents = 1
	if _, err := pipeline.RecordPrefixOverlay(discarded); !errors.Is(err, passpipeline.ErrInvalidRecord) {
		t.Fatalf("discarded logical claim err=%v", err)
	}

	valid := input(overlay, passpipeline.OutcomeApplied, "")
	if _, err := pipeline.RecordPrefixOverlay(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.RecordPrefixOverlay(valid); !errors.Is(err, passpipeline.ErrDuplicateOutcome) {
		t.Fatalf("duplicate outcome err=%v", err)
	}
	if len(pipeline.Records()) != 1 {
		t.Fatalf("duplicate outcome appended records=%d", len(pipeline.Records()))
	}
}

func TestPipelineBoundsRejectBeforeRecording(t *testing.T) {
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	limits := passpipeline.DefaultLimits()
	pipeline, err := passpipeline.New([]passpipeline.Entry{{Registration: patch, Stage: passpipeline.StageWholeProgramPatch, Enabled: true}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*passpipeline.RecordInput){
		"source growth": func(value *passpipeline.RecordInput) {
			value.Usage.DerivedSourceBytes = value.Usage.OriginalSourceBytes + limits.MaxSourceGrowthBytes + 1
		},
		"AST growth": func(value *passpipeline.RecordInput) {
			value.Usage.DerivedASTNodes = value.Usage.OriginalASTNodes + limits.MaxASTGrowthNodes + 1
		},
		"preparation": func(value *passpipeline.RecordInput) { value.Usage.PreparationBytes = limits.MaxPreparationBytes + 1 },
		"reanalysis":  func(value *passpipeline.RecordInput) { value.Usage.Reanalyses = limits.MaxReanalyses + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := input(patch, passpipeline.OutcomeApplied, "")
			value.DerivedSourceSHA256 = digest('d')
			value.DerivedASTSHA256 = digest('e')
			mutate(&value)
			if _, err := pipeline.RecordWholeProgramPatch(value); !errors.Is(err, passpipeline.ErrBoundsExceeded) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if len(pipeline.Records()) != 0 {
		t.Fatalf("overflow produced records: %+v", pipeline.Records())
	}
}

func TestPipelineRejectsPassCountOverflowDuplicateBindingDriftAndLimitWidening(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	limits := passpipeline.DefaultLimits()
	limits.MaxPasses = 0
	if _, err := passpipeline.New([]passpipeline.Entry{{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true}}, limits); !errors.Is(err, passpipeline.ErrBoundsExceeded) {
		t.Fatalf("pass limit err=%v", err)
	}
	widened := passpipeline.DefaultLimits()
	widened.MaxPreparationBytes++
	if _, err := passpipeline.New(nil, widened); !errors.Is(err, passpipeline.ErrBoundsExceeded) {
		t.Fatalf("widened limits err=%v", err)
	}
	if _, err := passpipeline.New([]passpipeline.Entry{
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true},
		{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: false},
	}, passpipeline.DefaultLimits()); !errors.Is(err, passpipeline.ErrDuplicatePass) {
		t.Fatalf("duplicate err=%v", err)
	}
	patch := registration(t, passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion, passregistration.ExecutionPatch, passregistration.PatchBindings())
	if _, err := passpipeline.New([]passpipeline.Entry{{Registration: patch, Stage: passpipeline.StageHybridPreparePatch, Enabled: true}}, passpipeline.DefaultLimits()); !errors.Is(err, passpipeline.ErrStageMismatch) {
		t.Fatalf("current stage drift err=%v", err)
	}
	pipeline, err := passpipeline.New([]passpipeline.Entry{{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true}}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	value := input(overlay, passpipeline.OutcomeApplied, "")
	delete(value.Bindings, passregistration.AnalysisSHA256)
	if _, err := pipeline.RecordPrefixOverlay(value); !errors.Is(err, passpipeline.ErrBindingMismatch) {
		t.Fatalf("binding drift err=%v", err)
	}
	value = input(overlay, passpipeline.OutcomeApplied, "")
	value.Bindings[passregistration.PassConfigSHA256] = digest('c')
	if _, err := pipeline.RecordPrefixOverlay(value); !errors.Is(err, passpipeline.ErrBindingMismatch) {
		t.Fatalf("registration binding drift err=%v", err)
	}
}

func TestPipelineRecordsAreDefensiveAndConcurrentOrdersAreGapFree(t *testing.T) {
	overlay := registration(t, passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion, passregistration.OverlayOnly, passregistration.OverlayBindings())
	pipeline, err := passpipeline.New([]passpipeline.Entry{{Registration: overlay, Stage: passpipeline.StagePrefixOverlay, Enabled: true}}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			value := input(overlay, passpipeline.OutcomeApplied, "")
			value.Bindings[passregistration.OccurrenceID] = fmt.Sprintf("occurrence-%016x", index)
			_, recordErr := pipeline.RecordPrefixOverlay(value)
			errorsSeen <- recordErr
		}(index)
	}
	wait.Wait()
	close(errorsSeen)
	for recordErr := range errorsSeen {
		if recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	records := pipeline.Records()
	if len(records) != count {
		t.Fatalf("records=%d", len(records))
	}
	for index, record := range records {
		if record.PassOrder != uint32(index+1) {
			t.Fatalf("record[%d]=%+v", index, record)
		}
	}
	records[0].Bindings[passregistration.SourceSHA256] = "mutated"
	if reflect.DeepEqual(records, pipeline.Records()) {
		t.Fatal("records alias pipeline state")
	}
}

func registration(t *testing.T, name passregistration.Name, version string, consumer passregistration.Consumer, bindings []passregistration.Binding) passregistration.Registration {
	t.Helper()
	value, err := passregistration.New(name, version, digest('a'), digest('b'), consumer, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func input(registration passregistration.Registration, outcome passpipeline.Outcome, reason passpipeline.RejectionReason) passpipeline.RecordInput {
	bindings := make(map[passregistration.Binding]string)
	for index, binding := range registration.RequiredBindings() {
		switch binding {
		case passregistration.SourceSHA256:
			bindings[binding] = digest('1')
		case passregistration.ASTSHA256:
			bindings[binding] = digest('2')
		case passregistration.AnalyzerSHA256:
			bindings[binding] = registration.AnalyzerSHA256()
		case passregistration.PassConfigSHA256:
			bindings[binding] = registration.ConfigSHA256()
		case passregistration.OccurrenceID:
			bindings[binding] = "occurrence-0000000000000001"
		case passregistration.RegionID:
			bindings[binding] = digest('f')
		default:
			bindings[binding] = digest(byte('1' + index%8))
		}
	}
	return passpipeline.RecordInput{
		PassName: registration.Name(), Outcome: outcome, RejectionReason: reason,
		OriginalSourceSHA256: digest('1'), OriginalASTSHA256: digest('2'), Bindings: bindings,
		Usage: passpipeline.Usage{OriginalSourceBytes: 100, DerivedSourceBytes: 100, OriginalASTNodes: 20, DerivedASTNodes: 20},
	}
}

func digest(character byte) string { return "sha256:" + fmt.Sprintf("%064c", character) }
