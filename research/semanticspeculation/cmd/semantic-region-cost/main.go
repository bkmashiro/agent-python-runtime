package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

type coordinate struct {
	Case  int
	Trial uint32
}
type trialEvidence struct {
	CaseID                          string `json:"case_id"`
	ConstructedRegionExecutionNanos uint64 `json:"constructed_region_execution_nanos"`
	OperatorCount                   uint32 `json:"operator_count"`
	TrialIndex                      uint32 `json:"trial_index"`
	WholeSourceExecutionNanos       uint64 `json:"whole_source_execution_nanos"`
}
type caseSummary struct {
	CaseID                                string `json:"case_id"`
	MedianConstructedRegionExecutionNanos uint64 `json:"median_constructed_region_execution_nanos"`
	MedianWholeSourceExecutionNanos       uint64 `json:"median_whole_source_execution_nanos"`
	OperatorCount                         uint32 `json:"operator_count"`
	Pilot                                 bool   `json:"pilot"`
}
type report struct {
	AnalyzerArtifactSHA256  string          `json:"analyzer_artifact_sha256"`
	Authority               map[string]any  `json:"authority"`
	CaseSummaries           []caseSummary   `json:"case_summaries"`
	MatrixIdentity          string          `json:"matrix_identity"`
	MeasurementSourceCommit string          `json:"measurement_source_commit"`
	OpportunityGatePassed   bool            `json:"opportunity_gate_passed"`
	PreregistrationIdentity string          `json:"preregistration_identity"`
	SchemaVersion           string          `json:"schema_version"`
	StructuralCasesPassed   uint32          `json:"structural_cases_passed"`
	StudyID                 string          `json:"study_id"`
	SupersedesSHA256        string          `json:"supersedes_sha256,omitempty"`
	Trials                  []trialEvidence `json:"trials"`
}

