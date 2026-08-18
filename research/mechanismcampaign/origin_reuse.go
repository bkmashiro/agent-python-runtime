package mechanismcampaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/semanticreuse"
)

type OriginSharingConfig struct {
	ArtifactPath string
	StoreRoot    string
}

type OriginSharingResult struct {
	Value               json.RawMessage
	PhysicalComputes    int32
	LeaderPhysicalID    string
	LogicalDispositions map[string]agentfunction.Disposition
	Retained            agentfunction.Result
	PassStats           semanticreuse.Stats
	StoreStats          agentfunction.Stats
	Events              []Event
	session             *originRetentionSession
}

type originRetentionSession struct {
	mu           sync.Mutex
	pass         *semanticreuse.Pass
	invocation   agentfunction.Invocation
	verifiedPlan semantic.VerifiedWholeRunPlan
	compute      agentfunction.FreshGuestCompute
	recorder     *eventRecorder
	store        *agentfunction.Store
	physical     *atomic.Int32
	physicalID   string
	retained     bool
}

func RunOriginSharingStage(ctx context.Context, config OriginSharingConfig) (OriginSharingResult, error) {
	if ctx == nil || config.ArtifactPath == "" || !filepath.IsAbs(config.ArtifactPath) || config.StoreRoot == "" || !filepath.IsAbs(config.StoreRoot) {
		return OriginSharingResult{}, errors.New("invalid origin-sharing config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return OriginSharingResult{}, err
	}
	recorder := newEventRecorder()
	code := `result={"day":"saturday","origin":"london","status":"ready"}`
	inputs := json.RawMessage(`{}`)
	invocation, runConfig, err := originInvocation(artifact, code, inputs)
	if err != nil {
		return OriginSharingResult{}, err
	}
	analysisRunnerContract, err := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
	if err != nil {
		return OriginSharingResult{}, err
	}
	analysisRunner, ok := analysisRunnerContract.(*wazeroengine.Engine)
	if !ok {
		_ = analysisRunnerContract.Close(context.Background())
		return OriginSharingResult{}, errors.New("origin analyzer is not Wazero")
	}
	analysisRequest, err := semantic.NewRequest(code, semantic.Bindings{
		ArtifactSHA256: invocation.ArtifactSHA256, ExecutionProfileSHA256: invocation.ExecutionProfileSHA256,
		ImportClosureSHA256: invocation.ImportClosureSHA256, CapabilityPlanSHA256: digestTextValue("no-capability-plan"),
	}, nil)
	if err != nil {
		_ = analysisRunner.Close(context.Background())
		return OriginSharingResult{}, err
	}
	verified, err := semantic.AnalyzeVerified(ctx, analysisRunner, analysisRequest)
	analysis, reportErr := verified.Analysis()
	closeErr := analysisRunner.Close(context.Background())
	if err = errors.Join(err, reportErr, closeErr); err != nil {
		return OriginSharingResult{}, err
	}
	dependencies, err := agentfunction.SemanticWholeRunDependencies(invocation)
	if err != nil {
		return OriginSharingResult{}, err
	}
	plan, _, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		Dependencies: dependencies, InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil || len(plan.Regions) != 1 || !plan.Regions[0].Reusable() {
		return OriginSharingResult{}, errors.Join(errors.New("origin function is not reusable"), err)
	}
	verifiedPlan, err := semantic.BindVerifiedWholeRunPlan(verified, plan)
	if err != nil {
		return OriginSharingResult{}, err
	}
	request, err := json.Marshal(map[string]any{
		"run_id": "origin-briefing", "code": code, "inputs": map[string]any{},
		"compatibility": map[string]any{"profile": "base", "imports": []string{}},
	})
	if err != nil {
		return OriginSharingResult{}, err
	}
	if err := os.Mkdir(config.StoreRoot, 0o700); err != nil {
		return OriginSharingResult{}, err
	}
	store, err := agentfunction.NewBoundedStore(config.StoreRoot, invocation.ProjectSHA256, 1024, 64<<10)
	if err != nil {
		return OriginSharingResult{}, err
	}
	flights := agentfunction.NewFlightGroup()
	pass := &semanticreuse.Pass{Enabled: true, FunctionEngine: agentfunction.Engine{Store: store, CacheEnabled: true, Flights: flights}}
	var physical atomic.Int32
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(runContext context.Context) (string, enginecontract.Runner, error) {
			physicalNumber := physical.Add(1)
			physicalID := fmt.Sprintf("origin-physical-%d", physicalNumber)
			deadline := time.Now().Add(5 * time.Second)
			for flights.Stats().Waiters != 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			runner, createErr := (wazeroengine.Factory{}).New(runContext, artifact, runConfig)
			if createErr != nil {
				return physicalID, nil, createErr
			}
			return physicalID, &campaignObservedRunner{Runner: runner, recorder: recorder, physicalID: physicalID}, nil
		},
		Request: request, MaxResultBytes: 1024,
	}
	type logicalOutcome struct {
		actor  string
		result agentfunction.Result
		err    error
	}
	outcomes := make(chan logicalOutcome, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, actor := range []string{"brighton", "oxford"} {
		logicalActor := actor
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			recorder.record(Event{Type: "function.logical", ActorID: logicalActor, LogicalID: "origin-" + logicalActor})
			result, executeErr := pass.ExecuteGuest(ctx, invocation, verifiedPlan, compute)
			outcomes <- logicalOutcome{actor: logicalActor, result: result, err: executeErr}
		}()
	}
	close(start)
	wait.Wait()
	close(outcomes)
	dispositions := make(map[string]agentfunction.Disposition, 2)
	var value json.RawMessage
	var physicalID string
	for outcome := range outcomes {
		if outcome.err != nil {
			return OriginSharingResult{}, fmt.Errorf("origin logical %s after %d physical factories: %w", outcome.actor, physical.Load(), outcome.err)
		}
		dispositions[outcome.actor] = outcome.result.Disposition
		physicalID = outcome.result.PhysicalExecutionID
		value = append(json.RawMessage(nil), outcome.result.Value...)
		recorder.record(Event{
			Type: "function." + string(outcome.result.Disposition), ActorID: outcome.actor,
			LogicalID: "origin-" + outcome.actor, PhysicalID: outcome.result.PhysicalExecutionID, Outcome: string(outcome.result.Disposition),
		})
	}
	stats := pass.Stats()
	storeStats := store.Stats()
	if physical.Load() != 1 || stats.Leaders != 1 || stats.Waiters != 1 || stats.Retained != 0 || stats.PhysicalComputes != 1 ||
		storeStats.Writes != 1 || storeStats.Hits != 0 {
		return OriginSharingResult{}, errors.New("origin sharing evidence did not close")
	}
	session := &originRetentionSession{
		pass: pass, invocation: invocation, verifiedPlan: verifiedPlan, compute: compute,
		recorder: recorder, store: store, physical: &physical, physicalID: physicalID,
	}
	return OriginSharingResult{
		Value: value, PhysicalComputes: physical.Load(), LeaderPhysicalID: physicalID,
		LogicalDispositions: dispositions, PassStats: stats, StoreStats: storeStats, Events: recorder.snapshot(), session: session,
	}, nil
}

