package subagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

func TestOrchestratorStartsTwoChildrenBeforeSealAndSelectsOne(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	var running atomic.Int32
	started := make(chan string, 2)
	release := make(chan struct{})
	executor := subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
		running.Add(1)
		defer running.Add(-1)
		started <- invocation.Descriptor.ChildID
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		lease, err := manager.Acquire(invocation.WorkspaceRef, "child-"+invocation.Descriptor.ChildID)
		if err != nil {
			return err
		}
		defer lease.Release()
		file, errno := lease.FS().OpenFile(invocation.Descriptor.ChildID+".txt", experimentalsys.O_CREAT|experimentalsys.O_WRONLY, 0o600)
		if errno != 0 {
			return errors.New("open child output")
		}
		_, errno = file.Write([]byte(invocation.Descriptor.ChildID))
		_ = file.Close()
		if errno != 0 {
			return errors.New("write child output")
		}
		return nil
	})
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 2, Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"left", "right"} {
		if err := orchestrator.Stage(context.Background(), childDescriptor(id, parentLineage)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("children did not start before parent seal")
		}
	}
	if !seen["left"] || !seen["right"] || running.Load() != 2 {
		t.Fatalf("pre-seal children=%v running=%d", seen, running.Load())
	}
	close(release)
	awaited, err := orchestrator.Await(context.Background())
	if err != nil || awaited.Completed != 2 || len(awaited.Timeline) != 2 {
		t.Fatalf("await=%+v err=%v", awaited, err)
	}
	if err := orchestrator.Stage(context.Background(), childDescriptor("late", parentLineage)); !errors.Is(err, subagent.ErrOrchestratorAwaited) {
		t.Fatalf("stage after await error=%v", err)
	}
	joined, err := orchestrator.Seal(context.Background(), "right")
	if err != nil {
		t.Fatal(err)
	}
	if joined.SelectedChildID != "right" || joined.SelectedRoot.Depth != 1 || joined.ChildCount != 2 || joined.Completed != 2 {
		t.Fatalf("join=%+v", joined)
	}
	if len(joined.Timeline) != 2 {
		t.Fatalf("timeline=%+v", joined.Timeline)
	}
	for _, event := range joined.Timeline {
		if event.DescriptorSHA256 == "" || event.ParentLineageSHA256 != parentLineage || event.ChildPlanSHA256 == "" || event.EndMS < event.StartMS || event.Outcome != "ok" {
			t.Fatalf("event=%+v", event)
		}
	}
	if _, err := manager.Inspect(joined.SelectedRoot.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(joined.SelectedRoot.Ref(), "sealed"); !errors.Is(err, workspace.ErrWorkspaceImmutable) {
		t.Fatalf("selected root mutable: %v", err)
	}
	if _, err := manager.Inspect(joined.DiscardedRefs[0]); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
		t.Fatalf("unselected root retained: %v", err)
	}

	selectedInfo, err := manager.Inspect(joined.SelectedRoot.Ref())
	if err != nil {
		t.Fatal(err)
	}
	selectedLineage, _, err := manager.PortableIdentity(joined.SelectedRoot.Ref())
	if err != nil {
		t.Fatal(err)
	}
	recursive, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: joined.SelectedRoot.Ref(), ParentWorkspaceSHA256: selectedInfo.WorkspaceSHA256,
		ParentLineage: selectedLineage, MaxFanout: 1, MaxDepth: 2,
		Executor: subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	grandchild := childDescriptor("grandchild", selectedLineage)
	grandchild.Depth = 2
	if err := recursive.Stage(context.Background(), grandchild); err != nil {
		t.Fatal(err)
	}
	recursiveJoin, err := recursive.Seal(context.Background(), "grandchild")
	if err != nil || recursiveJoin.SelectedRoot.Depth != 2 {
		t.Fatalf("recursive join=%+v err=%v", recursiveJoin, err)
	}
}

func TestChildFailureDiscardsSiblingAndLeavesParentBase(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	executor := subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
		if invocation.Descriptor.ChildID == "bad" {
			return errors.New("invalid child")
		}
		lease, err := manager.Acquire(invocation.WorkspaceRef, "good")
		if err != nil {
			return err
		}
		defer lease.Release()
		file, errno := lease.FS().OpenFile("good.txt", experimentalsys.O_CREAT|experimentalsys.O_WRONLY, 0o600)
		if errno != 0 {
			return errors.New("open")
		}
		_, errno = file.Write([]byte("good"))
		_ = file.Close()
		if errno != 0 {
			return errors.New("write")
		}
		return nil
	})
	orchestrator, _ := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 1, Executor: executor,
	})
	for _, id := range []string{"bad", "good"} {
		if err := orchestrator.Stage(context.Background(), childDescriptor(id, parentLineage)); err != nil {
			t.Fatal(err)
		}
	}
	refs := orchestrator.PrivateRefs()
	if _, err := orchestrator.Seal(context.Background(), "good"); !errors.Is(err, subagent.ErrChildExecution) {
		t.Fatalf("Seal() error=%v", err)
	}
	for _, ref := range refs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("failed child state retained: %v", err)
		}
	}
	info, err := manager.Inspect(base)
	if err != nil || info.WorkspaceSHA256 != parentWorkspaceSHA {
		t.Fatalf("parent changed info=%+v err=%v", info, err)
	}
}

