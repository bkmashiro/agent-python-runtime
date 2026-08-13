package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/composable"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestFullComposableRuntimeNorthStar(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	managerRoot := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(managerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(managerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, err := manager.Create([]workspace.InitialFile{{Path: "input.txt", Data: []byte("seed")}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, _ := manager.Inspect(base)
	parentLineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}

	completedChildren := make(chan string, 2)
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "child-" + descriptor.ChildID}
			return factory.New(ctx, artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			code := "from pathlib import Path\nPath('/workspace/" + descriptor.ChildID + ".txt').write_text('" + descriptor.ChildID + "')\nresult = {'child': '" + descriptor.ChildID + "'}"
			request, _ := json.Marshal(map[string]any{"run_id": "child-" + descriptor.ChildID, "code": code, "inputs": map[string]any{}})
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			if err := childRunner.Execute(ctx, invocation); err != nil {
				return err
			}
			completedChildren <- invocation.Descriptor.ChildID
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	parentAttempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	parentConfig := runtimeconfig.DefaultRunConfig()
	parentConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
	parentRunner, err := (wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: parentAttempt.Ref(), WorkspaceOwner: "composable-parent",
	}).New(context.Background(), artifact, parentConfig)
	if err != nil {
		t.Fatal(err)
	}
	streamRunner := parentRunner.(streaming.StreamRunner)
	prepares, err := streaming.BuildPrepareChunks(streaming.PrepareConfig{Inputs: json.RawMessage(`{}`), Chunks: []string{
		"from pathlib import Path\n",
		"Path('/workspace/parent.txt').write_text('parent')\n",
		"result = {'parent': 'sealed'}\n",
	}})
	if err != nil {
		t.Fatal(err)
	}
	prepareChannel := make(chan string)
	parentDone := make(chan struct {
		result streaming.RunResult
		err    error
	}, 1)
	go func() {
		request := []byte(`{"run_id":"composable-parent","code":"result = stream_final","inputs":{}}`)
		result, err := streaming.ExecuteStream(context.Background(), streamRunner, parentAttempt, request, prepareChannel)
		parentDone <- struct {
			result streaming.RunResult
			err    error
		}{result, err}
	}()
	prepareChannel <- prepares[0]
	prepareChannel <- prepares[1]
	for _, id := range []string{"left", "right"} {
		descriptor := composableDescriptor(id, parentLineage)
		if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case id := <-completedChildren:
			seen[id] = true
		case <-time.After(10 * time.Second):
			t.Fatal("real child Guest did not complete before parent EOF")
		}
	}
	if !seen["left"] || !seen["right"] {
		t.Fatalf("started=%v", seen)
	}
	for _, prepare := range prepares[2:] {
		prepareChannel <- prepare
	}
	close(prepareChannel)
	parent := <-parentDone
	if parent.err != nil || parent.result.PublishedWorkspace == "" {
		t.Fatalf("parent result=%+v err=%v", parent.result, parent.err)
	}
	joined, err := orchestrator.Seal(context.Background(), "right")
	if err != nil {
		t.Fatal(err)
	}
	if joined.ChangedBytes == 0 || joined.MaterializedBytes == 0 || joined.MaxBranchDepth != 1 || joined.ReachableRoots != 1 || joined.DiscardedRoots != 1 {
		t.Fatalf("branch measurements=%+v", joined)
	}
	if !rootContains(t, manager, joined.SelectedRoot, "right.txt") || rootContains(t, manager, joined.SelectedRoot, "left.txt") {
		t.Fatal("explicit select returned wrong child root")
	}

	functionStore, err := agentfunction.NewStore(filepath.Join(t.TempDir(), "functions"), hashCharacter('1'), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	flights := agentfunction.NewFlightGroup()
	functionEngine := agentfunction.Engine{Store: functionStore, CacheEnabled: true, Flights: flights}
	functionInvocation := composableFunctionInvocation(joined.SelectedRoot.IdentitySHA256)
	var physicalComputes int
	var computeMu sync.Mutex
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		computeMu.Lock()
		physicalComputes++
		computeMu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return []byte(`{"normalized":"right"}`), nil
	}
	var functionResults [2]agentfunction.Result
	var functionErrors [2]error
	var functionWait sync.WaitGroup
	functionWait.Add(2)
	for index := range 2 {
		index := index
		go func() {
			defer functionWait.Done()
			functionResults[index], functionErrors[index] = functionEngine.Execute(context.Background(), functionInvocation, compute)
		}()
	}
	functionWait.Wait()
	if functionErrors[0] != nil || functionErrors[1] != nil || physicalComputes != 1 || flights.Stats().Waiters != 1 {
		t.Fatalf("function errors=%v computes=%d flights=%+v", functionErrors, physicalComputes, flights.Stats())
	}
	cached, err := functionEngine.Execute(context.Background(), functionInvocation, compute)
	if err != nil || !cached.CacheHit || physicalComputes != 1 {
		t.Fatalf("cached=%+v computes=%d err=%v", cached, physicalComputes, err)
	}

	guestFactory := &realWorkflowGuestFactory{t: t, artifact: artifact, manager: manager, base: joined.SelectedRoot}
	observation := "stable"
	graph := workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "composable-workflow", Nodes: []workflow.Node{
		{ID: "before", Kind: workflow.Compute, VersionSHA256: hashCharacter('1'), Compute: realWorkflowCompute("before")},
		{ID: "observation", Kind: workflow.Observation, VersionSHA256: hashCharacter('2'), Dependencies: []string{"before"}, RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
			return workflow.ObservedValue{Value: []byte(observation), FreshnessSHA256: hashBytes([]byte(observation)), PolicySHA256: hashCharacter('3')}, nil
		}},
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: hashCharacter('4'), Dependencies: []string{"observation"}},
		{ID: "after", Kind: workflow.Compute, VersionSHA256: hashCharacter('5'), Dependencies: []string{"observation"}, Compute: realWorkflowCompute("after")},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: hashCharacter('6'), Dependencies: []string{"after"}},
	}}
	evaluator, err := workflow.New(workflow.Config{
		Graph: graph, Guests: guestFactory, ResumeEnabled: true,
		ImmutableRootSHA256: []string{joined.SelectedRoot.IdentitySHA256},
	})
	if err != nil {
		t.Fatal(err)
	}
	suspended, err := evaluator.Start(context.Background(), []byte(`{"resume":"fixture"}`))
	if err != nil || suspended.Disposition != workflow.Suspended || guestFactory.created != 1 || guestFactory.closed != 1 {
		t.Fatalf("suspended=%+v guest=%+v err=%v", suspended, guestFactory, err)
	}
	resumed, err := evaluator.Resume(context.Background(), suspended.State)
	if err != nil || resumed.Disposition != workflow.Completed || guestFactory.created != 2 || guestFactory.closed != 2 || resumed.Metrics.Lookups == 0 {
		t.Fatalf("resumed=%+v guest=%+v err=%v", resumed, guestFactory, err)
	}

	selected := runtimeconfig.MechanismSet{
		Streaming: true, StagedObservation: true, PrivateWorkspace: true, ImmutableBranches: true,
		ChildFanout: true, FunctionCache: true, SingleFlight: true, FreshReevaluation: true,
	}
	_, mechanismEvidence, err := runtimeconfig.ResolveMechanisms(selected, selected)
	if err != nil {
		t.Fatal(err)
	}
	evidence := composable.Evidence{
		SchemaVersion: composable.EvidenceSchemaVersion, SourceCommit: strings.Repeat("a", 40), ArtifactSHA256: hashBytes(artifact),
		ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256, SelectedRootSHA256: joined.SelectedRoot.IdentitySHA256,
		Mechanisms: mechanismEvidence,
		Branch: composable.BranchEvidence{
			ChangedBytes: joined.ChangedBytes, MaterializedBytes: joined.MaterializedBytes, MaxDepth: joined.MaxBranchDepth,
			ReachableRoots: joined.ReachableRoots, DiscardedRoots: joined.DiscardedRoots,
		},
		Children:  composable.ChildEvidence{Count: joined.ChildCount, Completed: joined.Completed, Timeline: joined.Timeline},
		Functions: functionStore.Stats(), Flights: flights.Stats(), Workflow: resumed.Metrics,
		GuestCreated: 4, GuestDestroyed: 4,
		Prepared: wazeroengine.PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1"},
		COW:      wazeroengine.COWProbe{SchemaVersion: "pysolate.cow-probe.v1", Platform: goruntime.GOOS},
		Claims:   []composable.Claim{composable.ClaimCacheReuse, composable.ClaimFreshResume, composable.ClaimRealChildFanout},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	encodedEvidence, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{managerRoot, "right.txt", "normalized", "/Users/", "parent.txt"} {
		if strings.Contains(string(encodedEvidence), forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, encodedEvidence)
		}
	}
}

