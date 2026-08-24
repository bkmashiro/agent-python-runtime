package semanticspeculation

import (
	"context"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

func TestEagerTreatmentRejectsCatalogWithoutStreamingPass(t *testing.T) {
	passes, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewEagerGuestTreatment(EagerGuestTreatmentConfig{
		Artifact: []byte{1}, RunConfig: runtimeconfig.DefaultRunConfig(), Plan: emptyTreatmentPlan(t),
		BrokerFactory: func(context.Context) (*capability.Broker, error) { return nil, nil },
		RunID:         "eager-pass-selection", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "owner", Passes: passes,
	}); err == nil {
		t.Fatal("EAGER treatment accepted a catalog without source_streaming_execution")
	}
}

func TestSemanticTreatmentRejectsCatalogWithoutPreDispatchPass(t *testing.T) {
	passes, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	config := semanticTreatmentPassConfig(t)
	config.Passes = passes
	if _, err := NewSemanticPreDispatchTreatment(config); err == nil {
		t.Fatal("semantic treatment accepted a catalog without semantic_pre_dispatch")
	}
}

func TestSemanticTreatmentRejectsAnalyzerPreparationMismatch(t *testing.T) {
	config := semanticTreatmentPassConfig(t)
	config.Passes, _ = passplugin.NewDefaultEnabledCatalog(passregistration.SemanticPreDispatch)
	config.AnalyzerPasses, _ = passplugin.NewDefaultUnifiedCatalog()
	config.RunConfig.Mechanisms.PreparedRuntime = true
	if _, err := NewSemanticPreDispatchTreatment(config); err == nil {
		t.Fatal("semantic treatment silently dropped requested prepared analyzer pass")
	}
}

func semanticTreatmentPassConfig(t *testing.T) SemanticPreDispatchTreatmentConfig {
	t.Helper()
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	return SemanticPreDispatchTreatmentConfig{
		Artifact: []byte{1}, RunConfig: config, Plan: emptyTreatmentPlan(t),
		ImportClosureSHA256: "sha256:" + strings.Repeat("a", 64), PhysicalReadBudget: 1,
		RunID: "semantic-pass-selection", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "owner",
	}
}

func emptyTreatmentPlan(t *testing.T) *capability.Plan {
	t.Helper()
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
