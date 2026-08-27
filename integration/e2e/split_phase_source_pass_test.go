package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

func TestRealGuestUnifiedSplitPhasePreissueThenRuntimeIssue(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('8'),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var getCalls atomic.Uint32
	var priceCalls atomic.Uint32
	plan := unifiedSplitPhasePlan(t,
		capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			getCalls.Add(1)
			var arguments struct {
				Key string `json:"key"`
			}
			if json.Unmarshal(raw, &arguments) != nil || arguments.Key != "alpha" {
				return nil, capability.ErrInvalidTool
			}
			return json.RawMessage(`{"value":5}`), nil
		}),
		capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			priceCalls.Add(1)
			var arguments struct {
				Value int `json:"value"`
			}
			if json.Unmarshal(raw, &arguments) != nil {
				return nil, capability.ErrInvalidTool
			}
			return json.Marshal(map[string]int{"quote": arguments.Value * 10})
		}),
	)
	source := "a = tools.get(\"alpha\")\nx = a + 1\nindependent = 3 * 4\nb = tools.price(x)\nresult = [b, independent]\n"

	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analysisRunner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}
	analysisRequest, err := semantic.NewRequest(source, bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), analysisRequest)
	if closeErr := analysisRunner.Close(context.Background()); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 || analysis.CallSites[0].Capability != "tools.get" {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	preissueContext := semantic.PreissueContext{
		StreamEpoch: "unified-stream", WorkflowEpoch: "unified-workflow", FreshnessEpoch: "unified-plan", ExpiryEpoch: "unified-expiry",
		PrivacyPartition: "unified-private", ParentLineageSHA256: semanticTestDigest('9'),
		BudgetReservationSHA256: semanticTestDigest('a'), RemainingPhysicalReads: 1,
	}
	qualified, ok := semantic.CanPreissue(verified, plan, analysis.CallSites[0].ID, preissueContext).QualifiedCall()
	if !ok {
		t.Fatal("source-time call was not positively admitted")
	}
	table, err := capability.NewSplitPhaseTable(plan, capability.SplitPhaseLimits{MaxCalls: 2, MaxCostUnits: 2, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := semantic.IssueQualifiedSplitPhase(context.Background(), table, qualified); err != nil {
		t.Fatal(err)
	}

	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	executionConfig := runtimeconfig.DefaultRunConfig()
	executionConfig.Timeout = 90 * time.Second
	executionConfig.ExecutionProfile = &profile
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "unified-split-phase", Plan: plan})
		if createErr == nil {
			createErr = created.AttachStagedClaimer(table)
		}
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, executionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	request, _ := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "unified-split-phase", Code: source, Inputs: json.RawMessage(`{}`)})
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 2)
	defer closeAnalysis()
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.SplitPhaseCapabilityCallsName, session, trustedSemanticRunner(t, runner), request,
		plan.PythonPrelude(), passplugin.CapabilityProjections(plan),
	)
	if err != nil || !execution.Applied {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(result) != `[60,12]` {
		t.Fatalf("result=%s err=%v payload=%s derived=%s", result, err, execution.Payload, execution.Patch.DerivedSource)
	}
	if getCalls.Load() != 1 || priceCalls.Load() != 1 || broker.CallCount() != 2 {
		t.Fatalf("get=%d price=%d logical=%d", getCalls.Load(), priceCalls.Load(), broker.CallCount())
	}
	evidence := trustedSemanticRunner(t, runner).SplitPhaseEvidence()
	if evidence.Submitted != 2 || evidence.Reused != 1 || evidence.Consumed != 2 || evidence.Discarded != 0 {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestRealGuestUnifiedSplitPhaseOverlapsIndependentCallsAndKeepsLogicalReceipts(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	source := "first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "split-phase-overlap-e2e", Code: source, Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	var active atomic.Int32
	var maxActive atomic.Int32
	var physical atomic.Uint32
	handler := capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		physical.Add(1)
		current := active.Add(1)
		for observed := maxActive.Load(); current > observed && !maxActive.CompareAndSwap(observed, current); observed = maxActive.Load() {
		}
		select {
		case <-ctx.Done():
			active.Add(-1)
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		active.Add(-1)
		return json.Marshal(map[string]string{"body": arguments.Path + "-body"})
	})
	plan := splitPhaseE2EPlan(t, 2, handler)
	prelude := plan.PythonPrelude()
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}

	var brokersMu sync.Mutex
	brokers := make([]*capability.Broker, 0, 2)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		broker, createErr := capability.NewBroker(capability.Config{RunIdentity: "split-phase-e2e", Plan: plan})
		if createErr == nil {
			brokersMu.Lock()
			brokers = append(brokers, broker)
			brokersMu.Unlock()
		}
		return broker, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)

	baselineStarted := time.Now()
	baseline, err := runner.Run(context.Background(), request, prelude)
	baselineDuration := time.Since(baselineStarted)
	if err != nil {
		t.Fatal(err)
	}
	baselineResult, err := decodeSuccessfulGuestResult(baseline)
	if err != nil || string(baselineResult) != `["alpha-body","beta-body"]` || maxActive.Load() != 1 {
		t.Fatalf("baseline result=%s max_active=%d err=%v payload=%s", baselineResult, maxActive.Load(), err, baseline)
	}
	maxActive.Store(0)

	treatmentStarted := time.Now()
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 4)
	defer closeAnalysis()
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.SplitPhaseCapabilityCallsName, session, engine, request, prelude,
		passplugin.CapabilityProjections(plan),
	)
	treatmentDuration := time.Since(treatmentStarted)
	if err != nil || !execution.Applied || execution.Patch.ReplacementCount != 2 {
		t.Fatalf("execution=%+v err=%v", execution, err)
	}
	treatmentResult, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil || string(treatmentResult) != string(baselineResult) || maxActive.Load() != 2 || physical.Load() != 4 {
		t.Fatalf("treatment result=%s max_active=%d physical=%d err=%v payload=%s", treatmentResult, maxActive.Load(), physical.Load(), err, execution.Payload)
	}

	brokersMu.Lock()
	captured := append([]*capability.Broker(nil), brokers...)
	brokersMu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("brokers=%d", len(captured))
	}
	for index, broker := range captured {
		receipts := broker.SnapshotReceipts()
		if broker.CallCount() != 2 || len(receipts) != 2 ||
			receipts[0].Capability != "sources.read" || receipts[1].Capability != "sources.read" {
			t.Fatalf("broker[%d] calls=%d receipts=%#v", index, broker.CallCount(), receipts)
		}
		if index == 1 && (receipts[0].CallID != "split-s1c8-e1c29-1" || receipts[1].CallID != "split-s2c9-e2c29-1") {
			t.Fatalf("treatment receipts=%#v", receipts)
		}
	}
	physicalEvidence := engine.SplitPhaseEvidence()
	if physicalEvidence.Submitted != 2 || physicalEvidence.PhysicalStarts != 2 || physicalEvidence.PhysicalFinishes != 2 || physicalEvidence.LogicalClaims != 2 || physicalEvidence.Consumed != 2 || physicalEvidence.MaximumConcurrent != 2 {
		t.Fatalf("physical evidence=%+v", physicalEvidence)
	}
	t.Logf("split-phase matched durations: baseline_ns=%d treatment_ns=%d", baselineDuration.Nanoseconds(), treatmentDuration.Nanoseconds())
}

