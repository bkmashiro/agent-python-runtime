package e2e_test

import (
	"context"

	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/operator"
	"github.com/bkmashiro/agent-python-runtime/research/workloads"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestCanonicalWorkloads(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		definition := definition
		t.Run(definition.ID, func(t *testing.T) {
			plan, entries := workloadPlayback(t, definition.ID)
			binding := workloadWorkspace(t, definition)
			response, transcript := runCanonicalWorkload(t, artifact, definition, plan, entries, binding)
			if len(transcript) != int(definition.ExpectedCapabilityCalls) {
				t.Fatalf("calls=%d", len(transcript))
			}
			snapshot, err := binding.manager.Snapshot(binding.ref)
			if err != nil {
				t.Fatal(err)
			}
			actual := make([]workloads.WorkspaceEntry, len(snapshot.Entries))
			for i, entry := range snapshot.Entries {
				actual[i] = workloads.WorkspaceEntry{Path: entry.Path, Kind: entry.Kind, Executable: entry.Executable, Size: entry.Size, SHA256: entry.SHA256}
			}
			if definition.ID == "bounded-planning-v1" {
				actual = []workloads.WorkspaceEntry{}
			}
			if err := definition.Verify(response.Result, actual, uint32(len(transcript))); err != nil {
				t.Fatalf("oracle: %v result=%s entries=%+v", err, response.Result, actual)
			}
		})
	}
}

func workloadWorkspace(t *testing.T, definition workloads.Workload) branchWorkspaceBinding {
	t.Helper()
	base := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]workspace.InitialFile, 0, len(definition.SeedFiles))
	for path, body := range definition.SeedFiles {
		files = append(files, workspace.InitialFile{Path: path, Data: body})
	}
	ref, err := manager.Create(files, workspace.DefaultLimits())
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

func workloadPlayback(t *testing.T, id string) (*capability.Plan, []capability.TranscriptEntry) {
	t.Helper()
	registry := capability.NewRegistry()
	entries := []capability.TranscriptEntry{}
	if id == "stateful-local-v1" {
		return nil, entries
	}
	demoPolicy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	if id == "structured-source-v1" || id == "bounded-planning-v1" {
		spec, grant, err := capability.DemoCatalogDefinition(demoPolicy)
		if err != nil || registry.Register(spec, grant, capability.NewPlaybackHandler()) != nil {
			t.Fatal(err)
		}
	}
	if id == "structured-source-v1" {
		benchmarkPolicy := capability.BenchmarkManifestPolicy{Endpoint: "http://127.0.0.1:1/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}
		if err := capability.RegisterBenchmarkManifestPlayback(registry, benchmarkPolicy); err != nil {
			t.Fatal(err)
		}
	}
	maxCalls := uint32(0)
	switch id {
	case "structured-source-v1":
		maxCalls = 2
		entries = []capability.TranscriptEntry{branchTranscriptEntry(0, "sources.demo_catalog", json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`), "http"), branchTranscriptEntry(1, "sources.benchmark_manifest", canonicalFixture(t, "benchmark-manifest.v1.json"), "http")}
	case "bounded-planning-v1":
		maxCalls = 1
		entries = []capability.TranscriptEntry{branchTranscriptEntry(0, "sources.demo_catalog", json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":9,"title":"Beta"},{"id":"gamma","score":6,"title":"Gamma"}]}`), "http")}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan, entries
}

func runCanonicalWorkload(t *testing.T, artifact []byte, definition workloads.Workload, plan *capability.Plan, entries []capability.TranscriptEntry, binding branchWorkspaceBinding) (runtimeconfig.RunResponse, []capability.TranscriptEntry) {
	t.Helper()
	var broker *capability.Broker
	factory := wazeroengine.Factory{WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: definition.ID}
	prelude := ""
	if plan != nil {
		factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
			created, err := capability.NewBroker(capability.Config{RunIdentity: definition.ID, Plan: plan, Playback: &capability.PlaybackConfig{Entries: entries}})
			broker = created
			return created, err
		}
		prelude = plan.PythonPrelude()
	}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: definition.ID, Code: definition.Code, Inputs: definition.Inputs})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{AgentRunID: "workload-evaluation", InvocationID: definition.ID, InvocationAttempt: 1, ExecutionID: definition.ID})
	if err != nil {
		t.Fatal(err)
	}
	payload, runErr := runner.Run(ctx, request, prelude)
	closeErr := runner.Close(context.Background())
	if runErr != nil || closeErr != nil || (plan != nil && broker == nil) {
		t.Fatalf("run=%v close=%v", runErr, closeErr)
	}
	transcript := []capability.TranscriptEntry{}
	if broker != nil {
		if err := broker.Finalize(true); err != nil {
			t.Fatal(err)
		}
		transcript = append([]capability.TranscriptEntry(nil), entries...)
	}
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decoded, payload)
	if err != nil {
		t.Fatal(err)
	}
	return response, transcript
}

