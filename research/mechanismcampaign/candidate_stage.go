package mechanismcampaign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type CandidateStageConfig struct {
	ArtifactPath      string
	Fixture           agenttrajectory.Fixture
	WorkspaceRoot     string
	GenerationStep    time.Duration
	FinalizationDelay time.Duration
	EnableCOW         bool
	OriginBriefing    json.RawMessage
}

type CandidateStageResult struct {
	Candidates      map[string]CandidateStageOutput
	Events          []Event
	Selected        workspace.Ref
	Base            workspace.Ref
	SelectedCapsule []byte
	SelectedInfo    workspace.CapsuleInfo
	SelectedRoot    workspace.Root
	LatencyNS       uint64
}

type CandidateStageOutput struct {
	CandidateID        string
	TotalCostGBP       float64
	Response           json.RawMessage
	Workspace          workspace.Ref
	SourceSHA256       string
	ControllerSnapshot semantic.StreamingPreDispatchSnapshot
	AdmissionSnapshot  semantic.StreamingPrefixAdmissionSnapshot
	COWSelected        bool
	COWFallbackReason  string
}

type candidatePrepared struct {
	id         string
	plan       *capability.Plan
	controller *semantic.StreamingSemanticPreDispatch
	admission  *semantic.StreamingPrefixAdmission
	generated  semantic.GeneratedSource
}

