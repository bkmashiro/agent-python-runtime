package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, []byte, string) ([]byte, error) {
	return []byte("ok"), nil
}
func (fakeRunner) Close(context.Context) error { return nil }
func (fakeRunner) Properties() engine.Properties {
	return engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance,
		ActiveStrategy:    engine.StrategyFreshInstance,
	}
}

type fakeFactory struct{}

func (fakeFactory) Name() string { return "fake" }
func (fakeFactory) New(context.Context, []byte, runtime.RunConfig) (engine.Runner, error) {
	return fakeRunner{}, nil
}

func TestRunnerAndFactoryAreBackendNeutralContracts(t *testing.T) {
	var factory engine.Factory = fakeFactory{}
	runner, err := factory.New(context.Background(), []byte("artifact"), runtime.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	if factory.Name() != "fake" {
		t.Fatalf("unexpected factory name %q", factory.Name())
	}
	if err := runner.Properties().Validate(); err != nil {
		t.Fatalf("valid properties rejected: %v", err)
	}
	if runner.Properties().ResetMode != engine.ResetModeFreshInstance {
		t.Fatalf("unexpected reset mode %q", runner.Properties().ResetMode)
	}
	if runner.Properties().RequestedStrategy != engine.StrategyFreshInstance || runner.Properties().ActiveStrategy != engine.StrategyFreshInstance {
		t.Fatalf("unexpected strategy properties %#v", runner.Properties())
	}
}

func TestPropertiesRejectUnknownOrIncompleteClaims(t *testing.T) {
	cases := []engine.Properties{
		{},
		{Backend: "fake"},
		{Backend: "fake", ResetMode: engine.ResetMode("memcpy-without-contract")},
		{Backend: "fake", ResetMode: engine.ResetModeFreshInstance},
		{Backend: "fake", ResetMode: engine.ResetModeFreshInstance, RequestedStrategy: engine.StrategyFreshInstance},
		{
			Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
			RequestedStrategy: engine.StrategyCOWReadySingleUse,
			ActiveStrategy:    engine.StrategyFreshInstance,
		},
		{
			Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
			RequestedStrategy: engine.StrategyCOWReadySingleUse,
			ActiveStrategy:    engine.StrategyFreshInstance,
			Fallback:          true,
		},
		{
			Backend: "fake", ResetMode: engine.ResetModePreparedRestore,
			RequestedStrategy: engine.StrategyCOWReadySingleUse,
			ActiveStrategy:    engine.StrategyCOWReadySingleUse,
		},
	}
	for _, properties := range cases {
		if err := properties.Validate(); !errors.Is(err, engine.ErrInvalidProperties) {
			t.Fatalf("expected invalid properties for %#v, got %v", properties, err)
		}
	}
}

func TestPropertiesRoundTripArtifactBoundProfile(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	properties := engine.Properties{
		Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
		RequestedStrategy: engine.StrategyFreshInstance, ActiveStrategy: engine.StrategyFreshInstance,
		ExecutionProfileID: "base", AllowedImports: []string{"json"}, AvailableImports: []string{"agent_runtime", "json", "sys"},
		ArtifactSHA256: digest, ManifestSHA256: digest,
	}
	if err := properties.Validate(); err != nil {
		t.Fatal(err)
	}
	profile := properties.ExecutionProfile()
	if profile == nil || profile.ArtifactSHA256() != digest || profile.ManifestSHA256() != digest {
		t.Fatalf("profile=%+v", profile)
	}
	properties.ManifestSHA256 = ""
	if !errors.Is(properties.Validate(), engine.ErrInvalidProperties) {
		t.Fatal("unpaired artifact identity was accepted")
	}
}

func TestPropertiesAcceptTruthfulFallbackAndPreparedRestore(t *testing.T) {
	valid := []engine.Properties{
		{
			Backend: "fake", ResetMode: engine.ResetModeFreshInstance,
			RequestedStrategy: engine.StrategyCOWReadySingleUse,
			ActiveStrategy:    engine.StrategyFreshInstance,
			Fallback:          true,
			FallbackReason:    "COW is unavailable",
		},
		{
			Backend: "fake", ResetMode: engine.ResetModePreparedRestore,
			RequestedStrategy: engine.StrategyCOWFullRemapRestore,
			ActiveStrategy:    engine.StrategyCOWFullRemapRestore,
		},
	}
	for _, properties := range valid {
		if err := properties.Validate(); err != nil {
			t.Fatalf("valid properties rejected for %#v: %v", properties, err)
		}
	}
}