func TestRealGuestLiveCaptureMatchesOffline(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 2} {
		definition := definitions[index]
		t.Run(definition.ID, func(t *testing.T) {
			demoBody := `{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`
			if definition.ID == "bounded-planning-v1" {
				demoBody = `{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":9,"title":"Beta"},{"id":"gamma","score":6,"title":"Gamma"}]}`
			}
			demoHits := 0
			demo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				demoHits++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(demoBody))
			}))
			defer demo.Close()
			registry := capability.NewRegistry()
			if err := capability.RegisterDemoCatalog(registry, capability.DemoCatalogPolicy{Endpoint: demo.URL, Timeout: time.Second, MaxResponseBytes: 4096}); err != nil {
				t.Fatal(err)
			}
			benchmarkHits := 0
			var benchmark *httptest.Server
			if definition.ID == "structured-source-v1" {
				body := canonicalFixture(t, "benchmark-manifest.v1.json")
				benchmark = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					benchmarkHits++
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}))
				defer benchmark.Close()
				if err := capability.RegisterBenchmarkManifest(registry, capability.BenchmarkManifestPolicy{Endpoint: benchmark.URL, Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := registry.Seal(capability.PlanConfig{MaxCalls: definition.ExpectedCapabilityCalls})
			if err != nil {
				t.Fatal(err)
			}
			liveBinding := workloadWorkspace(t, definition)
			liveResponse, tape := runLiveWorkload(t, artifact, definition, plan, liveBinding)
			if len(tape) != int(definition.ExpectedCapabilityCalls) {
				t.Fatalf("tape=%d", len(tape))
			}
			if demoHits != 1 || uint32(demoHits+benchmarkHits) != definition.ExpectedCapabilityCalls {
				t.Fatalf("hits demo=%d benchmark=%d", demoHits, benchmarkHits)
			}
			demo.Close()
			if benchmark != nil {
				benchmark.Close()
			}
			offlineBinding := workloadWorkspace(t, definition)
			offlineResponse, _ := runCanonicalWorkload(t, artifact, definition, plan, tape, offlineBinding)
			if !workloads.EqualResult(liveResponse.Result, offlineResponse.Result) {
				t.Fatal("live/offline result mismatch")
			}
			liveFinal, err := liveBinding.manager.Inspect(liveBinding.ref)
			if err != nil {
				t.Fatal(err)
			}
			offlineFinal, err := offlineBinding.manager.Inspect(offlineBinding.ref)
			if err != nil {
				t.Fatal(err)
			}
			if liveFinal.WorkspaceSHA256 != offlineFinal.WorkspaceSHA256 {
				t.Fatalf("live/offline workspace mismatch: live=%s offline=%s", liveFinal.WorkspaceSHA256, offlineFinal.WorkspaceSHA256)
			}
			if demoHits != 1 || uint32(demoHits+benchmarkHits) != definition.ExpectedCapabilityCalls {
				t.Fatal("offline touched live source")
			}
		})
	}
}

func runLiveWorkload(t *testing.T, artifact []byte, definition workloads.Workload, plan *capability.Plan, binding branchWorkspaceBinding) (runtimeconfig.RunResponse, []capability.TranscriptEntry) {
	t.Helper()
	var broker *capability.Broker
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, e := capability.NewBroker(capability.Config{RunIdentity: definition.ID + "-live", Plan: plan})
		broker = created
		return created, e
	}, WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: definition.ID + "-live"}
	runner, e := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if e != nil {
		t.Fatal(e)
	}
	request := workloadRequest(t, definition)
	payload, e := runner.Run(context.Background(), request, plan.PythonPrelude())
	closeErr := runner.Close(context.Background())
	if e != nil || closeErr != nil || broker == nil {
		t.Fatalf("run=%v close=%v", e, closeErr)
	}
	decoded, _ := runtimeconfig.DecodeRunRequest(request)
	response, e := runtimeconfig.DecodeAndValidateRunResponse(decoded, payload)
	if e != nil {
		t.Fatal(e)
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	return response, broker.SnapshotTranscript()
}