func TestRealGuestUnifiedSplitPhasePreservesDynamicActivation(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, source, inputs string
		submitted, minimum   uint32
		applied              bool
	}{
		{"branch-not-taken", "if inputs[\"take\"]:\n    first = sources.read(\"alpha\")\n    second = sources.read(\"beta\")\n    result = [first, second]\nelse:\n    result = []\n", `{"take":false}`, 0, 0, true},
		{"branch-taken", "if inputs[\"take\"]:\n    first = sources.read(\"alpha\")\n    second = sources.read(\"beta\")\n    result = [first, second]\nelse:\n    result = []\n", `{"take":true}`, 2, 1, true},
		{"zero-loop", "result = []\nfor item in inputs[\"items\"]:\n    first = sources.read(\"alpha\")\n    result.append(first)\n", `{"items":[]}`, 0, 0, true},
		{"two-loop-iterations", "result = []\nfor item in inputs[\"items\"]:\n    first = sources.read(\"alpha\")\n    result.append(first)\n", `{"items":[1,2]}`, 2, 1, true},
		{"one-line-branch-fallback", "if inputs[\"take\"]: first = sources.read(\"alpha\"); result = first\nelse: result = []\n", `{"take":false}`, 0, 0, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := capability.HandlerFunc(func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
				var request struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(arguments, &request) != nil {
					return nil, capability.ErrInvalidTool
				}
				return json.Marshal(map[string]string{"body": request.Path + "-body"})
			})
			plan := splitPhaseE2EPlan(t, 2, handler)
			plugins := unifiedPassCatalog(t)
			plugins, enableErr := plugins.Enable(sourcepatch.SplitPhaseCapabilityCallsName)
			if enableErr != nil {
				t.Fatal(enableErr)
			}
			var broker *capability.Broker
			factory := func(context.Context) (*capability.Broker, error) {
				created, createErr := capability.NewBroker(capability.Config{RunIdentity: "dynamic-" + testCase.name, Plan: plan})
				broker = created
				return created, createErr
			}
			config := runtimeconfig.DefaultRunConfig()
			config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
			runner, createErr := (wazeroengine.Factory{Passes: plugins, BrokerFactory: factory}).New(context.Background(), artifact, config)
			if createErr != nil {
				t.Fatal(createErr)
			}
			defer runner.Close(context.Background())
			engine := trustedSemanticRunner(t, runner)
			session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 2)
			defer closeAnalysis()
			request := runtimeconfig.RunRequest{RunID: "dynamic-" + strings.ReplaceAll(testCase.name, " ", "-"), Code: testCase.source, Inputs: json.RawMessage(testCase.inputs)}
			raw, _ := runtimeconfig.EncodeRunRequest(request)
			execution, runErr := plugins.ExecuteCapabilityHostScheduled(
				context.Background(), sourcepatch.SplitPhaseCapabilityCallsName, session, engine, raw,
				plan.PythonPrelude(), passplugin.CapabilityProjections(plan),
			)
			if runErr != nil {
				t.Fatal(runErr)
			}
			response, decodeErr := runtimeconfig.DecodeAndValidateRunResponse(request, execution.Payload)
			if decodeErr != nil || response.Status != runtimeconfig.RunResponseOK {
				t.Fatalf("response=%s decoded=%+v err=%v", execution.Payload, response, decodeErr)
			}
			if execution.Applied != testCase.applied {
				t.Fatalf("applied=%t want=%t patch=%+v", execution.Applied, testCase.applied, execution.Patch)
			}
			evidence := engine.SplitPhaseEvidence()
			if evidence.Submitted != testCase.submitted || evidence.LogicalClaims != testCase.submitted || evidence.MaximumConcurrent < testCase.minimum || evidence.MaximumConcurrent > testCase.submitted || broker.CallCount() != testCase.submitted {
				t.Fatalf("evidence=%+v calls=%d", evidence, broker.CallCount())
			}
		})
	}
}

