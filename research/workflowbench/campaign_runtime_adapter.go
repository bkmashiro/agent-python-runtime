package workflowbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workflow"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

var ErrInvalidRuntimeCampaignAdapter = errors.New("invalid runtime campaign adapter")

var campaignAdapterProjectSHA256 = campaignDigest([]byte("pysolate.transparent-campaign.adapter.v1"))

type RuntimeCampaignAdapterConfig struct {
	Guest                  CampaignGuestRunner
	Plans                  map[string]*capability.Plan
	WorkspaceManager       *workspace.Manager
	BaseWorkspaceRef       workspace.Ref
	ArtifactSHA256         string
	ExecutionProfileSHA256 string
	CacheDirectory         string
	Now                    func() time.Time
}

type RuntimeCampaignAdapter struct {
	guest                  CampaignGuestRunner
	plans                  map[string]*capability.Plan
	manager                *workspace.Manager
	baseRef                workspace.Ref
	baseSHA256             string
	baseLineageSHA256      string
	artifactSHA256         string
	executionProfileSHA256 string
	now                    func() time.Time
	functions              agentfunction.Engine
	optimizations          runtimeconfig.MechanismSet
	counter                atomic.Uint64
	mu                     sync.Mutex
	workflows              map[string]campaignWorkflowState
	delegations            map[string]*campaignDelegationState
	stagedChildren         map[string]*campaignStagedChild
}

type campaignWorkflowState struct {
	state     workflow.State
	authority workflow.AuthorityEnvelope
}

type campaignDelegationState struct {
	orchestrator *subagent.Orchestrator
}

type campaignStagedChild struct {
	request CampaignRequest
	launch  chan *CampaignRuntime
	result  chan CampaignOutcome
}

type campaignWorkflowGuest struct{}

func (campaignWorkflowGuest) Close(context.Context) error { return nil }

type campaignWorkflowGuestFactory struct{}

func (campaignWorkflowGuestFactory) NewGuest(context.Context) (workflow.Guest, error) {
	return campaignWorkflowGuest{}, nil
}

func NewRuntimeCampaignAdapter(config RuntimeCampaignAdapterConfig) (*RuntimeCampaignAdapter, error) {
	if config.Guest == nil || len(config.Plans) == 0 || config.WorkspaceManager == nil || config.BaseWorkspaceRef == "" ||
		!campaignDigestPattern.MatchString(config.ArtifactSHA256) || !campaignDigestPattern.MatchString(config.ExecutionProfileSHA256) || config.CacheDirectory == "" {
		return nil, ErrInvalidRuntimeCampaignAdapter
	}
	base, err := config.WorkspaceManager.Inspect(config.BaseWorkspaceRef)
	if err != nil {
		return nil, err
	}
	lineage, _, err := config.WorkspaceManager.PortableIdentity(config.BaseWorkspaceRef)
	if err != nil {
		return nil, err
	}
	plans := make(map[string]*capability.Plan, len(config.Plans))
	for identity, plan := range config.Plans {
		if plan == nil || identity != plan.Identity() {
			return nil, ErrInvalidRuntimeCampaignAdapter
		}
		plans[identity] = plan
	}
	store, err := agentfunction.NewStore(config.CacheDirectory, campaignAdapterProjectSHA256, 8<<20)
	if err != nil {
		return nil, err
	}
	passes, err := passplugin.NewDefaultEnabledCatalog(
		passregistration.AgentFunctionRetention,
		passregistration.AgentFunctionSingleFlight,
		passregistration.FreshWorkflowReevaluation,
	)
	if err != nil {
		return nil, err
	}
	selection, err := passes.LowerMechanisms(runtimeconfig.MechanismSet{})
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &RuntimeCampaignAdapter{
		guest: config.Guest, plans: plans, manager: config.WorkspaceManager, baseRef: config.BaseWorkspaceRef,
		baseSHA256: base.WorkspaceSHA256, baseLineageSHA256: lineage,
		artifactSHA256: config.ArtifactSHA256, executionProfileSHA256: config.ExecutionProfileSHA256, now: now,
		functions: agentfunction.Engine{
			Store: store, CacheEnabled: selection.Mechanisms.FunctionCache, Flights: agentfunction.NewFlightGroup(),
		},
		optimizations: selection.Mechanisms,
		workflows:     make(map[string]campaignWorkflowState), delegations: make(map[string]*campaignDelegationState), stagedChildren: make(map[string]*campaignStagedChild),
	}, nil
}

