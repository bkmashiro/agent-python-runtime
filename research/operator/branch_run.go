package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const BranchOutcomeSchemaVersion = "pysolate.research-branch-outcome.v1"

type BranchRunConfig struct {
	WASM           []byte
	Runtime        runtimeconfig.RunConfig
	Plan           *capability.Plan
	Parent         playback.Bundle
	Manifest       playback.BranchManifest
	Request        []byte
	TrustedPrepare string
	Invocation     runtimeconfig.InvocationRef

	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
	WorkspaceOwner   string
}

type BranchOutcome struct {
	SchemaVersion      string                    `json:"schema_version"`
	ParentBundleSHA256 string                    `json:"parent_bundle_sha256"`
	BranchSHA256       string                    `json:"branch_sha256"`
	ForkOperation      uint32                    `json:"fork_operation"`
	ChildBundle        playback.Bundle           `json:"child_bundle"`
	Response           runtimeconfig.RunResponse `json:"response"`
	FreshGuest         bool                      `json:"fresh_guest"`
}

// RunBranch performs one fresh child execution. It validates every protected
// parent, request, artifact, profile, plan, grant and initial-workspace
// relation before constructing the Runner; no Guest request field chooses the
// fork, suffix result, endpoint or authority.
func RunBranch(ctx context.Context, config BranchRunConfig) (BranchOutcome, error) {
	if ctx == nil || len(config.WASM) == 0 || config.Plan == nil || config.Manifest.ValidateParent(config.Parent) != nil ||
		config.Invocation.Validate() != nil || config.Invocation.ExecutionID != config.WorkspaceOwner && config.WorkspaceOwner != "" {
		return BranchOutcome{}, errors.New("invalid branch run configuration")
	}
	if config.Manifest.ChildCapabilityPlanSHA256 != config.Plan.Identity() || !equalGrants(config.Manifest.ChildGrants, config.Plan.Grants()) {
		return BranchOutcome{}, errors.New("branch child plan or grant mismatch")
	}
	request, err := runtimeconfig.DecodeRunRequest(config.Request)
	if err != nil {
		return BranchOutcome{}, err
	}
	requestSHA256, err := runtimeconfig.RunRequestSHA256(request)
	if err != nil || requestSHA256 != config.Manifest.RequestSHA256 || playback.SHA256(config.WASM) != config.Manifest.ArtifactSHA256 {
		return BranchOutcome{}, errors.New("branch request or artifact mismatch")
	}
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(config.Runtime)
	if err != nil || profileSHA256 != config.Manifest.ExecutionProfileSHA256 {
		return BranchOutcome{}, errors.New("branch execution profile mismatch")
	}
	workspaceFields := 0
	if config.WorkspaceManager != nil {
		workspaceFields++
	}
	if config.WorkspaceRef != "" {
		workspaceFields++
	}
	if config.WorkspaceOwner != "" {
		workspaceFields++
	}
	if workspaceFields != 0 && workspaceFields != 3 {
		return BranchOutcome{}, errors.New("incomplete branch workspace binding")
	}
	var initialWorkspaceSHA256 string
	if config.WorkspaceManager != nil {
		initial, inspectErr := config.WorkspaceManager.Inspect(config.WorkspaceRef)
		if inspectErr != nil {
			return BranchOutcome{}, inspectErr
		}
		initialWorkspaceSHA256 = initial.WorkspaceSHA256
	}
	if initialWorkspaceSHA256 != config.Manifest.InitialWorkspaceSHA256 {
		return BranchOutcome{}, errors.New("branch initial workspace mismatch")
	}
	prefix := append([]capability.TranscriptEntry(nil), config.Parent.Entries[:config.Manifest.ForkOperation]...)
	var broker *capability.Broker
	factory := wazeroengine.Factory{
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			created, createErr := capability.NewBroker(capability.Config{
				RunIdentity: config.Invocation.ExecutionID, Plan: config.Plan,
				Branch: &capability.BranchConfig{
					ForkOperation: config.Manifest.ForkOperation, PrefixEntries: prefix,
					Mode: capability.BranchMode(config.Manifest.SuffixMode), SuffixEntries: config.Manifest.SuffixEntries,
				},
			})
			if createErr == nil {
				broker = created
			}
			return created, createErr
		},
		WorkspaceManager: config.WorkspaceManager, WorkspaceRef: config.WorkspaceRef, WorkspaceOwner: config.WorkspaceOwner,
	}
	runner, err := factory.New(ctx, config.WASM, config.Runtime)
	if err != nil {
		return BranchOutcome{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = runner.Close(context.Background())
		}
	}()
	runContext, err := enginecontract.WithInvocationRef(ctx, config.Invocation)
	if err != nil {
		return BranchOutcome{}, err
	}
	payload, runErr := runner.Run(runContext, config.Request, config.TrustedPrepare)
	closeErr := runner.Close(context.Background())
	closed = true
	if err := errors.Join(runErr, closeErr); err != nil {
		return BranchOutcome{}, err
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(request, payload)
	if err != nil || broker == nil {
		return BranchOutcome{}, errors.Join(err, errors.New("branch broker or response unavailable"))
	}
	transcript := broker.SnapshotTranscript()
	if uint32(len(transcript)) != broker.Calls() {
		return BranchOutcome{}, errors.New("branch transcript is incomplete")
	}
	resultSHA256, err := playback.CanonicalSHA256(response.Result)
	if err != nil {
		return BranchOutcome{}, err
	}
	finalWorkspaceSHA256 := ""
	if config.WorkspaceManager != nil {
		final, inspectErr := config.WorkspaceManager.Inspect(config.WorkspaceRef)
		if inspectErr != nil {
			return BranchOutcome{}, inspectErr
		}
		finalWorkspaceSHA256 = final.WorkspaceSHA256
	}
	child, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: config.Plan.Identity(), RequestSHA256: requestSHA256,
		ArtifactSHA256: playback.SHA256(config.WASM), ExecutionProfileSHA256: profileSHA256,
		ExpectedStatus: string(response.Status), ExpectedResultSHA256: resultSHA256,
		InitialWorkspaceSHA256: initialWorkspaceSHA256, FinalWorkspaceSHA256: finalWorkspaceSHA256,
		Grants: config.Plan.Grants(),
	}, transcript)
	if err != nil {
		return BranchOutcome{}, fmt.Errorf("author branch child Bundle: %w", err)
	}
	return BranchOutcome{
		SchemaVersion: BranchOutcomeSchemaVersion, ParentBundleSHA256: config.Parent.Identity,
		BranchSHA256: config.Manifest.Identity, ForkOperation: config.Manifest.ForkOperation,
		ChildBundle: child, Response: response, FreshGuest: true,
	}, nil
}

func equalGrants(left, right []capability.GrantBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func EncodeBranchOutcome(outcome BranchOutcome) ([]byte, error) {
	if outcome.SchemaVersion != BranchOutcomeSchemaVersion || outcome.ParentBundleSHA256 == "" || outcome.BranchSHA256 == "" ||
		outcome.ChildBundle.Identity == "" || !outcome.FreshGuest {
		return nil, errors.New("invalid branch outcome")
	}
	return json.Marshal(outcome)
}
