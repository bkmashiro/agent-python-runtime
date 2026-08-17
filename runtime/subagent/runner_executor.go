package subagent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// RunnerFactory must return a newly-created single-use Runner for every child.
// Parent Plan/grants are intentionally absent from this interface.
type RunnerFactory interface {
	NewChildRunner(context.Context, Descriptor, workspace.Ref) (engine.Runner, error)
}

type RunnerFactoryFunc func(context.Context, Descriptor, workspace.Ref) (engine.Runner, error)

func (function RunnerFactoryFunc) NewChildRunner(ctx context.Context, descriptor Descriptor, ref workspace.Ref) (engine.Runner, error) {
	return function(ctx, descriptor, ref)
}

type ChildProgram struct {
	Request        []byte
	TrustedPrepare string
}

type ProgramBuilder interface {
	BuildChildProgram(Descriptor) (ChildProgram, error)
}

type ProgramBuilderFunc func(Descriptor) (ChildProgram, error)

func (function ProgramBuilderFunc) BuildChildProgram(descriptor Descriptor) (ChildProgram, error) {
	return function(descriptor)
}

// FreshRunnerExecutor executes each child in a distinct single-use Runner and
// always retires it. It never publishes the child workspace.
type FreshRunnerExecutor struct {
	Factory  RunnerFactory
	Builder  ProgramBuilder
	Observer ChildResponseObserver
}

// ChildResponseObserver is an explicit experiment/debug seam. Production
// executors leave it nil and retain no response body.
type ChildResponseObserver interface {
	ObserveChildResponse(context.Context, Descriptor, []byte) error
}

type ChildResponseObserverFunc func(context.Context, Descriptor, []byte) error

func (function ChildResponseObserverFunc) ObserveChildResponse(ctx context.Context, descriptor Descriptor, response []byte) error {
	return function(ctx, descriptor, response)
}

func (executor FreshRunnerExecutor) Execute(ctx context.Context, invocation Invocation) error {
	if executor.Factory == nil || executor.Builder == nil {
		return ErrInvalidOrchestrator
	}
	program, err := executor.Builder.BuildChildProgram(invocation.Descriptor)
	if err != nil || len(program.Request) == 0 {
		return ErrChildExecution
	}
	runner, err := executor.Factory.NewChildRunner(ctx, invocation.Descriptor, invocation.WorkspaceRef)
	if err != nil || runner == nil {
		return ErrChildExecution
	}
	response, runErr := runner.Run(ctx, program.Request, program.TrustedPrepare)
	closeErr := runner.Close(ctx)
	if runErr != nil || closeErr != nil {
		return ErrChildExecution
	}
	var envelope struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
		return errors.Join(ErrChildExecution, errors.New("child Guest response is not publishable"))
	}
	if executor.Observer != nil {
		if err := executor.Observer.ObserveChildResponse(ctx, invocation.Descriptor, append([]byte(nil), response...)); err != nil {
			return errors.Join(ErrChildExecution, err)
		}
	}
	return nil
}
