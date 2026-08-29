package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const plmPrefixEagerSchema = "pysolate.plm-prefix-eager-economics.v1"

var plmPrefixEagerChunks = []struct {
	offset time.Duration
	source string
}{
	{0, "value = sources.read(\"alpha\")\n"},
	{150 * time.Millisecond, "label = value.upper()\n"},
	{450 * time.Millisecond, "result = [label, 12]\n"},
}

type plmPrefixEagerTracker struct {
	attempts  atomic.Uint32
	ready     atomic.Uint32
	finalized atomic.Bool
}

func (tracker *plmPrefixEagerTracker) handler(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	tracker.attempts.Add(1)
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	if !tracker.finalized.Load() {
		tracker.ready.Add(1)
	}
	return json.RawMessage(`{"body":"alpha"}`), nil
}

func (tracker *plmPrefixEagerTracker) observation() semanticspeculation.ProviderObservation {
	attempts := tracker.attempts.Load()
	return semanticspeculation.ProviderObservation{
		Attempts: attempts, ResultBytes: uint64(attempts) * uint64(len(`{"body":"alpha"}`)), CostUnits: uint64(attempts),
		ReadyBeforeFinalize: tracker.ready.Load(),
		Dispositions:        semanticspeculation.PhysicalDispositions{Consumed: attempts},
	}
}

type plmPrefixEagerSample struct {
	Trial                     int                                  `json:"trial"`
	Order                     int                                  `json:"order"`
	Treatment                 string                               `json:"treatment"`
	ColdNanos                 uint64                               `json:"cold_nanos"`
	PostBeginNanos            uint64                               `json:"post_begin_nanos"`
	BeginNanos                uint64                               `json:"begin_nanos"`
	FinalizeNanos             uint64                               `json:"finalize_nanos"`
	Outcome                   semanticspeculation.TreatmentOutcome `json:"outcome"`
	SplitPhase                capability.SplitPhaseSnapshot        `json:"split_phase,omitempty"`
	PLMLifecycle              wazeroengine.PLMRunLifecycleEvidence `json:"plm_lifecycle,omitempty"`
	PrefixAnalysisNanos       uint64                               `json:"prefix_analysis_nanos,omitempty"`
	PrefixAnalyzerInvocations uint32                               `json:"prefix_analyzer_invocations,omitempty"`
}

type plmPrefixEagerEvidence struct {
	SchemaVersion   string                 `json:"schema_version"`
	SourceCommit    string                 `json:"source_commit"`
	SourceTree      string                 `json:"source_tree"`
	HostID          string                 `json:"host_id"`
	ArtifactSHA256  string                 `json:"artifact_sha256"`
	Runs            int                    `json:"runs"`
	ProviderDelayMS int                    `json:"provider_delay_ms"`
	ChunkOffsetsMS  []int                  `json:"chunk_offsets_ms"`
	SourceSHA256    string                 `json:"source_sha256"`
	Samples         []plmPrefixEagerSample `json:"samples"`
}

type plmPrefixAnalysisResult struct {
	nanos uint64
	err   error
}

type plmPrefixTreatment struct {
	artifact                  []byte
	config                    runtimeconfig.RunConfig
	plan                      *capability.Plan
	adapter                   *e2ePLMAdapter
	tracker                   *plmPrefixEagerTracker
	runID                     string
	workspaceRoot             string
	source                    strings.Builder
	analyzer                  *wazeroengine.Engine
	broker                    *capability.Broker
	table                     *capability.SplitPhaseTable
	admission                 *semantic.StreamingPrefixAdmission
	filter                    *semantic.ConservativePrefixReadinessFilter
	bindings                  semantic.Bindings
	profile                   *runtimeconfig.ExecutionProfile
	plugins                   *passplugin.Registry
	manager                   *workspace.Manager
	attempt                   *workspace.Attempt
	prefixAnalysisNanos       uint64
	prefixAnalyzerInvocations uint32
	prefixIndex               uint32
	analysisResult            chan plmPrefixAnalysisResult
	split                     capability.SplitPhaseSnapshot
	lifecycle                 wazeroengine.PLMRunLifecycleEvidence
}

