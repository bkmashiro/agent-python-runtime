package passplugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

func TestRegistryDispatchesSourcePatchAndKeepsExistingAdapters(t *testing.T) {
	semantic, err := passregistration.New(
		passregistration.SemanticPreDispatch, passregistration.SemanticPreDispatchVersion,
		passregistration.SemanticAnalyzerSHA256, digestFor('a'), passregistration.OverlayOnly,
		passregistration.OverlayBindings(),
	)
	if err != nil {
		t.Fatal(err)
	}
	semanticAdapter, err := AdaptExisting(semantic)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := passregistration.New(
		passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion,
		passregistration.SemanticAnalyzerSHA256, digestFor('b'), passregistration.ExecutionPatch,
		passregistration.PatchBindings(),
	)
	if err != nil {
		t.Fatal(err)
	}
	preparedAdapter, err := AdaptExisting(prepared)
	if err != nil {
		t.Fatal(err)
	}
	cse, err := sourcepatch.NewPureScalarCSE(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	fold, err := sourcepatch.NewPureScalarFold(passregistration.SemanticAnalyzerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := New(semanticAdapter, preparedAdapter, cse, fold)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Transform(context.Background(), sourcepatch.PureScalarCSEName, nil, "result = 1\n"); err != ErrPluginDisabled {
		t.Fatalf("default-off transform error=%v", err)
	}
	registry, err = registry.Enable(passregistration.SemanticPreDispatch, sourcepatch.PureScalarCSEName)
	if err != nil {
		t.Fatal(err)
	}
	if plugin, ok := registry.Lookup(passregistration.SemanticPreDispatch); !ok || plugin.Registration().Stage() != passregistration.StagePrefixOverlay {
		t.Fatalf("semantic plugin=%v ok=%v", plugin, ok)
	}
	if plugin, ok := registry.Lookup(passregistration.PreparedPureRegion); !ok || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		t.Fatalf("prepared plugin=%v ok=%v", plugin, ok)
	}
	if plugin, ok := registry.Lookup(sourcepatch.PureScalarFoldName); !ok || plugin.Registration().Stage() != passregistration.StageWholeProgramPatch {
		t.Fatalf("fold plugin=%v ok=%v", plugin, ok)
	}
	if _, err := registry.Transform(context.Background(), passregistration.SemanticPreDispatch, nil, "result = 1\n"); err != ErrUnsupportedStage {
		t.Fatalf("semantic transform error=%v", err)
	}
}

func digestFor(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}

func TestUnifiedCatalogRegistersEveryOptimizationDefaultOff(t *testing.T) {
	registry, err := NewUnifiedCatalog(UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: digestFor('a'),
		PreparedNumpyLoadConfigSHA256:   digestFor('b'),
		PreparedPureRegionConfigSHA256:  digestFor('c'),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[passregistration.Name]passregistration.Stage{
		passregistration.SemanticPreDispatch:          passregistration.StagePrefixOverlay,
		passregistration.PreparedPureRegion:           passregistration.StageWholeProgramPatch,
		passregistration.PreparedNumpyLoad:            passregistration.StageHybridPreparePatch,
		passregistration.PreparedValueBinding:         passregistration.StageRunBinding,
		sourcepatch.PureScalarCSEName:                 passregistration.StageWholeProgramPatch,
		sourcepatch.PureScalarFoldName:                passregistration.StageWholeProgramPatch,
		sourcepatch.PLMCapabilityCallsName:            passregistration.StageWholeProgramPatch,
		sourcepatch.DataLocalNumpySumName:             passregistration.StageWholeProgramPatch,
		passregistration.SourceStreamingExecution:     passregistration.StageRuntimeLowering,
		passregistration.StreamedChildFanout:          passregistration.StageRuntimeLowering,
		passregistration.AgentFunctionRetention:       passregistration.StageRuntimeLowering,
		passregistration.AgentFunctionSingleFlight:    passregistration.StageRuntimeLowering,
		passregistration.FreshWorkflowReevaluation:    passregistration.StageRuntimeLowering,
		passregistration.PreparedRuntimeInstantiation: passregistration.StageRuntimeLowering,
		passregistration.PrivateMemoryCOW:             passregistration.StageRuntimeLowering,
		passregistration.ColdIOResidency:              passregistration.StageRuntimeLowering,
		passregistration.SemanticWholeRunReuse:        passregistration.StageRuntimeLowering,
	}
	for name, stage := range expected {
		plugin, ok := registry.Lookup(name)
		if !ok || plugin.Registration().Stage() != stage {
			t.Fatalf("name=%s stage=%s plugin=%v ok=%v", name, stage, plugin, ok)
		}
	}
	if _, err := registry.BindRunValue(passregistration.PreparedValueBinding, "slot-numpy-sum-v1"); err != ErrPluginDisabled {
		t.Fatalf("default-off prepared value error=%v", err)
	}

	registry, err = registry.Enable(passregistration.PreparedValueBinding)
	if err != nil {
		t.Fatal(err)
	}
	prelude, err := registry.BindRunValue(passregistration.PreparedValueBinding, "slot-numpy-sum-v1")
	if err != nil || !strings.Contains(prelude, "prepared_value") || !strings.Contains(prelude, "materialize_slot") {
		t.Fatalf("prelude=%q err=%v", prelude, err)
	}
	if _, err := registry.ProjectPlan(passregistration.PreparedValueBinding, nil); err != ErrUnsupportedStage {
		t.Fatalf("run binding crossed into Plan stage: %v", err)
	}
}

func TestUnifiedCatalogLowersRuntimeOptimizationsToExistingMechanisms(t *testing.T) {
	registry, err := NewUnifiedCatalog(UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: digestFor('a'),
		PreparedNumpyLoadConfigSHA256:   digestFor('b'),
		PreparedPureRegionConfigSHA256:  digestFor('c'),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(
		passregistration.SourceStreamingExecution,
		passregistration.StreamedChildFanout,
		passregistration.AgentFunctionRetention,
		passregistration.AgentFunctionSingleFlight,
		passregistration.FreshWorkflowReevaluation,
		passregistration.PreparedRuntimeInstantiation,
		passregistration.PrivateMemoryCOW,
		passregistration.ColdIOResidency,
		passregistration.SemanticWholeRunReuse,
	)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.LowerMechanisms(runtimeconfig.MechanismSet{})
	if err != nil {
		t.Fatal(err)
	}
	mechanisms := selection.Mechanisms
	if !mechanisms.Streaming || !mechanisms.PrivateWorkspace || !mechanisms.ImmutableBranches || !mechanisms.ChildFanout ||
		!mechanisms.FunctionCache || !mechanisms.SingleFlight || !mechanisms.FreshReevaluation ||
		!mechanisms.PreparedRuntime || !mechanisms.MemoryCOW || !mechanisms.ColdIOContinuation ||
		!mechanisms.SemanticAnalysis || !mechanisms.SemanticReuse {
		t.Fatalf("lowered mechanisms=%+v", mechanisms)
	}
	if len(selection.Passes) != 9 {
		t.Fatalf("lowered passes=%v", selection.Passes)
	}
}

func TestEnablePreservesPriorExplicitSelections(t *testing.T) {
	registry, err := NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(sourcepatch.PureScalarCSEName)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(passregistration.PreparedValueBinding)
	if err != nil {
		t.Fatal(err)
	}
	if !registry.enabled[sourcepatch.PureScalarCSEName] || !registry.enabled[passregistration.PreparedValueBinding] {
		t.Fatalf("explicit selections were not preserved: %#v", registry.enabled)
	}
}

func TestUnifiedCatalogResolvesPassLoweringsAgainstHostAvailability(t *testing.T) {
	registry, err := NewDefaultEnabledCatalog(
		passregistration.PreparedRuntimeInstantiation,
		passregistration.PrivateMemoryCOW,
		passregistration.ColdIOResidency,
	)
	if err != nil {
		t.Fatal(err)
	}
	available := runtimeconfig.MechanismSet{PreparedRuntime: true}
	resolved, evidence, err := registry.ResolveRuntime(runtimeconfig.MechanismSet{}, available)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.PreparedRuntime || resolved.MemoryCOW || resolved.ColdIOContinuation {
		t.Fatalf("resolved=%+v", resolved)
	}
	if evidence.SchemaVersion != RuntimeSelectionEvidenceSchemaVersion || evidence.Mechanisms.Validate() != nil || len(evidence.Passes) != 3 {
		t.Fatalf("evidence=%+v", evidence)
	}
	for _, pass := range evidence.Passes {
		if pass.Name == "" || pass.Version == "" || pass.Stage != passregistration.StageRuntimeLowering || !strings.HasPrefix(pass.RegistrationSHA256, "sha256:") {
			t.Fatalf("pass evidence=%+v", pass)
		}
	}
	if RuntimeSelectionEvidenceSchemaVersion != "pysolate.optimization-pass-selection.v2" {
		t.Fatalf("selection evidence schema=%q", RuntimeSelectionEvidenceSchemaVersion)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"passes":[{"name":`) {
		t.Fatalf("structured pass evidence=%s", encoded)
	}
}

func TestUnifiedCatalogCombinesSemanticPrefixAnalysisWithPLMOwner(t *testing.T) {
	registry, err := NewUnifiedCatalog(UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: digestFor('a'),
		PreparedNumpyLoadConfigSHA256:   digestFor('b'),
		PreparedPureRegionConfigSHA256:  digestFor('c'),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(passregistration.SemanticPreDispatch, sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.LowerMechanisms(runtimeconfig.MechanismSet{})
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Mechanisms.SemanticAnalysis || !selection.Mechanisms.SplitPhaseCalls ||
		selection.Mechanisms.SemanticPreDispatch || selection.Mechanisms.StagedObservation {
		t.Fatalf("combined mechanisms=%+v", selection.Mechanisms)
	}
}

func TestUnifiedCatalogRejectsUnorderedSourceMutationPasses(t *testing.T) {
	registry, err := NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Enable(sourcepatch.PureScalarCSEName, sourcepatch.PureScalarFoldName); !errors.Is(err, ErrPassConflict) {
		t.Fatalf("unordered source mutations error=%v", err)
	}
}

func TestEnablePreservesPriorPassSelection(t *testing.T) {
	registry, err := NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(passregistration.AgentFunctionRetention)
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registry.Enable(passregistration.AgentFunctionSingleFlight)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.LowerMechanisms(runtimeconfig.MechanismSet{})
	if err != nil || !selection.Mechanisms.FunctionCache || !selection.Mechanisms.SingleFlight || len(selection.Passes) != 2 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}