func (adapter *RuntimeCampaignAdapter) Admit(ctx context.Context, request CampaignRequest, _ CampaignTreatment) CampaignAdmission {
	if adapter == nil || ctx == nil {
		return CampaignAdmission{Reason: "invalid_adapter", Disposition: "rejected"}
	}
	switch request.Execution.Kind {
	case CampaignResumeWorkflow:
		if request.Execution.Resume == nil {
			return CampaignAdmission{Reason: "invalid_resume", Disposition: "rejected"}
		}
		adapter.mu.Lock()
		stored, ok := adapter.workflows[request.Execution.Resume.StateKey]
		adapter.mu.Unlock()
		if !ok {
			return CampaignAdmission{Reason: "workflow_state_missing", Disposition: "rejected"}
		}
		if request.Execution.Resume.Transition == CampaignResumeExpired {
			authority := adapter.workflowAuthority(request, request.Execution.Resume.Transition, stored.authority)
			evaluator, err := workflow.New(workflow.Config{Graph: adapter.workflowGraph(request, nil), Guests: campaignWorkflowGuestFactory{}, ResumeEnabled: adapter.optimizations.FreshReevaluation, Authority: authority})
			if err == nil {
				_, err = evaluator.Resume(ctx, stored.state)
			}
			if !errors.Is(err, workflow.ErrAuthorityUnavailable) {
				return CampaignAdmission{Reason: "expired_resume_not_rejected", Disposition: "rejected"}
			}
			return CampaignAdmission{Reason: "authority_expired", Disposition: "rejected"}
		}
	case CampaignDelegateChild:
		return adapter.admitDelegation(ctx, request)
	}
	return CampaignAdmission{Allowed: true, Reason: "admitted"}
}

func (adapter *RuntimeCampaignAdapter) Execute(ctx context.Context, request CampaignRequest, treatment CampaignTreatment, runtime *CampaignRuntime) CampaignOutcome {
	if adapter == nil || ctx == nil || runtime == nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ErrInvalidRuntimeCampaignAdapter}
	}
	switch request.Execution.Kind {
	case CampaignExactRequest:
		return adapter.executeExact(ctx, request, treatment, runtime)
	case CampaignVerifyWorkspace:
		return adapter.executeWorkspace(ctx, request, treatment, runtime, true)
	case CampaignStartWorkflow:
		return adapter.executeWorkflowStart(ctx, request, runtime)
	case CampaignResumeWorkflow:
		return adapter.executeWorkflowResume(ctx, request, runtime)
	case CampaignDelegateChild:
		return adapter.executeDelegation(ctx, request, runtime)
	case CampaignExecutePython, CampaignConsumeResult:
		if request.Execution.CancelPoint == CampaignCancelAfterWorkspaceFork {
			return adapter.executeWorkspace(ctx, request, treatment, runtime, false)
		}
		return adapter.executeDirect(ctx, request, runtime)
	default:
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ErrInvalidRuntimeCampaignAdapter}
	}
}

