package releasereadiness

import (
	"context"
	"os"
	"path/filepath"
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

func TestMatchedPairUsesFreshFinalGuestsAndExactFourReadClaims(t *testing.T) {
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
		Pairs:         1,
		ScheduleScale: 0.01,
		ProviderScale: 0.01,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 1 {
		t.Fatalf("pairs=%d", len(result.Pairs))
	}
	pair := result.Pairs[0]
	if pair.Baseline.ResultSHA256 != pair.Optimized.ResultSHA256 || pair.Baseline.WorkspaceSHA256 != pair.Optimized.WorkspaceSHA256 {
		t.Fatalf("baseline=%+v optimized=%+v", pair.Baseline, pair.Optimized)
	}
	if pair.Baseline.PhysicalRequests != 4 || pair.Optimized.PhysicalRequests != 4 || pair.Optimized.LogicalClaims != 4 || pair.Optimized.Consumed != 4 {
		t.Fatalf("baseline=%+v optimized=%+v", pair.Baseline, pair.Optimized)
	}
	if pair.Baseline.EarlyPhysicalRequests != 0 || pair.Optimized.EarlyPhysicalRequests != 4 {
		t.Fatalf("baseline early=%d optimized early=%d", pair.Baseline.EarlyPhysicalRequests, pair.Optimized.EarlyPhysicalRequests)
	}
	if pair.Baseline.GuestStartNS <= pair.Baseline.SourceCompleteNS || pair.Optimized.GuestStartNS <= pair.Optimized.SourceCompleteNS {
		t.Fatalf("baseline guest/source=%d/%d optimized=%d/%d", pair.Baseline.GuestStartNS, pair.Baseline.SourceCompleteNS, pair.Optimized.GuestStartNS, pair.Optimized.SourceCompleteNS)
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
	})
	if _, ok := decision.QualifiedCall(); !ok {
		t.Fatalf("decision=%+v analysis=%+v", decision, verifiedAnalysis)
	}
}
