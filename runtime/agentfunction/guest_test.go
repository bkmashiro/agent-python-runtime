package agentfunction_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestExecuteGuestRejectsUnshareableRunnerBeforeRun(t *testing.T) {
	invocation := cacheableInvocation()
	base := engine.Properties{
		Backend: "wazero", ArtifactSHA256: invocation.ArtifactSHA256,
		ExecutionProfileBindingSHA256: invocation.ExecutionProfileSHA256,
		DeterministicProfileSHA256:    invocation.DeterministicSettingsSHA256,
	}
	for name, mutate := range map[string]func(*engine.Properties){
		"wrong artifact":           func(properties *engine.Properties) { properties.ArtifactSHA256 = digest('0') },
		"workspace":                func(properties *engine.Properties) { properties.WorkspaceMounted = true },
		"broker":                   func(properties *engine.Properties) { properties.CapabilityBrokerAvailable = true },
		"no deterministic profile": func(properties *engine.Properties) { properties.DeterministicProfileSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			properties := base
			mutate(&properties)
			runner := &probeRunner{properties: properties}
			compute := agentfunction.FreshGuestCompute{
				NewRunner: func(context.Context) (string, engine.Runner, error) { return "physical-1", runner, nil },
				Request:   []byte("request"), MaxResultBytes: 16,
				DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
			}
			result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, compute)
			if !errors.Is(err, agentfunction.ErrGuestNotShareable) || len(result.Value) != 0 || runner.runs.Load() != 0 || runner.closes.Load() != 1 {
				t.Fatalf("result=%+v err=%v runs=%d closes=%d", result, err, runner.runs.Load(), runner.closes.Load())
			}
		})
	}
}

type probeRunner struct {
	properties engine.Properties
	runs       atomic.Int32
	closes     atomic.Int32
}

func (runner *probeRunner) Run(context.Context, []byte, string) ([]byte, error) {
	runner.runs.Add(1)
	return []byte("result"), nil
}
func (runner *probeRunner) Close(context.Context) error   { runner.closes.Add(1); return nil }
func (runner *probeRunner) Properties() engine.Properties { return runner.properties }
