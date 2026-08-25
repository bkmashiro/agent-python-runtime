package releasereadiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type CampaignConfig struct {
	ArtifactPath  string
	WorkloadPath  string
	WorkspaceRoot string
	Pairs         int
	ScheduleScale float64
	ProviderScale float64
	Timeout       time.Duration
}

type LaneResult struct {
	Lane                  string `json:"lane"`
	ElapsedNS             int64  `json:"elapsed_ns"`
	SourceCompleteNS      int64  `json:"source_complete_ns"`
	GuestStartNS          int64  `json:"guest_start_ns"`
	GuestEndNS            int64  `json:"guest_end_ns"`
	PhysicalRequests      int    `json:"physical_requests"`
	EarlyPhysicalRequests int    `json:"early_physical_requests"`
	LogicalClaims         uint32 `json:"logical_claims"`
	Consumed              uint32 `json:"consumed"`
	QualifiedCalls        uint32 `json:"qualified_calls"`
	ResultSHA256          string `json:"result_sha256"`
	WorkspaceSHA256       string `json:"workspace_sha256"`
	SourceSHA256          string `json:"source_sha256"`
}

type PairResult struct {
	PairIndex  int        `json:"pair_index"`
	FirstLane  string     `json:"first_lane"`
	Baseline   LaneResult `json:"baseline"`
	Optimized  LaneResult `json:"optimized"`
	SavingNS   int64      `json:"saving_ns"`
	SavingRate float64    `json:"saving_rate"`
}

type CampaignSummary struct {
	BaselineMedianNS       int64   `json:"baseline_median_ns"`
	OptimizedMedianNS      int64   `json:"optimized_median_ns"`
	ScheduleMedianDiffNS   int64   `json:"schedule_median_difference_ns"`
	MedianPairedSavingNS   int64   `json:"median_paired_saving_ns"`
	MedianPairedSavingRate float64 `json:"median_paired_saving_rate"`
	BootstrapLowNS         int64   `json:"bootstrap_95_low_ns"`
	BootstrapHighNS        int64   `json:"bootstrap_95_high_ns"`
	ImprovedPairs          int     `json:"improved_pairs"`
	TiedPairs              int     `json:"tied_pairs"`
	RegressedPairs         int     `json:"regressed_pairs"`
	SignTestP              float64 `json:"two_sided_sign_test_p"`
}

type CampaignResult struct {
	SchemaVersion  string          `json:"schema_version"`
	ArtifactSHA256 string          `json:"artifact_sha256"`
	SourceSHA256   string          `json:"source_sha256"`
	RunIndex       int             `json:"recorded_run_index"`
	Pairs          []PairResult    `json:"pairs"`
	Summary        CampaignSummary `json:"summary"`
	ScheduleScale  float64         `json:"schedule_scale"`
	ProviderScale  float64         `json:"provider_scale"`
	Reportable     bool            `json:"reportable"`
}

type laneRecorder struct {
	mu     sync.Mutex
	start  time.Time
	events []ProviderEvent
}

func (recorder *laneRecorder) observe(phase, capability string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, ProviderEvent{Phase: phase, Capability: capability, AtNS: time.Since(recorder.start).Nanoseconds()})
}

func (recorder *laneRecorder) counts(sourceComplete int64) (physical, early int) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, event := range recorder.events {
		if event.Phase != "start" {
			continue
		}
		physical++
		if event.AtNS < sourceComplete {
			early++
		}
	}
	return physical, early
}