func TestCounterfactualBranchesChangeExpectedEvidence(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 2} {
		definition := definitions[index]
		t.Run(definition.ID, func(t *testing.T) {
			plan, entries := workloadPlayback(t, definition.ID)
			parentBinding := workloadWorkspace(t, definition)
			request := workloadRequest(t, definition)
			parent, parentResponse := runParentPlayback(t, artifact, plan, request, entries, parentBinding)
			operation := uint32(0)
			capabilityName := "sources.demo_catalog"
			var alternate json.RawMessage
			if definition.ID == "structured-source-v1" {
				operation = 1
				capabilityName = "sources.benchmark_manifest"
				alternate = alternateManifest(t, "counterfactual-suite")
			} else {
				alternate = json.RawMessage(`{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":4,"title":"Beta"},{"id":"gamma","score":10,"title":"Gamma"}]}`)
			}
			override := branchTranscriptEntry(operation, capabilityName, alternate, "branch_override")
			manifest, err := playback.NewBranchManifest(playback.BranchMetadata{ParentBundleSHA256: parent.Identity, ForkOperation: operation, RequestSHA256: parent.RequestSHA256, ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256, InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: plan.Identity(), ChildGrants: plan.Grants(), SuffixMode: playback.BranchOverride}, parent, []capability.TranscriptEntry{override})
			if err != nil {
				t.Fatal(err)
			}
			childBinding := workloadWorkspace(t, definition)
			outcome, err := operator.RunBranch(context.Background(), operator.BranchRunConfig{WASM: artifact, Runtime: runtimeconfig.DefaultRunConfig(), Plan: plan, Parent: parent, Manifest: manifest, Request: request, TrustedPrepare: plan.PythonPrelude(), Invocation: runtimeconfig.InvocationRef{AgentRunID: "workload-evaluation", InvocationID: definition.ID + "-branch", InvocationAttempt: 1, ExecutionID: definition.ID + "-branch"}, WorkspaceManager: childBinding.manager, WorkspaceRef: childBinding.ref, WorkspaceOwner: definition.ID + "-branch"})
			if err != nil {
				t.Fatal(err)
			}
			if workloads.EqualResult(parentResponse.Result, outcome.Response.Result) || parent.ExpectedResultSHA256 == outcome.ChildBundle.ExpectedResultSHA256 {
				t.Fatal("branch did not change result evidence")
			}
			if definition.ID == "structured-source-v1" && parent.FinalWorkspaceSHA256 == outcome.ChildBundle.FinalWorkspaceSHA256 {
				t.Fatal("structured branch did not change workspace evidence")
			}
		})
	}
}

func TestBoundedPlanningDeterministicFreshGuests(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[2]
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
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(identity.ArtifactSHA256, "bounded-planning-v1")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &bound
	config.DeterministicVerification = &deterministic
	plan, entries := workloadPlayback(t, definition.ID)
	run := func(id string) json.RawMessage {
		var broker *capability.Broker
		factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
			created, e := capability.NewBroker(capability.Config{RunIdentity: id, Plan: plan, Playback: &capability.PlaybackConfig{Entries: entries}})
			broker = created
			return created, e
		}}
		runner, e := factory.New(context.Background(), artifact, config)
		if e != nil {
			t.Fatal(e)
		}
		request := workloadRequestWithCompatibility(t, definition, &runtimeconfig.CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}})
		payload, e := runner.Run(context.Background(), request, plan.PythonPrelude())
		closeErr := runner.Close(context.Background())
		if e != nil || closeErr != nil || broker == nil || broker.Finalize(true) != nil {
			t.Fatalf("run=%v close=%v", e, closeErr)
		}
		decoded, _ := runtimeconfig.DecodeRunRequest(request)
		response, e := runtimeconfig.DecodeAndValidateRunResponse(decoded, payload)
		if e != nil {
			t.Fatal(e)
		}
		return response.Result
	}
	first, second := run("planning-deterministic-a"), run("planning-deterministic-b")
	if !workloads.EqualResult(first, second) {
		t.Fatalf("deterministic results differ first=%s second=%s", first, second)
	}
}

func workloadRequest(t *testing.T, definition workloads.Workload) []byte {
	return workloadRequestWithCompatibility(t, definition, nil)
}

func workloadRequestWithCompatibility(t *testing.T, definition workloads.Workload, compatibility *runtimeconfig.CompatibilityDeclaration) []byte {
	t.Helper()
	raw, err := json.Marshal(runtimeconfig.RunRequest{RunID: definition.ID, Code: definition.Code, Inputs: definition.Inputs, Compatibility: compatibility})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestStatefulFreshGuestEquivalenceWithoutBroker(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[1]
	run := func() (runtimeconfig.RunResponse, string) {
		binding := workloadWorkspace(t, definition)
		response, transcript := runCanonicalWorkload(t, artifact, definition, nil, nil, binding)
		if len(transcript) != 0 {
			t.Fatalf("stateful workload used broker transcript: %d", len(transcript))
		}
		final, err := binding.manager.Inspect(binding.ref)
		if err != nil {
			t.Fatal(err)
		}
		return response, final.WorkspaceSHA256
	}
	first, firstWorkspace := run()
	second, secondWorkspace := run()
	if !workloads.EqualResult(first.Result, second.Result) || firstWorkspace != secondWorkspace {
		t.Fatalf("fresh stateful Guests diverged: result_a=%s result_b=%s workspace_a=%s workspace_b=%s", first.Result, second.Result, firstWorkspace, secondWorkspace)
	}
}

func TestStatefulWorkloadDeterministicTreatmentIsUnsupported(t *testing.T) {
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeconfig.RunRequest{RunID: definitions[1].ID, Code: definitions[1].Code, Inputs: definitions[1].Inputs}
	if err := runtimeconfig.AdmitDeterministicVerificationExecution(request, true); !errors.Is(err, runtimeconfig.ErrDeterministicVerificationAdmission) {
		t.Fatalf("admission=%v", err)
	}
}