func RunCandidateStage(ctx context.Context, config CandidateStageConfig) (CandidateStageResult, error) {
	if ctx == nil || config.ArtifactPath == "" || !filepath.IsAbs(config.ArtifactPath) ||
		config.WorkspaceRoot == "" || !filepath.IsAbs(config.WorkspaceRoot) || config.GenerationStep <= 0 ||
		config.Fixture.AggregateSHA256 != agenttrajectory.DayTripFixtureAggregateSHA256 {
		return CandidateStageResult{}, errors.New("invalid unified candidate-stage config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return CandidateStageResult{}, err
	}
	profile, err := candidateExecutionProfile(artifact)
	if err != nil {
		return CandidateStageResult{}, err
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return CandidateStageResult{}, err
	}
	manager, err := workspace.NewManager(config.WorkspaceRoot)
	if err != nil {
		return CandidateStageResult{}, err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return CandidateStageResult{}, err
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		return CandidateStageResult{}, err
	}
	recorder := newEventRecorder()
	prepared := make(map[string]*candidatePrepared, 2)
	var preparedMu sync.Mutex
	var prepareWG sync.WaitGroup
	errCh := make(chan error, 2)
	for _, id := range []string{"brighton", "oxford"} {
		candidateID := id
		prepareWG.Add(1)
		go func() {
			defer prepareWG.Done()
			value, prepareErr := prepareCandidate(ctx, candidateID, artifact, profile, config, lineage, recorder)
			if prepareErr != nil {
				errCh <- prepareErr
				return
			}
			preparedMu.Lock()
			prepared[candidateID] = value
			preparedMu.Unlock()
		}()
	}
	prepareWG.Wait()
	close(errCh)
	if err := errors.Join(channelErrors(errCh)...); err != nil {
		for _, item := range prepared {
			_ = item.controller.Finalize(false)
		}
		return CandidateStageResult{}, err
	}
	if len(prepared) != 2 {
		return CandidateStageResult{}, errors.New("candidate source preparation did not close")
	}

	outputs := make(map[string]CandidateStageOutput, 2)
	var outputsMu sync.Mutex
	var executeWG sync.WaitGroup
	executeErrors := make(chan error, 2)
	for _, id := range []string{"brighton", "oxford"} {
		candidateID := id
		item := prepared[candidateID]
		executeWG.Add(1)
		go func() {
			defer executeWG.Done()
			output, executeErr := executeCandidate(ctx, candidateID, artifact, profile, config.EnableCOW, config.OriginBriefing, manager, base, item, recorder)
			if executeErr != nil {
				executeErrors <- executeErr
				return
			}
			outputsMu.Lock()
			outputs[candidateID] = output
			outputsMu.Unlock()
		}()
	}
	executeWG.Wait()
	close(executeErrors)
	if err := errors.Join(channelErrors(executeErrors)...); err != nil {
		return CandidateStageResult{}, err
	}
	brighton, brightonOK := outputs["brighton"]
	oxford, oxfordOK := outputs["oxford"]
	if !brightonOK || !oxfordOK || brighton.TotalCostGBP != 118.4 || oxford.TotalCostGBP != 78 {
		return CandidateStageResult{}, errors.New("candidate observations do not match the frozen oracle")
	}
	oxfordInfo, err := manager.Inspect(oxford.Workspace)
	if err != nil {
		return CandidateStageResult{}, err
	}
	selectedBranch, err := manager.ForkBranch(oxford.Workspace, oxfordInfo.WorkspaceSHA256)
	if err != nil {
		return CandidateStageResult{}, err
	}
	selectedRoot, err := selectedBranch.Seal(oxfordInfo.WorkspaceSHA256)
	if err != nil {
		return CandidateStageResult{}, err
	}
	var capsule bytes.Buffer
	capsuleInfo, err := manager.ExportCapsule(selectedRoot.Ref(), &capsule)
	if err != nil {
		return CandidateStageResult{}, err
	}
	if capsuleInfo.EntryCount == 0 || capsuleInfo.TotalBytes == 0 {
		return CandidateStageResult{}, errors.New("selected candidate capsule contains no resumable state")
	}
	recorder.record(Event{Type: "capsule.export", ActorID: "host", LogicalID: "oxford", IdentitySHA256: capsuleInfo.WorkspaceSHA256, Outcome: "serialized"})
	if err := manager.Destroy(brighton.Workspace); err != nil {
		return CandidateStageResult{}, err
	}
	if err := manager.Destroy(oxford.Workspace); err != nil {
		return CandidateStageResult{}, err
	}
	recorder.record(Event{Type: "branch.discard", ActorID: "host", LogicalID: "brighton", Outcome: "discarded"})
	recorder.record(Event{Type: "branch.seal", ActorID: "host", LogicalID: "oxford", IdentitySHA256: selectedRoot.IdentitySHA256, Outcome: "selected"})
	return CandidateStageResult{
		Candidates: outputs, Events: recorder.snapshot(), Selected: selectedRoot.Ref(), Base: base,
		SelectedCapsule: append([]byte(nil), capsule.Bytes()...), SelectedInfo: capsuleInfo, SelectedRoot: selectedRoot,
		LatencyNS: candidateLatencyNS(recorder.snapshot()),
	}, nil
}

func prepareCandidate(ctx context.Context, candidateID string, artifact []byte, profile runtimeconfig.ExecutionProfile, config CandidateStageConfig, lineage string, recorder *eventRecorder) (*candidatePrepared, error) {
	plan, err := agenttrajectory.NewTravelCapabilityPlan(config.Fixture, 3, func(event agenttrajectory.TravelCallEvent) {
		physicalID := "request-" + candidateID + "-" + event.API
		eventType := "request." + event.Phase
		recorder.record(Event{Type: eventType, ActorID: "host", LogicalID: candidateID + "-" + event.API, PhysicalID: physicalID, Outcome: event.Outcome})

	})
	if err != nil {
		return nil, err
	}
	budget, err := semantic.NewPreDispatchBudget(3)
	if err != nil {
		return nil, err
	}
	controller, err := semantic.NewObservedStreamingSemanticPreDispatch(plan, budget, campaignLauncher{}, func(event semantic.StreamingPreDispatchEvent) {
		api := event.Capability[len("travel."):]
		recorder.record(Event{
			Type: "semantic." + event.Kind, ActorID: "host", LogicalID: candidateID + "-" + api,
			PhysicalID: "request-" + candidateID + "-" + api, IdentitySHA256: event.OccurrenceSHA256,
		})
	})
	if err != nil {
		return nil, err
	}
	baseContext := semantic.PreissueContext{
		StreamEpoch: "stream-" + candidateID, WorkflowEpoch: "day-trip-v1", FreshnessEpoch: "fixture-v1",
		ExpiryEpoch: "run-end", PrivacyPartition: "candidate-" + candidateID,
		ParentLineageSHA256: lineage,
	}
	admission, err := semantic.NewStreamingPrefixAdmission(plan, controller, baseContext)
	if err != nil {
		return nil, err
	}
	analyzerConfig := runtimeconfig.DefaultRunConfig()
	analyzerConfig.ExecutionProfile = &profile
	analyzerConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	profileBinding, err := runtimeconfig.ExecutionProfileBindingSHA256(analyzerConfig)
	if err != nil {
		return nil, err
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: digestBytes(artifact), ExecutionProfileSHA256: profileBinding,
		ImportClosureSHA256: digestTextValue("day-trip-imports-v1"), CapabilityPlanSHA256: plan.Identity(),
	}
	chunks := candidateSourceChunks(candidateID)
	finalSourceSHA := digestTextValue(strings.Join(chunks, ""))
	sourceChunks := make(chan string, len(chunks))
	recorder.record(Event{Type: "source.generation.start", ActorID: candidateID, LogicalID: "source-" + candidateID})
	go func() {
		defer close(sourceChunks)
		for index, chunk := range chunks {
			recorder.record(Event{Type: "source.statement.complete", ActorID: candidateID, LogicalID: fmt.Sprintf("%s-statement-%d", candidateID, index+1)})
			select {
			case sourceChunks <- chunk:
			case <-ctx.Done():
				return
			}
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
					return
				}
			}
		}
		recorder.record(Event{Type: "source.feed.complete", ActorID: candidateID, LogicalID: "source-" + candidateID, IdentitySHA256: finalSourceSHA})
	}()
	generated, err := semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
		Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: sourceChunks,
		ShouldAnalyzePrefix: func(prefixIndex uint32, _ string) bool { return prefixIndex <= 3 },
		Analyze: func(analyzeContext context.Context, source string, prefixBindings semantic.Bindings, prefixPlan *capability.Plan) (semantic.VerifiedAnalysis, error) {
			analyzer, createErr := wazeroengine.New(analyzeContext, artifact, analyzerConfig)
			if createErr != nil {
				return semantic.VerifiedAnalysis{}, createErr
			}
			defer analyzer.Close(context.Background())
			request, requestErr := semantic.NewRequest(source, prefixBindings, prefixPlan)
			if requestErr != nil {
				return semantic.VerifiedAnalysis{}, requestErr
			}
			return semantic.AnalyzeVerified(analyzeContext, analyzer, request)
		},
		Observe: func(event semantic.VerifiedSourceGenerationEvent) {
			if event.Phase == "source_sealed" {
				recorder.record(Event{Type: "source.sealed", ActorID: candidateID, LogicalID: "source-" + candidateID, IdentitySHA256: event.SourceSHA256})
			}
		},
	})
	if err != nil {
		return nil, err
	}
	return &candidatePrepared{id: candidateID, plan: plan, controller: controller, admission: admission, generated: generated}, nil
}

