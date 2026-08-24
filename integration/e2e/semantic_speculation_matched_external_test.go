package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestExactGuestMatchedExternalReadCampaignPersistsSeededEvidence(t *testing.T) {
	runExactGuestMatchedExternalCase(t, 2, "external_read_valid_suffix")
}

func TestExactGuestMatchedUnknownWrapperAccountsLiveFallback(t *testing.T) {
	runExactGuestMatchedExternalCase(t, 6, "unknown_wrapper")
}

func runExactGuestMatchedExternalCase(t *testing.T, caseIndex int, caseID string) {
	t.Helper()
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
	artifactSHA := testDigestBytes(artifact)
	manifestSHA := testDigestBytes(manifest)
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
	identityPlan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	bindings := semanticspeculation.TrialBindings{
		ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA, ImportInventorySHA256: testDigestBytes(imports),
		ExecutionProfileSHA256: profileSHA, CapabilityPlanSHA256: identityPlan.Identity(), PrivacySHA256: testDigest("exact-partition-forbidden-coalescing-v1"),
	}
	var physicalTotal atomic.Uint32
	var semanticTreatment *semanticspeculation.SemanticPreDispatchTreatment
	factory := func(treatment string, trial uint32) (semanticspeculation.ScheduledTreatment, error) {
		var physical atomic.Uint32
		handler := capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			physical.Add(1)
			physicalTotal.Add(1)
			return json.RawMessage(`{"value":"weather"}`), nil
		})
		plan := eagerComparatorCapabilityPlan(t, handler)
		runID := fmt.Sprintf("matched-external-%s-%d", treatment, trial)
		brokerFactory := func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		}
		observation := func() semanticspeculation.ProviderObservation {
			calls := physical.Load()
			return semanticspeculation.ProviderObservation{
				Attempts: calls, ResultBytes: uint64(calls) * uint64(len(`{"value":"weather"}`)), CostUnits: uint64(calls),
				Dispositions: semanticspeculation.PhysicalDispositions{Consumed: calls},
			}
		}
		switch treatment {
		case "serial_whole_file":
			config := baseConfig
			config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
			return semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{
				Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory,
				ProviderObservation: observation, RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
			})
		case "eager_style_gate":
			config := baseConfig
			config.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
			return semanticspeculation.NewEagerGuestTreatment(semanticspeculation.EagerGuestTreatmentConfig{
				Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory, AllowedImportRoots: []string{"json"},
				ProviderObservation: observation, RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
			})
		case "semantic_pre_dispatch":
			created, createErr := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
				Artifact: artifact, RunConfig: baseConfig, Plan: plan, ProviderObservation: observation,
				ImportClosureSHA256: testDigestBytes(imports), PhysicalReadBudget: 1,
				RunID: runID, WorkspaceRoot: t.TempDir(), WorkspaceOwner: runID,
			})
			semanticTreatment = created
			return created, createErr
		default:
			return nil, fmt.Errorf("unexpected treatment %q", treatment)
		}
	}
	result, err := semanticspeculation.RunMatchedCaseCampaign(context.Background(), semanticspeculation.Phase3SyntheticCases()[caseIndex], 1, bindings, factory,
		func(serial semanticspeculation.TrialRecord) (uint64, error) {
			elapsed := serial.EndedNanos - serial.StartedNanos
			latency := uint64(250 * time.Millisecond)
			if elapsed <= latency {
				return 0, fmt.Errorf("serial elapsed %d does not cover frozen latency", elapsed)
			}
			return elapsed - latency, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 3 || physicalTotal.Load() != 3 || result.Aggregate.CaseID != caseID || !result.Aggregate.OracleExcludedFromAchievedSpeedup {
		t.Fatalf("result=%+v physical_total=%d", result, physicalTotal.Load())
	}
	for _, record := range result.Records {
		if record.FinalProgramOutcome != "success" || record.LogicalCalls != 1 || record.PhysicalAttempts != 1 || record.AuthorityDisposition != "read_consumed" || record.WorkspaceDisposition != "published" {
			t.Fatalf("record=%+v", record)
		}
	}
	wantInvocations := uint32(1)
	if caseID == "unknown_wrapper" {
		wantInvocations = 2
	}
	lifecycle := semanticTreatment.LifecycleEvidence()
	t.Logf("semantic lifecycle: %+v", lifecycle)
	if lifecycle.AnalyzerSessions != 1 || lifecycle.Analyzer.Invocations != wantInvocations ||
		lifecycle.Analyzer.ModuleInstantiations != 1 || lifecycle.Analyzer.InitializeCalls != 1 || lifecycle.Analyzer.RuntimeInitCalls != 1 {
		t.Fatalf("semantic lifecycle=%+v", lifecycle)
	}
	evidence, err := semanticspeculation.SealMatchedCaseEvidence(result)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ref, err := semanticspeculation.WriteMatchedCaseEvidenceFile(root, evidence)
	if err != nil || ref.Identity != evidence.Identity || ref.CaseID != caseID {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}
}
