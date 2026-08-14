package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

func TestRealGuestSemanticPreDispatchClaimsAtUnchangedPythonCall(t *testing.T) {
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
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('7'),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var physicalCalls atomic.Uint32
	physicalStarted := make(chan struct{})
	releasePhysical := make(chan struct{})
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"semantic-pre-dispatch-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.semantic-pre-dispatch-e2e.v0",
		Description: "Read one exact E2E fixture resource.", EffectClass: capability.EffectExternalRead,
		Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.semantic-pre-dispatch-e2e.v0",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}, ResultField: "value"},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "sources", Argument: "key"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		},
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if physicalCalls.Add(1) == 1 {
			close(physicalStarted)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releasePhysical:
			return json.RawMessage(`{"value":"predispatched"}`), nil
		}
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}

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
	source := "result = sources.read('profile')\n"
	analysisRequest, err := semantic.NewRequest(source, bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), analysisRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	decision := semantic.CanPreissue(verified, plan, analysis.CallSites[0].ID, semantic.PreissueContext{
		StreamEpoch: "stream-e2e", WorkflowEpoch: "workflow-e2e", FreshnessEpoch: "plan-e2e", ExpiryEpoch: "expiry-e2e",
		PrivacyPartition: "private-e2e", ParentLineageSHA256: semanticTestDigest('6'),
		BudgetReservationSHA256: semanticTestDigest('5'), RemainingPhysicalReads: 1,
	})
	qualified, ok := decision.QualifiedCall()
	if !ok {
		t.Fatalf("decision=%+v", decision)
	}
	budget, err := semantic.NewPreDispatchBudget(1)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := semantic.NewSemanticPreDispatch(qualified, plan, budget)
	if err != nil {
		t.Fatal(err)
	}
	executionConfig := runtimeconfig.DefaultRunConfig()
	executionConfig.ExecutionProfile = &profile
	executionConfig.Mechanisms = runtimeconfig.MechanismSet{
		SemanticAnalysis: true, SemanticPreDispatch: true, StagedObservation: true,
	}
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "semantic-pre-dispatch-e2e", Plan: plan, StagedClaimer: controller})
	}}
	executionRunner, err := factory.New(context.Background(), artifact, executionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer executionRunner.Close(context.Background())
	runRequest, _ := json.Marshal(runtimeconfig.RunRequest{RunID: "semantic-pre-dispatch-e2e", Code: source, Inputs: json.RawMessage(`{}`)})
	response, err := semantic.ExecuteSemanticPreDispatch(context.Background(), controller, explicitGoroutineLauncher{}, func() ([]byte, error) {
		<-physicalStarted
		if physicalCalls.Load() != 1 {
			t.Fatalf("physical calls before Guest=%d", physicalCalls.Load())
		}
		close(releasePhysical)
		return executionRunner.Run(context.Background(), runRequest, plan.PythonPrelude())
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope runtimeconfig.RunResponse
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" || string(envelope.Result) != `"predispatched"` {
		t.Fatalf("response=%s envelope=%+v err=%v", response, envelope, err)
	}
	if physicalCalls.Load() != 1 {
		t.Fatalf("dynamic boundary issued duplicate physical call: %d", physicalCalls.Load())
	}
	if snapshot := controller.Snapshot(); snapshot.PhysicalIssues != 1 || snapshot.PhysicalStarts != 1 || snapshot.PhysicalFinishes != 1 || snapshot.LogicalClaims != 1 || snapshot.RejectedClaims != 0 || snapshot.Disposition != streaming.ObservationConsumed {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

type explicitGoroutineLauncher struct{}

func (explicitGoroutineLauncher) Launch(task func()) error {
	go task()
	return nil
}