// RetainForMain performs the third logical invocation after candidate selection.
// It must hit the exact whole-run retained observation and cannot start a new Guest.
func (result *OriginSharingResult) RetainForMain(ctx context.Context) error {
	if result == nil || result.session == nil || ctx == nil {
		return errors.New("invalid origin retention session")
	}
	session := result.session
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.retained {
		return errors.New("origin retention already consumed")
	}
	session.recorder.record(Event{Type: "function.logical", ActorID: "main", LogicalID: "origin-main"})
	retained, err := session.pass.ExecuteGuest(ctx, session.invocation, session.verifiedPlan, session.compute)
	if err != nil {
		return err
	}
	session.recorder.record(Event{
		Type: "function.retained", ActorID: "main", LogicalID: "origin-main",
		PhysicalID: retained.PhysicalExecutionID, Outcome: string(retained.Disposition),
	})
	stats := session.pass.Stats()
	storeStats := session.store.Stats()
	if retained.Disposition != agentfunction.Retained || retained.PhysicalExecutionID != session.physicalID ||
		session.physical.Load() != 1 || stats.Retained != 1 || stats.PhysicalComputes != 1 || storeStats.Hits != 1 {
		return errors.New("origin retention evidence did not close")
	}
	session.retained = true
	result.Retained = retained
	result.PassStats = stats
	result.StoreStats = storeStats
	result.Events = session.recorder.snapshot()
	return nil
}

func originInvocation(artifact []byte, code string, inputs json.RawMessage) (agentfunction.Invocation, runtimeconfig.RunConfig, error) {
	artifactSHA := digestBytes(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"sys"})
	if err != nil {
		return agentfunction.Invocation{}, runtimeconfig.RunConfig{}, err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: digestTextValue("origin-manifest-v1"),
		ImportRoots: []string{"sys"}, QualifiedImportRoots: []string{"sys"},
	})
	if err != nil {
		return agentfunction.Invocation{}, runtimeconfig.RunConfig{}, err
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(artifactSHA, "day-trip-origin")
	if err != nil {
		return agentfunction.Invocation{}, runtimeconfig.RunConfig{}, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.DeterministicVerification = &deterministic
	config.Mechanisms = runtimeconfig.MechanismSet{
		ImmutableBranches: true, FunctionCache: true, SingleFlight: true,
		SemanticAnalysis: true, SemanticReuse: true,
	}
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		return agentfunction.Invocation{}, runtimeconfig.RunConfig{}, err
	}
	return agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: digestTextValue("unified-day-trip-project"), FunctionSourceSHA256: digestBytes([]byte(code)),
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: profileSHA,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"sys"}, []string{"sys"}), CanonicalInputs: inputs,
		ImmutableRootSHA256:         []string{digestTextValue("origin-immutable-root")},
		DeterministicSettingsSHA256: deterministic.Identity(), OutputSchemaSHA256: digestBytes(nil),
		PrivacyPartition: "unified-day-trip", PolicyEpochSHA256: digestTextValue("origin-policy-v1"),
	}, config, nil
}

type campaignObservedRunner struct {
	enginecontract.Runner
	recorder   *eventRecorder
	physicalID string
}

func (runner *campaignObservedRunner) Run(ctx context.Context, request []byte, prepare string) ([]byte, error) {
	runner.recorder.record(Event{Type: "function.physical.start", ActorID: "host", PhysicalID: runner.physicalID})
	payload, err := runner.Runner.Run(ctx, request, prepare)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	runner.recorder.record(Event{Type: "function.physical.end", ActorID: "host", PhysicalID: runner.physicalID, Outcome: outcome})
	return payload, err
}