func main() {
	artifactPath := flag.String("artifact", "", "exact Guest WASM artifact")
	matrixPath := flag.String("matrix", "", "frozen region matrix")
	outputPath := flag.String("output", "", "new evidence JSON path")
	measurementSourceCommit := flag.String("measurement-source-commit", "", "signed 40-hex measurement harness commit")
	flag.Parse()
	if flag.NArg() != 0 || *artifactPath == "" || *matrixPath == "" || *outputPath == "" || len(*measurementSourceCommit) != 40 {
		fatalf("artifact, matrix and output are required")
	}
	artifact := mustRead(*artifactPath)
	artifactSHA := digest(artifact)
	if artifactSHA != semanticspeculation.Phase4RegionRemediationArtifactSHA256 {
		fatalf("artifact=%s", artifactSHA)
	}
	matrixRaw := mustRead(*matrixPath)
	matrix, err := semanticspeculation.DecodePhase4RegionCaseMatrix(matrixRaw)
	if err != nil {
		fatalf("matrix: %v", err)
	}
	profile := boundProfile(artifactSHA)
	plan := analysisPlan()
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		fatalf("analysis engine: %v", err)
	}
	engine, ok := runner.(*wazeroengine.Engine)
	if !ok {
		fatalf("unexpected analysis engine %T", runner)
	}
	bindings := semantic.Bindings{ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: engine.Properties().ExecutionProfileBindingSHA256, ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json", "time"}, []string{"json", "time"}), CapabilityPlanSHA256: plan.Identity()}
	programs := map[int]string{}
	for index, candidate := range matrix.Cases {
		session, sessionErr := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{MaxRequests: 1, MaxCumulativeRequestBytes: uint64(config.MaxRequestBytes), MaxDuration: config.Timeout})
		if sessionErr != nil {
			fatalf("session %s: %v", candidate.ID, sessionErr)
		}
		request, requestErr := semantic.NewRequest(candidate.Source, bindings, plan)
		if requestErr != nil {
			fatalf("request %s: %v", candidate.ID, requestErr)
		}
		verified, analyzeErr := semantic.AnalyzeVerifiedSession(context.Background(), session, request)
		closeErr := session.Close(context.Background())
		if analyzeErr != nil || closeErr != nil {
			fatalf("analyze %s: %v close=%v", candidate.ID, analyzeErr, closeErr)
		}
		analysis, analysisErr := verified.Analysis()
		if analysisErr != nil || int(candidate.FocusRegionIndex) >= len(analysis.CandidateRegions) || analysis.CandidateRegions[candidate.FocusRegionIndex].LocallyReusable() != candidate.ExpectedLocalReusable {
			fatalf("structural gate %s failed: %v", candidate.ID, analysisErr)
		}
		if candidate.ExpectedLocalReusable {
			program, buildErr := semanticspeculation.BuildPhase4RegionCostProgram(candidate, analysis)
			if buildErr != nil {
				fatalf("cost program %s: %v", candidate.ID, buildErr)
			}
			programs[index] = program
		}
	}
	if err := engine.Close(context.Background()); err != nil {
		fatalf("close analyzer: %v", err)
	}

	config.Mechanisms = runtimeconfig.MechanismSet{}
	costRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		fatalf("cost engine: %v", err)
	}
	properties := costRunner.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		fatalf("cost runner has authority")
	}
	defer costRunner.Close(context.Background())
	coordinates := []coordinate{}
	for caseIndex := range matrix.Cases {
		if _, ok := programs[caseIndex]; !ok {
			continue
		}
		for trial := uint32(0); trial < 5; trial++ {
			coordinates = append(coordinates, coordinate{caseIndex, trial})
		}
	}
	rand.New(rand.NewSource(int64(matrix.ShuffleSeed))).Shuffle(len(coordinates), func(i, j int) { coordinates[i], coordinates[j] = coordinates[j], coordinates[i] })
	trials := make([]trialEvidence, 0, len(coordinates))
	for _, item := range coordinates {
		candidate := matrix.Cases[item.Case]
		req := runtimeconfig.RunRequest{RunID: fmt.Sprintf("p4-region-%s-%d", candidate.ID, item.Trial), Code: programs[item.Case], Inputs: json.RawMessage(`{}`)}
		raw, encodeErr := runtimeconfig.EncodeRunRequest(req)
		if encodeErr != nil {
			fatalf("encode: %v", encodeErr)
		}
		started := time.Now()
		payload, runErr := costRunner.Run(context.Background(), raw, "")
		wall := uint64(time.Since(started).Nanoseconds())
		if runErr != nil {
			fatalf("run %s/%d: %v", candidate.ID, item.Trial, runErr)
		}
		response, decodeErr := runtimeconfig.DecodeAndValidateGuestRunResponse(req, payload)
		if decodeErr != nil {
			fatalf("decode %s/%d: %v", candidate.ID, item.Trial, decodeErr)
		}
		var result struct {
			Constructed uint64 `json:"constructed_region_execution_nanos"`
		}
		if err := json.Unmarshal(response.Result, &result); err != nil || result.Constructed == 0 {
			fatalf("result %s/%d: %v %s", candidate.ID, item.Trial, err, response.Result)
		}
		trials = append(trials, trialEvidence{candidate.ID, result.Constructed, candidate.ConstructedCostShape.OperatorCount, item.Trial, wall})
	}
	summaries := summarize(matrix, trials)
	gate := true
	positive := []caseSummary{}
	for _, summary := range summaries {
		if !summary.Pilot {
			positive = append(positive, summary)
		}
	}
	sort.Slice(positive, func(i, j int) bool { return positive[i].OperatorCount < positive[j].OperatorCount })
	gate = len(positive) == 3 && positive[0].MedianConstructedRegionExecutionNanos < positive[1].MedianConstructedRegionExecutionNanos && positive[1].MedianConstructedRegionExecutionNanos < positive[2].MedianConstructedRegionExecutionNanos
	value := report{
		AnalyzerArtifactSHA256: artifactSHA,
		Authority:              map[string]any{"broker_available": false, "fresh_guest_per_trial": true, "whole_source_scope": "authority_free_constructed_dependency_closure_plus_exact_focus", "workspace_mounted": false},
		CaseSummaries:          summaries, MatrixIdentity: semanticspeculation.Phase4RegionMatrixIdentity,
		MeasurementSourceCommit: *measurementSourceCommit, OpportunityGatePassed: gate,
		PreregistrationIdentity: semanticspeculation.Phase4RegionPreregistrationIdentity,
		SchemaVersion:           "pysolate.semantic-speculation-phase4-region-cost-evidence.v2",
		StructuralCasesPassed:   uint32(len(matrix.Cases)), StudyID: semanticspeculation.Phase4RegionStudyID,
		SupersedesSHA256: "sha256:9efab34298523f81ff1294c2199f3154792345bcc456fcca72657ff15e420083",
		Trials:           trials,
	}
	encoded, _ := json.Marshal(value)
	encoded = append(encoded, '\n')
	output, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fatalf("create: %v", err)
	}
	if _, err := output.Write(encoded); err != nil {
		output.Close()
		fatalf("write: %v", err)
	}
	if err := output.Close(); err != nil {
		fatalf("close: %v", err)
	}
	fmt.Printf("%s gate=%t\n", digest(encoded), gate)
}

