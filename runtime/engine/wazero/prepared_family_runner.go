package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// PreparedRunnerConfig supplies one member's independent logical authority and workspace.
type PreparedRunnerConfig struct {
	RunConfig        runtimeconfig.RunConfig
	BrokerFactory    BrokerFactory
	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
	WorkspaceOwner   string
	InvocationRef    runtimeconfig.InvocationRef
	Plan             *capability.Plan
}

type borrowedCOWPreparedRuntime struct {
	delegate cowPreparedRuntime
}

func (runtime borrowedCOWPreparedRuntime) prepare(ctx context.Context, engine *Engine) (*preparedInstance, cowCloneLifecycle, error) {
	return runtime.delegate.prepare(ctx, engine)
}
func (borrowedCOWPreparedRuntime) close() error { return nil }
func (runtime borrowedCOWPreparedRuntime) imageState() PreparedImageState {
	return runtime.delegate.imageState()
}

type preparedGrantIdentity struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

func preparedGrantsSHA256(config runtimeconfig.RunConfig) (string, error) {
	keys := make([]string, 0, len(config.CapabilityGrants))
	for key := range config.CapabilityGrants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]preparedGrantIdentity, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, preparedGrantIdentity{Key: key, Name: config.CapabilityGrants[key].Name})
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return digestPreparedBytes(encoded), nil
}

func prepareFamilyBroker(ctx context.Context, factory BrokerFactory, plan *capability.Plan, executionID string) (*capability.Broker, error) {
	if factory == nil {
		return nil, nil
	}
	broker, err := factory(ctx)
	if err != nil {
		return nil, err
	}
	if broker == nil || plan == nil || broker.CapabilityPlan() != plan || broker.CapabilityPlanSHA256() != plan.Identity() || broker.RunIdentity() != executionID {
		return nil, ErrPreparedFamilyDrift
	}
	return broker, nil
}

func (family *PreparedFamily) bindPreparedBroker(broker *capability.Broker, plan *capability.Plan) error {
	if broker == nil {
		return nil
	}
	if _, exists := family.brokers[broker]; exists {
		return ErrPreparedFamilyBrokerReuse
	}
	if _, exists := family.plans[plan]; exists {
		return ErrPreparedFamilyPlanReuse
	}
	if family.brokers == nil {
		family.brokers = make(map[*capability.Broker]struct{})
	}
	if family.plans == nil {
		family.plans = make(map[*capability.Plan]struct{})
	}
	family.brokers[broker] = struct{}{}
	family.plans[plan] = struct{}{}
	return nil
}