func executeCandidate(ctx context.Context, candidateID string, artifact []byte, profile runtimeconfig.ExecutionProfile, enableCOW bool, origin json.RawMessage, manager *workspace.Manager, base workspace.Ref, item *candidatePrepared, recorder *eventRecorder) (CandidateStageOutput, error) {
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		return CandidateStageOutput{}, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{
		StagedObservation: true, PrivateWorkspace: true, SemanticAnalysis: true, SemanticPreDispatch: true,
		PreparedRuntime: enableCOW, MemoryCOW: enableCOW,
	}
	runnerContract, err := (wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "candidate-" + candidateID,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{
				RunIdentity: "candidate-" + candidateID, Plan: item.plan,
				StagedClaimer: item.controller, SemanticPreDispatch: true,
			})
		},
	}).New(ctx, artifact, config)
	if err != nil {
		_ = attempt.Discard()
		return CandidateStageOutput{}, err
	}
	runner, ok := runnerContract.(*wazeroengine.Engine)
	if !ok {
		_ = attempt.Discard()
		return CandidateStageOutput{}, errors.New("candidate runner is not the Wazero engine")
	}
	if len(origin) == 0 {
		origin = json.RawMessage(`{"day":"saturday","origin":"london","status":"ready"}`)
	}
	request, err := json.Marshal(map[string]any{"run_id": "candidate-" + candidateID, "code": item.generated.Source(), "inputs": map[string]any{"origin": origin}})
	if err != nil {
		_ = attempt.Discard()
		return CandidateStageOutput{}, err
	}
	recorder.record(Event{Type: "guest.start", ActorID: candidateID, LogicalID: "candidate-" + candidateID, PhysicalID: "guest-" + candidateID})
	var cow wazeroengine.COWProbe
	runResult, err := semantic.ExecuteGeneratedSourceObserved(ctx, runner, attempt, request, item.plan.StreamingPythonPrelude(), item.generated, func(observed enginecontract.Runner) error {
		engine, ok := observed.(*wazeroengine.Engine)
		if !ok {
			return errors.New("candidate evidence runner is not Wazero")
		}
		cow = engine.COWProbe()
		return nil
	})
	if err != nil {
		return CandidateStageOutput{}, err
	}
	recorder.record(Event{Type: "guest.end", ActorID: candidateID, LogicalID: "candidate-" + candidateID, PhysicalID: "guest-" + candidateID, Outcome: "ok"})
	var response struct {
		Status string `json:"status"`
		Result struct {
			CandidateID  string  `json:"candidate_id"`
			TotalCostGBP float64 `json:"total_cost_gbp"`
		} `json:"result"`
	}
	if json.Unmarshal(runResult.Response, &response) != nil || response.Status != "ok" || response.Result.CandidateID != candidateID {
		return CandidateStageOutput{}, errors.New("invalid unified candidate Guest response")
	}
	if cow.COWSelected {
		recorder.record(Event{Type: "cow.selected", ActorID: candidateID, LogicalID: "candidate-" + candidateID, PhysicalID: "guest-" + candidateID, Outcome: "private_memory"})
	}
	return CandidateStageOutput{
		CandidateID: candidateID, TotalCostGBP: response.Result.TotalCostGBP, Response: append(json.RawMessage(nil), runResult.Response...),
		Workspace: runResult.PublishedWorkspace, SourceSHA256: item.generated.SHA256(),
		ControllerSnapshot: item.controller.Snapshot(), AdmissionSnapshot: item.admission.Snapshot(),
		COWSelected: cow.COWSelected, COWFallbackReason: strings.Join(cow.Blockers, ","),
	}, nil
}

