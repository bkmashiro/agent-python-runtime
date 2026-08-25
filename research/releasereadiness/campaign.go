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
	"sync/atomic"
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

var campaignLaneOrders = [][]string{
	{"baseline", "post_source_parallel", "optimized"},
	{"baseline", "optimized", "post_source_parallel"},
	{"post_source_parallel", "baseline", "optimized"},
	{"post_source_parallel", "optimized", "baseline"},
	{"optimized", "baseline", "post_source_parallel"},
	{"optimized", "post_source_parallel", "baseline"},
}

type CampaignConfig struct {
	ArtifactPath  string
	WorkloadPath  string
	WorkspaceRoot string
	Groups        int
	ScheduleScale float64
	ProviderScale float64
	Timeout       time.Duration
}

type LaneResult struct {
	Lane      string `json:"lane"`
	ElapsedNS int64  `json:"elapsed_ns"`
	// SourceCompleteNS is the final source-arrival boundary: the last recorded
	// source chunk has been handed to the semantic pipeline. It is not the later
	// semantic source-sealed timestamp.
	SourceCompleteNS      int64  `json:"source_complete_ns"`
	GuestStartNS          int64  `json:"guest_start_ns"`
	GuestEndNS            int64  `json:"guest_end_ns"`
	PhysicalRequests      int    `json:"physical_requests"`
	EarlyPhysicalRequests int    `json:"early_physical_requests"`
	MaxConcurrentRequests int    `json:"max_concurrent_requests"`
	LogicalClaims         uint32 `json:"logical_claims"`
	Consumed              uint32 `json:"consumed"`
	QualifiedCalls        uint32 `json:"qualified_calls"`
	ResultSHA256          string `json:"result_sha256"`
	WorkspaceSHA256       string `json:"workspace_sha256"`
	SourceSHA256          string `json:"source_sha256"`
}

type GroupResult struct {
	GroupIndex                  int        `json:"group_index"`
	LaneOrder                   []string   `json:"lane_order"`
	Baseline                    LaneResult `json:"baseline"`
	PostSourceParallel          LaneResult `json:"post_source_parallel"`
	Optimized                   LaneResult `json:"optimized"`
	BaselineVsParallelSavingNS  int64      `json:"baseline_vs_post_source_parallel_saving_ns"`
	ParallelVsOptimizedSavingNS int64      `json:"post_source_parallel_vs_optimized_saving_ns"`
	BaselineVsOptimizedSavingNS int64      `json:"baseline_vs_optimized_saving_ns"`
}

type ComparisonSummary struct {
	LeftLane               string  `json:"left_lane"`
	RightLane              string  `json:"right_lane"`
	LeftMedianNS           int64   `json:"left_median_ns"`
	RightMedianNS          int64   `json:"right_median_ns"`
	MedianPairedSavingNS   int64   `json:"median_paired_saving_ns"`
	MedianPairedSavingRate float64 `json:"median_paired_saving_rate"`
	BootstrapLowNS         int64   `json:"bootstrap_95_low_ns"`
	BootstrapHighNS        int64   `json:"bootstrap_95_high_ns"`
	ImprovedGroups         int     `json:"improved_groups"`
	TiedGroups             int     `json:"tied_groups"`
	RegressedGroups        int     `json:"regressed_groups"`
	SignTestP              float64 `json:"two_sided_sign_test_p"`
}

type CampaignSummary struct {
	BaselineMedianNS           int64             `json:"baseline_median_ns"`
	PostSourceParallelMedianNS int64             `json:"post_source_parallel_median_ns"`
	OptimizedMedianNS          int64             `json:"optimized_median_ns"`
	BaselineVsParallel         ComparisonSummary `json:"baseline_vs_post_source_parallel"`
	ParallelVsOptimized        ComparisonSummary `json:"post_source_parallel_vs_optimized"`
	BaselineVsOptimized        ComparisonSummary `json:"baseline_vs_optimized"`
}