func newPLMPrefixTreatment(artifact []byte, config runtimeconfig.RunConfig, plan *capability.Plan, adapter *e2ePLMAdapter, tracker *plmPrefixEagerTracker, runID, workspaceRoot string) *plmPrefixTreatment {
	return &plmPrefixTreatment{artifact: artifact, config: config, plan: plan, adapter: adapter, tracker: tracker, runID: runID, workspaceRoot: workspaceRoot}
}

func (treatment *plmPrefixTreatment) Begin(ctx context.Context, _ json.RawMessage) error {
	if err := os.MkdirAll(treatment.workspaceRoot, 0o700); err != nil {
		return err
	}
	manager, err := workspace.NewManager(treatment.workspaceRoot)
	if err != nil {
		return err
	}
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return err
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		return err
	}
	treatment.manager, treatment.attempt = manager, attempt
	artifactDigest := sha256.Sum256(treatment.artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	allowedImports := []string{"json"}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		return err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("plm-prefix-eager-manifest"),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		return err
	}
	analysisConfig := treatment.config
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analyzer, err := (wazeroengine.Factory{}).New(ctx, treatment.artifact, analysisConfig)
	if err != nil {
		return err
	}
	analyzerEngine := trustedSemanticRunnerNoTest(analyzer)
	if analyzerEngine == nil {
		return fmt.Errorf("prefix analyzer requires wazero engine")
	}
	treatment.analyzer = analyzerEngine
	treatment.profile = &profile
	broker, err := capability.NewBroker(capability.Config{RunIdentity: treatment.runID, Plan: treatment.plan})
	if err != nil {
		return err
	}
	treatment.broker = broker
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 4096})
	if err != nil {
		return err
	}
	treatment.table = table
	admission, err := semantic.NewStreamingPLMPrefixAdmission(treatment.plan, table, semantic.PreissueContext{
		StreamEpoch: treatment.runID + "-stream", WorkflowEpoch: treatment.runID + "-workflow", FreshnessEpoch: treatment.runID + "-fresh",
		ExpiryEpoch: treatment.runID + "-expiry", PrivacyPartition: treatment.runID + "-private", ParentLineageSHA256: testDigest(treatment.runID + "-parent"),
	})
	if err != nil {
		return err
	}
	treatment.admission = admission
	filter, err := semantic.NewConservativePrefixReadinessFilter(treatment.plan)
	if err != nil {
		return err
	}
	treatment.filter = filter
	treatment.bindings = semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity(allowedImports, allowedImports), CapabilityPlanSHA256: treatment.plan.Identity(),
	}
	plugins := unifiedPassCatalogForExperiment()
	plugins, err = plugins.Enable(passregistration.SemanticPreDispatch, sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		return err
	}
	treatment.plugins = plugins
	return nil
}

func (treatment *plmPrefixTreatment) ObserveChunk(ctx context.Context, chunk string) error {
	treatment.source.WriteString(chunk)
	treatment.prefixIndex++
	prefix := treatment.source.String()
	if !treatment.filter.ShouldAnalyzePrefix(treatment.prefixIndex, prefix) {
		return nil
	}
	if treatment.analysisResult != nil {
		return fmt.Errorf("experiment fixture supports one prefix analysis")
	}
	result := make(chan plmPrefixAnalysisResult, 1)
	treatment.analysisResult = result
	go func() {
		started := time.Now()
		request, err := semantic.NewRequest(prefix, treatment.bindings, treatment.plan)
		if err == nil {
			var verified semantic.VerifiedAnalysis
			verified, err = semantic.AnalyzeVerified(ctx, treatment.analyzer, request)
			if err == nil {
				_, err = treatment.admission.AdmitVerifiedPrefix(ctx, prefix, verified)
			}
		}
		result <- plmPrefixAnalysisResult{nanos: uint64(time.Since(started)), err: err}
	}()
	return nil
}

