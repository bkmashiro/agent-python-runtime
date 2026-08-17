package subagent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestFreshRunnerExecutorCreatesAndRetiresOneRunnerPerChild(t *testing.T) {
	var mu sync.Mutex
	var runners []*childRunner
	factory := subagent.RunnerFactoryFunc(func(_ context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
		mu.Lock()
		defer mu.Unlock()
		runner := &childRunner{id: descriptor.ChildID, ref: ref}
		runners = append(runners, runner)
		return runner, nil
	})
	var observed []string
	executor := subagent.FreshRunnerExecutor{
		Factory: factory,
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			return subagent.ChildProgram{Request: []byte(`{"run_id":"` + descriptor.ChildID + `"}`)}, nil
		}),
		Observer: subagent.ChildResponseObserverFunc(func(_ context.Context, descriptor subagent.Descriptor, response []byte) error {
			observed = append(observed, descriptor.ChildID+":"+string(response))
			response[0] = 'x'
			return nil
		}),
	}
	for _, id := range []string{"left", "right"} {
		descriptor := childDescriptor(id, digest('1'))
		if err := executor.Execute(context.Background(), subagent.Invocation{Descriptor: descriptor, WorkspaceRef: workspace.Ref("ws-" + id)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(runners) != 2 || runners[0] == runners[1] || !runners[0].closed || !runners[1].closed || runners[0].runs != 1 || runners[1].runs != 1 {
		t.Fatalf("runners=%+v", runners)
	}
	if len(observed) != 2 || observed[0] != `left:{"status":"ok"}` || observed[1] != `right:{"status":"ok"}` {
		t.Fatalf("observed=%v", observed)
	}
}

func TestFreshRunnerExecutorFailsClosedWhenResponseObserverFails(t *testing.T) {
	executor := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(_ context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			return &childRunner{id: descriptor.ChildID, ref: ref}, nil
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			return subagent.ChildProgram{Request: []byte(`{"run_id":"` + descriptor.ChildID + `"}`)}, nil
		}),
		Observer: subagent.ChildResponseObserverFunc(func(context.Context, subagent.Descriptor, []byte) error { return errors.New("capture failed") }),
	}
	err := executor.Execute(context.Background(), subagent.Invocation{Descriptor: childDescriptor("child", digest('1')), WorkspaceRef: workspace.Ref("ws-child")})
	if !errors.Is(err, subagent.ErrChildExecution) || err == nil {
		t.Fatalf("observer error=%v", err)
	}
}

type childRunner struct {
	id     string
	ref    workspace.Ref
	runs   int
	closed bool
}

func (runner *childRunner) Run(context.Context, []byte, string) ([]byte, error) {
	runner.runs++
	return []byte(`{"status":"ok"}`), nil
}
func (runner *childRunner) Close(context.Context) error {
	runner.closed = true
	return nil
}
func (runner *childRunner) Properties() engine.Properties {
	return engine.Properties{Backend: "fixture"}
}
