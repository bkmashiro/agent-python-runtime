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

	effectgraph "github.com/bkmashiro/agent-python-runtime/research/effectgraph"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func main() {
	artifactPath := flag.String("artifact", "", "verified target-Guest WASM artifact")
	artifactSourceCommit := flag.String("artifact-source-commit", "", "40-hex source commit used to build the artifact")
	root := flag.String("root", "research/effectgraph", "corpus source root")
	manifestPath := flag.String("manifest", "research/effectgraph/manifest.json", "source manifest")
	corpusPath := flag.String("corpus-output", "research/effectgraph/corpus.json", "bound corpus output")
	reportPath := flag.String("report-output", "docs/evidence/effect-aware-opportunity-census.json", "census output")
	flag.Parse()
	if *artifactPath == "" || *artifactSourceCommit == "" {
		fatal(errors.New("-artifact and -artifact-source-commit are required"))
	}
	if err := run(context.Background(), *artifactPath, *artifactSourceCommit, *root, *manifestPath, *corpusPath, *reportPath); err != nil {
		fatal(err)
	}
}

func run(ctx context.Context, artifactPath, artifactSourceCommit, root, manifestPath, corpusPath, reportPath string) error {
	artifact, err := os.ReadFile(artifactPath)
	if err != nil || len(artifact) == 0 {
		return errors.New("read Guest artifact")
	}
	artifactSHA := digest(artifact)
	manifestSHA := digest([]byte("pysolate.effectgraph-target-manifest.v0"))
	allowedImports := []string{"math", "random", "time"}

	plan, contractSetSHA, err := censusPlan()
	if err != nil {
		return err
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		return err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA,
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		return err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(ctx, artifact, config)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())

	bindings := semantic.Bindings{
		ArtifactSHA256:         artifactSHA,
		ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256:    agentfunction.ImportClosureIdentity(allowedImports, allowedImports),
		CapabilityPlanSHA256:   plan.Identity(),
	}
	target := effectgraph.Target{
		ArtifactSourceCommit: artifactSourceCommit,
		ArtifactSHA256:       artifactSHA, ExecutionProfileSHA256: bindings.ExecutionProfileSHA256,
		ImportClosureSHA256: bindings.ImportClosureSHA256, CapabilityPlanSHA256: plan.Identity(),
		ContractSetSHA256: contractSetSHA,
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	manifest, err := effectgraph.DecodeManifest(manifestFile)
	_ = manifestFile.Close()
	if err != nil {
		return err
	}
	corpus, err := effectgraph.BuildCorpus(manifest, root, target)
	if err != nil {
		return err
	}
	corpusJSON, err := effectgraph.EncodeCorpus(corpus)
	if err != nil {
		return err
	}
	if err := writeAtomic(corpusPath, corpusJSON); err != nil {
		return err
	}

	shard, err := runtimeconfig.NewShardProfile(runtimeconfig.ShardProfileConfig{
		ID: "plain", ExecutionProfileID: "base",
		QualifiedImports: []string{"agent_runtime", "json", "math", "random", "sys", "time"},
		ArtifactSHA256:   artifactSHA, ManifestSHA256: manifestSHA,
		IdlePolicy: runtimeconfig.ShardIdleRetireWhenIdle,
	})
	if err != nil {
		return err
	}
	placementPolicy := placement.Policy{
		AnalyzerVersion: placement.AnalyzerStaticV1, PysolateAvailable: true,
		NativeAvailable: true, PlainShard: shard,
	}
	report, err := effectgraph.RunCensus(ctx, corpus, root,
		func(ctx context.Context, source []byte) (semantic.Analysis, error) {
			request, err := semantic.NewRequest(string(source), bindings, plan)
			if err != nil {
				return semantic.Analysis{}, err
			}
			return semantic.Analyze(ctx, runner, request)
		},
		func(source []byte) (string, error) {
			decision, err := placement.Analyze(runtimeconfig.RunRequest{
				RunID: "effectgraph-census", Code: string(source), Inputs: json.RawMessage(`{}`),
			}, runtimeconfig.StatePortableValue, false, placementPolicy)
			if err != nil {
				return "", err
			}
			switch decision.Backend {
			case runtimeconfig.BackendPysolateWASM:
				return effectgraph.PlacementWASM, nil
			case runtimeconfig.BackendNativeSandbox:
				return effectgraph.PlacementNative, nil
			default:
				return effectgraph.PlacementUnknown, nil
			}
		},
	)
	if err != nil {
		return err
	}
	reportJSON, err := effectgraph.EncodeReport(report)
	if err != nil {
		return err
	}
	if err := writeAtomic(reportPath, reportJSON); err != nil {
		return err
	}
	fmt.Printf("corpus_sha256=%s programs=%d unclassifiable=%d opaque=%d\n", report.CorpusSHA256, report.ProgramsAnalyzed, report.ProgramsUnclassifiable, report.ProgramsOpaque)
	return nil
}

func censusPlan() (*capability.Plan, string, error) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"effectgraph-public-fixture"}`))
	if err != nil {
		return nil, "", err
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.effectgraph.sources-read.v0",
		Description: "Read one public effectgraph fixture value.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "pysolate.effectgraph.sources-read.v0",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","minLength":1,"maxLength":256}},"required":["key"],"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"value":{}},"required":["value"],"additionalProperties":false}`),
		Python:          &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}, ResultField: "value"},
		ReadOnly:        true, Idempotent: true, SpeculativeSafe: true,
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":null}`), nil
	})); err != nil {
		return nil, "", err
	}
	for _, method := range []string{"benchmark_manifest", "demo_catalog"} {
		sourceSpec := capability.Spec{
			Name: "sources." + method, Version: "pysolate.effectgraph." + method + ".v0",
			Description: "Read one public effectgraph runtime fixture.",
			EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
			HandlerIdentity: "pysolate.effectgraph." + method + ".v0",
			InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema:    json.RawMessage(`{"type":"object"}`),
			Python:          &capability.PythonProjection{Module: "sources", Method: method, Arguments: []string{}},
			ReadOnly:        true, Idempotent: true, SpeculativeSafe: true,
		}
		if err := registry.Register(sourceSpec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		})); err != nil {
			return nil, "", err
		}
	}
	workspace, err := capability.NewWorkspace(map[string]string{"value.txt": "fixture"})
	if err != nil || capability.RegisterWorkspaceTools(registry, workspace) != nil {
		return nil, "", errors.New("register workspace fixture")
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 128})
	if err != nil {
		return nil, "", err
	}
	specs := plan.Specs()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	encoded, err := json.Marshal(struct {
		Schema string            `json:"schema"`
		Specs  []capability.Spec `json:"specs"`
	}{Schema: "pysolate.effectgraph-contract-set.v0", Specs: specs})
	if err != nil {
		return nil, "", err
	}
	return plan, digest(encoded), nil
}

func writeAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