func (treatment *plmPrefixTreatment) Finalize(ctx context.Context) (semanticspeculation.TreatmentOutcome, error) {
	if treatment.analysisResult != nil {
		select {
		case analyzed := <-treatment.analysisResult:
			treatment.prefixAnalysisNanos = analyzed.nanos
			treatment.prefixAnalyzerInvocations = 1
			if analyzed.err != nil {
				return semanticspeculation.TreatmentOutcome{}, analyzed.err
			}
		case <-ctx.Done():
			return semanticspeculation.TreatmentOutcome{}, ctx.Err()
		}
	}
	fullSource := treatment.source.String()
	if err := treatment.admission.SealFinalSource(fullSource); err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: treatment.runID, Code: fullSource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	config := treatment.config
	config.ExecutionProfile = treatment.bindingsProfile()
	runner, err := (wazeroengine.Factory{Passes: treatment.plugins, WorkspaceManager: treatment.manager, WorkspaceRef: treatment.attempt.Ref(), WorkspaceOwner: treatment.runID, BrokerFactory: func(context.Context) (*capability.Broker, error) { return treatment.broker, nil }}).New(ctx, treatment.artifact, config)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	engine := trustedSemanticRunnerNoTest(runner)
	if engine == nil {
		return semanticspeculation.TreatmentOutcome{}, fmt.Errorf("PLM experiment requires wazero engine")
	}
	execution, err := treatment.plugins.ExecuteCapabilityHostScheduled(ctx, sourcepatch.PLMCapabilityCallsName, engine, request, treatment.plan.PythonPrelude(), passplugin.PLMCapabilityProjections(treatment.plan))
	closeErr := runner.Close(ctx)
	_ = treatment.analyzer.Close(ctx)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	if closeErr != nil {
		return semanticspeculation.TreatmentOutcome{}, closeErr
	}
	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	resultSHA, err := playback.CanonicalSHA256(result)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	treatment.split = engine.SplitPhaseEvidence()
	treatment.lifecycle = engine.PLMRunLifecycleEvidence()
	if _, err := treatment.attempt.Publish(); err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	if err := treatment.manager.Close(); err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	return semanticspeculation.TreatmentOutcome{
		FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: resultSHA,
		LogicalCalls: treatment.broker.CallCount(), PhysicalAttempts: treatment.tracker.attempts.Load(), ReadyBeforeFinalize: treatment.tracker.ready.Load(),
		PhysicalDispositions: semanticspeculation.PhysicalDispositions{Consumed: treatment.tracker.attempts.Load()},
		AuthorityDisposition: "read_consumed", WorkspaceDisposition: "published",
	}, nil
}

func (treatment *plmPrefixTreatment) bindingsProfile() *runtimeconfig.ExecutionProfile {
	return treatment.profile
}

func (treatment *plmPrefixTreatment) Cancel(ctx context.Context) error {
	if treatment.analyzer != nil {
		_ = treatment.analyzer.Close(ctx)
	}
	if treatment.attempt != nil {
		_ = treatment.attempt.Discard()
	}
	if treatment.manager != nil {
		_ = treatment.manager.Close()
	}
	return nil
}

func trustedSemanticRunnerNoTest(runner interface{ Close(context.Context) error }) *wazeroengine.Engine {
	engine, _ := runner.(*wazeroengine.Engine)
	return engine
}

func unifiedPassCatalogForExperiment() *passplugin.Registry {
	registry, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		panic(err)
	}
	return registry
}