func RunCampaign(ctx context.Context, config CampaignConfig) (CampaignResult, error) {
	if ctx == nil || config.ArtifactPath == "" || !filepath.IsAbs(config.ArtifactPath) || config.WorkloadPath == "" ||
		config.WorkspaceRoot == "" || !filepath.IsAbs(config.WorkspaceRoot) || config.Pairs < 1 || config.Pairs > 30 ||
		config.ScheduleScale <= 0 || config.ProviderScale <= 0 {
		return CampaignResult{}, errors.New("invalid release-readiness campaign config")
	}
	artifact, err := os.ReadFile(config.ArtifactPath)
	if err != nil {
		return CampaignResult{}, err
	}
	workload, err := LoadRecordedWorkload(config.WorkloadPath)
	if err != nil {
		return CampaignResult{}, err
	}
	profile, err := executionProfile(artifact)
	if err != nil {
		return CampaignResult{}, err
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	if err := os.MkdirAll(config.WorkspaceRoot, 0o700); err != nil {
		return CampaignResult{}, err
	}
	result := CampaignResult{
		SchemaVersion: "pysolate.release-readiness-matched-campaign.v1", ArtifactSHA256: digestBytes(artifact),
		SourceSHA256: workload.SourceSHA256, RunIndex: workload.RunIndex, Pairs: make([]PairResult, 0, config.Pairs),
		ScheduleScale: config.ScheduleScale, ProviderScale: config.ProviderScale,
		Reportable: config.Pairs == 30 && config.ScheduleScale == 1 && config.ProviderScale == 1,
	}
	for pairIndex := 0; pairIndex < config.Pairs; pairIndex++ {
		first := "baseline"
		if pairIndex%2 == 1 {
			first = "optimized"
		}
		var baseline, optimized LaneResult
		for _, lane := range []string{first, opposite(first)} {
			laneContext, cancel := context.WithTimeout(ctx, config.Timeout)
			laneRoot := filepath.Join(config.WorkspaceRoot, fmt.Sprintf("pair-%02d-%s", pairIndex+1, lane))
			laneResult, laneErr := runLane(laneContext, lane, artifact, profile, workload, laneRoot, config.ScheduleScale, config.ProviderScale)
			cancel()
			if laneErr != nil {
				return CampaignResult{}, fmt.Errorf("pair %d %s: %w", pairIndex+1, lane, laneErr)
			}
			if lane == "baseline" {
				baseline = laneResult
			} else {
				optimized = laneResult
			}
		}
		if baseline.ResultSHA256 != optimized.ResultSHA256 || baseline.ResultSHA256 != ExpectedFixtureResultSHA256 ||
			baseline.WorkspaceSHA256 != optimized.WorkspaceSHA256 || baseline.SourceSHA256 != optimized.SourceSHA256 {
			return CampaignResult{}, fmt.Errorf("pair %d result or workspace parity failed", pairIndex+1)
		}
		if baseline.PhysicalRequests != 4 || baseline.EarlyPhysicalRequests != 0 || optimized.PhysicalRequests != 4 ||
			optimized.EarlyPhysicalRequests != 4 || optimized.LogicalClaims != 4 || optimized.Consumed != 4 || optimized.QualifiedCalls != 4 ||
			baseline.GuestStartNS <= baseline.SourceCompleteNS || optimized.GuestStartNS <= optimized.SourceCompleteNS {
			return CampaignResult{}, fmt.Errorf("pair %d mechanism gate failed: baseline=%+v optimized=%+v", pairIndex+1, baseline, optimized)
		}
		saving := baseline.ElapsedNS - optimized.ElapsedNS
		result.Pairs = append(result.Pairs, PairResult{
			PairIndex: pairIndex + 1, FirstLane: first, Baseline: baseline, Optimized: optimized,
			SavingNS: saving, SavingRate: float64(saving) / float64(baseline.ElapsedNS),
		})
	}
	result.Summary = summarizePairs(result.Pairs)
	return result, nil
}

func runLane(ctx context.Context, lane string, artifact []byte, profile runtimeconfig.ExecutionProfile, workload RecordedWorkload, root string, scheduleScale, providerScale float64) (LaneResult, error) {
	if lane != "baseline" && lane != "optimized" {
		return LaneResult{}, errors.New("unknown campaign lane")
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return LaneResult{}, err
	}
	manager, err := workspace.NewManager(root)
	if err != nil {
		return LaneResult{}, err
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return LaneResult{}, err
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		return LaneResult{}, err
	}
	recorder := &laneRecorder{}
	plan, err := newFixturePlan(workload, providerScale, recorder.observe)
	if err != nil {
		return LaneResult{}, err
	}
	var controller *semantic.StreamingSemanticPreDispatch
	var admission *semantic.StreamingPrefixAdmission
	var generated semantic.GeneratedSource
	if lane == "optimized" {
		budget, budgetErr := semantic.NewPreDispatchBudget(4)
		if budgetErr != nil {
			return LaneResult{}, budgetErr
		}
		controller, err = semantic.NewStreamingSemanticPreDispatch(plan, budget, campaignLauncher{})
		if err != nil {
			return LaneResult{}, err
		}
		admission, err = semantic.NewStreamingPrefixAdmission(plan, controller, semantic.PreissueContext{
			StreamEpoch: "checkout-stream-v1", WorkflowEpoch: "checkout-readiness-v1", FreshnessEpoch: "fixture-v1",
			ExpiryEpoch: "run-end", PrivacyPartition: "checkout-private-run", ParentLineageSHA256: lineage,
		})
		if err != nil {
			return LaneResult{}, err
		}
	}
	recorder.start = time.Now()
	start := recorder.start
	var sourceComplete int64
	if lane == "optimized" {
		analyzerConfig := runtimeconfig.DefaultRunConfig()
		analyzerConfig.ExecutionProfile = &profile
		analyzerConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
		profileBinding, bindErr := runtimeconfig.ExecutionProfileBindingSHA256(analyzerConfig)
		if bindErr != nil {
			return LaneResult{}, bindErr
		}
		bindings := semantic.Bindings{
			ArtifactSHA256: digestBytes(artifact), ExecutionProfileSHA256: profileBinding,
			ImportClosureSHA256: digestBytes([]byte("checkout-readiness-imports-v1")), CapabilityPlanSHA256: plan.Identity(),
		}
		chunks := make(chan string, 32)
		complete := make(chan int64, 1)
		go feedRecordedSource(ctx, start, workload, scheduleScale, chunks, complete)
		eligible := make(map[uint32]struct{}, len(workload.ToolCalls))
		for _, call := range workload.ToolCalls {
			eligible[uint32(call.Statement)] = struct{}{}
		}
		generated, err = semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
			Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: chunks,
			ShouldAnalyzePrefix: func(prefixIndex uint32, _ string) bool { _, ok := eligible[prefixIndex]; return ok },
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
		})
		if err != nil {
			return LaneResult{}, err
		}
		sourceComplete = <-complete
	} else {
		sourceComplete, err = waitRecordedSource(ctx, start, workload, scheduleScale)
		if err != nil {
			return LaneResult{}, err
		}
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		return LaneResult{}, err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	factory := wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "checkout-" + lane,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			brokerConfig := capability.Config{RunIdentity: "checkout-" + lane, Plan: plan}
			if lane == "optimized" {
				brokerConfig.StagedClaimer = controller
				brokerConfig.SemanticPreDispatch = true
			}
			return capability.NewBroker(brokerConfig)
		},
	}
	if lane == "optimized" {
		passes, passErr := passplugin.NewDefaultEnabledCatalog(passregistration.SemanticPreDispatch)
		if passErr != nil {
			_ = attempt.Discard()
			return LaneResult{}, passErr
		}
		factory.Passes = passes
	}
	runner, err := factory.New(ctx, artifact, runConfig)
	if err != nil {
		_ = attempt.Discard()
		return LaneResult{}, err
	}
	request, err := json.Marshal(map[string]any{"run_id": "checkout-" + lane, "code": workload.Source, "inputs": map[string]any{}})
	if err != nil {
		_ = runner.Close(ctx)
		_ = attempt.Discard()
		return LaneResult{}, err
	}
	guestStart := time.Since(start).Nanoseconds()
	var runResult streaming.RunResult
	if lane == "optimized" {
		runResult, err = semantic.ExecuteGeneratedSource(ctx, runner, attempt, request, plan.StreamingPythonPrelude(), generated)
	} else {
		runResult, err = streaming.Execute(ctx, runner, attempt, request, plan.StreamingPythonPrelude())
	}
	if err != nil {
		return LaneResult{}, err
	}
	guestEnd := time.Since(start).Nanoseconds()
	resultValue, resultDigest, err := decodeResult(runResult.Response)
	if err != nil || ValidateReleaseResult(resultValue) != nil {
		return LaneResult{}, errors.New("invalid checkout release-readiness Guest result")
	}
	workspaceInfo, err := manager.Inspect(runResult.PublishedWorkspace)
	if err != nil {
		return LaneResult{}, err
	}
	physical, early := recorder.counts(sourceComplete)
	laneResult := LaneResult{
		Lane: lane, ElapsedNS: guestEnd, SourceCompleteNS: sourceComplete, GuestStartNS: guestStart, GuestEndNS: guestEnd,
		PhysicalRequests: physical, EarlyPhysicalRequests: early, ResultSHA256: resultDigest,
		WorkspaceSHA256: workspaceInfo.WorkspaceSHA256, SourceSHA256: workload.SourceSHA256,
	}
	if lane == "optimized" {
		controllerSnapshot := controller.Snapshot()
		admissionSnapshot := admission.Snapshot()
		laneResult.LogicalClaims = controllerSnapshot.LogicalClaims
		laneResult.Consumed = controllerSnapshot.Consumed
		laneResult.QualifiedCalls = admissionSnapshot.QualifiedCallCount
		if !controllerSnapshot.SourceSealed || !admissionSnapshot.Complete || controllerSnapshot.FinalSourceSHA256 != workload.SourceSHA256 {
			return LaneResult{}, errors.New("optimized source seal evidence mismatch")
		}
	}
	return laneResult, nil
}

