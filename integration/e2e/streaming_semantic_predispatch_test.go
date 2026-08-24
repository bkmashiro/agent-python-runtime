package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestStreamingPrefixesPreDispatchThreeReadsBeforeSourceFeedCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(wasm)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("streaming-prefix-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var timingMu sync.Mutex
	physicalStarts := map[string]time.Time{}
	physicalCalls := map[string]int{}

	plan := streamingPreDispatchPlan(t, capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		var input struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(arguments, &input) != nil {
			return nil, fmt.Errorf("invalid test arguments")
		}
		timingMu.Lock()
		physicalStarts[input.Key] = time.Now()
		physicalCalls[input.Key]++
		timingMu.Unlock()

		timer := time.NewTimer(180 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		return json.RawMessage(`{"value":"` + input.Key + `"}`), nil
	}))

	analyzerConfig := runtimeconfig.DefaultRunConfig()
	analyzerConfig.ExecutionProfile = &profile
	analyzerConfig.Mechanisms.SemanticAnalysis = true
	analyzer, err := wazeroengine.New(ctx, wasm, analyzerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close(context.Background())
	properties := analyzer.Properties()
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: properties.ExecutionProfileBindingSHA256,
		ImportClosureSHA256: testDigest("streaming-prefix-imports"), CapabilityPlanSHA256: plan.Identity(),
	}

	budget, err := semantic.NewPreDispatchBudget(3)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := semantic.NewStreamingSemanticPreDispatch(plan, budget, e2eGoroutineLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := semantic.NewStreamingPrefixAdmission(plan, controller, semantic.PreissueContext{
		StreamEpoch: "stream-day-trip-1", WorkflowEpoch: "workflow-day-trip-1", FreshnessEpoch: "plan-epoch-1", ExpiryEpoch: "expiry-1",
		PrivacyPartition: "private-day-trip-1", ParentLineageSHA256: testDigest("parent-lineage"),
	})
	if err != nil {
		t.Fatal(err)
	}

	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}

	chunks := []string{
		`weather = sources.read("weather")` + "\n",
		`rail = sources.read("rail")` + "\n",
		`attraction = sources.read("attractions")` + "\n",
		`result = {"weather": weather, "rail": rail, "attraction": attraction}` + "\n",
	}
	sourceChunks := make(chan string, len(chunks))
	var sourceFeedComplete time.Time
	var sourceSealed time.Time
	go func() {
		defer close(sourceChunks)
		for index, chunk := range chunks {
			select {
			case sourceChunks <- chunk:
			case <-ctx.Done():
				return
			}
			if index < len(chunks)-1 {
				if index == 2 {
					time.Sleep(12 * time.Second)
				} else {
					time.Sleep(25 * time.Millisecond)
				}
			}
		}
		timingMu.Lock()
		sourceFeedComplete = time.Now()
		timingMu.Unlock()
	}()
	generated, err := semantic.GenerateVerifiedSourceWithPreDispatch(ctx, semantic.VerifiedSourceGenerationConfig{
		Analyzer: analyzer, Plan: plan, Bindings: bindings, Admission: admission, SourceChunks: sourceChunks,
		Observe: func(event semantic.VerifiedSourceGenerationEvent) {
			if event.Phase == "source_sealed" {
				timingMu.Lock()
				sourceSealed = time.Now()
				timingMu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	passes := unifiedPassCatalog(t)
	passes, err = passes.Enable(passregistration.SemanticPreDispatch)
	if err != nil {
		t.Fatal(err)
	}
	executionConfig := runtimeconfig.DefaultRunConfig()
	executionConfig.ExecutionProfile = &profile
	executionConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
	runner, err := (wazeroengine.Factory{
		Passes: passes, WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: "streaming-day-trip",
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{
				RunIdentity: "streaming-day-trip", Plan: plan, StagedClaimer: controller, SemanticPreDispatch: true,
			})
		},
	}).New(ctx, wasm, executionConfig)
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest, _ := json.Marshal(map[string]any{
		"run_id": "streaming-day-trip", "code": generated.Source() + "# mismatch\n", "inputs": map[string]any{},
	})
	if _, err := semantic.ExecuteGeneratedSource(ctx, runner, attempt, wrongRequest, plan.StreamingPythonPrelude(), generated); !errors.Is(err, semantic.ErrAnalysisBinding) {
		t.Fatalf("mismatched final source error=%v", err)
	}
	request, _ := json.Marshal(map[string]any{"run_id": "streaming-day-trip", "code": generated.Source(), "inputs": map[string]any{}})
	guestStarted := time.Now()
	outcome, err := semantic.ExecuteGeneratedSource(ctx, runner, attempt, request, plan.StreamingPythonPrelude(), generated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := semantic.ExecuteGeneratedSource(ctx, runner, attempt, request, plan.StreamingPythonPrelude(), generated); !errors.Is(err, semantic.ErrPreDispatchInvalid) {
		t.Fatalf("second execution error=%v", err)
	}
	if guestStarted.Before(sourceSealed) || sourceSealed.Before(sourceFeedComplete) {
		t.Fatalf("fresh Guest lifecycle: feed=%v sealed=%v guest=%v", sourceFeedComplete, sourceSealed, guestStarted)
	}

	timingMu.Lock()
	defer timingMu.Unlock()
	for _, key := range []string{"weather", "rail", "attractions"} {
		if physicalCalls[key] != 1 || physicalStarts[key].IsZero() || !physicalStarts[key].Before(sourceFeedComplete) {
			t.Fatalf("%s calls=%d start=%v source_feed_complete=%v", key, physicalCalls[key], physicalStarts[key], sourceFeedComplete)
		}
	}
	controllerSnapshot := controller.Snapshot()
	if controllerSnapshot.PhysicalIssues != 3 || controllerSnapshot.PhysicalStarts != 3 || controllerSnapshot.PhysicalFinishes != 3 || controllerSnapshot.LogicalClaims != 3 || controllerSnapshot.Consumed != 3 {
		t.Fatalf("controller snapshot=%+v", controllerSnapshot)
	}
	admissionSnapshot := admission.Snapshot()
	if admissionSnapshot.PrefixCount != 4 || admissionSnapshot.QualifiedCallCount != 3 || admissionSnapshot.RejectedCallCount != 0 {
		t.Fatalf("admission snapshot=%+v", admissionSnapshot)
	}
	var response struct {
		Status string `json:"status"`
		Result map[string]struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(outcome.Response, &response) != nil || response.Status != "ok" ||
		response.Result["weather"].Value != "weather" || response.Result["rail"].Value != "rail" || response.Result["attraction"].Value != "attractions" {
		t.Fatalf("response=%s", outcome.Response)
	}
}

type e2eGoroutineLauncher struct{}

func (e2eGoroutineLauncher) Launch(task func()) { go task() }

func streamingPreDispatchPlan(t *testing.T, handler capability.Handler) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"fixture":"streaming-day-trip","network":false}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "sources.read.v1", Description: "Read one deterministic travel source.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "sources-read-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","enum":["weather","rail","attractions"]}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}}, ReadOnly: true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "source", Argument: "key"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition,
			Privacy:   capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
			MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func testDigestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}