func TestPLMPrefixEagerEconomicsFixture(t *testing.T) {
	output := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_OUTPUT")
	if output == "" {
		t.Skip("set PYSOLATE_PLM_PREFIX_EAGER_OUTPUT to run")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	runs := 5
	if raw := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_RUNS"); raw != "" {
		runs, err = strconv.Atoi(raw)
		if err != nil || runs < 1 || runs > 10 {
			t.Fatal("runs must be in [1,10]")
		}
	}
	offset := 0
	if raw := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_ORDER_OFFSET"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 2 {
			t.Fatal("order offset must be in [0,2]")
		}
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	artifactDigest := sha256.Sum256(artifact)
	treatments := []string{"serial_whole_file", "eager_style_gate", "plm_prefix_prepare"}
	evidence := plmPrefixEagerEvidence{
		SchemaVersion: plmPrefixEagerSchema, SourceCommit: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_COMMIT"), SourceTree: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_TREE"),
		HostID: os.Getenv("EVALUATION_HOST_ID"), ArtifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]), Runs: runs, ProviderDelayMS: 250,
		ChunkOffsetsMS: []int{0, 150, 450}, SourceSHA256: testDigest(plmPrefixEagerChunks[0].source + plmPrefixEagerChunks[1].source + plmPrefixEagerChunks[2].source),
	}
	for trial := 0; trial < runs; trial++ {
		for order := 0; order < len(treatments); order++ {
			name := treatments[(trial+offset+order)%len(treatments)]
			tracker := &plmPrefixEagerTracker{}
			adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(tracker.handler)}
			plan := plmE2EPlan(t, 1, adapter)
			runID := fmt.Sprintf("plm-prefix-eager-%d-%s", trial, name)
			workspaceRoot := filepath.Join(t.TempDir(), runID)
			brokerFactory := func(context.Context) (*capability.Broker, error) {
				return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
			}
			var treatment semanticspeculation.ScheduledTreatment
			switch name {
			case "serial_whole_file":
				runConfig := config
				runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
				treatment, err = semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{Artifact: artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory, ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
			case "eager_style_gate":
				treatment, err = semanticspeculation.NewEagerGuestTreatment(semanticspeculation.EagerGuestTreatmentConfig{Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory, ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
			case "plm_prefix_prepare":
				treatment = newPLMPrefixTreatment(artifact, config, plan, adapter, tracker, runID, workspaceRoot)
			}
			if err != nil {
				t.Fatal(err)
			}
			sample, runErr := runPLMPrefixEagerTreatment(context.Background(), trial, order, name, tracker, treatment)
			if runErr != nil {
				t.Fatalf("trial=%d treatment=%s: %v", trial, name, runErr)
			}
			if plm, ok := treatment.(*plmPrefixTreatment); ok {
				sample.SplitPhase, sample.PLMLifecycle = plm.split, plm.lifecycle
				sample.PrefixAnalysisNanos, sample.PrefixAnalyzerInvocations = plm.prefixAnalysisNanos, plm.prefixAnalyzerInvocations
			}
			if sample.Outcome.FinalProgramOutcome != "success" || sample.Outcome.LogicalCalls != 1 || sample.Outcome.PhysicalAttempts != 1 {
				t.Fatalf("invalid outcome: %+v", sample.Outcome)
			}
			evidence.Samples = append(evidence.Samples, sample)
		}
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runPLMPrefixEagerTreatment(ctx context.Context, trial, order int, name string, tracker *plmPrefixEagerTracker, treatment semanticspeculation.ScheduledTreatment) (plmPrefixEagerSample, error) {
	coldStarted := time.Now()
	beginStarted := time.Now()
	if err := treatment.Begin(ctx, json.RawMessage(`{}`)); err != nil {
		return plmPrefixEagerSample{}, err
	}
	beginNanos := uint64(time.Since(beginStarted))
	postBeginStarted := time.Now()
	for _, chunk := range plmPrefixEagerChunks {
		due := postBeginStarted.Add(chunk.offset)
		if delay := time.Until(due); delay > 0 {
			time.Sleep(delay)
		}
		if err := treatment.ObserveChunk(ctx, chunk.source); err != nil {
			return plmPrefixEagerSample{}, err
		}
	}
	tracker.finalized.Store(true)
	finalizeStarted := time.Now()
	outcome, err := treatment.Finalize(ctx)
	return plmPrefixEagerSample{Trial: trial, Order: order, Treatment: name, ColdNanos: uint64(time.Since(coldStarted)), PostBeginNanos: uint64(time.Since(postBeginStarted)), BeginNanos: beginNanos, FinalizeNanos: uint64(time.Since(finalizeStarted)), Outcome: outcome}, err
}
