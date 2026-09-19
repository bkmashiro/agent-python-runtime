package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"

	"github.com/bkmashiro/agent-python-runtime/research/sourceboundpasses"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

type artifactManifest struct {
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
	} `json:"build"`
	Artifact struct {
		SHA256 string `json:"sha256"`
	} `json:"artifact"`
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cleanHarnessCommit() (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("missing Go build identity")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(settings["vcs.revision"]) || settings["vcs.modified"] != "false" {
		return "", errors.New("workload evidence harness must be built from a clean commit")
	}
	return settings["vcs.revision"], nil
}

func run(ctx context.Context, artifactPath, manifestPath, preregistrationPath string) ([]byte, error) {
	harnessCommit, err := cleanHarnessCommit()
	if err != nil {
		return nil, err
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, err
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest artifactManifest
	if json.Unmarshal(manifestRaw, &manifest) != nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(manifest.Build.RepositoryCommit) {
		return nil, errors.New("invalid exact Guest manifest")
	}
	artifactSHA := digestBytes(artifact)
	if manifest.Artifact.SHA256 != artifactSHA[7:] {
		return nil, errors.New("artifact and manifest digest mismatch")
	}
	preregistrationRaw, err := os.ReadFile(preregistrationPath)
	if err != nil {
		return nil, err
	}
	canonicalPreregistration, err := sourceboundpasses.EncodeAuthoredWorkloadPreregistration(sourceboundpasses.AuthoredWorkloadPreregistrationV2())
	if err != nil || !bytes.Equal(preregistrationRaw, append(canonicalPreregistration, '\n')) {
		return nil, errors.New("authored workload preregistration drift")
	}

	workspace, err := capability.NewWorkspace(map[string]string{
		"alpha.py": "def alpha():\n    return 1\n\nclass Alpha:\n    pass\n",
		"beta.py":  "def beta():\n    return 2\n",
	})
	if err != nil {
		return nil, err
	}
	registry := capability.NewRegistry()
	if err := capability.RegisterWorkspaceTools(registry, workspace); err != nil {
		return nil, err
	}
	if err := registerSyntheticSourceReads(registry); err != nil {
		return nil, err
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 12})
	if err != nil {
		return nil, err
	}
	allowedImports := []string{"numpy"}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", allowedImports)
	if err != nil {
		return nil, err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "numpy-core", ArtifactSHA256: artifactSHA, ManifestSHA256: digestBytes(manifestRaw),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		return nil, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	runner, err := (wazeroengine.Factory{}).New(ctx, artifact, config)
	if err != nil {
		return nil, err
	}
	defer runner.Close(context.Background())
	analyzer, ok := runner.(*wazeroengine.Engine)
	if !ok {
		return nil, errors.New("exact Guest analyzer is not a Wazero engine")
	}
	bindings := semantic.Bindings{
		ArtifactSHA256:         artifactSHA,
		ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256:    agentfunction.ImportClosureIdentity(allowedImports, allowedImports),
		CapabilityPlanSHA256:   plan.Identity(),
	}
	contextBinding := semantic.PreissueContext{
		StreamEpoch: "authored-v2", WorkflowEpoch: "authored-v2", FreshnessEpoch: "workspace-v1",
		ExpiryEpoch: "run-end", PrivacyPartition: "authored-public", ParentLineageSHA256: digestBytes([]byte("authored-parent-v1")),
		BudgetReservationSHA256: digestBytes([]byte("authored-budget-v1")), RemainingPhysicalReads: 8,
	}
	preregistration := sourceboundpasses.AuthoredWorkloadPreregistrationV2()
	sources := sourceboundpasses.AuthoredWorkloadSourcesV2()
	rows := make([]sourceboundpasses.AuthoredWorkloadEvidenceInput, len(sources))
	for index, item := range sources {
		request, requestErr := semantic.NewRequest(item.Source, bindings, plan)
		if requestErr != nil {
			return nil, requestErr
		}
		verified, analyzeErr := semantic.AnalyzeVerified(ctx, analyzer, request)
		if analyzeErr != nil {
			return nil, fmt.Errorf("case %s exact Guest analysis: %w", item.ID, analyzeErr)
		}
		analysis, analysisErr := verified.Analysis()
		if analysisErr != nil {
			return nil, analysisErr
		}
		off, offErr := semantic.BuildSourceBoundPlan(verified, plan, semantic.PlannerConfig{})
		if offErr != nil || len(off.Projection().Passes) != 0 || len(off.Projection().Decisions) != 0 {
			return nil, fmt.Errorf("case %s pass-off drift", item.ID)
		}
		on, onErr := semantic.BuildSourceBoundPlan(verified, plan, semantic.PlannerConfig{
			Passes:          []semantic.PassConfig{{Name: semantic.PassSemanticPreDispatch, Version: semantic.SemanticPreDispatchPassVersion, Enabled: true}},
			PreissueContext: contextBinding,
		})
		if onErr != nil {
			return nil, fmt.Errorf("case %s semantic pass plan: %w", item.ID, onErr)
		}
		admitted, rejected := uint32(0), uint32(0)
		for _, decision := range on.Projection().Decisions {
			switch decision.Disposition {
			case semantic.PassAdmitted:
				admitted++
			case semantic.PassRejected:
				rejected++
			default:
				return nil, fmt.Errorf("case %s unknown pass disposition", item.ID)
			}
		}
		locallyReusable := uint32(0)
		for _, region := range analysis.CandidateRegions {
			if region.LocallyReusable() {
				locallyReusable++
			}
		}
		rows[index] = sourceboundpasses.AuthoredWorkloadEvidenceInput{
			ID: item.ID, SourceSHA256: analysis.SourceSHA256, ASTSHA256: analysis.ASTSHA256,
			AnalyzerSHA256: analysis.AnalyzerSHA256, CandidateRegions: uint32(len(analysis.CandidateRegions)),
			LocallyReusableRegions: locallyReusable, CallSites: uint32(len(analysis.CallSites)),
			SemanticAdmitted: admitted, SemanticRejected: rejected,
		}
	}
	evidence, err := sourceboundpasses.BuildAuthoredWorkloadEvidence(sourceboundpasses.AuthoredWorkloadEvidenceBuild{
		Preregistration: preregistration, PreregistrationSHA256: digestBytes(preregistrationRaw),
		ArtifactSourceCommit: manifest.Build.RepositoryCommit, ArtifactSHA256: artifactSHA,
		ArtifactManifestSHA256: digestBytes(manifestRaw), HarnessSourceCommit: harnessCommit,
		CapabilityPlanSHA256: plan.Identity(), ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		Rows: rows,
	})
	if err != nil {
		return nil, err
	}
	encoded, err := sourceboundpasses.EncodeAuthoredWorkloadEvidence(evidence)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func registerSyntheticSourceReads(registry *capability.Registry) error {
	grant, err := capability.NewGrant(json.RawMessage(`{"fixture":"synthetic-predispatch-positive","network":false}`))
	if err != nil {
		return err
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "sources.read.synthetic.v1", Description: "Deterministic synthetic read used to exercise positive pre-dispatch admission.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "sources-read-synthetic-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","enum":["weather","rail","attractions"]}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}},
		ReadOnly:     true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{
			Resource: capability.ResourceReference{Namespace: "synthetic-source", Argument: "key"}, Freshness: capability.FreshnessPlanEpoch,
			Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition,
			Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	return registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"synthetic"}`), nil
	}))
}

func main() {
	artifact := flag.String("artifact", "", "exact Guest artifact")
	manifest := flag.String("manifest", "", "exact Guest build manifest")
	preregistration := flag.String("preregistration", "", "frozen authored workload preregistration")
	flag.Parse()
	if *artifact == "" || *manifest == "" || *preregistration == "" {
		fmt.Fprintln(os.Stderr, "artifact, manifest and preregistration are required")
		os.Exit(2)
	}
	raw, err := run(context.Background(), *artifact, *manifest, *preregistration)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(raw); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
