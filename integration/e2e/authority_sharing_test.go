package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestAuthorityBifurcationAndExactRootVerifierSharing(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	manager, base := newComposableWorkspace(t)
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	parentLineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}

	producerInvocation := composableFunctionInvocation(baseInfo.WorkspaceSHA256)
	producerInvocation.CanonicalInputs = json.RawMessage(`{"producer":"shared"}`)
	producerFlights := agentfunction.NewFlightGroup()
	producerEngine := agentfunction.Engine{Flights: producerFlights}
	var producerRuns atomic.Uint32
	producerStarted := make(chan struct{})
	producerRelease := make(chan struct{})
	producerOnce := sync.Once{}
	producer := func(_ context.Context, guard *agentfunction.Guard) ([]byte, error) {
		producerRuns.Add(1)
		if err := guard.BindPhysicalExecution("producer-1"); err != nil {
			return nil, err
		}
		producerOnce.Do(func() { close(producerStarted) })
		<-producerRelease
		return []byte("shared-result"), nil
	}
	type producerOutcome struct {
		result agentfunction.Result
		err    error
	}
	producerOutcomes := make(chan producerOutcome, 2)
	go func() {
		result, err := producerEngine.Execute(context.Background(), producerInvocation, producer)
		producerOutcomes <- producerOutcome{result, err}
	}()
	<-producerStarted
	go func() {
		result, err := producerEngine.Execute(context.Background(), producerInvocation, producer)
		producerOutcomes <- producerOutcome{result, err}
	}()
	deadline := time.Now().Add(time.Second)
	for producerFlights.Stats().Waiters != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(producerRelease)
	producerDispositions := map[agentfunction.Disposition]int{}
	for range 2 {
		outcome := <-producerOutcomes
		if outcome.err != nil || string(outcome.result.Value) != "shared-result" || outcome.result.PhysicalExecutionID != "producer-1" {
			t.Fatalf("producer outcome=%+v err=%v", outcome.result, outcome.err)
		}
		producerDispositions[outcome.result.Disposition]++
	}
	if producerRuns.Load() != 1 || producerDispositions[agentfunction.Leader] != 1 || producerDispositions[agentfunction.Waiter] != 1 {
		t.Fatalf("producer runs=%d dispositions=%v", producerRuns.Load(), producerDispositions)
	}

	parentPlan, childPlans := bifurcationPlans(t)
	var childGuests atomic.Uint32
	childRunner := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
			childGuests.Add(1)
			return (wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "bifurcation-" + descriptor.ChildID}).New(ctx, artifact, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request, err := json.Marshal(map[string]any{
				"run_id": "bifurcation-" + descriptor.ChildID,
				"code":   "from pathlib import Path\nPath('/workspace/" + descriptor.ChildID + ".txt').write_text('shared-result')\nresult = '" + descriptor.ChildID + "'",
				"inputs": map[string]any{},
			})
			return subagent.ChildProgram{Request: request}, err
		}),
	}
	var refsMu sync.Mutex
	refs := map[string]workspace.Ref{}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: childPlans, MaxDelegatedCalls: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			refsMu.Lock()
			refs[invocation.Descriptor.ChildID] = invocation.WorkspaceRef
			refsMu.Unlock()
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"left", "right"}
	for _, id := range ids {
		descriptor := composableDescriptor(id, parentLineage)
		descriptor.ChildPlanSHA256 = planForChild(t, childPlans, id).Identity()
		if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
			t.Fatal(err)
		}
	}
	joined, err := orchestrator.Seal(context.Background(), "left")
	if err != nil {
		t.Fatal(err)
	}
	if producerRuns.Load() != 1 || childGuests.Load() != 2 || joined.ReservedCalls != 2 || refs["left"] == refs["right"] || !rootContains(t, manager, joined.SelectedRoot, "left.txt") {
		t.Fatalf("producer=%d guests=%d joined=%+v refs=%v", producerRuns.Load(), childGuests.Load(), joined, refs)
	}
	for _, ref := range joined.DiscardedRefs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("discarded sibling remained ref=%q err=%v", ref, err)
		}
	}

	var siblingRuns atomic.Uint32
	failedRefs := map[string]workspace.Ref{}
	failing, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: 2, MaxDepth: 1,
		ParentPlan: parentPlan, ChildPlans: childPlans, MaxDelegatedCalls: 2,
		Executor: subagent.ExecutorFunc(func(ctx context.Context, invocation subagent.Invocation) error {
			refsMu.Lock()
			failedRefs[invocation.Descriptor.ChildID] = invocation.WorkspaceRef
			refsMu.Unlock()
			if invocation.Descriptor.ChildID == "left" {
				return errors.New("intentional consumer failure")
			}
			siblingRuns.Add(1)
			return childRunner.Execute(ctx, invocation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		descriptor := composableDescriptor(id, parentLineage)
		descriptor.ChildPlanSHA256 = planForChild(t, childPlans, id).Identity()
		if err := failing.Stage(context.Background(), descriptor); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := failing.Seal(context.Background(), "right"); !errors.Is(err, subagent.ErrChildExecution) {
		t.Fatalf("failing consumer seal error=%v", err)
	}
	if siblingRuns.Load() != 1 {
		t.Fatalf("failing consumer canceled sibling runs=%d", siblingRuns.Load())
	}
	for id, ref := range failedRefs {
		if _, err := manager.Inspect(ref); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("failed group published private ref child=%s ref=%q err=%v", id, ref, err)
		}
	}

	verifierInvocation := composableFunctionInvocation(joined.SelectedRoot.IdentitySHA256)
	verifierInvocation.FunctionSourceSHA256 = hashBytes([]byte("exact-root-verifier-v1"))
	verifierInvocation.CanonicalInputs = json.RawMessage(`{"verifier":"v1"}`)
	verifierFlights := agentfunction.NewFlightGroup()
	verifierEngine := agentfunction.Engine{Flights: verifierFlights}
	var verifierRuns atomic.Uint32
	verifierStarted := make(chan struct{})
	verifierRelease := make(chan struct{})
	verifierOnce := sync.Once{}
	verify := func(_ context.Context, guard *agentfunction.Guard) ([]byte, error) {
		run := verifierRuns.Add(1)
		if err := guard.BindPhysicalExecution(fmt.Sprintf("verifier-%d", run)); err != nil {
			return nil, err
		}
		verifierOnce.Do(func() { close(verifierStarted) })
		if run == 1 {
			<-verifierRelease
		}
		return []byte("verified"), nil
	}
	verifierOutcomes := make(chan producerOutcome, 2)
	go func() {
		result, err := verifierEngine.Execute(context.Background(), verifierInvocation, verify)
		verifierOutcomes <- producerOutcome{result, err}
	}()
	<-verifierStarted
	go func() {
		result, err := verifierEngine.Execute(context.Background(), verifierInvocation, verify)
		verifierOutcomes <- producerOutcome{result, err}
	}()
	deadline = time.Now().Add(time.Second)
	for verifierFlights.Stats().Waiters != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(verifierRelease)
	for range 2 {
		outcome := <-verifierOutcomes
		if outcome.err != nil || outcome.result.PhysicalExecutionID != "verifier-1" {
			t.Fatalf("exact verifier outcome=%+v err=%v", outcome.result, outcome.err)
		}
	}
	if verifierRuns.Load() != 1 {
		t.Fatalf("exact verifier runs=%d", verifierRuns.Load())
	}

	distinct, err := manager.Create([]workspace.InitialFile{{Path: "different.txt", Data: []byte("different")}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	distinctInfo, err := manager.Inspect(distinct)
	if err != nil {
		t.Fatal(err)
	}
	distinctInvocation := verifierInvocation
	distinctInvocation.ImmutableRootSHA256 = []string{distinctInfo.WorkspaceSHA256}
	distinctResult, err := verifierEngine.Execute(context.Background(), distinctInvocation, verify)
	if err != nil || distinctResult.PhysicalExecutionID != "verifier-2" || verifierRuns.Load() != 2 {
		t.Fatalf("distinct verifier=%+v runs=%d err=%v", distinctResult, verifierRuns.Load(), err)
	}
}

func bifurcationPlans(t *testing.T) (*capability.Plan, map[string]*capability.Plan) {
	t.Helper()
	leftSpec, leftGrant := bifurcationCapability(t, "left")
	rightSpec, rightGrant := bifurcationCapability(t, "right")
	parent := sealCapabilityPlan(t, 2, []capability.Spec{leftSpec, rightSpec}, []capability.Grant{leftGrant, rightGrant})
	left := sealCapabilityPlan(t, 1, []capability.Spec{leftSpec}, []capability.Grant{leftGrant})
	right := sealCapabilityPlan(t, 1, []capability.Spec{rightSpec}, []capability.Grant{rightGrant})
	return parent, map[string]*capability.Plan{left.Identity(): left, right.Identity(): right}
}

func bifurcationCapability(t *testing.T, id string) (capability.Spec, capability.Grant) {
	t.Helper()
	grant, err := capability.NewGrant(json.RawMessage(`{"consumer":"` + id + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	return capability.Spec{
		Name: "consumer." + id, Version: "pysolate.consumer." + id + ".v1", Description: "Bounded consumer " + id,
		EffectClass: capability.EffectWorkspaceRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "pysolate.consumer." + id + ".handler.v1",
		InputSchema:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}, grant
}

func sealCapabilityPlan(t *testing.T, maxCalls uint32, specs []capability.Spec, grants []capability.Grant) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	for index, spec := range specs {
		if err := registry.Register(spec, grants[index], capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
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

func planForChild(t *testing.T, plans map[string]*capability.Plan, childID string) *capability.Plan {
	t.Helper()
	name := "consumer." + childID
	for _, plan := range plans {
		for _, spec := range plan.Specs() {
			if spec.Name == name {
				return plan
			}
		}
	}
	t.Fatalf("missing child plan %q", childID)
	return nil
}