// NewRunner reserves one member and returns an ordinary single-use Runner surface.
func (family *PreparedFamily) NewRunner(ctx context.Context, config PreparedRunnerConfig) (enginecontract.Runner, error) {
	if family == nil || ctx == nil || config.InvocationRef.Validate() != nil || config.RunConfig.Validate() != nil || config.RunConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect ||
		(config.BrokerFactory != nil) != (config.Plan != nil) {
		return nil, ErrPreparedFamilyConfig
	}
	planSHA256 := ""
	if config.Plan != nil {
		planSHA256 = config.Plan.Identity()
		if !validPreparedDigest(planSHA256) {
			return nil, ErrPreparedFamilyConfig
		}
	}
	broker, err := prepareFamilyBroker(ctx, config.BrokerFactory, config.Plan, config.InvocationRef.ExecutionID)
	if err != nil {
		return nil, err
	}
	family.mu.Lock()
	defer family.mu.Unlock()
	if family.closed {
		return nil, ErrPreparedFamilyClosed
	}
	workspaceKey := string(config.WorkspaceRef)
	if _, exists := family.invocationIDs[config.InvocationRef.InvocationID]; exists {
		return nil, ErrPreparedFamilyIdentityReuse
	}
	if _, exists := family.executionIDs[config.InvocationRef.ExecutionID]; exists {
		return nil, ErrPreparedFamilyIdentityReuse
	}
	if workspaceKey != "" {
		if _, exists := family.workspaceRefs[workspaceKey]; exists {
			return nil, ErrPreparedFamilyIdentityReuse
		}
	}
	childConfig := cloneFamilyRunConfig(config.RunConfig)
	grantsSHA256, err := preparedGrantsSHA256(childConfig)
	if err != nil {
		return nil, err
	}
	if !preparedRunnerMechanismsSupported(childConfig.Mechanisms) {
		return nil, ErrPreparedFamilyConfig
	}
	if family.disposition == PreparedDispositionPrivateCOW {
		childConfig.Mechanisms.PreparedRuntime = true
		childConfig.Mechanisms.MemoryCOW = true
	} else {
		childConfig.Mechanisms.PreparedRuntime = false
		childConfig.Mechanisms.MemoryCOW = false
	}
	if family.input.validateForConfig(childConfig) != nil {
		return nil, ErrPreparedFamilyDrift
	}
	identity, err := preparedImageIdentity(childConfig, family.input, PreparedNumpyABIV1)
	if err != nil || identity != family.identity {
		return nil, ErrPreparedFamilyDrift
	}
	var brokerFactory BrokerFactory
	if broker != nil {
		brokerFactory = func(context.Context) (*capability.Broker, error) { return broker, nil }
	}
	factory := Factory{
		BrokerFactory: brokerFactory, WorkspaceManager: config.WorkspaceManager,
		WorkspaceRef: config.WorkspaceRef, WorkspaceOwner: config.WorkspaceOwner,
	}
	binding, err := factory.validatedBinding(childConfig)
	if err != nil {
		return nil, err
	}
	if err := family.bindPreparedBroker(broker, config.Plan); err != nil {
		return nil, err
	}
	memberID, err := family.lifecycle.reserve()
	if err != nil {
		delete(family.brokers, broker)
		delete(family.plans, config.Plan)
		return nil, err
	}
	var delegate *Engine
	if family.disposition == PreparedDispositionPrivateCopy {
		delegate, err = newPreparedNumpyCopyEngine(ctx, family.wasm, childConfig, brokerFactory, binding, family.input)
	} else {
		delegate, err = family.newCOWChildEngine(ctx, childConfig, brokerFactory, binding)
	}
	if err != nil {
		_ = family.lifecycle.release(memberID)
		delete(family.brokers, broker)
		delete(family.plans, config.Plan)
		return nil, err
	}
	runner := newPreparedFamilyRunner(delegate, config.InvocationRef, family.lifecycle, memberID)
	runner.preparePrefix = trustedCOWPackageSource
	if config.Plan != nil {
		runner.prepare = config.Plan.PythonPrelude()
	}
	runner.onTerminal = func(id uint64, runID string, outcome PreparedMemberDisposition, response []byte) {
		family.recordTerminal(id, runID, outcome, planSHA256, grantsSHA256, decodeWorkspaceRoot(response))
	}
	runner.onClose = func(id uint64) {
		family.recordClose(id, planSHA256, grantsSHA256)
		if config.WorkspaceManager != nil && config.WorkspaceRef != "" {
			if info, inspectErr := config.WorkspaceManager.Inspect(config.WorkspaceRef); inspectErr == nil {
				family.recordWorkspace(id, info.WorkspaceSHA256)
			}
		}
	}
	family.runners[memberID] = runner
	family.invocations[memberID] = config.InvocationRef
	family.invocationIDs[config.InvocationRef.InvocationID] = struct{}{}
	family.executionIDs[config.InvocationRef.ExecutionID] = struct{}{}
	if workspaceKey != "" {
		family.workspaceRefs[workspaceKey] = struct{}{}
	}
	return runner, nil
}

func preparedRunnerMechanismsSupported(mechanisms runtimeconfig.MechanismSet) bool {
	allowed := runtimeconfig.MechanismSet{
		PrivateWorkspace:  mechanisms.PrivateWorkspace,
		ImmutableBranches: mechanisms.ImmutableBranches,
		PreparedRuntime:   mechanisms.PreparedRuntime,
		MemoryCOW:         mechanisms.MemoryCOW,
		SemanticAnalysis:  mechanisms.SemanticAnalysis,
		SplitPhaseCalls:   mechanisms.SplitPhaseCalls,
	}
	return mechanisms == allowed
}

func (family *PreparedFamily) newCOWChildEngine(ctx context.Context, config runtimeconfig.RunConfig, brokerFactory BrokerFactory, binding *workspaceBinding) (*Engine, error) {
	if family.parent == nil {
		return nil, ErrPreparedFamilyDrift
	}
	family.parent.cowMu.Lock()
	shared := family.parent.cowRuntime
	family.parent.cowMu.Unlock()
	if shared == nil || shared.imageState().PreparedInputSHA256 != family.input.identity {
		return nil, ErrPreparedFamilyDrift
	}
	child, err := newEngine(ctx, family.wasm, config, brokerFactory, binding, nil, nil)
	if err != nil {
		return nil, err
	}
	child.preparedInitMu.Lock()
	child.preparedInitialized = true
	child.preparedTrustedSHA = family.identity
	child.preparedInitMu.Unlock()
	child.cowMu.Lock()
	child.cowRuntime = borrowedCOWPreparedRuntime{delegate: shared}
	child.cowMu.Unlock()
	child.preparedMu.Lock()
	child.preparedState.Ready = true
	child.preparedMu.Unlock()
	return child, nil
}

