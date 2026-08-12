package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/operator"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestFreshCounterfactualBranchesDivergeAtBenchmarkBoundary(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := branchSourcePlan(t)
	request := []byte(`{"run_id":"research-branch-e2e","code":"import builtins\nfresh_guest=not hasattr(builtins, '_pysolate_branch_marker')\nbuiltins._pysolate_branch_marker=True\ncatalog=sources.demo_catalog()\nmanifest=sources.benchmark_manifest()\nsuite=manifest['suite']['id']\nwith open('/workspace/plan.txt','w',encoding='utf-8') as handle:\n    handle.write(suite)\nresult={'fresh_guest':fresh_guest,'catalog':catalog[0]['title'],'suite':suite}","inputs":{}}`)
	parentEntries := []capability.TranscriptEntry{
		branchTranscriptEntry(0, "sources.demo_catalog", json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`), "http"),
		branchTranscriptEntry(1, "sources.benchmark_manifest", canonicalFixture(t, "benchmark-manifest.v1.json"), "http"),
	}
	parentWorkspace := newBranchWorkspace(t, "parent-workspace")
	parent, parentResponse := runParentPlayback(t, artifact, plan, request, parentEntries, parentWorkspace)
	var parentResult map[string]any
	if err := json.Unmarshal(parentResponse.Result, &parentResult); err != nil {
		t.Fatal(err)
	}
	if parentResult["fresh_guest"] != true || parentResult["suite"] != "pysolate-core" {
		t.Fatalf("parent result=%+v", parentResult)
	}

	type childResult struct {
		outcome operator.BranchOutcome
		manager *workspace.Manager
		ref     workspace.Ref
	}
	children := make([]childResult, 0, 2)
	manifests := make([]playback.BranchManifest, 0, 2)
	for index, suiteID := range []string{"counterfactual-a", "counterfactual-b"} {
		alternate := alternateManifest(t, suiteID)
		override := branchTranscriptEntry(1, "sources.benchmark_manifest", alternate, "branch_override")
		manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
			ParentBundleSHA256: parent.Identity, ForkOperation: 1, RequestSHA256: parent.RequestSHA256,
			ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256,
			InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: plan.Identity(),
			ChildGrants: plan.Grants(), SuffixMode: playback.BranchOverride,
		}, parent, []capability.TranscriptEntry{override})
		if err != nil {
			t.Fatal(err)
		}
		binding := newBranchWorkspace(t, "child-workspace")
		executionID := "research-child-" + string(rune('a'+index))
		outcome, err := operator.RunBranch(context.Background(), operator.BranchRunConfig{
			WASM: artifact, Runtime: runtimeconfig.DefaultRunConfig(), Plan: plan, Parent: parent, Manifest: manifest,
			Request: request, TrustedPrepare: plan.PythonPrelude(),
			Invocation: runtimeconfig.InvocationRef{
				AgentRunID: "research-agent", InvocationID: "branch-invocation-" + string(rune('a'+index)),
				InvocationAttempt: 1, ExecutionID: executionID,
			},
			WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: executionID,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := outcome.Response.Result
		var semantic map[string]any
		if err := json.Unmarshal(result, &semantic); err != nil || semantic["fresh_guest"] != true || semantic["suite"] != suiteID {
			t.Fatalf("child=%s semantic=%+v err=%v", suiteID, semantic, err)
		}
		if outcome.ParentBundleSHA256 != parent.Identity || outcome.BranchSHA256 != manifest.Identity || outcome.ForkOperation != 1 || !outcome.FreshGuest ||
			outcome.ChildBundle.ExpectedResultSHA256 == parent.ExpectedResultSHA256 || outcome.ChildBundle.FinalWorkspaceSHA256 == parent.FinalWorkspaceSHA256 {
			t.Fatalf("outcome=%+v", outcome)
		}
		if len(outcome.ChildBundle.Entries) != 2 || outcome.ChildBundle.Entries[0].ResultSHA256 != parent.Entries[0].ResultSHA256 ||
			outcome.ChildBundle.Entries[1].Evidence.Kind != "branch_override" {
			t.Fatalf("child transcript=%+v", outcome.ChildBundle.Entries)
		}
		children = append(children, childResult{outcome: outcome, manager: binding.manager, ref: binding.ref})
		manifests = append(manifests, manifest)
	}
	if children[0].outcome.ChildBundle.Identity == children[1].outcome.ChildBundle.Identity ||
		children[0].outcome.ChildBundle.ExpectedResultSHA256 == children[1].outcome.ChildBundle.ExpectedResultSHA256 {
		t.Fatalf("children did not diverge: a=%s b=%s", children[0].outcome.ChildBundle.Identity, children[1].outcome.ChildBundle.Identity)
	}
	dag, err := operator.ExportBranchDAG(parent, []operator.ChildRelation{
		{Manifest: manifests[0], Child: children[0].outcome.ChildBundle},
		{Manifest: manifests[1], Child: children[1].outcome.ChildBundle},
	}, 16)
	if err != nil || len(dag.Nodes) != 3 || len(dag.Edges) != 2 {
		t.Fatalf("dag=%+v err=%v", dag, err)
	}
}

func TestExperimentalDeterministicBranchRepeatsQualifiedFreshGuests(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	runConfig := qualifiedDeterministicBranchConfig(t, artifactPath, artifact)
	plan := branchSourcePlan(t)
	request := []byte(`{"run_id":"deterministic-branch","code":"import datetime, sys\nbuiltins_module=sys.modules['builtins']\nfresh_guest=not hasattr(builtins_module, '_pysolate_det_branch_marker')\nbuiltins_module._pysolate_det_branch_marker=True\ncatalog=sources.demo_catalog()\nmanifest=sources.benchmark_manifest()\nos_module=sys.modules['os']\ntime_module=sys.modules['time']\nresult={'fresh_guest':fresh_guest,'suite':manifest['suite']['id'],'catalog':catalog[0]['title'],'wall':time_module.time_ns(),'datetime':datetime.datetime.now().isoformat(),'urandom':os_module.urandom(16).hex(),'hash':hash('pysolate')} ","inputs":{},"compatibility":{"profile":"base","imports":["datetime","sys"]}}`)
	entries := []capability.TranscriptEntry{
		branchTranscriptEntry(0, "sources.demo_catalog", json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`), "http"),
		branchTranscriptEntry(1, "sources.benchmark_manifest", canonicalFixture(t, "benchmark-manifest.v1.json"), "http"),
	}
	executionID := "deterministic-branch-parent"
	var parentBroker *capability.Broker
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: executionID, Plan: plan, Playback: &capability.PlaybackConfig{Entries: entries},
		})
		if createErr == nil {
			parentBroker = created
		}
		return created, createErr
	}}).New(context.Background(), artifact, runConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "deterministic-agent", InvocationID: "deterministic-parent", InvocationAttempt: 1, ExecutionID: executionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, runErr := runner.Run(ctx, request, plan.PythonPrelude())
	closeErr := runner.Close(context.Background())
	if runErr != nil || closeErr != nil || parentBroker == nil || parentBroker.Finalize(true) != nil {
		t.Fatalf("parent run=%v close=%v broker=%+v", runErr, closeErr, parentBroker)
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	parentResponse, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
	if err != nil {
		t.Fatal(err)
	}
	requestSHA256, _ := runtimeconfig.RunRequestSHA256(decodedRequest)
	profileSHA256, _ := runtimeconfig.ExecutionProfileBindingSHA256(runConfig)
	parentResultSHA256, _ := playback.CanonicalSHA256(parentResponse.Result)
	parent, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: plan.Identity(), RequestSHA256: requestSHA256, ArtifactSHA256: playback.SHA256(artifact),
		ExecutionProfileSHA256: profileSHA256, ExpectedStatus: string(parentResponse.Status), ExpectedResultSHA256: parentResultSHA256,
		Grants: plan.Grants(),
	}, entries)
	if err != nil {
		t.Fatal(err)
	}
	override := branchTranscriptEntry(1, "sources.benchmark_manifest", alternateManifest(t, "qualified-alternate"), "branch_override")
	manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: 1, RequestSHA256: parent.RequestSHA256,
		ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256,
		ChildCapabilityPlanSHA256: plan.Identity(), ChildGrants: plan.Grants(), SuffixMode: playback.BranchOverride,
	}, parent, []capability.TranscriptEntry{override})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := make([]operator.BranchOutcome, 0, 2)
	for index := 0; index < 2; index++ {
		childExecutionID := "deterministic-branch-child-" + string(rune('a'+index))
		outcome, branchErr := operator.RunBranch(context.Background(), operator.BranchRunConfig{
			WASM: artifact, Runtime: runConfig, Plan: plan, Parent: parent, Manifest: manifest, Request: request,
			TrustedPrepare: plan.PythonPrelude(), Invocation: runtimeconfig.InvocationRef{
				AgentRunID: "deterministic-agent", InvocationID: "deterministic-child", InvocationAttempt: uint32(index + 1), ExecutionID: childExecutionID,
			},
		})
		if branchErr != nil {
			t.Fatal(branchErr)
		}
		var result map[string]any
		if err := json.Unmarshal(outcome.Response.Result, &result); err != nil || result["fresh_guest"] != true || result["suite"] != "qualified-alternate" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		outcomes = append(outcomes, outcome)
	}
	if outcomes[0].ChildBundle.Identity != outcomes[1].ChildBundle.Identity ||
		outcomes[0].ChildBundle.ExpectedResultSHA256 != outcomes[1].ChildBundle.ExpectedResultSHA256 ||
		string(outcomes[0].Response.Result) != string(outcomes[1].Response.Result) {
		t.Fatalf("qualified deterministic children diverged:\na=%s\nb=%s", outcomes[0].Response.Result, outcomes[1].Response.Result)
	}
}

