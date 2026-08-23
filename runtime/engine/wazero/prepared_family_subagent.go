package wazero

import (
	"context"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// PreparedChildAuthority is one frozen member authority tuple for the Host-owned
// subagent adapter. The Plan must be a distinct sealed object for this member.
type PreparedChildAuthority struct {
	RunConfig        runtimeconfig.RunConfig
	InvocationRef    runtimeconfig.InvocationRef
	Plan             *capability.Plan
	PrivacyPartition string
}

// PreparedFamilyRunnerFactory binds validated subagent descriptors to one
// prepared family without exposing scheduling or publication authority.
type PreparedFamilyRunnerFactory struct {
	family      *PreparedFamily
	manager     *workspace.Manager
	ownerPrefix string
	artifact    string
	profile     string

	mu       sync.Mutex
	children map[string]PreparedChildAuthority
	used     map[string]struct{}
}

// NewPreparedFamilyRunnerFactory freezes a finite set of per-child authority
// tuples. Descriptor identity, Plan, artifact, profile and privacy are checked
// again before any runner is created.
func NewPreparedFamilyRunnerFactory(
	family *PreparedFamily,
	manager *workspace.Manager,
	ownerPrefix string,
	children map[string]PreparedChildAuthority,
) (*PreparedFamilyRunnerFactory, error) {
	if family == nil || family.lifecycle == nil || manager == nil || ownerPrefix == "" || len(children) == 0 || len(children) > 1024 || uint64(len(children)) > uint64(family.lifecycle.maxConsumers) {
		return nil, ErrPreparedFamilyConfig
	}
	family.mu.Lock()
	if family.closed || family.closeComplete || family.imageConfig.ExecutionProfile == nil {
		family.mu.Unlock()
		return nil, ErrPreparedFamilyClosed
	}
	imageConfig := cloneFamilyRunConfig(family.imageConfig)
	input := family.input
	familyIdentity := family.identity
	family.mu.Unlock()
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(imageConfig)
	if err != nil {
		return nil, ErrPreparedFamilyConfig
	}
	frozen := make(map[string]PreparedChildAuthority, len(children))
	plans := make(map[*capability.Plan]struct{}, len(children))
	invocations := make(map[string]struct{}, len(children))
	executions := make(map[string]struct{}, len(children))
	for childID, authority := range children {
		if authority.InvocationRef.Validate() != nil || authority.InvocationRef.ExecutionID != childID ||
			authority.RunConfig.Validate() != nil || authority.RunConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect ||
			authority.Plan == nil || !validPreparedDigest(authority.Plan.Identity()) || authority.PrivacyPartition == "" {
			return nil, ErrPreparedFamilyConfig
		}
		if _, exists := plans[authority.Plan]; exists {
			return nil, ErrPreparedFamilyPlanReuse
		}
		if _, exists := invocations[authority.InvocationRef.InvocationID]; exists {
			return nil, ErrPreparedFamilyIdentityReuse
		}
		if _, exists := executions[authority.InvocationRef.ExecutionID]; exists {
			return nil, ErrPreparedFamilyIdentityReuse
		}
		config := cloneFamilyRunConfig(authority.RunConfig)
		if input.validateForConfig(config) != nil {
			return nil, ErrPreparedFamilyDrift
		}
		identity, identityErr := preparedImageIdentity(config, input, PreparedNumpyABIV1)
		childProfileSHA256, profileErr := runtimeconfig.ExecutionProfileBindingSHA256(config)
		if identityErr != nil || profileErr != nil || identity != familyIdentity ||
			config.ExecutionProfile == nil || config.ExecutionProfile.ArtifactSHA256() != imageConfig.ExecutionProfile.ArtifactSHA256() || childProfileSHA256 != profileSHA256 {
			return nil, ErrPreparedFamilyDrift
		}
		authority.RunConfig = config
		frozen[childID] = authority
		plans[authority.Plan] = struct{}{}
		invocations[authority.InvocationRef.InvocationID] = struct{}{}
		executions[authority.InvocationRef.ExecutionID] = struct{}{}
	}
	return &PreparedFamilyRunnerFactory{
		family: family, manager: manager, ownerPrefix: ownerPrefix,
		artifact: imageConfig.ExecutionProfile.ArtifactSHA256(), profile: profileSHA256,
		children: frozen, used: make(map[string]struct{}),
	}, nil
}

// NewChildRunner implements subagent.RunnerFactory for one descriptor-bound,
// single-use prepared member.
func (factory *PreparedFamilyRunnerFactory) NewChildRunner(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (enginecontract.Runner, error) {
	if factory == nil || ctx == nil || descriptor.Validate() != nil || ref == "" {
		return nil, ErrPreparedFamilyConfig
	}
	factory.mu.Lock()
	authority, exists := factory.children[descriptor.ChildID]
	if !exists || descriptor.ArtifactSHA256 != factory.artifact || descriptor.ExecutionProfileSHA256 != factory.profile ||
		descriptor.ChildPlanSHA256 != authority.Plan.Identity() || descriptor.PrivacyPartition != authority.PrivacyPartition {
		factory.mu.Unlock()
		return nil, ErrPreparedFamilyDrift
	}
	if _, used := factory.used[descriptor.ChildID]; used {
		factory.mu.Unlock()
		return nil, ErrPreparedFamilyIdentityReuse
	}
	factory.used[descriptor.ChildID] = struct{}{}
	factory.mu.Unlock()

	broker, err := capability.NewBroker(capability.Config{RunIdentity: authority.InvocationRef.ExecutionID, Plan: authority.Plan})
	if err != nil {
		factory.release(descriptor.ChildID)
		return nil, err
	}
	runner, err := factory.family.NewRunner(ctx, PreparedRunnerConfig{
		RunConfig:        authority.RunConfig,
		BrokerFactory:    func(context.Context) (*capability.Broker, error) { return broker, nil },
		WorkspaceManager: factory.manager, WorkspaceRef: ref, WorkspaceOwner: factory.ownerPrefix + ":" + descriptor.ChildID,
		InvocationRef: authority.InvocationRef, Plan: authority.Plan,
	})
	if err != nil {
		factory.release(descriptor.ChildID)
		return nil, err
	}
	return runner, nil
}

func (factory *PreparedFamilyRunnerFactory) release(childID string) {
	factory.mu.Lock()
	delete(factory.used, childID)
	factory.mu.Unlock()
}

var _ subagent.RunnerFactory = (*PreparedFamilyRunnerFactory)(nil)