type CampaignResult struct {
	SchemaVersion  string          `json:"schema_version"`
	ArtifactSHA256 string          `json:"artifact_sha256"`
	SourceSHA256   string          `json:"source_sha256"`
	RunIndex       int             `json:"recorded_run_index"`
	Groups         []GroupResult   `json:"groups"`
	Summary        CampaignSummary `json:"summary"`
	ScheduleScale  float64         `json:"schedule_scale"`
	ProviderScale  float64         `json:"provider_scale"`
	Reportable     bool            `json:"reportable"`
}

type laneRecorder struct {
	mu       sync.Mutex
	sequence atomic.Uint64
	start    time.Time
	events   []ProviderEvent
}

func (recorder *laneRecorder) observe(phase, capability string) {
	atNS := time.Since(recorder.start).Nanoseconds()
	event := ProviderEvent{
		Phase: phase, Capability: capability,
		AtNS: atNS, Sequence: recorder.sequence.Add(1),
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
}

func (recorder *laneRecorder) metrics(sourceComplete int64) (physical, early, maxConcurrent int) {
	recorder.mu.Lock()
	events := append([]ProviderEvent(nil), recorder.events...)
	recorder.mu.Unlock()
	sort.SliceStable(events, func(left, right int) bool {
		if events[left].AtNS != events[right].AtNS {
			return events[left].AtNS < events[right].AtNS
		}
		return events[left].Sequence < events[right].Sequence
	})
	active := 0
	for _, event := range events {
		switch event.Phase {
		case "start":
			physical++
			active++
			if active > maxConcurrent {
				maxConcurrent = active
			}
			if event.AtNS < sourceComplete {
				early++
			}
		case "finish", "finish_error":
			if active > 0 {
				active--
			}
		}
	}
	return physical, early, maxConcurrent
}

func RunCampaign(ctx context.Context, config CampaignConfig) (CampaignResult, error) {
	if ctx == nil || config.ArtifactPath == "" || !filepath.IsAbs(config.ArtifactPath) || config.WorkloadPath == "" ||
		config.WorkspaceRoot == "" || !filepath.IsAbs(config.WorkspaceRoot) || config.Groups < 1 || config.Groups > 30 ||
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
		SchemaVersion: "pysolate.release-readiness-three-lane-campaign.v2", ArtifactSHA256: digestBytes(artifact),
		SourceSHA256: workload.SourceSHA256, RunIndex: workload.RunIndex, Groups: make([]GroupResult, 0, config.Groups),
		ScheduleScale: config.ScheduleScale, ProviderScale: config.ProviderScale,
		Reportable: config.Groups == 30 && config.ScheduleScale == 1 && config.ProviderScale == 1,
	}
	for groupIndex := 0; groupIndex < config.Groups; groupIndex++ {
		order := campaignLaneOrders[groupIndex%len(campaignLaneOrders)]
		var baseline, parallel, optimized LaneResult
		for _, lane := range order {
			laneContext, cancel := context.WithTimeout(ctx, config.Timeout)
			laneRoot := filepath.Join(config.WorkspaceRoot, fmt.Sprintf("group-%02d-%s", groupIndex+1, lane))
			laneResult, laneErr := runLane(laneContext, lane, artifact, profile, workload, laneRoot, config.ScheduleScale, config.ProviderScale)
			cancel()
			if laneErr != nil {
				return CampaignResult{}, fmt.Errorf("group %d %s: %w", groupIndex+1, lane, laneErr)
			}
			switch lane {
			case "baseline":
				baseline = laneResult
			case "post_source_parallel":
				parallel = laneResult
			case "optimized":
				optimized = laneResult
			}
		}
		if baseline.ResultSHA256 != parallel.ResultSHA256 || baseline.ResultSHA256 != optimized.ResultSHA256 ||
			baseline.ResultSHA256 != ExpectedFixtureResultSHA256 || baseline.WorkspaceSHA256 != parallel.WorkspaceSHA256 ||
			baseline.WorkspaceSHA256 != optimized.WorkspaceSHA256 || baseline.SourceSHA256 != parallel.SourceSHA256 ||
			baseline.SourceSHA256 != optimized.SourceSHA256 {
			return CampaignResult{}, fmt.Errorf("group %d result, workspace or source parity failed", groupIndex+1)
		}
		if baseline.PhysicalRequests != 4 || baseline.EarlyPhysicalRequests != 0 || baseline.MaxConcurrentRequests != 1 ||
			parallel.PhysicalRequests != 4 || parallel.EarlyPhysicalRequests != 0 || parallel.MaxConcurrentRequests != 4 ||
			parallel.LogicalClaims != 4 || parallel.Consumed != 4 || parallel.QualifiedCalls != 4 ||
			optimized.PhysicalRequests != 4 || optimized.EarlyPhysicalRequests != 4 || optimized.MaxConcurrentRequests != 4 || optimized.LogicalClaims != 4 ||
			optimized.Consumed != 4 || optimized.QualifiedCalls != 4 || baseline.GuestStartNS <= baseline.SourceCompleteNS ||
			parallel.GuestStartNS <= parallel.SourceCompleteNS || optimized.GuestStartNS <= optimized.SourceCompleteNS {
			return CampaignResult{}, fmt.Errorf("group %d mechanism gate failed: baseline=%+v parallel=%+v optimized=%+v", groupIndex+1, baseline, parallel, optimized)
		}
		result.Groups = append(result.Groups, GroupResult{
			GroupIndex: groupIndex + 1, LaneOrder: append([]string(nil), order...), Baseline: baseline,
			PostSourceParallel: parallel, Optimized: optimized,
			BaselineVsParallelSavingNS:  baseline.ElapsedNS - parallel.ElapsedNS,
			ParallelVsOptimizedSavingNS: parallel.ElapsedNS - optimized.ElapsedNS,
			BaselineVsOptimizedSavingNS: baseline.ElapsedNS - optimized.ElapsedNS,
		})
	}
	result.Summary = summarizeGroups(result.Groups)
	return result, nil
}