func candidateSourceChunks(candidateID string) []string {
	return []string{
		fmt.Sprintf("weather = travel.weather(%q)\n", candidateID),
		fmt.Sprintf("rail = travel.rail(%q, travellers=2)\n", candidateID),
		fmt.Sprintf("attraction = travel.attractions(%q)\n", candidateID),
		"import json\n",
		fmt.Sprintf("result = {\"candidate_id\": %q, \"origin\": inputs[\"origin\"], \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}\n", candidateID),
		"with open(\"/workspace/candidate-result.json\", \"w\", encoding=\"utf-8\") as handle:\n    json.dump({\"candidate_id\": result[\"candidate_id\"], \"total_cost_gbp\": result[\"total_cost_gbp\"]}, handle, sort_keys=True, separators=(\",\", \":\"))\n",
	}
}

func candidateExecutionProfile(artifact []byte) (runtimeconfig.ExecutionProfile, error) {
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, err
	}
	return profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: digestBytes(artifact), ManifestSHA256: digestTextValue("unified-day-trip-manifest-v1"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
}

func channelErrors(channel <-chan error) []error {
	var errs []error
	for err := range channel {
		errs = append(errs, err)
	}
	return errs
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func digestTextValue(value string) string { return digestBytes([]byte(value)) }

type campaignLauncher struct{}

func (campaignLauncher) Launch(task func()) { go task() }
