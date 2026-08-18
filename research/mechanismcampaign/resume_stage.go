package mechanismcampaign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type ResumeStageConfig struct {
	ArtifactPath  string
	Capsule       []byte
	PortableRoot  workspace.Root
	WorkspaceRoot string
	EnableColdIO  bool
	PayloadBytes  int
}

type ResumeStageResult struct {
	Response     json.RawMessage
	ImportedInfo workspace.CapsuleInfo
	BoundRoot    workspace.Root
	Published    workspace.Ref
	COW          wazeroengine.COWProbe
	ColdIO       wazeroengine.ColdIOEvidence
	Events       []Event
}

func RunResumeStage(ctx context.Context, config ResumeStageConfig) (ResumeStageResult, error) {
	if ctx == nil || config.ArtifactPath == "" || len(config.Capsule) == 0 || config.WorkspaceRoot == "" ||
		config.PortableRoot.IdentitySHA256 == "" {
		return ResumeStageResult{}, errors.New("invalid resume stage config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return ResumeStageResult{}, err
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return ResumeStageResult{}, err
	}
	manager, err := workspace.NewManager(config.WorkspaceRoot)
	if err != nil {
		return ResumeStageResult{}, err
	}
	defer manager.Close()
	recorder := newEventRecorder()
	importedRef, importedInfo, err := manager.ImportCapsule(bytes.NewReader(config.Capsule), workspace.DefaultLimits())
	if err != nil {
		return ResumeStageResult{}, err
	}
	if importedInfo.WorkspaceSHA256 != config.PortableRoot.WorkspaceSHA256 {
		return ResumeStageResult{}, workspace.ErrWorkspaceConflict
	}
	recorder.record(Event{Type: "capsule.import", ActorID: "host", LogicalID: "oxford", IdentitySHA256: importedInfo.WorkspaceSHA256, Outcome: "verified"})
	boundRoot, err := manager.BindImportedRoot(importedRef, config.PortableRoot)
	if err != nil {
		return ResumeStageResult{}, err
	}
	recorder.record(Event{Type: "capsule.bind", ActorID: "host", LogicalID: "oxford", IdentitySHA256: boundRoot.IdentitySHA256, Outcome: "portable_root_bound"})
	attempt, err := manager.ForkAttempt(boundRoot.Ref())
	if err != nil {
		return ResumeStageResult{}, err
	}
	failed := true
	defer func() {
		if failed {
			_ = attempt.Discard()
		}
	}()

	plan, err := platformNoticePlan(func(phase string) {
		recorder.record(Event{Type: "request." + phase, ActorID: "host", LogicalID: "main-platform-notice", PhysicalID: "request-main-platform-notice", Outcome: phase})
	})
	if err != nil {
		return ResumeStageResult{}, err
	}
	profile, err := candidateExecutionProfile(artifact)
	if err != nil {
		return ResumeStageResult{}, err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	if config.EnableColdIO {
		runConfig.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true, MemoryCOW: true, ColdIOContinuation: true}
		runConfig.ColdIO = &runtimeconfig.ColdIOPolicy{ColdAfter: 10 * time.Millisecond, PageOutAfter: 20 * time.Millisecond}
	}
	factory := wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "campaign-resume-main",
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "campaign-resume-main", Plan: plan})
		},
	}
	runnerContract, err := factory.New(ctx, artifact, runConfig)
	if err != nil {
		return ResumeStageResult{}, err
	}
	runner, ok := runnerContract.(*wazeroengine.Engine)
	if !ok {
		_ = runnerContract.Close(ctx)
		return ResumeStageResult{}, errors.New("resume stage requires wazero engine")
	}
	payloadBytes := config.PayloadBytes
	if payloadBytes <= 0 {
		if config.EnableColdIO {
			payloadBytes = 200_000_000
		} else {
			payloadBytes = 1_000_000
		}
	}
	code := fmt.Sprintf("import builtins\nimport json\nwith open('/workspace/candidate-result.json', 'r', encoding='utf-8') as handle:\n    selected_observation=json.load(handle)\npayload=bytearray(%d)\npayload[-1]=7\nstate={'selected':selected_observation['candidate_id'],'total':selected_observation['total_cost_gbp']}\nbuiltins._campaign_resume_marker=state\nbefore=id(state)\nnotice=travel.platform_notice('GWR')\nresult={'last':payload[-1],'notice':notice,'same':id(state)==before and builtins._campaign_resume_marker is state,'selected':state['selected'],'total_gbp':state['total']}", payloadBytes)
	request, err := json.Marshal(map[string]any{
		"run_id": "campaign-resume-main", "code": code,
		"inputs": map[string]any{},
	})
	if err != nil {
		return ResumeStageResult{}, err
	}
	recorder.record(Event{Type: "guest.start", ActorID: "main", LogicalID: "resume-main", PhysicalID: "guest-main-resumed"})
	var cow wazeroengine.COWProbe
	var cold wazeroengine.ColdIOEvidence
	runResult, err := streaming.ExecuteObserved(ctx, runner, attempt, request, plan.PythonPrelude(), func(observed enginecontract.Runner) error {
		engine, ok := observed.(*wazeroengine.Engine)
		if !ok {
			return errors.New("resume evidence runner is not Wazero")
		}
		cow = engine.COWProbe()
		cold = engine.ColdIOEvidence()
		return nil
	})
	if err != nil {
		return ResumeStageResult{}, err
	}
	failed = false
	recorder.record(Event{Type: "guest.complete", ActorID: "main", LogicalID: "resume-main", PhysicalID: "guest-main-resumed", Outcome: "published"})
	var response struct {
		Status string `json:"status"`
		Result struct {
			Last     int     `json:"last"`
			Notice   string  `json:"notice"`
			Same     bool    `json:"same"`
			Selected string  `json:"selected"`
			Total    float64 `json:"total_gbp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(runResult.Response, &response); err != nil || response.Status != "ok" || !response.Result.Same ||
		response.Result.Selected != "oxford" || response.Result.Total != 78 || response.Result.Last != 7 || response.Result.Notice == "" {
		return ResumeStageResult{}, fmt.Errorf("invalid resumed Main response: %s", runResult.Response)
	}

	if config.EnableColdIO {
		if err := cold.Validate(); err != nil || !cow.MemoryCOWCandidate || cold.State != wazeroengine.ColdIOTerminal ||
			cold.Waits != 1 || cold.ColdSucceeded != 1 || cold.PageOutSucceeded != 1 || cold.Resumes != 1 {
			return ResumeStageResult{}, fmt.Errorf("cold resume evidence invalid: cow=%+v cold=%+v err=%v", cow, cold, err)
		}
		recorder.record(Event{Type: "cold_io.resume", ActorID: "main", LogicalID: "resume-main", PhysicalID: "guest-main-resumed", Outcome: "fresh_continuation"})
	}
	return ResumeStageResult{
		Response: append(json.RawMessage(nil), runResult.Response...), ImportedInfo: importedInfo, BoundRoot: boundRoot,
		Published: runResult.PublishedWorkspace, COW: cow, ColdIO: cold, Events: recorder.snapshot(),
	}, nil
}

func platformNoticePlan(observe func(string)) (*capability.Plan, error) {
	grant, err := capability.NewGrant(json.RawMessage(`{"service":"GWR"}`))
	if err != nil {
		return nil, err
	}
	registry := capability.NewRegistry()
	spec := capability.Spec{
		Name: "travel.platform_notice", Version: "pysolate.travel.platform-notice.v1", Description: "Fetch a bounded platform notice.",
		EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: campaignDigest("platform-notice-handler"),
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"service":{"type":"string","enum":["GWR"]}},"required":["service"],"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"notice":{"type":"string"}},"required":["notice"],"additionalProperties":false}`),
		Python:          &capability.PythonProjection{Module: "travel", Method: "platform_notice", Arguments: []string{"service"}, ResultField: "notice"},
	}
	handler := capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if observe != nil {
			observe("start")
			defer observe("finish")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return json.RawMessage(`{"notice":"Platform shown after live operations check"}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err := registry.Register(spec, grant, handler); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
}

func campaignDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}
