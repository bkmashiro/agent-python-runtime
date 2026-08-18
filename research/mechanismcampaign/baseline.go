package mechanismcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type BaselineStageResult struct {
	Candidates map[string]CandidateStageOutput
	Events     []Event
	LatencyNS  uint64
}

func RunBaselineCandidateStage(ctx context.Context, config CandidateStageConfig) (BaselineStageResult, error) {
	if ctx == nil || config.ArtifactPath == "" || config.WorkspaceRoot == "" {
		return BaselineStageResult{}, errors.New("invalid baseline candidate config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return BaselineStageResult{}, err
	}
	profile, err := candidateExecutionProfile(artifact)
	if err != nil {
		return BaselineStageResult{}, err
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return BaselineStageResult{}, err
	}
	manager, err := workspace.NewManager(config.WorkspaceRoot)
	if err != nil {
		return BaselineStageResult{}, err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return BaselineStageResult{}, err
	}
	recorder := newEventRecorder()
	type generated struct {
		id     string
		source string
		err    error
	}
	generatedSources := make(chan generated, 2)
	for _, candidateID := range []string{"brighton", "oxford"} {
		candidateID := candidateID
		go func() {
			recorder.record(Event{Type: "source.generation.start", ActorID: candidateID, LogicalID: "source-" + candidateID})
			var source string
			chunks := candidateSourceChunks(candidateID)
			for index, chunk := range chunks {
				source += chunk
				recorder.record(Event{Type: "source.statement.complete", ActorID: candidateID, LogicalID: candidateID + "-baseline-statement"})
				if index < len(chunks)-1 {
					delay := config.GenerationStep
					if index == 2 && config.FinalizationDelay > 0 {
						delay = config.FinalizationDelay
					}
					timer := time.NewTimer(delay)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						generatedSources <- generated{err: ctx.Err()}
						return
					}
				}
			}
			recorder.record(Event{Type: "source.feed.complete", ActorID: candidateID, LogicalID: "source-" + candidateID, IdentitySHA256: digestTextValue(source)})
			generatedSources <- generated{id: candidateID, source: source}
		}()
	}
	sources := make(map[string]string, 2)
	for range 2 {
		item := <-generatedSources
		if item.err != nil {
			return BaselineStageResult{}, item.err
		}
		sources[item.id] = item.source
	}
	outputs := make(chan CandidateStageOutput, 2)
	errorsOut := make(chan error, 2)
	var wait sync.WaitGroup
	for _, candidateID := range []string{"brighton", "oxford"} {
		candidateID := candidateID
		wait.Add(1)
		go func() {
			defer wait.Done()
			output, runErr := executeBaselineCandidate(ctx, candidateID, artifact, profile, config, manager, base, sources[candidateID], recorder)
			if runErr != nil {
				errorsOut <- runErr
				return
			}
			outputs <- output
		}()
	}
	wait.Wait()
	close(outputs)
	close(errorsOut)
	for runErr := range errorsOut {
		if runErr != nil {
			return BaselineStageResult{}, runErr
		}
	}
	byID := make(map[string]CandidateStageOutput, 2)
	for output := range outputs {
		byID[output.CandidateID] = output
	}
	if byID["brighton"].TotalCostGBP != 118.4 || byID["oxford"].TotalCostGBP != 78 {
		return BaselineStageResult{}, errors.New("baseline candidate oracle mismatch")
	}
	events := recorder.snapshot()
	return BaselineStageResult{Candidates: byID, Events: events, LatencyNS: candidateLatencyNS(events)}, nil
}

func executeBaselineCandidate(ctx context.Context, candidateID string, artifact []byte, profile runtimeconfig.ExecutionProfile, config CandidateStageConfig, manager *workspace.Manager, base workspace.Ref, source string, recorder *eventRecorder) (CandidateStageOutput, error) {
	plan, err := agenttrajectory.NewTravelCapabilityPlan(config.Fixture, 3, func(event agenttrajectory.TravelCallEvent) {
		recorder.record(Event{Type: "request." + event.Phase, ActorID: "host", LogicalID: candidateID + "-" + event.API, PhysicalID: "baseline-request-" + candidateID + "-" + event.API, Outcome: event.Outcome})
	})
	if err != nil {
		return CandidateStageOutput{}, err
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		return CandidateStageOutput{}, err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	runner, err := (wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "baseline-" + candidateID,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "baseline-" + candidateID, Plan: plan})
		},
	}).New(ctx, artifact, runConfig)
	if err != nil {
		_ = attempt.Discard()
		return CandidateStageOutput{}, err
	}
	origin := config.OriginBriefing
	if len(origin) == 0 {
		origin = json.RawMessage(`{"day":"saturday","origin":"london","status":"ready"}`)
	}
	request, _ := json.Marshal(map[string]any{"run_id": "baseline-" + candidateID, "code": source, "inputs": map[string]any{"origin": origin}})
	recorder.record(Event{Type: "guest.start", ActorID: candidateID, LogicalID: "baseline-" + candidateID, PhysicalID: "baseline-guest-" + candidateID})
	result, err := streaming.Execute(ctx, runner, attempt, request, plan.StreamingPythonPrelude())
	if err != nil {
		return CandidateStageOutput{}, err
	}
	recorder.record(Event{Type: "guest.end", ActorID: candidateID, LogicalID: "baseline-" + candidateID, PhysicalID: "baseline-guest-" + candidateID, Outcome: "ok"})
	var response struct {
		Status string `json:"status"`
		Result struct {
			CandidateID  string  `json:"candidate_id"`
			TotalCostGBP float64 `json:"total_cost_gbp"`
		} `json:"result"`
	}
	if json.Unmarshal(result.Response, &response) != nil || response.Status != "ok" || response.Result.CandidateID != candidateID {
		return CandidateStageOutput{}, errors.New("invalid baseline Guest response")
	}
	return CandidateStageOutput{CandidateID: candidateID, TotalCostGBP: response.Result.TotalCostGBP, Response: result.Response, Workspace: result.PublishedWorkspace, SourceSHA256: digestTextValue(source)}, nil
}

func candidateLatencyNS(events []Event) uint64 {
	var last int64
	for _, event := range events {
		if event.Type == "guest.end" && event.AtNS > last {
			last = event.AtNS
		}
	}
	if last < 0 {
		return 0
	}
	return uint64(last)
}
