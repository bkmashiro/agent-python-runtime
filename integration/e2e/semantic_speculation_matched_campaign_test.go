package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestExactGuestMatchedPureLocalCampaignSealsThreeAchievedLanes(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	imports, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "import-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA, manifestSHA := testDigestBytes(artifact), testDigestBytes(manifest)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA,
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseConfig := runtimeconfig.DefaultRunConfig()
	baseConfig.ExecutionProfile = &profile
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(baseConfig)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Uint32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	brokerFactory := func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: "matched-pure-local", Plan: plan})
	}
	bindings := semanticspeculation.TrialBindings{
		ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA, ImportInventorySHA256: testDigestBytes(imports),
		ExecutionProfileSHA256: profileSHA, CapabilityPlanSHA256: plan.Identity(), PrivacySHA256: testDigest("exact-partition-forbidden-coalescing-v1"),
	}
	factory := func(treatment string, trial uint32) (semanticspeculation.ScheduledTreatment, error) {
		runID := fmt.Sprintf("matched-lane-%s-%d", treatment, trial)
		switch treatment {
		case "serial_whole_file":
			config := baseConfig
			config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
			return semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
				Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory,
				RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
				ProviderObservation: func() semanticspeculation.ProviderObservation { return semanticspeculation.ProviderObservation{} },
			})
		case "eager_style_gate":
			config := baseConfig
			config.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
			return semanticspeculation.NewEagerGuestTreatment(semanticspeculation.EagerGuestTreatmentConfig{
				Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory, AllowedImportRoots: []string{"json"},
				RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
				ProviderObservation: func() semanticspeculation.ProviderObservation { return semanticspeculation.ProviderObservation{} },
			})
		case "semantic_pre_dispatch":
			return semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
				Artifact: artifact, RunConfig: baseConfig, Plan: plan, ImportClosureSHA256: testDigestBytes(imports), PhysicalReadBudget: 1,
				RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
			})
		default:
			return nil, fmt.Errorf("unexpected treatment %q", treatment)
		}
	}
	result, err := semanticspeculation.RunMatchedCaseCampaign(context.Background(), semanticspeculation.Phase3SyntheticCases()[5], 1, bindings, factory,
		func(serial semanticspeculation.TrialRecord) (uint64, error) {
			return serial.EndedNanos - serial.StartedNanos, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 3 || calls.Load() != 0 || result.Aggregate.CaseID != "pure_local" || !result.Aggregate.OracleExcludedFromAchievedSpeedup {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	evidence, err := semanticspeculation.SealMatchedCaseEvidence(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := semanticspeculation.EncodeMatchedCaseEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := semanticspeculation.DecodeMatchedCaseEvidence(encoded)
	if err != nil || decoded.Identity != evidence.Identity || decoded.ProductionGeneralization || !decoded.OracleAnalysisOnly {
		t.Fatalf("evidence=%+v decoded=%+v err=%v", evidence, decoded, err)
	}
}