func (family *PreparedFamily) recordTerminal(memberID uint64, runID string, outcome PreparedMemberDisposition, planSHA256, grantsSHA256, workspaceSHA256 string) {
	family.mu.Lock()
	defer family.mu.Unlock()
	invocation := family.invocations[memberID]
	family.records[memberID] = PreparedMemberRecord{
		SchemaVersion: "pysolate.prepared-family-member.v1", FamilySHA256: family.identity,
		InputSHA256: family.input.identity, MemberID: memberID, RunID: runID,
		AgentRunID: invocation.AgentRunID, TurnSeq: invocation.TurnSeq, OutputItemSeq: invocation.OutputItemSeq, SegmentSeq: invocation.SegmentSeq,
		InvocationID: invocation.InvocationID, InvocationAttempt: invocation.InvocationAttempt, ExecutionID: invocation.ExecutionID,
		PlanSHA256: planSHA256, GrantsSHA256: grantsSHA256,
		PhysicalDisposition: family.disposition, Outcome: outcome,
		FinalWorkspaceSHA256: workspaceSHA256,
	}
}

func (family *PreparedFamily) recordClose(memberID uint64, planSHA256, grantsSHA256 string) {
	family.mu.Lock()
	defer family.mu.Unlock()
	if _, exists := family.records[memberID]; !exists {
		invocation := family.invocations[memberID]
		family.records[memberID] = PreparedMemberRecord{
			SchemaVersion: "pysolate.prepared-family-member.v1", FamilySHA256: family.identity,
			InputSHA256: family.input.identity, MemberID: memberID,
			AgentRunID: invocation.AgentRunID, TurnSeq: invocation.TurnSeq, OutputItemSeq: invocation.OutputItemSeq, SegmentSeq: invocation.SegmentSeq,
			InvocationID: invocation.InvocationID, InvocationAttempt: invocation.InvocationAttempt, ExecutionID: invocation.ExecutionID,
			PlanSHA256: planSHA256, GrantsSHA256: grantsSHA256,
			PhysicalDisposition: family.disposition, Outcome: PreparedMemberClosedUnrun,
		}
	}
	delete(family.runners, memberID)
}

func (family *PreparedFamily) recordWorkspace(memberID uint64, workspaceSHA256 string) {
	if !validPreparedDigest(workspaceSHA256) {
		return
	}
	family.mu.Lock()
	defer family.mu.Unlock()
	record, exists := family.records[memberID]
	if !exists {
		return
	}
	record.FinalWorkspaceSHA256 = workspaceSHA256
	family.records[memberID] = record
}

// Close rejects active members, retires inactive runners, and then releases the image.
func (family *PreparedFamily) Close(ctx context.Context) error {
	if family == nil || family.lifecycle == nil {
		return nil
	}
	if ctx == nil {
		return ErrPreparedFamilyConfig
	}
	family.closeMu.Lock()
	defer family.closeMu.Unlock()
	if family.closeComplete {
		return nil
	}
	family.mu.Lock()
	if !family.closed {
		if err := family.lifecycle.close(); err != nil {
			family.mu.Unlock()
			return err
		}
		family.closed = true
	}
	runners := make([]*preparedFamilyRunner, 0, len(family.runners))
	for _, runner := range family.runners {
		runners = append(runners, runner)
	}
	parent := family.parent
	family.mu.Unlock()

	var runnerErr error
	for _, runner := range runners {
		runnerErr = errors.Join(runnerErr, runner.Close(ctx))
	}
	if runnerErr != nil {
		return runnerErr
	}
	if parent != nil {
		if err := parent.Close(ctx); err != nil {
			return err
		}
	}
	family.mu.Lock()
	family.input.body = nil
	family.wasm = nil
	family.parent = nil
	family.runners = nil
	family.invocations = nil
	family.invocationIDs = nil
	family.executionIDs = nil
	family.workspaceRefs = nil
	family.brokers = nil
	family.plans = nil
	family.closeComplete = true
	family.mu.Unlock()
	return nil
}