func feedRecordedSource(ctx context.Context, start time.Time, workload RecordedWorkload, scale float64, chunks chan<- string, complete chan<- int64) {
	defer close(chunks)
	for _, statement := range workload.Statements {
		if waitUntil(ctx, start.Add(scaledDuration(statement.ClosedNS, scale))) != nil {
			return
		}
		select {
		case chunks <- statement.Source + "\n":
		case <-ctx.Done():
			return
		}
	}
	complete <- time.Since(start).Nanoseconds()
}

func waitRecordedSource(ctx context.Context, start time.Time, workload RecordedWorkload, scale float64) (int64, error) {
	for _, statement := range workload.Statements {
		if err := waitUntil(ctx, start.Add(scaledDuration(statement.ClosedNS, scale))); err != nil {
			return 0, err
		}
	}
	return time.Since(start).Nanoseconds(), nil
}

func waitUntil(ctx context.Context, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay <= 0 {
		return nil
	}
	return waitContext(ctx, delay)
}

func decodeResult(response []byte) (any, string, error) {
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" || len(envelope.Result) == 0 {
		return nil, "", errors.New("invalid Guest response envelope")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, "", errors.New("invalid Guest result JSON")
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return value, digestBytes(body), nil
}

func executionProfile(artifact []byte) (runtimeconfig.ExecutionProfile, error) {
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		return runtimeconfig.ExecutionProfile{}, err
	}
	return profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: digestBytes(artifact), ManifestSHA256: digestBytes([]byte("checkout-readiness-manifest-v1")),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
}