func qualifiedDeterministicBranchConfig(t *testing.T, artifactPath string, artifact []byte) runtimeconfig.RunConfig {
	t.Helper()
	root := filepath.Dir(artifactPath)
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := os.ReadFile(filepath.Join(root, "import-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := os.ReadFile(filepath.Join(root, "import-qualification.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), artifact, manifest, inventory, qualification)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"datetime", "sys"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(identity.ArtifactSHA256, "qualified-branch-repeat")
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	config.DeterministicVerification = &deterministic
	return config
}

type branchWorkspaceBinding struct {
	manager *workspace.Manager
	ref     workspace.Ref
	initial workspace.CapsuleInfo
}

func newBranchWorkspace(t *testing.T, label string) branchWorkspaceBinding {
	t.Helper()
	base := filepath.Join(t.TempDir(), label)
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := manager.Inspect(ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return branchWorkspaceBinding{manager: manager, ref: ref, initial: initial}
}

func branchSourcePlan(t *testing.T) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	demoPolicy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	demoSpec, demoGrant, err := capability.DemoCatalogDefinition(demoPolicy)
	if err != nil || registry.Register(demoSpec, demoGrant, capability.NewPlaybackHandler()) != nil {
		t.Fatalf("demo source: %v", err)
	}
	benchmarkPolicy := capability.BenchmarkManifestPolicy{Endpoint: "http://127.0.0.1:1/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}
	if err := capability.RegisterBenchmarkManifestPlayback(registry, benchmarkPolicy); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func runParentPlayback(t *testing.T, artifact []byte, plan *capability.Plan, request []byte, entries []capability.TranscriptEntry, binding branchWorkspaceBinding) (playback.Bundle, runtimeconfig.RunResponse) {
	t.Helper()
	factory := wazeroengine.Factory{
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{RunIdentity: "research-parent", Plan: plan, Playback: &capability.PlaybackConfig{Entries: entries}})
		},
		WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: "research-parent",
	}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, plan.PythonPrelude())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
	if err != nil {
		t.Fatal(err)
	}
	requestSHA256, _ := runtimeconfig.RunRequestSHA256(decodedRequest)
	profileSHA256, _ := runtimeconfig.ExecutionProfileBindingSHA256(runtimeconfig.DefaultRunConfig())
	resultSHA256, _ := playback.CanonicalSHA256(response.Result)
	final, err := binding.manager.Inspect(binding.ref)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: plan.Identity(), RequestSHA256: requestSHA256, ArtifactSHA256: playback.SHA256(artifact),
		ExecutionProfileSHA256: profileSHA256, ExpectedStatus: string(response.Status), ExpectedResultSHA256: resultSHA256,
		InitialWorkspaceSHA256: binding.initial.WorkspaceSHA256, FinalWorkspaceSHA256: final.WorkspaceSHA256, Grants: plan.Grants(),
	}, entries)
	if err != nil {
		t.Fatal(err)
	}
	return bundle, response
}

func branchTranscriptEntry(operation uint32, name string, result json.RawMessage, kind string) capability.TranscriptEntry {
	evidence := capability.TransportEvidence{Kind: kind, Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: playback.SHA256(result)}
	return capability.TranscriptEntry{
		OperationIndex: operation, Capability: name, Arguments: json.RawMessage(`{}`), ArgumentsSHA256: playback.SHA256([]byte(`{}`)),
		Result: append(json.RawMessage(nil), result...), ResultSHA256: playback.SHA256(result), Evidence: evidence,
	}
}

func canonicalFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	raw := readBenchmarkFixture(t, name)
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func alternateManifest(t *testing.T, suiteID string) json.RawMessage {
	t.Helper()
	raw := canonicalFixture(t, "benchmark-manifest.v1.json")
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	suite := document["suite"].(map[string]any)
	suite["id"] = suiteID
	suite["title"] = "Counterfactual " + suiteID
	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
