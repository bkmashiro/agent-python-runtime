package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestRealGuestSharedLegalityQualifiesOnlyExactMustReachCall(t *testing.T) {
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
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"semantic-legality-e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.semantic-legality-e2e.v0",
		Description: "Read one exact E2E fixture resource.", EffectClass: capability.EffectExternalRead,
		Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.semantic-legality-e2e.v0",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}, ResultField: "value"},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "sources", Argument: "key"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		},
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"fixture"}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}
	request, err := semantic.NewRequest("result = sources.read('profile')\n", bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), runner, request)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	decision := semantic.CanPreissue(verified, plan, analysis.CallSites[0].ID, semantic.PreissueContext{
		StreamEpoch: "stream-1", WorkflowEpoch: "workflow-1", FreshnessEpoch: "plan-1", ExpiryEpoch: "expiry-1",
		PrivacyPartition: "private-1", ParentLineageSHA256: semanticTestDigest('6'), RemainingPhysicalReads: 1,
	})
	call, qualified := decision.QualifiedCall()
	if !decision.Allowed() || !qualified || call.Capability() != spec.Name || call.ResourceSHA256() == "" {
		t.Fatalf("decision=%+v qualified=%v call=%+v", decision, qualified, call)
	}

	conditional, err := semantic.NewRequest("if inputs['flag']:\n    result = sources.read('profile')\n", bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	conditionalVerified, err := semantic.AnalyzeVerified(context.Background(), runner, conditional)
	if err != nil {
		t.Fatal(err)
	}
	conditionalAnalysis, err := conditionalVerified.Analysis()
	if err != nil || len(conditionalAnalysis.CallSites) != 0 {
		t.Fatalf("conditional=%+v err=%v", conditionalAnalysis, err)
	}
}