func summarizePairs(pairs []PairResult) CampaignSummary {
	baseline := make([]int64, len(pairs))
	optimized := make([]int64, len(pairs))
	savings := make([]int64, len(pairs))
	rates := make([]float64, len(pairs))
	summary := CampaignSummary{}
	for index, pair := range pairs {
		baseline[index] = pair.Baseline.ElapsedNS
		optimized[index] = pair.Optimized.ElapsedNS
		savings[index] = pair.SavingNS
		rates[index] = pair.SavingRate
		switch {
		case pair.SavingNS > 0:
			summary.ImprovedPairs++
		case pair.SavingNS < 0:
			summary.RegressedPairs++
		default:
			summary.TiedPairs++
		}
	}
	summary.BaselineMedianNS = medianInt64(baseline)
	summary.OptimizedMedianNS = medianInt64(optimized)
	summary.ScheduleMedianDiffNS = summary.BaselineMedianNS - summary.OptimizedMedianNS
	summary.MedianPairedSavingNS = medianInt64(savings)
	summary.MedianPairedSavingRate = medianFloat64(rates)
	summary.BootstrapLowNS, summary.BootstrapHighNS = bootstrapMedianInterval(savings, 10_000, 20260825)
	summary.SignTestP = twoSidedSignTest(summary.ImprovedPairs, summary.RegressedPairs)
	return summary
}

func medianInt64(values []int64) int64 {
	copied := append([]int64(nil), values...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	middle := len(copied) / 2
	if len(copied)%2 == 1 {
		return copied[middle]
	}
	return (copied[middle-1] + copied[middle]) / 2
}

func medianFloat64(values []float64) float64 {
	copied := append([]float64(nil), values...)
	sort.Float64s(copied)
	middle := len(copied) / 2
	if len(copied)%2 == 1 {
		return copied[middle]
	}
	return (copied[middle-1] + copied[middle]) / 2
}

func bootstrapMedianInterval(values []int64, samples int, seed int64) (int64, int64) {
	generator := rand.New(rand.NewSource(seed))
	medians := make([]int64, samples)
	resample := make([]int64, len(values))
	for sample := 0; sample < samples; sample++ {
		for index := range resample {
			resample[index] = values[generator.Intn(len(values))]
		}
		medians[sample] = medianInt64(resample)
	}
	sort.Slice(medians, func(i, j int) bool { return medians[i] < medians[j] })
	return medians[int(0.025*float64(samples))], medians[int(0.975*float64(samples))-1]
}

func twoSidedSignTest(positive, negative int) float64 {
	n := positive + negative
	if n == 0 {
		return 1
	}
	k := positive
	if negative < k {
		k = negative
	}
	probability := 0.0
	for index := 0; index <= k; index++ {
		probability += math.Gamma(float64(n+1)) / (math.Gamma(float64(index+1)) * math.Gamma(float64(n-index+1))) * math.Pow(0.5, float64(n))
	}
	return math.Min(1, 2*probability)
}

func opposite(lane string) string {
	if lane == "baseline" {
		return "optimized"
	}
	return "baseline"
}

type campaignLauncher struct{}

func (campaignLauncher) Launch(task func()) { go task() }
