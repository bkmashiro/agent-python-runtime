package passplugin

import (
	"context"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
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

func TestUnifiedCatalogRegistersFourStageAwarePassesDefaultOff(t *testing.T) {
	registry, err := NewUnifiedCatalog(UnifiedCatalogConfig{
		SemanticPreDispatchConfigSHA256: digestFor('a'),
		PreparedNumpyLoadConfigSHA256:   digestFor('b'),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[passregistration.Name]passregistration.Stage{
		passregistration.SemanticPreDispatch:        passregistration.StagePrefixOverlay,
		passregistration.PreparedNumpyLoad:          passregistration.StageHybridPreparePatch,
		passregistration.CapabilityFutureProjection: passregistration.StagePlanProjection,
		passregistration.PreparedValueBinding:       passregistration.StageRunBinding,
	}
	for name, stage := range expected {
		plugin, ok := registry.Lookup(name)
		if !ok || plugin.Registration().Stage() != stage {
			t.Fatalf("name=%s stage=%s plugin=%v ok=%v", name, stage, plugin, ok)
		}
	}
	if _, err := registry.ProjectPlan(passregistration.CapabilityFutureProjection, nil); err != ErrPluginDisabled {
		t.Fatalf("default-off Future projection error=%v", err)
	}
	if _, err := registry.BindRunValue(passregistration.PreparedValueBinding, "slot-numpy-sum-v1"); err != ErrPluginDisabled {
		t.Fatalf("default-off prepared value error=%v", err)
	}

	registry, err = registry.Enable(passregistration.CapabilityFutureProjection, passregistration.PreparedValueBinding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ProjectPlan(passregistration.CapabilityFutureProjection, (*capability.Plan)(nil)); err == nil {
		t.Fatal("nil Plan projected")
	}
	prelude, err := registry.BindRunValue(passregistration.PreparedValueBinding, "slot-numpy-sum-v1")
	if err != nil || !strings.Contains(prelude, "prepared_value") || !strings.Contains(prelude, "materialize_slot") {
		t.Fatalf("prelude=%q err=%v", prelude, err)
	}
	if _, err := registry.ProjectPlan(passregistration.PreparedValueBinding, nil); err != ErrUnsupportedStage {
		t.Fatalf("run binding crossed into Plan stage: %v", err)
	}
	if _, err := registry.BindRunValue(passregistration.CapabilityFutureProjection, "slot-numpy-sum-v1"); err != ErrUnsupportedStage {
		t.Fatalf("Plan projection crossed into Run stage: %v", err)
	}
}