func TestRealGuestUnifiedSplitPhaseFailureDiscardsLaterPhysicalReadWithoutLogicalReceipt(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	source := "first = sources.read(\"alpha\")\nsecond = sources.read(\"beta\")\nresult = [first, second]\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "split-phase-failure-e2e", Code: source, Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var started atomic.Uint32
	release := make(chan struct{})
	var releaseOnce sync.Once
	handler := capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var arguments struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &arguments) != nil {
			return nil, capability.ErrInvalidTool
		}
		if started.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		if arguments.Path == "alpha" {
			return nil, errors.New("alpha failed")
		}
		return json.RawMessage(`{"body":"beta-body"}`), nil
	})
	plan := splitPhaseE2EPlan(t, 2, handler)
	plugins := unifiedPassCatalog(t)
	plugins, err = plugins.Enable(sourcepatch.SplitPhaseCapabilityCallsName)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, StagedObservation: true}
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: "split-phase-failure", Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	engine := trustedSemanticRunner(t, runner)
	session, closeAnalysis := splitPhaseAnalysisSession(t, artifact, 2)
	defer closeAnalysis()
	execution, err := plugins.ExecuteCapabilityHostScheduled(
		context.Background(), sourcepatch.SplitPhaseCapabilityCallsName, session, engine, request,
		plan.PythonPrelude(), passplugin.CapabilityProjections(plan),
	)
	if err != nil || !execution.Applied || started.Load() != 2 {
		t.Fatalf("execution=%+v started=%d err=%v", execution, started.Load(), err)
	}
	var response struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(execution.Payload, &response) != nil || response.Status != "error" || response.Error == nil || response.Error.Code != "python_exception" {
		t.Fatalf("response=%+v payload=%s", response, execution.Payload)
	}
	receipts := broker.SnapshotReceipts()
	if broker.CallCount() != 1 || len(receipts) != 1 || receipts[0].CallID != "split-s1c8-e1c29-1" || receipts[0].Outcome != "error" {
		t.Fatalf("calls=%d receipts=%#v", broker.CallCount(), receipts)
	}
	physicalEvidence := engine.SplitPhaseEvidence()
	if physicalEvidence.Submitted != 2 || physicalEvidence.LogicalClaims != 1 || physicalEvidence.Consumed != 1 || physicalEvidence.Discarded != 1 {
		t.Fatalf("physical evidence=%+v", physicalEvidence)
	}
}

