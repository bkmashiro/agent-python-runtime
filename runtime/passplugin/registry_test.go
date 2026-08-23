package passplugin

import (
	"context"
	"testing"

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
	registry, err := New(semanticAdapter, preparedAdapter, cse)
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
