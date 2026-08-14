package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const reportSchema = "pysolate.semantic-predispatch-experiment.v0"

type reportProvenance struct {
	ArtifactSHA256       string
	SourceSHA256         string
	CapabilityPlanSHA256 string
}

type trial struct {
	Condition        string `json:"condition"`
	DurationMicros   int64  `json:"duration_micros"`
	LogicalCalls     uint32 `json:"logical_calls"`
	PhysicalCalls    uint32 `json:"physical_calls"`
	PhysicalIssues   uint32 `json:"physical_issues"`
	PhysicalStarts   uint32 `json:"physical_starts"`
	PhysicalFinishes uint32 `json:"physical_finishes"`
	RejectedClaims   uint32 `json:"rejected_claims"`
	ResultSHA256     string `json:"result_sha256"`
}

type report struct {
	SchemaVersion           string  `json:"schema_version"`
	ArtifactSHA256          string  `json:"artifact_sha256"`
	SourceSHA256            string  `json:"source_sha256"`
	CapabilityPlanSHA256    string  `json:"capability_plan_sha256"`
	TrialsPerCondition      int     `json:"trials_per_condition"`
	PhysicalDelayMicros     int64   `json:"physical_delay_micros"`
	BaselineMedianMicros    int64   `json:"baseline_median_micros"`
	OptimizedMedianMicros   int64   `json:"optimized_median_micros"`
	MedianSavingsMicros     int64   `json:"median_savings_micros"`
	EquivalentResults       bool    `json:"equivalent_results"`
	NoDuplicatePhysicalCall bool    `json:"no_duplicate_physical_call"`
	ContentSHA256           string  `json:"content_sha256"`
	Trials                  []trial `json:"trials"`
}

