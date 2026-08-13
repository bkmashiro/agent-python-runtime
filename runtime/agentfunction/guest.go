package agentfunction

import (
	"context"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	ErrInvalidGuestCompute = errors.New("invalid fresh Guest compute")
	ErrGuestResultTooLarge = errors.New("fresh Guest result exceeds bound")
)

type GuestRunnerFactory func(context.Context) (physicalExecutionID string, runner engine.Runner, err error)
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
	payload, runErr := runner.Run(ctx, append([]byte(nil), compute.Request...), compute.TrustedPrepare)
	closeErr := runner.Close(context.Background())
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