func TestRealGuestPreparedRuntimeSingleUseParity(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	request := plainRequest(t, "from pathlib import Path\nPath('/tmp/prepared-only').write_text('private')\nresult = {'parity': 'same'}")
	manager, base := newComposableWorkspace(t)
	baselineFactory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: base, WorkspaceOwner: "prepared-parity"}
	baselineRunner, err := baselineFactory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := baselineRunner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := baselineRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true}
	preparedRunner, err := baselineFactory.New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedRunner.(*wazeroengine.Engine)
	if state := prepared.PreparedState(); !state.Selected || !state.Ready || state.PrepareMS < 0 {
		t.Fatalf("initial state=%+v", state)
	}
	probe := prepared.COWProbe()
	if probe.SchemaVersion != "pysolate.cow-probe.v1" || probe.COWSelected || !probe.Fallback || len(probe.Blockers) < 3 {
		t.Fatalf("cow probe=%+v", probe)
	}
	t.Logf("prepared_state=%+v cow_probe=%+v", prepared.PreparedState(), probe)
	first, err := prepared.Run(context.Background(), request, "")
	if err != nil || responseResult(t, first) != responseResult(t, baseline) {
		t.Fatalf("prepared parity err=%v\nfirst=%s\nbaseline=%s", err, first, baseline)
	}
	second, err := prepared.Run(context.Background(), plainRequest(t, "from pathlib import Path\nresult = {'leaked': Path('/tmp/prepared-only').exists()}"), "")
	if err != nil || responseResult(t, second) != `{"leaked":false}` {
		t.Fatalf("fallback freshness err=%v\nsecond=%s", err, second)
	}
	state := prepared.PreparedState()
	if state.Ready || state.PreparedRuns != 1 || state.FreshFallbackRuns != 1 {
		t.Fatalf("terminal state=%+v", state)
	}
	if err := prepared.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	unusedRunner, err := baselineFactory.New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	unused := unusedRunner.(*wazeroengine.Engine)
	if !unused.PreparedState().Ready {
		t.Fatal("unused prepared slot was not ready")
	}
	if err := unused.Close(context.Background()); err != nil || unused.PreparedState().Ready {
		t.Fatalf("unused close err=%v state=%+v", err, unused.PreparedState())
	}
	cowConfig := config
	cowConfig.Mechanisms.MemoryCOW = true
	if _, err := baselineFactory.New(context.Background(), artifact, cowConfig); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
		t.Fatalf("memory COW did not fail closed: %v", err)
	}
}