func (adapter *RuntimeCampaignAdapter) Close(ctx context.Context) error {
	if adapter == nil || ctx == nil {
		return ErrInvalidRuntimeCampaignAdapter
	}
	adapter.mu.Lock()
	groups := make([]*subagent.Orchestrator, 0, len(adapter.delegations))
	for _, state := range adapter.delegations {
		groups = append(groups, state.orchestrator)
	}
	adapter.delegations = make(map[string]*campaignDelegationState)
	adapter.stagedChildren = make(map[string]*campaignStagedChild)
	adapter.mu.Unlock()
	var joined error
	for _, group := range groups {
		if err := group.Abort(ctx, subagent.ParentCancelled); err != nil && !errors.Is(err, subagent.ErrOrchestratorTerminal) {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (adapter *RuntimeCampaignAdapter) nextPhysicalID(prefix string) string {
	return fmt.Sprintf("campaign-%s-%d", prefix, adapter.counter.Add(1))
}

func (adapter *RuntimeCampaignAdapter) executeDirect(ctx context.Context, request CampaignRequest, runtime *CampaignRuntime) CampaignOutcome {
	physicalID := adapter.nextPhysicalID("guest")
	value, err := runtime.Physical(ctx, physicalID, func(runCtx context.Context) ([]byte, error) {
		return adapter.guest.Execute(runCtx, CampaignGuestExecution{ExecutionID: physicalID, Request: request})
	})
	return CampaignOutcome{Disposition: "complete", Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
}

func (adapter *RuntimeCampaignAdapter) functionInvocation(request CampaignRequest, admission agentfunction.Admission, roots []string, sourceSHA256 string, inputs json.RawMessage) agentfunction.Invocation {
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: admission,
		ProjectSHA256: campaignAdapterProjectSHA256, FunctionSourceSHA256: sourceSHA256,
		ArtifactSHA256: adapter.artifactSHA256, ExecutionProfileSHA256: adapter.executionProfileSHA256,
		ImportClosureSHA256: campaignDigest([]byte("campaign-import-closure-v1")), CanonicalInputs: append(json.RawMessage(nil), inputs...),
		ImmutableRootSHA256: append([]string(nil), roots...), DeterministicSettingsSHA256: campaignDigest([]byte("campaign-deterministic-settings-v1")),
		OutputSchemaSHA256: campaignDigest([]byte("campaign-output-schema-v1")), PrivacyPartition: request.PrivacyPartition,
		PolicyEpochSHA256: campaignDigest([]byte(request.PlanSHA256 + "\x00" + request.GrantSetSHA256)),
	}
}

func (adapter *RuntimeCampaignAdapter) executeExact(ctx context.Context, request CampaignRequest, treatment CampaignTreatment, runtime *CampaignRuntime) CampaignOutcome {
	admission := agentfunction.NotCacheable
	if treatment == CampaignQualified {
		admission = agentfunction.Cacheable
	}
	invocation := adapter.functionInvocation(request, admission, []string{request.WorkspaceFixtureSHA256}, request.SourceSHA256, request.Inputs)
	result, err := adapter.functions.Execute(ctx, invocation, func(runCtx context.Context, guard *agentfunction.Guard) ([]byte, error) {
		physicalID := adapter.nextPhysicalID("exact")
		if err := guard.BindPhysicalExecution(physicalID); err != nil {
			return nil, err
		}
		return runtime.Physical(runCtx, physicalID, func(guestCtx context.Context) ([]byte, error) {
			return adapter.guest.Execute(guestCtx, CampaignGuestExecution{ExecutionID: physicalID, Request: request})
		})
	})
	sharing := "independent"
	if result.Shared || result.CacheHit || result.Disposition == agentfunction.Waiter || result.Disposition == agentfunction.Retained {
		sharing = "exact_shared"
	}
	if err == nil {
		if eventErr := runtime.Event("sharing.decided", sharing, result.PhysicalExecutionID); eventErr != nil {
			err = eventErr
		}
	}
	return CampaignOutcome{Disposition: "complete", Result: result.Value, PhysicalExecutionID: result.PhysicalExecutionID, Sharing: sharing, Err: err}
}

func (adapter *RuntimeCampaignAdapter) executeWorkspace(ctx context.Context, request CampaignRequest, treatment CampaignTreatment, runtime *CampaignRuntime, verify bool) CampaignOutcome {
	branch, err := adapter.manager.ForkBranch(adapter.baseRef, adapter.baseSHA256)
	if err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	if err := runtime.Event("workspace.forked", "private_attempt", ""); err != nil {
		_ = branch.Discard()
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	physicalID := adapter.nextPhysicalID("workspace")
	value, runErr := runtime.Physical(ctx, physicalID, func(runCtx context.Context) ([]byte, error) {
		return adapter.guest.Execute(runCtx, CampaignGuestExecution{
			ExecutionID: physicalID, Request: request,
			Workspace: &CampaignWorkspaceBinding{Manager: adapter.manager, Ref: branch.Ref(), Owner: physicalID},
		})
	})
	if runErr != nil || request.Execution.CancelPoint == CampaignCancelAfterWorkspaceFork {
		_ = branch.Discard()
		_ = runtime.Event("workspace.discarded", string(request.Execution.CancelPoint), "")
		disposition := "failed"
		if request.Execution.CancelPoint == CampaignCancelAfterWorkspaceFork && runErr == nil {
			disposition = "cancelled"
		}
		return CampaignOutcome{Disposition: disposition, Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: runErr}
	}
	root, err := branch.Seal(adapter.baseSHA256)
	if err != nil {
		_ = branch.Discard()
		return CampaignOutcome{Disposition: "failed", Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
	}
	if err := runtime.Event("workspace.sealed", root.WorkspaceSHA256, ""); err != nil {
		_ = adapter.manager.Destroy(root.Ref())
		return CampaignOutcome{Disposition: "failed", Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
	}
	sharing := "independent"
	if verify {
		sharing, err = adapter.verifyRoot(ctx, request, treatment, runtime, root)
	}
	_ = adapter.manager.Destroy(root.Ref())
	return CampaignOutcome{Disposition: "complete", Result: value, PhysicalExecutionID: physicalID, Sharing: sharing, Err: err}
}

func (adapter *RuntimeCampaignAdapter) verifyRoot(ctx context.Context, request CampaignRequest, treatment CampaignTreatment, runtime *CampaignRuntime, root workspace.Root) (string, error) {
	var capsule bytes.Buffer
	info, err := adapter.manager.ExportCapsule(root.Ref(), &capsule)
	if err != nil || info.WorkspaceSHA256 != root.WorkspaceSHA256 {
		return "independent", errors.Join(err, ErrInvalidRuntimeCampaignAdapter)
	}
	capsuleSHA := campaignDigest(capsule.Bytes())
	inputs, _ := json.Marshal(map[string]string{"capsule_sha256": capsuleSHA, "workspace_sha256": root.WorkspaceSHA256})
	admission := agentfunction.NotCacheable
	if treatment == CampaignQualified {
		admission = agentfunction.Cacheable
	}
	invocation := adapter.functionInvocation(request, admission, []string{root.WorkspaceSHA256}, request.Execution.Verifier.SourceSHA256, inputs)
	result, err := adapter.functions.Execute(ctx, invocation, func(runCtx context.Context, guard *agentfunction.Guard) ([]byte, error) {
		physicalID := adapter.nextPhysicalID("verifier")
		if err := guard.BindPhysicalExecution(physicalID); err != nil {
			return nil, err
		}
		return runtime.Physical(runCtx, physicalID, func(context.Context) ([]byte, error) {
			if campaignDigest(capsule.Bytes()) != capsuleSHA {
				return nil, ErrInvalidRuntimeCampaignAdapter
			}
			return json.Marshal(map[string]any{"verified": true, "workspace_sha256": info.WorkspaceSHA256})
		})
	})
	sharing := "independent"
	if result.Shared || result.CacheHit || result.Disposition == agentfunction.Waiter || result.Disposition == agentfunction.Retained {
		sharing = "root_exact_shared"
	}
	if err == nil {
		verifierJSON, marshalErr := json.Marshal(request.Execution.Verifier)
		if marshalErr != nil {
			return sharing, marshalErr
		}
		err = runtime.Event("verification.completed", root.WorkspaceSHA256+":"+campaignDigest(verifierJSON)+":"+sharing, result.PhysicalExecutionID)
	}
	return sharing, err
}

func (adapter *RuntimeCampaignAdapter) workflowAuthority(request CampaignRequest, transition CampaignResumeTransition, previous workflow.AuthorityEnvelope) workflow.AuthorityEnvelope {
	if previous.SchemaVersion == "" {
		previous = workflow.AuthorityEnvelope{
			SchemaVersion: workflow.AuthorityEnvelopeSchemaVersion, PlanSHA256: request.PlanSHA256, GrantSetSHA256: request.GrantSetSHA256,
			PrivacyPartition: request.PrivacyPartition, EpochSHA256: campaignDigest([]byte("workflow-authority-epoch-v1")), NotAfterUnixMS: adapter.now().Add(time.Hour).UnixMilli(),
		}
	}
	switch transition {
	case CampaignResumePlanGrantChanged:
		previous.PlanSHA256 = request.PlanSHA256
		previous.GrantSetSHA256 = request.GrantSetSHA256
		previous.EpochSHA256 = campaignDigest([]byte(request.PlanSHA256 + request.GrantSetSHA256))
	case CampaignResumeExpired:
		previous.NotAfterUnixMS = adapter.now().Add(-time.Millisecond).UnixMilli()
	}
	return previous
}

func (adapter *RuntimeCampaignAdapter) workflowGraph(request CampaignRequest, runtime *CampaignRuntime) workflow.Graph {
	run := func(ctx context.Context) ([]byte, error) {
		if runtime == nil {
			return nil, ErrInvalidRuntimeCampaignAdapter
		}
		physicalID := adapter.nextPhysicalID("workflow")
		return runtime.Physical(ctx, physicalID, func(guestCtx context.Context) ([]byte, error) {
			return adapter.guest.Execute(guestCtx, CampaignGuestExecution{ExecutionID: physicalID, Request: request})
		})
	}
	freshness := campaignDigest([]byte("workflow-freshness-v1"))
	if request.Execution.Resume != nil && request.Execution.Resume.Transition == CampaignResumeFreshnessChanged {
		freshness = campaignDigest([]byte("workflow-freshness-v2"))
	}
	return workflow.Graph{SchemaVersion: workflow.GraphSchemaVersion, WorkflowID: "campaign-workflow-main", Nodes: []workflow.Node{
		{ID: "pre", Kind: workflow.Compute, VersionSHA256: campaignDigest([]byte("workflow-pre-v1")), Compute: func(ctx context.Context, _ workflow.Guest, _ map[string][]byte) ([]byte, error) { return run(ctx) }},
		{ID: "fresh", Kind: workflow.Observation, VersionSHA256: campaignDigest([]byte("workflow-observation-v1")), RefreshOnResume: true, Observe: func(context.Context, workflow.Guest, map[string][]byte) (workflow.ObservedValue, error) {
			return workflow.ObservedValue{Value: []byte(`{"ready":true}`), FreshnessSHA256: freshness, PolicySHA256: campaignDigest([]byte("workflow-observation-policy-v1"))}, nil
		}},
		{ID: "wait", Kind: workflow.Wait, VersionSHA256: campaignDigest([]byte("workflow-wait-v1")), Dependencies: []string{"pre", "fresh"}},
		{ID: "post", Kind: workflow.Compute, VersionSHA256: campaignDigest([]byte("workflow-post-v1")), Dependencies: []string{"fresh"}, Compute: func(ctx context.Context, _ workflow.Guest, _ map[string][]byte) ([]byte, error) { return run(ctx) }},
		{ID: "terminal", Kind: workflow.Terminal, VersionSHA256: campaignDigest([]byte("workflow-terminal-v1")), Dependencies: []string{"post"}},
	}}
}

func (adapter *RuntimeCampaignAdapter) executeWorkflowStart(ctx context.Context, request CampaignRequest, runtime *CampaignRuntime) CampaignOutcome {
	authority := adapter.workflowAuthority(request, CampaignResumeSameAuthority, workflow.AuthorityEnvelope{})
	evaluator, err := workflow.New(workflow.Config{Graph: adapter.workflowGraph(request, runtime), Guests: campaignWorkflowGuestFactory{}, ResumeEnabled: adapter.optimizations.FreshReevaluation, Authority: authority})
	if err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	result, err := evaluator.Start(ctx, nil)
	if err != nil || result.Disposition != workflow.Suspended {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: errors.Join(err, ErrInvalidRuntimeCampaignAdapter)}
	}
	if err := runtime.Event("workflow.waiting", request.Execution.WorkflowStateKey, ""); err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	adapter.mu.Lock()
	adapter.workflows[request.Execution.WorkflowStateKey] = campaignWorkflowState{state: result.State, authority: authority}
	adapter.mu.Unlock()
	value := append(json.RawMessage(nil), result.State.Records["pre"].Value...)
	return CampaignOutcome{Disposition: "complete", Result: value, PhysicalExecutionID: physicalIDFromEvents(runtime), Sharing: "independent"}
}

// physicalIDFromEvents is intentionally empty here; workflow callbacks bind the actual
// physical event, while the adapter outcome requires its identity. The callback path stores
// it in the runtime's latest physical event through this helper.
func physicalIDFromEvents(runtime *CampaignRuntime) string {
	if runtime == nil || runtime.recorder == nil {
		return ""
	}
	runtime.recorder.mu.Lock()
	defer runtime.recorder.mu.Unlock()
	for index := len(runtime.recorder.events) - 1; index >= 0; index-- {
		event := runtime.recorder.events[index]
		if event.ProgramID == runtime.programID && event.Type == "physical.ended" {
			return event.PhysicalExecutionID
		}
	}
	return ""
}

func (adapter *RuntimeCampaignAdapter) executeWorkflowResume(ctx context.Context, request CampaignRequest, runtime *CampaignRuntime) CampaignOutcome {
	adapter.mu.Lock()
	stored, ok := adapter.workflows[request.Execution.Resume.StateKey]
	adapter.mu.Unlock()
	if !ok {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ErrInvalidRuntimeCampaignAdapter}
	}
	authority := adapter.workflowAuthority(request, request.Execution.Resume.Transition, stored.authority)
	evaluator, err := workflow.New(workflow.Config{Graph: adapter.workflowGraph(request, runtime), Guests: campaignWorkflowGuestFactory{}, ResumeEnabled: adapter.optimizations.FreshReevaluation, Authority: authority})
	if err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	if err := runtime.Event("workflow.resumed", string(request.Execution.Resume.Transition), ""); err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	if request.Execution.Resume.Transition == CampaignResumePlanGrantChanged {
		if err := runtime.Event("authority.refreshed", authority.PlanSHA256, ""); err != nil {
			return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
		}
	}
	result, err := evaluator.Resume(ctx, stored.state)
	if err != nil || result.Disposition != workflow.Completed {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: errors.Join(err, ErrInvalidRuntimeCampaignAdapter)}
	}
	return CampaignOutcome{Disposition: "complete", Result: append(json.RawMessage(nil), result.Output...), PhysicalExecutionID: physicalIDFromEvents(runtime), Sharing: "independent"}
}

func (adapter *RuntimeCampaignAdapter) admitDelegation(ctx context.Context, request CampaignRequest) CampaignAdmission {
	contract := request.Execution.Delegation
	if contract == nil {
		return CampaignAdmission{Reason: "invalid_delegation", Disposition: "rejected"}
	}
	expectedParent, _, planErr := campaignPlan(contract.ParentPlanRole)
	child := adapter.plans[request.PlanSHA256]
	childDecision := capability.CompareDelegation(child, child)
	if planErr != nil || expectedParent != contract.ParentPlanSHA256 || !childDecision.Allowed || childDecision.ReservedCalls != contract.ChildReservedCalls {
		return CampaignAdmission{Reason: "invalid_delegation_contract", Disposition: "rejected"}
	}
	adapter.mu.Lock()
	group := adapter.delegations[contract.GroupID]
	if group == nil {
		parent := adapter.plans[contract.ParentPlanSHA256]
		orchestrator, err := subagent.New(subagent.Config{
			Manager: adapter.manager, ParentRef: adapter.baseRef, ParentWorkspaceSHA256: adapter.baseSHA256, ParentLineage: adapter.baseLineageSHA256,
			MaxFanout: 20, MaxDepth: 1, ParentPlan: parent, ChildPlans: adapter.plans, MaxDelegatedCalls: contract.MaxDelegatedCalls,
			Executor: subagent.ExecutorFunc(adapter.executeStagedChild),
		})
		if err != nil {
			adapter.mu.Unlock()
			return CampaignAdmission{Reason: "invalid_delegation", Disposition: "rejected"}
		}
		group = &campaignDelegationState{orchestrator: orchestrator}
		adapter.delegations[contract.GroupID] = group
	}
	if request.Execution.CancelPoint == CampaignCancelAfterParentTerminal {
		adapter.mu.Unlock()
		_ = group.orchestrator.Abort(ctx, subagent.ParentCancelled)
		descriptor := adapter.delegationDescriptor(request)
		if !errors.Is(group.orchestrator.Stage(ctx, descriptor), subagent.ErrOrchestratorTerminal) {
			return CampaignAdmission{Reason: "terminal_not_enforced", Disposition: "rejected"}
		}
		return CampaignAdmission{Reason: "parent_terminal", Disposition: "cancelled"}
	}
	childID := fmt.Sprintf("child-%d", adapter.counter.Add(1))
	staged := &campaignStagedChild{request: request, launch: make(chan *CampaignRuntime, 1), result: make(chan CampaignOutcome, 1)}
	adapter.stagedChildren[childID] = staged
	adapter.mu.Unlock()
	descriptor := adapter.delegationDescriptor(request)
	descriptor.ChildID = childID
	if err := group.orchestrator.Stage(ctx, descriptor); err != nil {
		adapter.mu.Lock()
		delete(adapter.stagedChildren, childID)
		adapter.mu.Unlock()
		reason := "delegation_rejected"
		if errors.Is(err, subagent.ErrAuthorityWidening) {
			reason = "authority_widening"
		} else if errors.Is(err, subagent.ErrDelegationBudget) {
			reason = "delegation_budget"
		}
		return CampaignAdmission{Reason: reason, Disposition: "rejected"}
	}
	return CampaignAdmission{Allowed: true, Reason: "admitted"}
}

func (adapter *RuntimeCampaignAdapter) delegationDescriptor(request CampaignRequest) subagent.Descriptor {
	return subagent.Descriptor{
		SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: "child-pending", ParentStreamEpoch: "campaign-parent-v1",
		ParentLineageSHA256: adapter.baseLineageSHA256, SourceOccurrence: "campaign-fixture", SourceSHA256: request.SourceSHA256,
		InputsSHA256: request.InputsSHA256, ArtifactSHA256: adapter.artifactSHA256, ExecutionProfileSHA256: adapter.executionProfileSHA256,
		ChildPlanSHA256: request.PlanSHA256, PrivacyPartition: request.PrivacyPartition, Depth: 1,
	}
}

func (adapter *RuntimeCampaignAdapter) executeStagedChild(ctx context.Context, invocation subagent.Invocation) error {
	adapter.mu.Lock()
	staged := adapter.stagedChildren[invocation.Descriptor.ChildID]
	adapter.mu.Unlock()
	if staged == nil {
		return ErrInvalidRuntimeCampaignAdapter
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case runtime := <-staged.launch:
		physicalID := adapter.nextPhysicalID("child")
		value, err := runtime.Physical(ctx, physicalID, func(runCtx context.Context) ([]byte, error) {
			return adapter.guest.Execute(runCtx, CampaignGuestExecution{
				ExecutionID: physicalID, Request: staged.request,
				Workspace: &CampaignWorkspaceBinding{Manager: adapter.manager, Ref: invocation.WorkspaceRef, Owner: physicalID},
			})
		})
		staged.result <- CampaignOutcome{Disposition: "complete", Result: value, PhysicalExecutionID: physicalID, Sharing: "independent", Err: err}
		return err
	}
}

func (adapter *RuntimeCampaignAdapter) executeDelegation(ctx context.Context, request CampaignRequest, runtime *CampaignRuntime) CampaignOutcome {
	adapter.mu.Lock()
	var staged *campaignStagedChild
	var childID string
	for id, candidate := range adapter.stagedChildren {
		if candidate.request.SourceSHA256 == request.SourceSHA256 && candidate.request.InputsSHA256 == request.InputsSHA256 && candidate.request.PlanSHA256 == request.PlanSHA256 {
			staged, childID = candidate, id
			break
		}
	}
	adapter.mu.Unlock()
	if staged == nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ErrInvalidRuntimeCampaignAdapter}
	}
	if err := runtime.Event("delegation.child_started", request.Execution.Delegation.GroupID, ""); err != nil {
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: err}
	}
	select {
	case <-ctx.Done():
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ctx.Err()}
	case staged.launch <- runtime:
	}
	select {
	case <-ctx.Done():
		return CampaignOutcome{Disposition: "failed", Sharing: "independent", Err: ctx.Err()}
	case outcome := <-staged.result:
		adapter.mu.Lock()
		delete(adapter.stagedChildren, childID)
		adapter.mu.Unlock()
		return outcome
	}
}
