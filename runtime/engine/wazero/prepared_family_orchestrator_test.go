package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func emptyPreparedPlan(t *testing.T, maxCalls uint32) *capability.Plan {
	t.Helper()
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func preparedChildDescriptor(id, parentLineage, planSHA256, artifactSHA256, profileSHA256 string) subagent.Descriptor {
	return subagent.Descriptor{
		SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: id, ParentStreamEpoch: "cohort",
		ParentLineageSHA256: parentLineage, SourceOccurrence: "source-" + id,
		SourceSHA256: digestPreparedBytes([]byte("source-" + id)), InputsSHA256: digestPreparedBytes([]byte("inputs-" + id)),
		ArtifactSHA256: artifactSHA256, ExecutionProfileSHA256: profileSHA256,
		ChildPlanSHA256: planSHA256, PrivacyPartition: "private-" + id, Depth: 1,
	}
}

func TestPreparedFamilyBrokerBindingRejectsPlanDriftAndReuse(t *testing.T) {
	planA := emptyPreparedPlan(t, 1)
	planB := emptyPreparedPlan(t, 2)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "broker-binding", Plan: planA})
	if err != nil {
		t.Fatal(err)
	}
	family := &PreparedFamily{brokers: make(map[*capability.Broker]struct{})}
	factory := func(context.Context) (*capability.Broker, error) { return broker, nil }
	if _, err := family.distinctBrokerFactory(factory, planB.Identity())(context.Background()); !errors.Is(err, ErrPreparedFamilyDrift) {
		t.Fatalf("plan drift err=%v", err)
	}
	bound := family.distinctBrokerFactory(factory, planA.Identity())
	if _, err := bound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := bound(context.Background()); !errors.Is(err, ErrPreparedFamilyBrokerReuse) {
		t.Fatalf("reuse err=%v", err)
	}
}

func TestPreparedFamilyWorkspaceFailureAndCancellationRemainPrivate(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
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
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	branches := make(map[string]*workspace.Branch)
	for _, id := range []string{"good", "failed", "cancelled"} {
		branches[id], err = manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
		if err != nil {
			t.Fatal(err)
		}
	}
	imageConfig := runtimeconfig.DefaultRunConfig()
	imageConfig.Timeout = 2 * time.Minute
	imageConfig.ExecutionProfile = profile
	input := realPreparedInput(t, profile, []uint64{2}, []uint64{4, 5})
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{
		ImageConfig: imageConfig, MaxConsumers: 3, MaxActive: 3, Mode: PreparedFamilyPrivateCopy,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	makeRunner := func(id string) engine.Runner {
		runner, runnerErr := family.NewRunner(context.Background(), PreparedRunnerConfig{
			RunConfig: imageConfig, WorkspaceManager: manager, WorkspaceRef: branches[id].Ref(), WorkspaceOwner: "workspace-" + id,
			InvocationRef: runtimeconfig.InvocationRef{AgentRunID: "workspace-cohort", InvocationID: "inv-" + id, InvocationAttempt: 1, ExecutionID: id},
		})
		if runnerErr != nil {
			t.Fatal(runnerErr)
		}
		return runner
	}
	run := func(ctx context.Context, runner engine.Runner, id, code string) (runtimeconfig.RunResponseStatus, error) {
		request := runtimeconfig.RunRequest{
			RunID: id, Code: "import numpy\n" + code, Inputs: json.RawMessage(`{}`),
			Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}},
		}
		raw, encodeErr := runtimeconfig.EncodeRunRequest(request)
		if encodeErr != nil {
			return "", encodeErr
		}
		response, runErr := runner.Run(ctx, raw, "")
		if runErr != nil {
			return "", runErr
		}
		decoded, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, response)
		return decoded.Status, decodeErr
	}
	good := makeRunner("good")
	if status, err := run(context.Background(), good, "good", "dataset[0] = 99\nwith open('/workspace/good.txt','w') as f:\n    f.write('good')\nresult = int(dataset.sum())\n"); err != nil || status != runtimeconfig.RunResponseOK {
		t.Fatalf("good status=%s err=%v", status, err)
	}
	if err := good.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := makeRunner("failed")
	if status, err := run(context.Background(), failed, "failed", "with open('/workspace/failed.txt','w') as f:\n    f.write('failed')\nraise RuntimeError('fixture')\n"); err != nil || status != runtimeconfig.RunResponseError {
		t.Fatalf("failed status=%s err=%v", status, err)
	}
	if err := failed.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancelled := makeRunner("cancelled")
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(cancelledContext, cancelled, "cancelled", "result = int(dataset.sum())\n"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err=%v", err)
	}
	if err := cancelled.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	baseAfter, err := manager.Inspect(base)
	if err != nil || baseAfter.WorkspaceSHA256 != baseInfo.WorkspaceSHA256 {
		t.Fatalf("base changed info=%+v err=%v", baseAfter, err)
	}
	goodInfo, goodErr := manager.Inspect(branches["good"].Ref())
	failedInfo, failedErr := manager.Inspect(branches["failed"].Ref())
	if goodErr != nil || failedErr != nil || goodInfo.WorkspaceSHA256 == baseInfo.WorkspaceSHA256 || failedInfo.WorkspaceSHA256 == baseInfo.WorkspaceSHA256 || goodInfo.WorkspaceSHA256 == failedInfo.WorkspaceSHA256 {
		t.Fatalf("branch isolation good=%+v/%v failed=%+v/%v", goodInfo, goodErr, failedInfo, failedErr)
	}
	root, err := branches["good"].Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := branches["failed"].Discard(); err != nil {
		t.Fatal(err)
	}
	if err := branches["cancelled"].Discard(); err != nil {
		t.Fatal(err)
	}
	records := family.Records()
	if len(records) != 3 || records[0].Outcome != PreparedMemberOK || records[1].Outcome != PreparedMemberGuestError || records[2].Outcome != PreparedMemberCancelled {
		t.Fatalf("records=%+v", records)
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, err := manager.Inspect(root.Ref()); err != nil || info.WorkspaceSHA256 != root.WorkspaceSHA256 {
		t.Fatalf("sealed root after family close info=%+v err=%v", info, err)
	}
}