func TestComposableFeatureMatrixAndOffStateFallback(t *testing.T) {
	requested := runtimeconfig.MechanismSet{
		Streaming: true, StagedObservation: true, PrivateWorkspace: true,
		ImmutableBranches: true, ChildFanout: true, FunctionCache: true,
		SingleFlight: true, FreshReevaluation: true, PreparedRuntime: true, MemoryCOW: true,
	}
	resolved, evidence, err := runtimeconfig.ResolveMechanisms(requested, runtimeconfig.MechanismSet{})
	if err != nil || resolved != (runtimeconfig.MechanismSet{}) || evidence.Validate() != nil {
		t.Fatalf("resolved=%+v evidence=%+v err=%v", resolved, evidence, err)
	}
	for _, name := range []runtimeconfig.MechanismName{
		runtimeconfig.MechanismStreaming, runtimeconfig.MechanismChildFanout,
		runtimeconfig.MechanismFunctionCache, runtimeconfig.MechanismSingleFlight,
		runtimeconfig.MechanismFreshReevaluation, runtimeconfig.MechanismMemoryCOW,
	} {
		if evidence.Disposition(name) != runtimeconfig.MechanismFallback {
			t.Fatalf("%s did not report fallback", name)
		}
	}
	invocation := composableFunctionInvocation(hashCharacter('9'))
	var calls int
	compute := func(context.Context, *agentfunction.Guard) ([]byte, error) {
		calls++
		return []byte(`{"same":true}`), nil
	}
	plain := agentfunction.Engine{}
	first, err := plain.Execute(context.Background(), invocation, compute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := plain.Execute(context.Background(), invocation, compute)
	if err != nil || calls != 2 || first.CacheHit || second.CacheHit || string(first.Value) != string(second.Value) {
		t.Fatalf("off-state first=%+v second=%+v calls=%d err=%v", first, second, calls, err)
	}
}

func TestComposableParentInvalidDiscardsRealChildBranches(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "workspaces")
	_ = os.Mkdir(root, 0o700)
	manager, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, _ := manager.Create(nil, workspace.DefaultLimits())
	baseInfo, _ := manager.Inspect(base)
	lineage, _, _ := manager.PortableIdentity(base)
	started := make(chan struct{}, 1)
	runnerExecutor := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			started <- struct{}{}
			return (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "invalid-child"}).New(ctx, artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request := []byte(`{"run_id":"invalid-child","code":"result = 'private'","inputs":{}}`)
			return subagent.ChildProgram{Request: request}, nil
		}),
	}
	orchestrator, _ := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: lineage, MaxFanout: 1, MaxDepth: 1, Executor: runnerExecutor,
	})
	if err := orchestrator.Stage(context.Background(), composableDescriptor("invalid", lineage)); err != nil {
		t.Fatal(err)
	}
	<-started
	private := orchestrator.PrivateRefs()
	if err := orchestrator.Abort(context.Background(), subagent.ParentInvalid); err != nil {
		t.Fatal(err)
	}
	for _, ref := range private {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("invalid parent retained child %s: %v", ref, err)
		}
	}
	final, _ := manager.Inspect(base)
	if final.WorkspaceSHA256 != baseInfo.WorkspaceSHA256 {
		t.Fatal("invalid parent changed base")
	}
}