func TestParentAbortCascadesCancellationAndZeroPublication(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	executor := subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1, Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Stage(context.Background(), childDescriptor("cancelled", parentLineage)); err != nil {
		t.Fatal(err)
	}
	<-started
	refs := orchestrator.PrivateRefs()
	if err := orchestrator.Abort(context.Background(), subagent.ParentInvalid); err != nil {
		t.Fatal(err)
	}
	<-cancelled
	for _, ref := range refs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("private child published after abort: %s err=%v", ref, err)
		}
	}
	if _, err := orchestrator.Seal(context.Background(), "cancelled"); !errors.Is(err, subagent.ErrOrchestratorTerminal) {
		t.Fatalf("Seal after abort error=%v", err)
	}
}

func TestDescriptorIdentityAndBudgetsFailClosed(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	executor := subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error { return nil })
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1, Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := childDescriptor("one", parentLineage)
	identity, document, err := valid.Identity()
	if err != nil || identity == "" || len(document) == 0 {
		t.Fatalf("identity=%q document=%s err=%v", identity, document, err)
	}
	if err := orchestrator.Stage(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Stage(context.Background(), childDescriptor("two", parentLineage)); !errors.Is(err, subagent.ErrFanoutBudget) {
		t.Fatalf("fanout error=%v", err)
	}
	_ = orchestrator.Abort(context.Background(), subagent.ParentCancelled)

	invalids := []subagent.Descriptor{
		func() subagent.Descriptor {
			value := childDescriptor("x", parentLineage)
			value.Depth = 2
			return value
		}(),
		func() subagent.Descriptor {
			value := childDescriptor("x", parentLineage)
			value.ParentLineageSHA256 = digest('2')
			return value
		}(),
		func() subagent.Descriptor {
			value := childDescriptor("x", parentLineage)
			value.ChildPlanSHA256 = ""
			return value
		}(),
	}
	for index, descriptor := range invalids {
		fresh, _ := subagent.New(subagent.Config{
			Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
			ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1, Executor: executor,
		})
		if err := fresh.Stage(context.Background(), descriptor); !errors.Is(err, subagent.ErrInvalidDescriptor) {
			t.Fatalf("invalid %d error=%v", index, err)
		}
	}
}

func TestAuthorityAdmissionRejectsWideningAndAggregateOvercommitBeforeExecution(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	parentPlan := subagentPlan(t, 4, "workspace.read")
	childPlan := subagentPlan(t, 2, "workspace.read")
	widenedPlan := subagentPlan(t, 1, "workspace.read", "network.read")
	var executions atomic.Uint32
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 3, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: map[string]*capability.Plan{
			childPlan.Identity(): childPlan, widenedPlan.Identity(): widenedPlan,
		},
		MaxDelegatedCalls: 3,
		Executor: subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error {
			executions.Add(1)
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := childDescriptor("valid", parentLineage)
	valid.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Stage(context.Background(), valid); !errors.Is(err, subagent.ErrInvalidDescriptor) {
		t.Fatalf("duplicate error=%v", err)
	}
	overcommit := childDescriptor("overcommit", parentLineage)
	overcommit.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), overcommit); !errors.Is(err, subagent.ErrDelegationBudget) {
		t.Fatalf("overcommit error=%v", err)
	}
	widened := childDescriptor("widened", parentLineage)
	widened.ChildPlanSHA256 = widenedPlan.Identity()
	if err := orchestrator.Stage(context.Background(), widened); !errors.Is(err, subagent.ErrAuthorityWidening) {
		t.Fatalf("widening error=%v", err)
	}
	joined, err := orchestrator.Seal(context.Background(), "valid")
	if err != nil || executions.Load() != 1 || joined.ReservedCalls != 2 {
		t.Fatalf("joined=%+v executions=%d err=%v", joined, executions.Load(), err)
	}
}