func summarize(matrix semanticspeculation.Phase4RegionCaseMatrix, trials []trialEvidence) []caseSummary {
	out := []caseSummary{}
	for _, candidate := range matrix.Cases {
		if !candidate.ExpectedLocalReusable {
			continue
		}
		region, wall := []uint64{}, []uint64{}
		for _, trial := range trials {
			if trial.CaseID == candidate.ID {
				region = append(region, trial.ConstructedRegionExecutionNanos)
				wall = append(wall, trial.WholeSourceExecutionNanos)
			}
		}
		sort.Slice(region, func(i, j int) bool { return region[i] < region[j] })
		sort.Slice(wall, func(i, j int) bool { return wall[i] < wall[j] })
		out = append(out, caseSummary{candidate.ID, region[len(region)/2], wall[len(wall)/2], candidate.ConstructedCostShape.OperatorCount, candidate.ID == "scalar_chain_2_before_effect"})
	}
	return out
}

func boundProfile(artifactSHA string) runtimeconfig.ExecutionProfile {
	imports := []string{"json", "time"}
	profile, err := runtimeconfig.NewExecutionProfile("base", imports)
	if err != nil {
		fatalf("profile: %v", err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: artifactSHA, ImportRoots: imports, QualifiedImportRoots: imports})
	if err != nil {
		fatalf("bind: %v", err)
	}
	return profile
}
func analysisPlan() *capability.Plan {
	r := capability.NewRegistry()
	register := func(spec capability.Spec) {
		grant, err := capability.NewGrant(json.RawMessage(`{"study":"phase4-region-cost"}`))
		if err != nil {
			fatalf("grant: %v", err)
		}
		if err := r.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })); err != nil {
			fatalf("register %s: %v", spec.Name, err)
		}
	}
	baseIn := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	baseOut := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	register(capability.Spec{Name: "sources.demo_catalog", Version: "pysolate.phase4-source.v1", Description: "Frozen source effect", EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.phase4-source-handler.v1", InputSchema: baseIn, OutputSchema: baseOut, Python: &capability.PythonProjection{Module: "sources", Method: "demo_catalog"}})
	register(capability.Spec{Name: "mail.send", Version: "pysolate.phase4-mail.v1", Description: "Frozen sink effect", EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.phase4-mail-handler.v1", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{}},"required":["value"],"additionalProperties":false}`), OutputSchema: baseOut, Python: &capability.PythonProjection{Module: "sinks", Method: "demo_publish", Arguments: []string{"value"}}})
	plan, err := r.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		fatalf("seal: %v", err)
	}
	return plan
}
func mustRead(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		fatalf("read %s: %v", path, err)
	}
	return raw
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return fmt.Sprintf("sha256:%x", sum[:]) }
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "semantic-region-cost: "+format+"\n", args...)
	os.Exit(1)
}
