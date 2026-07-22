package engine_test

import (
	"context"
	"errors"
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
	return engine.Properties{Backend: "fake", ResetMode: engine.ResetModeFreshInstance}
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
}

func TestPropertiesRejectUnknownOrIncompleteClaims(t *testing.T) {
	cases := []engine.Properties{
		{},
		{Backend: "fake"},
		{Backend: "fake", ResetMode: engine.ResetMode("memcpy-without-contract")},
	}
	for _, properties := range cases {
		if err := properties.Validate(); !errors.Is(err, engine.ErrInvalidProperties) {
			t.Fatalf("expected invalid properties for %#v, got %v", properties, err)
		}
	}
}
