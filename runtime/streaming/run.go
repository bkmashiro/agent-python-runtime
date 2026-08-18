package streaming

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// RunResult is published only after Guest execution and final source admission
// both succeed. PublishedWorkspace is a new identity, never an in-place base
// mutation.
type RunResult struct {
	Response           []byte
	PublishedWorkspace workspace.Ref
}

// StreamRunner retains one fresh Guest while preparation fragments arrive.
type StreamRunner interface {
	engine.Runner
	RunStream(context.Context, []byte, <-chan string) ([]byte, error)
}

// Execute preserves the complete-source compatibility path.
func Execute(ctx context.Context, runner engine.Runner, attempt *workspace.Attempt, request []byte, prepare string) (RunResult, error) {
	return ExecuteObserved(ctx, runner, attempt, request, prepare, nil)
}

// ExecuteObserved exposes read-only post-run/pre-close evidence without
// extending the Guest lifetime or transferring runner ownership.
func ExecuteObserved(ctx context.Context, runner engine.Runner, attempt *workspace.Attempt, request []byte, prepare string, observe func(engine.Runner) error) (RunResult, error) {
	if runner == nil || attempt == nil {
		return RunResult{}, errors.New("streaming runner and workspace attempt are required")
	}
	response, err := runner.Run(ctx, request, prepare)
	if err == nil && observe != nil {
		err = observe(runner)
	}
	return finish(ctx, runner, attempt, response, err)
}

// ExecuteStream consumes Host-trusted fragments as they arrive and owns the
// attempt's terminal disposition.
func ExecuteStream(ctx context.Context, runner StreamRunner, attempt *workspace.Attempt, request []byte, prepares <-chan string) (RunResult, error) {
	if runner == nil || attempt == nil || prepares == nil {
		return RunResult{}, errors.New("live streaming requires runner, workspace attempt, and prepare channel")
	}
	response, err := runner.RunStream(ctx, request, prepares)
	return finish(ctx, runner, attempt, response, err)
}

func finish(ctx context.Context, runner engine.Runner, attempt *workspace.Attempt, response []byte, runErr error) (RunResult, error) {
	if runErr != nil {
		_ = runner.Close(ctx)
		_ = attempt.Discard()
		return RunResult{}, runErr
	}
	if err := runner.Close(ctx); err != nil {
		_ = attempt.Discard()
		return RunResult{}, err
	}
	var envelope struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
		_ = attempt.Discard()
		return RunResult{}, errors.New("streaming Guest did not produce a publishable response")
	}
	published, err := attempt.Publish()
	if err != nil {
		_ = attempt.Discard()
		return RunResult{}, err
	}
	return RunResult{Response: append([]byte(nil), response...), PublishedWorkspace: published}, nil
}