func splitPhaseAnalysisSession(t *testing.T, artifact []byte, maxRequests uint32) (sourcepatch.Transformer, func()) {
	t.Helper()
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	session, err := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: maxRequests, MaxCumulativeRequestBytes: 1 << 20, MaxDuration: 60 * time.Second,
	})
	if err != nil {
		_ = runner.Close(context.Background())
		t.Fatal(err)
	}
	return session, func() {
		if closeErr := session.Close(context.Background()); closeErr != nil {
			t.Fatalf("close analysis session: %v", closeErr)
		}
		if closeErr := runner.Close(context.Background()); closeErr != nil {
			t.Fatalf("close analysis runner: %v", closeErr)
		}
	}
}

func unifiedSplitPhasePlan(t *testing.T, getHandler, priceHandler capability.Handler) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"unified-split-phase-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	specs := []struct {
		spec    capability.Spec
		handler capability.Handler
	}{
		{spec: capability.Spec{
			Name: "tools.get", Version: "pysolate.tools.get.split-phase-e2e.v1", Description: "Get one immutable fixture value.",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "tools-get-split-phase-e2e.v1",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
			Python:       &capability.PythonProjection{Module: "tools", Method: "get", Arguments: []string{"key"}, ResultField: "value"},
			ReadOnly:     true, Idempotent: true,
			PreDispatch: &capability.PreDispatchContract{
				Resource: capability.ResourceReference{Namespace: "tools-get", Argument: "key"}, Freshness: capability.FreshnessPlanEpoch,
				Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
				Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1024, CostUnits: 1,
			},
		}, handler: getHandler},
		{spec: capability.Spec{
			Name: "tools.price", Version: "pysolate.tools.price.split-phase-e2e.v1", Description: "Price one immutable fixture value.",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "tools-price-split-phase-e2e.v1",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"quote":{"type":"integer"}},"required":["quote"],"additionalProperties":false}`),
			Python:       &capability.PythonProjection{Module: "tools", Method: "price", Arguments: []string{"value"}, ResultField: "quote"},
			ReadOnly:     true, Idempotent: true,
			PreDispatch: &capability.PreDispatchContract{
				Resource: capability.ResourceReference{Namespace: "tools-price", Argument: "value"}, Freshness: capability.FreshnessPlanEpoch,
				Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
				Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1024, CostUnits: 1,
			},
		}, handler: priceHandler},
	}
	for _, item := range specs {
		if err := registry.Register(item.spec, grant, item.handler); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func splitPhaseE2EPlan(t *testing.T, maxCalls uint32, handler capability.Handler) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"split-phase-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.sources.read.split-phase-e2e.v1", Description: "Read one immutable source fixture.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "split-phase-e2e-handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":256}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string","maxLength":4096}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}, ResultField: "body"},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "sources", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
			Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 4096, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func unifiedPassCatalog(t *testing.T) *passplugin.Registry {
	t.Helper()
	registry, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
