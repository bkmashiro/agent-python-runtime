package workflowbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

var ErrInvalidCampaignGuestExecution = errors.New("invalid campaign Guest execution")

type CampaignGuestExecutorConfig struct {
	Artifact  []byte
	Plans     map[string]*capability.Plan
	RunConfig *runtimeconfig.RunConfig
}

type CampaignWorkspaceBinding struct {
	Manager *workspace.Manager
	Ref     workspace.Ref
	Owner   string
}

type CampaignGuestExecution struct {
	ExecutionID string
	Request     CampaignRequest
	Workspace   *CampaignWorkspaceBinding
}

type CampaignGuestRunner interface {
	Execute(context.Context, CampaignGuestExecution) (json.RawMessage, error)
}

type CampaignGuestExecutor struct {
	artifact  []byte
	plans     map[string]*capability.Plan
	runConfig runtimeconfig.RunConfig
}

func NewCampaignGuestExecutor(config CampaignGuestExecutorConfig) (*CampaignGuestExecutor, error) {
	if len(config.Artifact) == 0 || len(config.Plans) == 0 {
		return nil, ErrInvalidCampaignGuestExecution
	}
	plans := make(map[string]*capability.Plan, len(config.Plans))
	for identity, plan := range config.Plans {
		if plan == nil || identity == "" || identity != plan.Identity() {
			return nil, ErrInvalidCampaignGuestExecution
		}
		plans[identity] = plan
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	if config.RunConfig != nil {
		runConfig = *config.RunConfig
	}
	if err := runConfig.Validate(); err != nil {
		return nil, errors.Join(ErrInvalidCampaignGuestExecution, err)
	}
	return &CampaignGuestExecutor{artifact: append([]byte(nil), config.Artifact...), plans: plans, runConfig: runConfig}, nil
}

func (executor *CampaignGuestExecutor) ArtifactSHA256() string {
	if executor == nil {
		return ""
	}
	return campaignDigest(executor.artifact)
}

func (executor *CampaignGuestExecutor) ExecutionProfileSHA256() string {
	if executor == nil {
		return ""
	}
	identity, err := runtimeconfig.ExecutionProfileBindingSHA256(executor.runConfig)
	if err != nil {
		return ""
	}
	return identity
}

func (executor *CampaignGuestExecutor) Execute(ctx context.Context, execution CampaignGuestExecution) (json.RawMessage, error) {
	if executor == nil || ctx == nil || execution.ExecutionID == "" || execution.Request.Source == "" ||
		execution.Request.SourceSHA256 != campaignDigest([]byte(execution.Request.Source)) ||
		execution.Request.InputsSHA256 != campaignDigest(execution.Request.Inputs) || !canonicalCampaignJSON(execution.Request.Inputs) {
		return nil, ErrInvalidCampaignGuestExecution
	}
	plan := executor.plans[execution.Request.PlanSHA256]
	if plan == nil {
		return nil, ErrInvalidCampaignGuestExecution
	}
	grants, err := json.Marshal(plan.Grants())
	if err != nil || campaignDigest(grants) != execution.Request.GrantSetSHA256 {
		return nil, ErrInvalidCampaignGuestExecution
	}
	factory := wazeroengine.Factory{}
	if execution.Workspace != nil {
		if execution.Workspace.Manager == nil || execution.Workspace.Ref == "" || execution.Workspace.Owner == "" {
			return nil, ErrInvalidCampaignGuestExecution
		}
		factory.WorkspaceManager = execution.Workspace.Manager
		factory.WorkspaceRef = execution.Workspace.Ref
		factory.WorkspaceOwner = execution.Workspace.Owner
	}
	var broker *capability.Broker
	factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: execution.ExecutionID, Plan: plan})
		if createErr == nil {
			broker = created
		}
		return created, createErr
	}
	runner, err := factory.New(ctx, executor.artifact, executor.runConfig)
	if err != nil {
		return nil, err
	}
	request := runtimeconfig.RunRequest{RunID: execution.ExecutionID, Code: execution.Request.Source, Inputs: append(json.RawMessage(nil), execution.Request.Inputs...)}
	raw, err := runtimeconfig.EncodeRunRequest(request)
	if err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	runCtx, err := enginecontract.WithInvocationRef(ctx, runtimeconfig.InvocationRef{
		AgentRunID: "transparent-campaign", InvocationID: execution.ExecutionID, InvocationAttempt: 1, ExecutionID: execution.ExecutionID,
	})
	if err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	payload, runErr := runner.Run(runCtx, raw, plan.PythonPrelude())
	closeErr := runner.Close(context.Background())
	if runErr != nil || closeErr != nil || broker == nil {
		return nil, errors.Join(runErr, closeErr, ErrInvalidCampaignGuestExecution)
	}
	response, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, payload)
	succeeded := decodeErr == nil && response.Status == runtimeconfig.RunResponseOK
	finalizeErr := broker.Finalize(succeeded)
	if decodeErr != nil || finalizeErr != nil {
		return nil, errors.Join(decodeErr, finalizeErr)
	}
	if response.Status != runtimeconfig.RunResponseOK {
		return nil, fmt.Errorf("campaign Guest returned %s: %v", response.Status, response.Error)
	}
	return append(json.RawMessage(nil), response.Result...), nil
}
