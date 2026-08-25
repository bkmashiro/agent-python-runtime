package releasereadiness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestRecordedRunOneIsDeterministicExecutableWorkload(t *testing.T) {
	workload, err := LoadRecordedWorkload(filepath.Join("testdata", "recorded-run-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if workload.RunIndex != 1 || workload.SourceSHA256 != RecordedRunOneSourceSHA256 {
		t.Fatalf("run=%d source=%s", workload.RunIndex, workload.SourceSHA256)
	}
	if len(workload.Statements) != 379 || workload.SourceCompleteNS != 23_405_477_625 || workload.EligibleWindowNS != 20_819_912_584 {
		t.Fatalf("statements=%d source=%d window=%d", len(workload.Statements), workload.SourceCompleteNS, workload.EligibleWindowNS)
	}
	want := []string{
		"observability.query_metrics",
		"observability.query_logs",
		"github.latest_deployment",
		"kubernetes.read_deployment",
	}
	if len(workload.ToolCalls) != len(want) {
		t.Fatalf("tool calls=%d", len(workload.ToolCalls))
	}
	for index, name := range want {
		if workload.ToolCalls[index].Capability != name {
			t.Fatalf("tool call %d=%q", index, workload.ToolCalls[index].Capability)
		}
	}
}

func TestFixtureOracleIsStableAndReleaseReady(t *testing.T) {
	result := ExpectedReleaseResult()
	if err := ValidateReleaseResult(result); err != nil {
		t.Fatal(err)
	}
	if digestJSON(result) != ExpectedFixtureResultSHA256 {
		t.Fatalf("result digest=%s", digestJSON(result))
	}
}

func TestMatchedGroupUsesFreshFinalGuestsAndPostSourceParallelReads(t *testing.T) {
	artifact := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifact == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	absoluteArtifact, err := filepath.Abs(artifact)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunCampaign(t.Context(), CampaignConfig{
		ArtifactPath:  absoluteArtifact,
		WorkloadPath:  filepath.Join("testdata", "recorded-run-1.json"),
		WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"),
		Groups:        1,
		ScheduleScale: 0.5,
		ProviderScale: 0.5,
		Timeout:       45 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != "pysolate.release-readiness-three-lane-campaign.v2" || len(result.Groups) != 1 {
		t.Fatalf("schema=%q groups=%d", result.SchemaVersion, len(result.Groups))
	}
	group := result.Groups[0]
	lanes := []LaneResult{group.Baseline, group.PostSourceParallel, group.Optimized}
	for _, lane := range lanes {
		if lane.ResultSHA256 != ExpectedFixtureResultSHA256 || lane.WorkspaceSHA256 != group.Baseline.WorkspaceSHA256 || lane.SourceSHA256 != group.Baseline.SourceSHA256 {
			t.Fatalf("lane parity failed: %+v", lane)
		}
		if lane.GuestStartNS <= lane.SourceCompleteNS || lane.PhysicalRequests != 4 {
			t.Fatalf("lane mechanism failed: %+v", lane)
		}
	}
	parallel := group.PostSourceParallel
	if parallel.EarlyPhysicalRequests != 0 || parallel.MaxConcurrentRequests != 4 || parallel.LogicalClaims != 4 || parallel.Consumed != 4 || parallel.QualifiedCalls != 4 {
		t.Fatalf("post-source parallel=%+v", parallel)
	}
	if group.Baseline.EarlyPhysicalRequests != 0 || group.Baseline.MaxConcurrentRequests != 1 || group.Optimized.EarlyPhysicalRequests != 4 {
		t.Fatalf("baseline=%+v optimized=%+v", group.Baseline, group.Optimized)
	}
	if len(group.LaneOrder) != 3 || group.LaneOrder[0] != "baseline" || group.LaneOrder[1] != "post_source_parallel" || group.LaneOrder[2] != "optimized" {
		t.Fatalf("lane order=%v", group.LaneOrder)
	}
}

func TestThreeLaneOrdersAreBalancedAcrossReportableCampaign(t *testing.T) {
	lanes := []string{"baseline", "post_source_parallel", "optimized"}
	counts := make(map[string][3]int, len(lanes))
	seen := make(map[string]struct{}, len(campaignLaneOrders))
	for _, order := range campaignLaneOrders {
		if len(order) != len(lanes) {
			t.Fatalf("invalid lane order: %v", order)
		}
		members := append([]string(nil), order...)
		sort.Strings(members)
		if strings.Join(members, ",") != "baseline,optimized,post_source_parallel" {
			t.Fatalf("lane order is not a permutation: %v", order)
		}
		seen[strings.Join(order, ",")] = struct{}{}
	}
	if len(seen) != 6 {
		t.Fatalf("unique lane orders=%d want=6", len(seen))
	}
	for groupIndex := 0; groupIndex < 30; groupIndex++ {
		order := campaignLaneOrders[groupIndex%len(campaignLaneOrders)]
		if len(order) != 3 {
			t.Fatalf("order=%v", order)
		}
		for position, lane := range order {
			row := counts[lane]
			row[position]++
			counts[lane] = row
		}
	}
	for _, lane := range lanes {
		if counts[lane] != [3]int{10, 10, 10} {
			t.Fatalf("lane=%s counts=%v", lane, counts[lane])
		}
	}
}

func TestLaneRecorderTracksObservedConcurrency(t *testing.T) {
	recorder := &laneRecorder{events: []ProviderEvent{
		{Phase: "start", Capability: "b", AtNS: 2, Sequence: 2},
		{Phase: "finish", Capability: "a", AtNS: 3, Sequence: 3},
		{Phase: "start", Capability: "a", AtNS: 1, Sequence: 1},
		{Phase: "finish", Capability: "b", AtNS: 4, Sequence: 4},
	}}
	physical, early, concurrent := recorder.metrics(2)
	if physical != 2 || early != 1 || concurrent != 2 {
		t.Fatalf("physical=%d early=%d concurrent=%d", physical, early, concurrent)
	}
}

func TestWaitForSourceCompleteStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForSourceComplete(ctx, make(chan int64)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestThreeLaneSummaryUsesWithinGroupComparisons(t *testing.T) {
	groups := []GroupResult{
		{Baseline: LaneResult{ElapsedNS: 100}, PostSourceParallel: LaneResult{ElapsedNS: 80}, Optimized: LaneResult{ElapsedNS: 70}},
		{Baseline: LaneResult{ElapsedNS: 120}, PostSourceParallel: LaneResult{ElapsedNS: 90}, Optimized: LaneResult{ElapsedNS: 75}},
		{Baseline: LaneResult{ElapsedNS: 110}, PostSourceParallel: LaneResult{ElapsedNS: 85}, Optimized: LaneResult{ElapsedNS: 72}},
	}
	summary := summarizeGroups(groups)
	if summary.BaselineMedianNS != 110 || summary.PostSourceParallelMedianNS != 85 || summary.OptimizedMedianNS != 72 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.BaselineVsParallel.MedianPairedSavingNS != 25 || summary.ParallelVsOptimized.MedianPairedSavingNS != 13 || summary.BaselineVsOptimized.MedianPairedSavingNS != 38 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.BaselineVsParallel.ImprovedGroups != 3 || summary.ParallelVsOptimized.ImprovedGroups != 3 || summary.BaselineVsOptimized.ImprovedGroups != 3 {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestRecordedPrefixQualifiesTheFirstRead(t *testing.T) {
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	workload, err := LoadRecordedWorkload(filepath.Join("testdata", "recorded-run-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newFixturePlan(workload, 0.01, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := executionProfile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	analyzerConfig := runtimeconfig.DefaultRunConfig()
	analyzerConfig.ExecutionProfile = &profile
	analyzerConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	prefixLines := make([]string, 0, 35)
	for _, statement := range workload.Statements[:35] {
		prefixLines = append(prefixLines, statement.Source)
	}
	prefix := strings.Join(prefixLines, "\n") + "\n"
	profileBinding, err := runtimeconfig.ExecutionProfileBindingSHA256(analyzerConfig)
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256:         digestBytes(artifact),
		ExecutionProfileSHA256: profileBinding,
		ImportClosureSHA256:    digestBytes([]byte("release-readiness-import-closure-v1")),
		CapabilityPlanSHA256:   plan.Identity(),
	}
	analyzer, err := wazeroengine.New(context.Background(), artifact, analyzerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close(context.Background())
	request, err := semantic.NewRequest(prefix, bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), analyzer, request)
	if err != nil {
		t.Fatal(err)
	}
	verifiedAnalysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifiedAnalysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v", verifiedAnalysis)
	}
	decision := semantic.CanPreissueStreamingPrefix(verified, plan, verifiedAnalysis.CallSites[0].ID, semantic.PreissueContext{
		StreamEpoch: "checkout-stream", WorkflowEpoch: "checkout-workflow", FreshnessEpoch: "checkout-fresh",
		ExpiryEpoch: "run-end", PrivacyPartition: "checkout", ParentLineageSHA256: digestBytes([]byte("lineage")),
		BudgetReservationSHA256: digestBytes([]byte("budget-reservation")), RemainingPhysicalReads: 4,
	})
	if _, ok := decision.QualifiedCall(); !ok {
		t.Fatalf("decision=%+v analysis=%+v", decision, verifiedAnalysis)
	}
}