func TestPreparedFamilyComposesWithPrivateSubagentBranchesAndAttenuatedPlans(t *testing.T) {
	artifact, profile := realPreparedGuest(t)
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
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	parentLineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	parentPlan := emptyPreparedPlan(t, 4)
	leftPlan := emptyPreparedPlan(t, 1)
	rightPlan := emptyPreparedPlan(t, 2)
	plans := map[string]*capability.Plan{leftPlan.Identity(): leftPlan, rightPlan.Identity(): rightPlan}
	imageConfig := runtimeconfig.DefaultRunConfig()
	imageConfig.Timeout = 2 * time.Minute
	imageConfig.ExecutionProfile = profile
	input := realPreparedInput(t, profile, []uint64{3}, []uint64{10, 20, 30})
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(imageConfig)
	if err != nil {
		t.Fatal(err)
	}
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{
		ImageConfig: imageConfig, MaxConsumers: 2, MaxActive: 2, Mode: PreparedFamilyPrivateCopy,
	}, input)
	if err != nil {
		t.Fatal(err)
	}

	var bindingsMu sync.Mutex
	workspaceBindings := make(map[string]workspace.Ref)
	brokerBindings := make(map[string]*capability.Broker)
	factory := subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (engine.Runner, error) {
		plan := plans[descriptor.ChildPlanSHA256]
		memberConfig := imageConfig
		memberConfig.CapabilityGrants = map[string]runtimeconfig.CapabilityGrant{
			"grant-" + descriptor.ChildID: {Name: "grant-" + descriptor.ChildID},
		}
		broker, brokerErr := capability.NewBroker(capability.Config{RunIdentity: descriptor.ChildID, Plan: plan})
		if brokerErr != nil {
			return nil, brokerErr
		}
		runner, runnerErr := family.NewRunner(ctx, PreparedRunnerConfig{
			RunConfig: memberConfig, BrokerFactory: func(context.Context) (*capability.Broker, error) { return broker, nil },
			WorkspaceManager: manager, WorkspaceRef: ref, WorkspaceOwner: "cohort-" + descriptor.ChildID,
			InvocationRef: runtimeconfig.InvocationRef{AgentRunID: "cohort", InvocationID: "inv-" + descriptor.ChildID, InvocationAttempt: 1, ExecutionID: descriptor.ChildID},
			PlanSHA256:    descriptor.ChildPlanSHA256,
		})
		if runnerErr == nil {
			bindingsMu.Lock()
			workspaceBindings[descriptor.ChildID] = ref
			brokerBindings[descriptor.ChildID] = broker
			bindingsMu.Unlock()
		}
		return runner, runnerErr
	})
	builder := subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
		request, err := json.Marshal(runtimeconfig.RunRequest{
			RunID:  descriptor.ChildID,
			Code:   "import numpy\nwith open('/workspace/" + descriptor.ChildID + ".txt','w') as f:\n    f.write('" + descriptor.ChildID + "')\nresult = {'child':'" + descriptor.ChildID + "','sum':int(dataset.sum())}",
			Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}},
		})
		return subagent.ChildProgram{Request: request}, err
	})
	executor := subagent.FreshRunnerExecutor{Factory: factory, Builder: builder}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: manager, ParentRef: base, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256, ParentLineage: parentLineage,
		MaxFanout: 2, MaxDepth: 1, ParentPlan: parentPlan, ChildPlans: plans, MaxDelegatedCalls: 3, Executor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []subagent.Descriptor{
		preparedChildDescriptor("left", parentLineage, leftPlan.Identity(), profile.ArtifactSHA256(), profileSHA256),
		preparedChildDescriptor("right", parentLineage, rightPlan.Identity(), profile.ArtifactSHA256(), profileSHA256),
	} {
		if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
			t.Fatal(err)
		}
	}
	awaited, err := orchestrator.Await(context.Background())
	if err != nil || awaited.Completed != 2 {
		t.Fatalf("awaited=%+v err=%v", awaited, err)
	}
	bindingsMu.Lock()
	leftRef, rightRef := workspaceBindings["left"], workspaceBindings["right"]
	leftBroker, rightBroker := brokerBindings["left"], brokerBindings["right"]
	bindingsMu.Unlock()
	if leftRef == "" || rightRef == "" || leftRef == rightRef || leftBroker == nil || rightBroker == nil || leftBroker == rightBroker {
		t.Fatalf("workspace/broker bindings left=%q right=%q brokers=%p/%p", leftRef, rightRef, leftBroker, rightBroker)
	}
	if _, err := family.NewRunner(context.Background(), PreparedRunnerConfig{
		RunConfig: imageConfig, InvocationRef: runtimeconfig.InvocationRef{AgentRunID: "cohort", InvocationID: "inv-left", InvocationAttempt: 2, ExecutionID: "new-execution"},
	}); !errors.Is(err, ErrPreparedFamilyIdentityReuse) {
		t.Fatalf("duplicate invocation err=%v", err)
	}
	if _, err := family.NewRunner(context.Background(), PreparedRunnerConfig{
		RunConfig: imageConfig, WorkspaceManager: manager, WorkspaceRef: leftRef, WorkspaceOwner: "duplicate-workspace",
		InvocationRef: runtimeconfig.InvocationRef{AgentRunID: "cohort", InvocationID: "new-invocation", InvocationAttempt: 1, ExecutionID: "new-execution"},
	}); !errors.Is(err, ErrPreparedFamilyIdentityReuse) {
		t.Fatalf("duplicate workspace err=%v", err)
	}
	joined, err := orchestrator.Seal(context.Background(), "right")
	if err != nil || joined.SelectedChildID != "right" || len(joined.DiscardedRefs) != 1 || joined.ReservedCalls != 3 {
		t.Fatalf("join=%+v err=%v", joined, err)
	}
	records := family.Records()
	if len(records) != 2 || records[0].PlanSHA256 == records[1].PlanSHA256 || records[0].GrantsSHA256 == records[1].GrantsSHA256 ||
		records[0].FinalWorkspaceSHA256 == "" || records[1].FinalWorkspaceSHA256 == "" || records[0].FinalWorkspaceSHA256 == records[1].FinalWorkspaceSHA256 {
		t.Fatalf("records=%+v", records)
	}
	for _, record := range records {
		if err := record.Validate(); err != nil || record.Outcome != PreparedMemberOK {
			t.Fatalf("record=%+v err=%v", record, err)
		}
	}
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectedInfo, err := manager.Inspect(joined.SelectedRoot.Ref())
	if err != nil || selectedInfo.WorkspaceSHA256 != joined.SelectedRoot.WorkspaceSHA256 {
		t.Fatalf("selected root after family close info=%+v err=%v", selectedInfo, err)
	}
}
