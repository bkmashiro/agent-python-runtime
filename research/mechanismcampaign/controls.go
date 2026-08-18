package mechanismcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type ControlStageConfig struct {
	ArtifactPath  string
	Fixture       agenttrajectory.Fixture
	WorkspaceRoot string
}

type ControlStageResult struct {
	ArgumentMismatchRejected bool
	SourceMismatchRejected   bool
	Snapshot                 semantic.StreamingPreDispatchSnapshot
	Events                   []Event
}

func RunFailClosedControls(ctx context.Context, config ControlStageConfig) (ControlStageResult, error) {
	if ctx == nil || config.ArtifactPath == "" || config.WorkspaceRoot == "" {
		return ControlStageResult{}, errors.New("invalid fail-closed control config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return ControlStageResult{}, err
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return ControlStageResult{}, err
	}
	profile, err := candidateExecutionProfile(artifact)
	if err != nil {
		return ControlStageResult{}, err
	}
	plan, err := agenttrajectory.NewTravelCapabilityPlan(config.Fixture, 1, nil)
	if err != nil {
		return ControlStageResult{}, err
	}
	analyzerConfig := runtimeconfig.DefaultRunConfig()
	analyzerConfig.ExecutionProfile = &profile
	analyzerConfig.Mechanisms.SemanticAnalysis = true
	analyzer, err := wazeroengine.New(ctx, artifact, analyzerConfig)
	if err != nil {
		return ControlStageResult{}, err
	}
	defer analyzer.Close(ctx)
	budget, err := semantic.NewPreDispatchBudget(1)
	if err != nil {
		return ControlStageResult{}, err
	}
	controller, err := semantic.NewStreamingSemanticPreDispatch(plan, budget, campaignLauncher{})
	if err != nil {
		return ControlStageResult{}, err
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: digestBytes(artifact), ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: campaignDigest("control-imports"), CapabilityPlanSHA256: plan.Identity(),
	}
	admission, err := semantic.NewStreamingPrefixAdmission(plan, controller, semantic.PreissueContext{
		StreamEpoch: "stream-control", WorkflowEpoch: "day-trip-v1", FreshnessEpoch: "fixture-v1",
		ExpiryEpoch: "run-end", PrivacyPartition: "control", ParentLineageSHA256: campaignDigest("control-lineage"),
	})
	if err != nil {
		return ControlStageResult{}, err
	}
	chunks := make(chan string, 2)
	chunks <- "weather = travel.weather(\"brighton\")\n"
	chunks <- "result = {\"weather\": weather}\n"
	close(chunks)
	generated, err := semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
		Analyzer: analyzer, Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: chunks,
	})
	if err != nil {
		return ControlStageResult{}, err
	}
	recorder := newEventRecorder()
	_, claimErr := controller.Claim(ctx, "travel.weather", json.RawMessage(`{"destination":"oxford"}`))
	argumentRejected := errors.Is(claimErr, semantic.ErrPreDispatchClaimMismatch)
	if !argumentRejected {
		_ = controller.Finalize(false)
		return ControlStageResult{}, errors.New("argument mismatch was not rejected")
	}
	recorder.record(Event{Type: "control.argument_mismatch", ActorID: "host", LogicalID: "argument-mismatch", Outcome: "rejected"})

	managerRoot := filepath.Join(config.WorkspaceRoot, "manager")
	if err := os.Mkdir(managerRoot, 0o700); err != nil {
		_ = controller.Finalize(false)
		return ControlStageResult{}, err
	}
	manager, err := workspace.NewManager(managerRoot)
	if err != nil {
		_ = controller.Finalize(false)
		return ControlStageResult{}, err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		_ = controller.Finalize(false)
		return ControlStageResult{}, err
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		_ = controller.Finalize(false)
		return ControlStageResult{}, err
	}
	runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		_ = attempt.Discard()
		_ = controller.Finalize(false)
		return ControlStageResult{}, err
	}
	wrongRequest, _ := json.Marshal(map[string]any{
		"run_id": "campaign-control", "code": generated.Source() + "# mutation\n", "inputs": map[string]any{},
	})
	_, sourceErr := semantic.ExecuteGeneratedSource(ctx, runner, attempt, wrongRequest, plan.PythonPrelude(), generated)
	sourceRejected := errors.Is(sourceErr, semantic.ErrAnalysisBinding)
	_ = runner.Close(ctx)
	_ = attempt.Discard()
	if !sourceRejected {
		_ = controller.Finalize(false)
		return ControlStageResult{}, errors.New("source mismatch was not rejected")
	}
	recorder.record(Event{Type: "control.source_mismatch", ActorID: "host", LogicalID: "source-mismatch", Outcome: "rejected"})
	if err := controller.Finalize(false); err != nil {
		return ControlStageResult{}, err
	}
	snapshot := controller.Snapshot()
	if snapshot.RejectedClaims != 1 || snapshot.Consumed != 0 || snapshot.PhysicalIssues != 1 {
		return ControlStageResult{}, errors.New("fail-closed disposition did not close")
	}
	return ControlStageResult{
		ArgumentMismatchRejected: argumentRejected, SourceMismatchRejected: sourceRejected,
		Snapshot: snapshot, Events: recorder.snapshot(),
	}, nil
}

var _ capability.StagedObservationClaimer = (*semantic.StreamingSemanticPreDispatch)(nil)