func main() {
	artifactPath := flag.String("artifact", "", "reviewed CPython/WASI artifact")
	output := flag.String("output", "docs/evidence/semantic-predispatch-experiment.json", "report path")
	trials := flag.Int("trials", 5, "trials per condition (fixed at 5 for v0 evidence)")
	delay := flag.Duration("delay", 250*time.Millisecond, "physical read latency")
	flag.Parse()
	if *artifactPath == "" || *trials != 5 || *delay < 10*time.Millisecond || *delay > 2*time.Second {
		fatal(errors.New("artifact, exactly 5 trials, and a 10ms..2s delay are required"))
	}
	artifact, err := os.ReadFile(*artifactPath)
	if err != nil {
		fatal(err)
	}
	artifactSHA := digest(artifact)
	source := "result = sources.read('profile')\n"

	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: digest([]byte("semantic-predispatch-experiment-manifest")),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		fatal(err)
	}
	var handlerCalls atomic.Uint32
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"semantic-predispatch-experiment"}`))
	if err != nil {
		fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.semantic-predispatch-experiment.v0",
		Description: "Bounded delayed read for semantic pre-dispatch evaluation.", EffectClass: capability.EffectExternalRead,
		Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.semantic-predispatch-experiment.v0",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}, ResultField: "value"},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "sources", Argument: "key"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		handlerCalls.Add(1)
		timer := time.NewTimer(*delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return json.RawMessage(`{"value":"fixture"}`), nil
		}
	})); err != nil {
		fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		fatal(err)
	}
	verified, siteID := analyze(artifact, profile, artifactSHA, source, plan)

	expectedProvenance := reportProvenance{
		ArtifactSHA256: artifactSHA, SourceSHA256: digest([]byte(source)), CapabilityPlanSHA256: plan.Identity(),
	}
	result := report{
		SchemaVersion: reportSchema, ArtifactSHA256: expectedProvenance.ArtifactSHA256, SourceSHA256: expectedProvenance.SourceSHA256,
		CapabilityPlanSHA256: expectedProvenance.CapabilityPlanSHA256, TrialsPerCondition: *trials, PhysicalDelayMicros: delay.Microseconds(),
		EquivalentResults: true, NoDuplicatePhysicalCall: true, Trials: make([]trial, 0, *trials*2),
	}
	for index := 0; index < *trials; index++ {
		for _, condition := range []string{"baseline", "semantic_pre_dispatch"} {
			before := handlerCalls.Load()
			row, err := runTrial(artifact, profile, source, plan, verified, siteID, condition, index)
			if err != nil {
				fatal(err)
			}
			row.PhysicalCalls = handlerCalls.Load() - before
			if row.PhysicalCalls != 1 || row.ResultSHA256 != digest([]byte(`"fixture"`)) {
				result.NoDuplicatePhysicalCall = false
				result.EquivalentResults = false
			}
			result.Trials = append(result.Trials, row)
		}
	}
	result.BaselineMedianMicros = median(result.Trials, "baseline")
	result.OptimizedMedianMicros = median(result.Trials, "semantic_pre_dispatch")
	result.MedianSavingsMicros = result.BaselineMedianMicros - result.OptimizedMedianMicros
	if !result.EquivalentResults || !result.NoDuplicatePhysicalCall {
		fatal(errors.New("runtime differential invariant failed"))
	}
	result.ContentSHA256 = sealReport(result)
	if err := validateReport(result, expectedProvenance); err != nil {
		fatal(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, encoded, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("baseline_median_us=%d optimized_median_us=%d savings_us=%d trials=%d\n", result.BaselineMedianMicros, result.OptimizedMedianMicros, result.MedianSavingsMicros, len(result.Trials))
}

func analyze(artifact []byte, profile runtimeconfig.ExecutionProfile, artifactSHA, source string, plan *capability.Plan) (semantic.VerifiedAnalysis, string) {
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		fatal(err)
	}
	defer runner.Close(context.Background())
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}
	request, err := semantic.NewRequest(source, bindings, plan)
	if err != nil {
		fatal(err)
	}
	trusted, ok := runner.(*wazeroengine.Engine)
	if !ok {
		fatal(errors.New("analysis runner is not target Wazero"))
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trusted, request)
	if err != nil {
		fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 {
		fatal(errors.New("expected one exact call site"))
	}
	return verified, analysis.CallSites[0].ID
}

func runTrial(artifact []byte, profile runtimeconfig.ExecutionProfile, source string, plan *capability.Plan, verified semantic.VerifiedAnalysis, siteID, condition string, index int) (trial, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	var broker *capability.Broker
	var controller *semantic.SemanticPreDispatch
	if condition == "semantic_pre_dispatch" {
		config.Mechanisms.SemanticPreDispatch = true
		config.Mechanisms.StagedObservation = true
		decision := semantic.CanPreissue(verified, plan, siteID, semantic.PreissueContext{
			StreamEpoch: fmt.Sprintf("stream-%d", index), WorkflowEpoch: "workflow-experiment",
			FreshnessEpoch: "plan-experiment", ExpiryEpoch: "expiry-experiment", PrivacyPartition: "private-experiment",
			ParentLineageSHA256: digest([]byte("parent-lineage")), BudgetReservationSHA256: digest([]byte(fmt.Sprintf("reservation-%d", index))), RemainingPhysicalReads: 1,
		})
		call, ok := decision.QualifiedCall()
		if !ok {
			return trial{}, errors.New("exact trial call was not qualified")
		}
		budget, err := semantic.NewPreDispatchBudget(1)
		if err != nil {
			return trial{}, err
		}
		controller, err = semantic.NewSemanticPreDispatch(call, plan, budget)
		if err != nil {
			return trial{}, err
		}
	}
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		var err error
		brokerConfig := capability.Config{RunIdentity: fmt.Sprintf("experiment-%s-%d", condition, index), Plan: plan}
		if controller != nil {
			brokerConfig.StagedClaimer = controller
			brokerConfig.SemanticPreDispatch = true
		}
		broker, err = capability.NewBroker(brokerConfig)
		return broker, err
	}}
	runner, err := factory.New(context.Background(), artifact, config)
	if err != nil {
		return trial{}, err
	}
	defer runner.Close(context.Background())
	request, _ := json.Marshal(runtimeconfig.RunRequest{RunID: fmt.Sprintf("experiment-%s-%d", condition, index), Code: source, Inputs: json.RawMessage(`{}`)})
	started := time.Now()
	var response []byte
	if controller == nil {
		response, err = runner.Run(context.Background(), request, plan.PythonPrelude())
	} else {
		response, err = semantic.ExecuteSemanticPreDispatch(context.Background(), controller, goroutineLauncher{}, func() ([]byte, error) {
			return runner.Run(context.Background(), request, plan.PythonPrelude())
		})
	}
	duration := time.Since(started).Microseconds()
	if err != nil {
		return trial{}, err
	}
	var envelope runtimeconfig.RunResponse
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
		return trial{}, errors.New("Guest response was not successful")
	}
	row := trial{Condition: condition, DurationMicros: duration, ResultSHA256: digest(envelope.Result)}
	if broker != nil {
		row.LogicalCalls = broker.CallCount()
	}
	if controller != nil {
		snapshot := controller.Snapshot()
		row.PhysicalIssues, row.PhysicalStarts, row.PhysicalFinishes = snapshot.PhysicalIssues, snapshot.PhysicalStarts, snapshot.PhysicalFinishes
		row.RejectedClaims = snapshot.RejectedClaims
	}
	return row, nil
}

type goroutineLauncher struct{}

func (goroutineLauncher) Launch(task func()) {
	go task()
}

func median(rows []trial, condition string) int64 {
	values := make([]int64, 0)
	for _, row := range rows {
		if row.Condition == condition {
			values = append(values, row.DurationMicros)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func sealReport(value report) string {
	value.ContentSHA256 = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return digest(encoded)
}

func validateReport(value report, expected reportProvenance) error {
	if value.SchemaVersion != reportSchema || value.TrialsPerCondition != 5 ||
		len(value.Trials) != value.TrialsPerCondition*2 || value.PhysicalDelayMicros <= 0 ||
		!validSHA256(value.ArtifactSHA256) || !validSHA256(value.SourceSHA256) || !validSHA256(value.CapabilityPlanSHA256) ||
		value.ArtifactSHA256 != expected.ArtifactSHA256 || value.SourceSHA256 != expected.SourceSHA256 ||
		value.CapabilityPlanSHA256 != expected.CapabilityPlanSHA256 ||
		!value.EquivalentResults || !value.NoDuplicatePhysicalCall ||
		value.ContentSHA256 == "" || value.ContentSHA256 != sealReport(value) {
		return errors.New("invalid semantic pre-dispatch report envelope")
	}
	expectedResult := digest([]byte(`"fixture"`))
	for index, row := range value.Trials {
		expectedCondition := "baseline"
		if index%2 == 1 {
			expectedCondition = "semantic_pre_dispatch"
		}
		if row.Condition != expectedCondition || row.DurationMicros <= 0 || row.LogicalCalls != 1 ||
			row.PhysicalCalls != 1 || row.ResultSHA256 != expectedResult {
			return fmt.Errorf("invalid trial %d", index)
		}
		if row.Condition == "baseline" {
			if row.PhysicalIssues != 0 || row.PhysicalStarts != 0 || row.PhysicalFinishes != 0 || row.RejectedClaims != 0 {
				return fmt.Errorf("invalid baseline observation %d", index)
			}
		} else if row.PhysicalIssues != 1 || row.PhysicalStarts != 1 || row.PhysicalFinishes != 1 || row.RejectedClaims != 0 {
			return fmt.Errorf("invalid optimized observation %d", index)
		}
	}
	if value.BaselineMedianMicros != median(value.Trials, "baseline") ||
		value.OptimizedMedianMicros != median(value.Trials, "semantic_pre_dispatch") ||
		value.MedianSavingsMicros != value.BaselineMedianMicros-value.OptimizedMedianMicros {
		return errors.New("invalid latency aggregates")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