func composableDescriptor(id, lineage string) subagent.Descriptor {
	return subagent.Descriptor{
		SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: id, ParentStreamEpoch: "parent-stream-1",
		ParentLineageSHA256: lineage, SourceOccurrence: "suite:1:child:" + id,
		SourceSHA256: hashCharacter('2'), InputsSHA256: hashCharacter('3'), ArtifactSHA256: hashCharacter('4'),
		ExecutionProfileSHA256: hashCharacter('5'), ChildPlanSHA256: hashCharacter('6'), PrivacyPartition: "fixture-private", Depth: 1,
	}
}

func composableFunctionInvocation(rootIdentity string) agentfunction.Invocation {
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: hashCharacter('1'), FunctionSourceSHA256: hashCharacter('2'), ArtifactSHA256: hashCharacter('3'),
		ExecutionProfileSHA256: hashCharacter('4'), ImportClosureSHA256: hashCharacter('5'), CanonicalInputs: []byte(`{"child":"right"}`),
		ImmutableRootSHA256: []string{rootIdentity}, DeterministicSettingsSHA256: hashCharacter('6'),
		OutputSchemaSHA256: hashCharacter('7'), PrivacyPartition: "fixture-private", PolicyEpochSHA256: hashCharacter('8'),
	}
}

func rootContains(t *testing.T, manager *workspace.Manager, root workspace.Root, path string) bool {
	t.Helper()
	branch, err := manager.ForkBranch(root.Ref(), root.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer branch.Discard()
	lease, err := manager.Acquire(branch.Ref(), "inspect-root")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := lease.Snapshot()
	_ = lease.Release()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range snapshot.Entries {
		if entry.Path == path {
			return true
		}
	}
	return false
}

type realWorkflowGuestFactory struct {
	t        *testing.T
	artifact []byte
	manager  *workspace.Manager
	base     workspace.Root
	created  int
	closed   int
}

func (factory *realWorkflowGuestFactory) NewGuest(ctx context.Context) (workflow.Guest, error) {
	branch, err := factory.manager.ForkBranch(factory.base.Ref(), factory.base.WorkspaceSHA256)
	if err != nil {
		return nil, err
	}
	factory.created++
	runner, err := (wazeroengine.Factory{
		WorkspaceManager: factory.manager, WorkspaceRef: branch.Ref(), WorkspaceOwner: "workflow-guest",
	}).New(ctx, factory.artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		_ = branch.Discard()
		return nil, err
	}
	return &realWorkflowGuest{factory: factory, runner: runner, branch: branch}, nil
}

type realWorkflowGuest struct {
	factory *realWorkflowGuestFactory
	runner  engine.Runner
	branch  *workspace.Branch
	ran     bool
}

func (guest *realWorkflowGuest) Run(ctx context.Context, label string) ([]byte, error) {
	if guest.ran {
		return nil, errors.New("workflow Guest is single-use")
	}
	guest.ran = true
	request, _ := json.Marshal(map[string]any{"run_id": "workflow-" + label, "code": "result = {'period': '" + label + "'}", "inputs": map[string]any{}})
	return guest.runner.Run(ctx, request, "")
}

func (guest *realWorkflowGuest) Close(ctx context.Context) error {
	guest.factory.closed++
	return errors.Join(guest.runner.Close(ctx), guest.branch.Discard())
}

func newComposableWorkspace(t *testing.T) (*workspace.Manager, workspace.Ref) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "workspace-manager")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return manager, ref
}

func responseResult(t *testing.T, payload []byte) string {
	t.Helper()
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &response); err != nil || response.Status != "ok" || string(response.Error) != "null" {
		t.Fatalf("invalid response %s: %v", payload, err)
	}
	return string(response.Result)
}

func plainRequest(t *testing.T, code string) []byte {
	t.Helper()
	request, err := json.Marshal(map[string]any{"run_id": "composable-prepared", "code": code, "inputs": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func realWorkflowCompute(label string) workflow.ComputeFunc {
	return func(ctx context.Context, guest workflow.Guest, _ map[string][]byte) ([]byte, error) {
		return guest.(*realWorkflowGuest).Run(ctx, label)
	}
}

func hashCharacter(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
