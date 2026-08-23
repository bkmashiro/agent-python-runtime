package wazero

import (
	"context"
	"errors"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type PreparedRunnerConfig struct {
	RunConfig        runtimeconfig.RunConfig
	BrokerFactory    BrokerFactory
	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
	WorkspaceOwner   string
	InvocationRef    runtimeconfig.InvocationRef
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

func (family *PreparedFamily) NewRunner(ctx context.Context, config PreparedRunnerConfig) (enginecontract.Runner, error) {
	if family == nil || ctx == nil || config.InvocationRef.Validate() != nil || config.RunConfig.Validate() != nil || config.RunConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect {
		return nil, ErrPreparedFamilyConfig
	}
	family.mu.Lock()
	defer family.mu.Unlock()
	if family.closed {
		return nil, ErrPreparedFamilyClosed
	}
	childConfig := cloneFamilyRunConfig(config.RunConfig)
	if !preparedRunnerMechanismsSupported(childConfig.Mechanisms) {
		return nil, ErrPreparedFamilyConfig
	}
	if family.disposition == PreparedDispositionPrivateCOW {
		childConfig.Mechanisms.PreparedRuntime = true
		childConfig.Mechanisms.MemoryCOW = true
	}
	if family.input.validateForConfig(childConfig) != nil {
		return nil, ErrPreparedFamilyDrift
	}
	identity, err := preparedImageIdentity(childConfig, family.input, PreparedNumpyABIV1)
	if err != nil || identity != family.identity {
		return nil, ErrPreparedFamilyDrift
	}
	factory := Factory{
		BrokerFactory: config.BrokerFactory, WorkspaceManager: config.WorkspaceManager,
		WorkspaceRef: config.WorkspaceRef, WorkspaceOwner: config.WorkspaceOwner,
	}
	binding, err := factory.validatedBinding(childConfig)
	if err != nil {
		return nil, err
	}
	memberID, err := family.lifecycle.reserve()
	if err != nil {
		return nil, err
	}
	var delegate *Engine
	if family.disposition == PreparedDispositionPrivateCopy {
		delegate, err = newPreparedNumpyCopyEngine(ctx, family.wasm, childConfig, config.BrokerFactory, binding, family.input)
	} else {
		delegate, err = family.newCOWChildEngine(ctx, childConfig, config.BrokerFactory, binding)
	}
	if err != nil {
		_ = family.lifecycle.retire(memberID)
		return nil, err
	}
	runner := newPreparedFamilyRunner(delegate, config.InvocationRef, family.lifecycle, memberID)
	runner.onTerminal = family.recordTerminal
	runner.onClose = family.recordClose
	family.runners[memberID] = runner
	family.invocations[memberID] = config.InvocationRef
	return runner, nil
}

func preparedRunnerMechanismsSupported(mechanisms runtimeconfig.MechanismSet) bool {
	allowed := runtimeconfig.MechanismSet{
		PrivateWorkspace:  mechanisms.PrivateWorkspace,
		ImmutableBranches: mechanisms.ImmutableBranches,
		PreparedRuntime:   mechanisms.PreparedRuntime,
		MemoryCOW:         mechanisms.MemoryCOW,
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
	child, err := newEngine(ctx, family.wasm, config, brokerFactory, binding, nil)
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

func (family *PreparedFamily) recordTerminal(memberID uint64, runID string, outcome PreparedMemberDisposition, response []byte) {
	family.mu.Lock()
	defer family.mu.Unlock()
	invocation := family.invocations[memberID]
	family.records[memberID] = PreparedMemberRecord{
		SchemaVersion: "pysolate.prepared-family-member.v1", FamilySHA256: family.identity,
		InputSHA256: family.input.identity, MemberID: memberID, RunID: runID,
		InvocationID: invocation.InvocationID, ExecutionID: invocation.ExecutionID,
		PhysicalDisposition: family.disposition, Outcome: outcome,
		FinalWorkspaceSHA256: decodeWorkspaceRoot(response),
	}
}

func (family *PreparedFamily) recordClose(memberID uint64) {
	family.mu.Lock()
	defer family.mu.Unlock()
	if _, exists := family.records[memberID]; !exists {
		invocation := family.invocations[memberID]
		family.records[memberID] = PreparedMemberRecord{
			SchemaVersion: "pysolate.prepared-family-member.v1", FamilySHA256: family.identity,
			InputSHA256: family.input.identity, MemberID: memberID,
			InvocationID: invocation.InvocationID, ExecutionID: invocation.ExecutionID,
			PhysicalDisposition: family.disposition, Outcome: PreparedMemberClosedUnrun,
		}
	}
	delete(family.runners, memberID)
}

func (family *PreparedFamily) Close(ctx context.Context) error {
	if family == nil || family.lifecycle == nil {
		return nil
	}
	family.mu.Lock()
	if family.closed {
		family.mu.Unlock()
		return nil
	}
	if err := family.lifecycle.close(); err != nil {
		family.mu.Unlock()
		return err
	}
	family.closed = true
	runners := make([]*preparedFamilyRunner, 0, len(family.runners))
	for _, runner := range family.runners {
		runners = append(runners, runner)
	}
	parent := family.parent
	family.mu.Unlock()

	var result error
	for _, runner := range runners {
		result = errors.Join(result, runner.Close(ctx))
	}
	if parent != nil {
		result = errors.Join(result, parent.Close(ctx))
	}
	family.mu.Lock()
	family.input.body = nil
	family.parent = nil
	family.mu.Unlock()
	return result
}
