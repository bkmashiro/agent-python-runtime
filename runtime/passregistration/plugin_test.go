package passregistration_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/passpipeline"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

const (
	testAnalyzer = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testConfig   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestCustomDefinitionRegistersWithoutCentralSwitch(t *testing.T) {
	definition, err := passregistration.Define(
		"pure_scalar_cse",
		"pysolate.pure-scalar-cse-pass.v1",
		passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch,
		passregistration.PatchBindings(),
	)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := definition.Register(testAnalyzer, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := passregistration.NewRegistry(registration)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := registry.Lookup("pure_scalar_cse")
	if !ok || stored.IdentitySHA256() != registration.IdentitySHA256() || stored.Stage() != passregistration.StageWholeProgramPatch {
		t.Fatalf("stored=%+v ok=%v", stored, ok)
	}
	entry, err := passpipeline.CurrentEntry(stored, true)
	if err != nil || entry.Stage != passpipeline.StageWholeProgramPatch {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestAnalyzerFreeStageDefinitionsRegisterWithoutAnalyzer(t *testing.T) {
	tests := []struct {
		definition passregistration.Definition
		stage      passregistration.Stage
		consumer   passregistration.Consumer
	}{
		{passregistration.CapabilityFutureProjectionDefinition(), passregistration.StagePlanProjection, passregistration.PlanProjection},
		{passregistration.PreparedValueBindingDefinition(), passregistration.StageRunBinding, passregistration.RunBinding},
	}
	for _, test := range tests {
		registration, err := test.definition.Register("", testConfig)
		if err != nil {
			t.Fatalf("stage=%s: %v", test.stage, err)
		}
		if registration.Stage() != test.stage || registration.Consumer() != test.consumer || registration.AnalyzerSHA256() != "" {
			t.Fatalf("registration=%+v", registration)
		}
		entry, err := passpipeline.CurrentEntry(registration, true)
		if err != nil || entry.Stage != test.stage {
			t.Fatalf("entry=%+v err=%v", entry, err)
		}
	}
}

func TestSourceStagesStillRequireAnalyzerIdentity(t *testing.T) {
	if _, err := passregistration.PreparedNumpyLoadDefinition().Register("", testConfig); err == nil {
		t.Fatal("hybrid source pass accepted an empty analyzer identity")
	}
}
