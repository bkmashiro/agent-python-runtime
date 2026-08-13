package agentfunction_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestExecuteGuestRejectsUnshareableRunnerBeforeRun(t *testing.T) {
	invocation, request := guestInvocation()
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
				NewRunner:      func(context.Context) (string, engine.Runner, error) { return "physical-1", runner, nil },
				Request:        request,
				MaxResultBytes: 16,
				DecodeResult:   func(value []byte) ([]byte, error) { return value, nil },
			}
			result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, compute)
			if !errors.Is(err, agentfunction.ErrGuestNotShareable) || len(result.Value) != 0 || runner.runs.Load() != 0 || runner.closes.Load() != 1 {
				t.Fatalf("result=%+v err=%v runs=%d closes=%d", result, err, runner.runs.Load(), runner.closes.Load())
			}
		})
	}
}

func TestExecuteGuestRejectsRequestIdentityMismatchBeforeRunnerCreation(t *testing.T) {
	invocation, _ := guestInvocation()
	var created atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(context.Context) (string, engine.Runner, error) {
			created.Add(1)
			return "", nil, nil
		},
		Request: []byte(`{"run_id":"mismatch","code":"result = 2","inputs":{"value":1}}`), MaxResultBytes: 16,
		DecodeResult: func(value []byte) ([]byte, error) { return value, nil },
	}
	result, err := (agentfunction.Engine{}).ExecuteGuest(context.Background(), invocation, compute)
	if !errors.Is(err, agentfunction.ErrGuestIdentity) || len(result.Value) != 0 || created.Load() != 0 {
		t.Fatalf("result=%+v err=%v created=%d", result, err, created.Load())
	}
}

func guestInvocation() (agentfunction.Invocation, []byte) {
	invocation := cacheableInvocation()
	code := "result = 1"
	digest := sha256.Sum256([]byte(code))
	invocation.FunctionSourceSHA256 = fmt.Sprintf("sha256:%x", digest[:])
	invocation.CanonicalInputs = []byte(`{"value":1}`)
	return invocation, []byte(`{"run_id":"unit","code":"result = 1","inputs":{"value":1}}`)
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
