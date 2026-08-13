package agentfunction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	ErrInvalidGuestCompute = errors.New("invalid fresh Guest compute")
	ErrGuestResultTooLarge = errors.New("fresh Guest result exceeds bound")
	ErrGuestNotShareable   = errors.New("fresh Guest execution profile is not shareable")
	ErrGuestIdentity       = errors.New("fresh Guest request does not match invocation identity")
	ErrGuestRetention      = errors.New("fresh Guest completed-result retention is unsupported")
)

type GuestRunnerFactory func(context.Context) (physicalExecutionID string, runner enginecontract.Runner, err error)
type GuestResultDecoder func([]byte) ([]byte, error)

// FreshGuestCompute adapts an agent-function leader to one single-use Runner.
// The Runner and its Guest state are never shared; only copied result bytes are.
type FreshGuestCompute struct {
	NewRunner      GuestRunnerFactory
	Request        []byte
	TrustedPrepare string
	DecodeResult   GuestResultDecoder
	MaxResultBytes uint64
}

func (compute FreshGuestCompute) Run(ctx context.Context, guard *Guard) ([]byte, error) {
	if compute.NewRunner == nil || compute.DecodeResult == nil || len(compute.Request) == 0 || compute.MaxResultBytes == 0 {
		return nil, ErrInvalidGuestCompute
	}
	physicalID, runner, err := compute.NewRunner(ctx)
	if err != nil || runner == nil {
		return nil, errors.Join(ErrInvalidGuestCompute, err)
	}
	if err := guard.BindPhysicalExecution(physicalID); err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	runContext, effects := enginecontract.WithEffectProbe(ctx)
	payload, runErr := runner.Run(runContext, append([]byte(nil), compute.Request...), compute.TrustedPrepare)
	closeErr := runner.Close(context.Background())
	if effects.HostCallAttempted() {
		return nil, errors.Join(ErrGuestNotShareable, runErr, closeErr)
	}
	if runErr != nil || closeErr != nil {
		return nil, errors.Join(runErr, closeErr)
	}
	value, err := compute.DecodeResult(payload)
	if err != nil {
		return nil, err
	}
	if uint64(len(value)) > compute.MaxResultBytes {
		return nil, ErrGuestResultTooLarge
	}
	return append([]byte(nil), value...), nil
}

// ExecuteGuest binds the invocation identity to the actual Runner properties
// before single-flight may publish the immutable Guest result.
func (functionEngine Engine) ExecuteGuest(ctx context.Context, invocation Invocation, compute FreshGuestCompute) (Result, error) {
	if compute.NewRunner == nil {
		return Result{}, ErrInvalidGuestCompute
	}
	if functionEngine.CacheEnabled {
		return Result{}, ErrGuestRetention
	}
	request, err := runtimeconfig.DecodeRunRequest(compute.Request)
	codeDigest := sha256.Sum256([]byte(request.Code))
	if err != nil || fmt.Sprintf("sha256:%x", codeDigest[:]) != invocation.FunctionSourceSHA256 ||
		!bytes.Equal(request.Inputs, invocation.CanonicalInputs) {
		return Result{}, ErrGuestIdentity
	}
	originalFactory := compute.NewRunner
	compute.NewRunner = func(runContext context.Context) (string, enginecontract.Runner, error) {
		physicalID, runner, err := originalFactory(runContext)
		if err != nil || runner == nil {
			return physicalID, runner, err
		}
		properties := runner.Properties()
		if properties.Backend != "wazero" || properties.ArtifactSHA256 != invocation.ArtifactSHA256 ||
			properties.ExecutionProfileBindingSHA256 != invocation.ExecutionProfileSHA256 ||
			properties.DeterministicProfileSHA256 != invocation.DeterministicSettingsSHA256 ||
			properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
			_ = runner.Close(context.Background())
			return "", nil, ErrGuestNotShareable
		}
		return physicalID, runner, nil
	}
	return functionEngine.execute(ctx, invocation, compute.Run, "fresh-guest")
}