func runLane(ctx context.Context, lane string, artifact []byte, profile runtimeconfig.ExecutionProfile, workload RecordedWorkload, root string, scheduleScale, providerScale float64) (LaneResult, error) {
	if lane != "baseline" && lane != "post_source_parallel" && lane != "optimized" {
		return LaneResult{}, errors.New("unknown campaign lane")
	}
	usesPreDispatch := lane != "baseline"
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
	if usesPreDispatch {
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
	if usesPreDispatch {
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
		analyze := func(analyzeContext context.Context, source string, prefixBindings semantic.Bindings, prefixPlan *capability.Plan) (semantic.VerifiedAnalysis, error) {
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
		}
		chunks := make(chan string, 32)
		var complete chan int64
		eligible := make(map[uint32]struct{}, len(workload.ToolCalls))
		for _, call := range workload.ToolCalls {
			eligible[uint32(call.Statement)] = struct{}{}
		}
		shouldAnalyze := func(prefixIndex uint32, _ string) bool { _, ok := eligible[prefixIndex]; return ok }
		if lane == "optimized" {
			complete = make(chan int64, 1)
			go feedRecordedSource(ctx, start, workload, scheduleScale, chunks, complete)
		} else {
			sourceComplete, err = waitRecordedSource(ctx, start, workload, scheduleScale)
			if err != nil {
				return LaneResult{}, err
			}
			go feedAvailableSource(ctx, workload, chunks)
		}
		generated, err = semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
			Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: chunks,
			ShouldAnalyzePrefix: shouldAnalyze, Analyze: analyze,
		})
		if err != nil {
			return LaneResult{}, err
		}
		if lane == "optimized" {
			sourceComplete, err = waitForSourceComplete(ctx, complete)
			if err != nil {
				return LaneResult{}, err
			}
		}
	} else {
		sourceComplete, err = waitRecordedSource(ctx, start, workload, scheduleScale)
		if err != nil {
			return LaneResult{}, err
		}
	}
	if usesPreDispatch {
		defer func() { _ = controller.Finalize(false) }()
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
			if usesPreDispatch {
				brokerConfig.StagedClaimer = controller
				brokerConfig.SemanticPreDispatch = true
			}
			return capability.NewBroker(brokerConfig)
		},
	}
	if usesPreDispatch {
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
	if usesPreDispatch {
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
	physical, early, maxConcurrent := recorder.metrics(sourceComplete)
	laneResult := LaneResult{
		Lane: lane, ElapsedNS: guestEnd, SourceCompleteNS: sourceComplete, GuestStartNS: guestStart, GuestEndNS: guestEnd,
		PhysicalRequests: physical, EarlyPhysicalRequests: early, MaxConcurrentRequests: maxConcurrent, ResultSHA256: resultDigest,
		WorkspaceSHA256: workspaceInfo.WorkspaceSHA256, SourceSHA256: workload.SourceSHA256,
	}
	if usesPreDispatch {
		controllerSnapshot := controller.Snapshot()
		admissionSnapshot := admission.Snapshot()
		laneResult.LogicalClaims = controllerSnapshot.LogicalClaims
		laneResult.Consumed = controllerSnapshot.Consumed
		laneResult.QualifiedCalls = admissionSnapshot.QualifiedCallCount
		if !controllerSnapshot.SourceSealed || !admissionSnapshot.Complete || controllerSnapshot.FinalSourceSHA256 != workload.SourceSHA256 {
			return LaneResult{}, fmt.Errorf("%s source seal evidence mismatch", lane)
		}
	}
	return laneResult, nil
}

func waitForSourceComplete(ctx context.Context, complete <-chan int64) (int64, error) {
	select {
	case sourceComplete := <-complete:
		return sourceComplete, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func feedAvailableSource(ctx context.Context, workload RecordedWorkload, chunks chan<- string) {
	defer close(chunks)
	for _, statement := range workload.Statements {
		select {
		case chunks <- statement.Source + "\n":
		case <-ctx.Done():
			return
		}
	}
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

func summarizeGroups(groups []GroupResult) CampaignSummary {
	baseline := make([]int64, len(groups))
	parallel := make([]int64, len(groups))
	optimized := make([]int64, len(groups))
	for index, group := range groups {
		baseline[index] = group.Baseline.ElapsedNS
		parallel[index] = group.PostSourceParallel.ElapsedNS
		optimized[index] = group.Optimized.ElapsedNS
	}
	return CampaignSummary{
		BaselineMedianNS: medianInt64(baseline), PostSourceParallelMedianNS: medianInt64(parallel), OptimizedMedianNS: medianInt64(optimized),
		BaselineVsParallel:  summarizeComparison("baseline", "post_source_parallel", baseline, parallel, 20260825),
		ParallelVsOptimized: summarizeComparison("post_source_parallel", "optimized", parallel, optimized, 20260826),
		BaselineVsOptimized: summarizeComparison("baseline", "optimized", baseline, optimized, 20260827),
	}
}

func summarizeComparison(leftLane, rightLane string, left, right []int64, seed int64) ComparisonSummary {
	savings := make([]int64, len(left))
	rates := make([]float64, len(left))
	summary := ComparisonSummary{LeftLane: leftLane, RightLane: rightLane, LeftMedianNS: medianInt64(left), RightMedianNS: medianInt64(right)}
	for index := range left {
		savings[index] = left[index] - right[index]
		rates[index] = float64(savings[index]) / float64(left[index])
		switch {
		case savings[index] > 0:
			summary.ImprovedGroups++
		case savings[index] < 0:
			summary.RegressedGroups++
		default:
			summary.TiedGroups++
		}
	}
	summary.MedianPairedSavingNS = medianInt64(savings)
	summary.MedianPairedSavingRate = medianFloat64(rates)
	summary.BootstrapLowNS, summary.BootstrapHighNS = bootstrapMedianInterval(savings, 10_000, seed)
	summary.SignTestP = twoSidedSignTest(summary.ImprovedGroups, summary.RegressedGroups)
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

type campaignLauncher struct{}

func (campaignLauncher) Launch(task func()) { go task() }