func TestOrchestratorFreezesChildPlanMap(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	parentPlan := subagentPlan(t, 4, "workspace.read")
	childPlan := subagentPlan(t, 2, "workspace.read")
	widerPlan := subagentPlan(t, 3, "workspace.read", "network.read")
	plans := map[string]*capability.Plan{childPlan.Identity(): childPlan}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: plans, MaxDelegatedCalls: 2,
		Executor: subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	plans[childPlan.Identity()] = widerPlan
	descriptor := childDescriptor("frozen-plan", parentLineage)
	descriptor.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
		t.Fatalf("caller map mutation changed admitted plan: %v", err)
	}
	if err := orchestrator.Abort(context.Background(), subagent.ParentCancelled); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityChildFailureDiscardsReservedPrivateBranch(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	parentPlan := subagentPlan(t, 1, "workspace.read")
	childPlan := subagentPlan(t, 1, "workspace.read")
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: map[string]*capability.Plan{childPlan.Identity(): childPlan}, MaxDelegatedCalls: 1,
		Executor: subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error {
			return errors.New("fixture child failure")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := childDescriptor("failing-authority", parentLineage)
	descriptor.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	refs := orchestrator.PrivateRefs()
	if _, err := orchestrator.Seal(context.Background(), descriptor.ChildID); !errors.Is(err, subagent.ErrChildExecution) {
		t.Fatalf("seal error=%v", err)
	}
	for _, ref := range refs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("failed child private ref %q remained: %v", ref, err)
		}
	}
}

func TestAuthorityReservationDoesNotEnableLateChildAfterAbort(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	parentPlan := subagentPlan(t, 2, "workspace.read")
	childPlan := subagentPlan(t, 2, "workspace.read")
	entered := make(chan struct{})
	var executions atomic.Uint32
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: map[string]*capability.Plan{childPlan.Identity(): childPlan}, MaxDelegatedCalls: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, _ subagent.Invocation) error {
			executions.Add(1)
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := childDescriptor("cancelled-authority", parentLineage)
	descriptor.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	<-entered
	refs := orchestrator.PrivateRefs()
	if err := orchestrator.Abort(context.Background(), subagent.ParentCancelled); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("cancelled private ref %q remained: %v", ref, err)
		}
	}
	late := childDescriptor("late-authority", parentLineage)
	late.ChildPlanSHA256 = childPlan.Identity()
	if err := orchestrator.Stage(context.Background(), late); !errors.Is(err, subagent.ErrOrchestratorTerminal) {
		t.Fatalf("late stage error=%v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("executions=%d", executions.Load())
	}
}

func TestAbortConcurrentWithCompletionIsRaceSafe(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	var entered sync.WaitGroup
	entered.Add(1)
	executor := subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
		entered.Done()
		return nil
	})
	orchestrator, _ := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1, Executor: executor,
	})
	if err := orchestrator.Stage(context.Background(), childDescriptor("race", parentLineage)); err != nil {
		t.Fatal(err)
	}
	entered.Wait()
	if err := orchestrator.Abort(context.Background(), subagent.ParentTimeout); err != nil {
		t.Fatal(err)
	}
}

func childDescriptor(id, parentLineage string) subagent.Descriptor {
	return subagent.Descriptor{
		SchemaVersion: subagent.DescriptorSchemaVersion,
		ChildID:       id, ParentStreamEpoch: "stream-1", ParentLineageSHA256: parentLineage,
		SourceOccurrence: "suite:1:child:" + id, SourceSHA256: digest('2'), InputsSHA256: digest('3'),
		ArtifactSHA256: digest('4'), ExecutionProfileSHA256: digest('5'), ChildPlanSHA256: digest('6'),
		PrivacyPartition: "private-a", Depth: 1,
	}
}

func subagentPlan(t *testing.T, maxCalls uint32, names ...string) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"root":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		spec := capability.Spec{
			Name: name, Version: "pysolate." + name + ".v1", Description: "Fixture capability " + name,
			EffectClass: capability.EffectWorkspaceRead, Playback: capability.PlaybackLiveOnly,
			HandlerIdentity: "pysolate." + name + ".handler.v1",
			InputSchema:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		}
		if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestOrchestratorAbortSurfacesWorkspaceCleanupFailure(t *testing.T) {
	manager, base, parentWorkspaceSHA, parentLineage := subagentWorkspace(t)
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: parentWorkspaceSHA,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1,
		Executor: subagent.ExecutorFunc(func(context.Context, subagent.Invocation) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Stage(context.Background(), childDescriptor("cleanup", parentLineage)); err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Await(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := orchestrator.PrivateRefs()[0]
	if err := manager.Destroy(ref); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Abort(context.Background(), subagent.ParentInvalid); err == nil {
		t.Fatal("cleanup failure was hidden")
	}
}

func subagentWorkspace(t *testing.T) (*workspace.Manager, workspace.Ref, string, string) {
	t.Helper()
	baseDir := t.TempDir()
	if err := os.Chmod(baseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	info, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	return manager, base, info.WorkspaceSHA256, lineage
}

func digest(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return "sha256:" + string(value)
}
