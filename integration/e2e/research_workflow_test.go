package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/operator"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestRealGuestCombinedResearchWorkflow(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var demoHits, benchmarkHits uint32
	demoServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		demoHits++
		if len(request.Header.Values("Authorization")) != 0 {
			t.Errorf("demo source received credentials")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[{"id":"alpha","score":7,"title":"Alpha"}]}`))
	}))
	benchmarkBody := canonicalFixture(t, "benchmark-manifest.v1.json")
	benchmarkServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		benchmarkHits++
		if len(request.Header.Values("Authorization")) != 0 {
			t.Errorf("benchmark source received credentials")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(benchmarkBody)
	}))
	demoPolicy := capability.DemoCatalogPolicy{Endpoint: demoServer.URL, Timeout: time.Second, MaxResponseBytes: 4096}
	benchmarkPolicy := capability.BenchmarkManifestPolicy{Endpoint: benchmarkServer.URL, Timeout: time.Second, MaxResponseBytes: 32 << 10}
	plan := workflowPlan(t, demoPolicy, benchmarkPolicy, false)
	request := []byte(`{"run_id":"combined-research-e2e","code":"import builtins\nfresh_guest=not hasattr(builtins, '_pysolate_combined_marker')\nbuiltins._pysolate_combined_marker=True\ncatalog=sources.demo_catalog()\nmanifest=sources.benchmark_manifest()\nsuite=manifest['suite']['id']\nwith open('/workspace/experiment-plan.txt','w',encoding='utf-8') as handle:\n    handle.write(catalog[0]['title']+'\\n'+suite+'\\n')\nresult={'fresh_guest':fresh_guest,'catalog':catalog[0]['title'],'suite':suite}","inputs":{}}`)

	liveWorkspace := newBranchWorkspace(t, "combined-live")
	parent, liveResponse, transcript, liveEvents := runWorkflowParent(t, artifact, plan, request, liveWorkspace, nil, "combined-live")
	if demoHits != 1 || benchmarkHits != 1 || len(transcript) != 2 {
		t.Fatalf("live hits demo=%d benchmark=%d transcript=%d", demoHits, benchmarkHits, len(transcript))
	}
	if len(liveEvents) != 6 || liveEvents[0].Type != observe.EventExecutionStarted || liveEvents[5].Type != observe.EventExecutionCompleted {
		t.Fatalf("live observation events=%+v", liveEvents)
	}
	demoServer.Close()
	benchmarkServer.Close()

	// The offline registry has no live source handler. A new Runner/Guest consumes
	// the complete captured tape after the live sources are logically unavailable.
	offlinePlan := workflowPlan(t, demoPolicy, benchmarkPolicy, true)
	if offlinePlan.Identity() != plan.Identity() {
		t.Fatalf("offline plan=%s live plan=%s", offlinePlan.Identity(), plan.Identity())
	}
	offlineWorkspace := newBranchWorkspace(t, "combined-offline")
	offlineBundle, offlineResponse, offlineTranscript, offlineEvents := runWorkflowParent(t, artifact, offlinePlan, request, offlineWorkspace, transcript, "combined-offline")
	if demoHits != 1 || benchmarkHits != 1 {
		t.Fatalf("offline playback performed live reads demo=%d benchmark=%d", demoHits, benchmarkHits)
	}
	if !reflect.DeepEqual(liveResponse.Result, offlineResponse.Result) || parent.ExpectedResultSHA256 != offlineBundle.ExpectedResultSHA256 ||
		parent.FinalWorkspaceSHA256 != offlineBundle.FinalWorkspaceSHA256 || len(offlineTranscript) != len(transcript) {
		t.Fatalf("offline evidence diverged parent=%+v offline=%+v", parent, offlineBundle)
	}
	if len(offlineEvents) != len(liveEvents) || offlineEvents[len(offlineEvents)-1].Type != observe.EventExecutionCompleted {
		t.Fatalf("offline observation events=%+v", offlineEvents)
	}

	type childEvidence struct {
		manifest playback.BranchManifest
		outcome  operator.BranchOutcome
	}
	children := make([]childEvidence, 0, 2)
	for index, suiteID := range []string{"counterfactual-a", "counterfactual-b"} {
		override := branchTranscriptEntry(1, "sources.benchmark_manifest", alternateManifest(t, suiteID), "branch_override")
		manifest, manifestErr := playback.NewBranchManifest(playback.BranchMetadata{
			ParentBundleSHA256: parent.Identity, ForkOperation: 1, RequestSHA256: parent.RequestSHA256,
			ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256,
			InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256, ChildCapabilityPlanSHA256: plan.Identity(),
			ChildGrants: plan.Grants(), SuffixMode: playback.BranchOverride,
		}, parent, []capability.TranscriptEntry{override})
		if manifestErr != nil {
			t.Fatal(manifestErr)
		}
		binding := newBranchWorkspace(t, fmt.Sprintf("combined-child-%d", index))
		executionID := fmt.Sprintf("combined-child-%d", index)
		outcome, branchErr := operator.RunBranch(context.Background(), operator.BranchRunConfig{
			WASM: artifact, Runtime: runtimeconfig.DefaultRunConfig(), Plan: plan, Parent: parent, Manifest: manifest,
			Request: request, TrustedPrepare: plan.PythonPrelude(), Invocation: runtimeconfig.InvocationRef{
				AgentRunID: "combined-research", InvocationID: fmt.Sprintf("branch-%d", index), InvocationAttempt: 1, ExecutionID: executionID,
			}, WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: executionID,
		})
		if branchErr != nil {
			t.Fatal(branchErr)
		}
		var result map[string]any
		if err := json.Unmarshal(outcome.Response.Result, &result); err != nil || result["fresh_guest"] != true || result["suite"] != suiteID {
			t.Fatalf("child %d result=%+v err=%v", index, result, err)
		}
		children = append(children, childEvidence{manifest: manifest, outcome: outcome})
	}
	if children[0].outcome.ChildBundle.Identity == children[1].outcome.ChildBundle.Identity ||
		children[0].outcome.ChildBundle.ExpectedResultSHA256 == children[1].outcome.ChildBundle.ExpectedResultSHA256 {
		t.Fatal("counterfactual children did not diverge")
	}
	dag, err := operator.ExportBranchDAG(parent, []operator.ChildRelation{
		{Manifest: children[0].manifest, Child: children[0].outcome.ChildBundle},
		{Manifest: children[1].manifest, Child: children[1].outcome.ChildBundle},
	}, 16)
	if err != nil || len(dag.Nodes) != 3 || len(dag.Edges) != 2 {
		t.Fatalf("dag=%+v err=%v", dag, err)
	}
	comparison := operator.CompareBundles(children[0].outcome.ChildBundle, children[1].outcome.ChildBundle, 16)
	if comparison.SameResult || comparison.CallDifferences != 1 {
		t.Fatalf("comparison=%+v", comparison)
	}

	storeRoot := filepath.Join(t.TempDir(), "research-store")
	store, err := labstore.Open(storeRoot, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	private := labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent}
	prefixBytes, _ := json.Marshal(parent.Entries[:1])
	prefixRef, created, err := store.PutJSON(labstore.KindToolPayload, prefixBytes, private)
	if err != nil || !created {
		t.Fatalf("prefix put created=%v err=%v", created, err)
	}
	if same, createdAgain, putErr := store.PutJSON(labstore.KindToolPayload, prefixBytes, private); putErr != nil || createdAgain || same != prefixRef {
		t.Fatalf("prefix dedup same=%v created=%v err=%v", same == prefixRef, createdAgain, putErr)
	}
	initialTree, _, err := store.PutWorkspaceTree(nil, private)
	if err != nil {
		t.Fatal(err)
	}
	eventRefs := make([]labstore.Ref, 0, len(liveEvents))
	forbiddenObservation := [][]byte{
		[]byte("_pysolate_combined_marker"), []byte(`"title":"Alpha"`), []byte("Alpha"),
		[]byte("pysolate-core"), []byte(demoPolicy.Endpoint), []byte(benchmarkPolicy.Endpoint),
		[]byte("Authorization"), []byte("credential"),
	}
	for _, event := range liveEvents {
		eventBytes, _ := observe.Encode(event)
		for _, forbidden := range forbiddenObservation {
			if bytes.Contains(eventBytes, forbidden) {
				t.Fatalf("observation leaked forbidden body or transport detail %q: %s", forbidden, eventBytes)
			}
		}
		eventRef, _, putErr := store.PutJSON(labstore.KindMetadataEvent, eventBytes, private)
		if putErr != nil {
			t.Fatal(putErr)
		}
		eventRefs = append(eventRefs, eventRef)
	}
	workspaceEvent, _ := observe.Encode(liveEvents[4])
	if !bytes.Contains(workspaceEvent, []byte(`"path":"experiment-plan.txt"`)) ||
		!bytes.Contains(workspaceEvent, []byte(`"after_sha256":"sha256:`)) ||
		bytes.Contains(workspaceEvent, []byte("Alpha")) {
		t.Fatalf("workspace observation metadata/body boundary violated: %s", workspaceEvent)
	}
	capabilityEvents := 0
	for _, event := range liveEvents {
		if event.Type != observe.EventCapabilityCall {
			continue
		}
		capabilityEvents++
		encoded, _ := observe.Encode(event)
		if !bytes.Contains(encoded, []byte(`"arguments_sha256":"sha256:`)) ||
			!bytes.Contains(encoded, []byte(`"result_sha256":"sha256:`)) {
			t.Fatalf("capability observation omitted digest metadata: %s", encoded)
		}
	}
	if capabilityEvents != 2 {
		t.Fatalf("capability observation events=%d want=2", capabilityEvents)
	}
	parentBytes, _ := json.Marshal(parent)
	parentPolicy := private
	parentPolicy.Links = append([]labstore.Ref{prefixRef, initialTree}, eventRefs...)
	parentRef, _, err := store.PutJSON(labstore.KindRun, parentBytes, parentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	for index, child := range children {
		manifestBytes, _ := json.Marshal(child.manifest)
		manifestRef, _, putErr := store.PutJSON(labstore.KindSemanticDocument, manifestBytes, private)
		if putErr != nil {
			t.Fatal(putErr)
		}
		childBytes, _ := json.Marshal(child.outcome.ChildBundle)
		childPolicy := private
		childPolicy.Links = []labstore.Ref{prefixRef, initialTree, manifestRef}
		childRef, _, putErr := store.PutJSON(labstore.KindExecution, childBytes, childPolicy)
		if putErr != nil {
			t.Fatal(putErr)
		}
		if _, _, putErr = store.PutBranch(labstore.Branch{ParentRun: parentRef, ChildExecution: childRef, ForkOperation: 1, Prefix: prefixRef, InitialWorkspace: initialTree, Manifest: manifestRef}, private); putErr != nil {
			t.Fatalf("branch %d store: %v", index, putErr)
		}
	}
	if _, err := store.GetPortable(parentRef); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("private parent was exportable err=%v", err)
	}
	portableSummary := map[string]any{
		"schema_version":       "pysolate.portable-research-summary.v1",
		"parent_bundle_sha256": parent.Identity,
		"child_bundle_sha256":  []string{children[0].outcome.ChildBundle.Identity, children[1].outcome.ChildBundle.Identity},
		"network_hits":         demoHits + benchmarkHits,
		"observation_events":   len(liveEvents),
	}
	portableBytes, err := json.Marshal(portableSummary)
	if err != nil {
		t.Fatal(err)
	}
	portablePolicy := labstore.PutOptions{Privacy: labstore.PrivacyPortable, Credentials: labstore.CredentialsAbsent}
	portableRef, _, err := store.PutJSON(labstore.KindSemanticDocument, portableBytes, portablePolicy)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := store.GetPortable(portableRef)
	if err != nil || !bytes.Equal(exported.Body, portableBytes) {
		t.Fatalf("portable summary export err=%v body=%s", err, exported.Body)
	}
	credentialPolicy := portablePolicy
	credentialPolicy.Credentials = labstore.CredentialsPresent
	if _, _, err := store.PutJSON(labstore.KindSemanticDocument, portableBytes, credentialPolicy); !errors.Is(err, labstore.ErrCredentials) {
		t.Fatalf("credential-bearing portable evidence accepted err=%v", err)
	}
	stats, err := store.Stats()
	if err != nil || stats.ObjectCount < 8 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}

	report := map[string]any{
		"schema_version": "pysolate.combined-research-proof.v1", "artifact_sha256": playback.SHA256(artifact),
		"parent_bundle_sha256": parent.Identity, "offline_bundle_sha256": offlineBundle.Identity,
		"child_bundle_sha256": []string{children[0].outcome.ChildBundle.Identity, children[1].outcome.ChildBundle.Identity},
		"network_hits":        demoHits + benchmarkHits, "observation_events": len(liveEvents), "store_objects": stats.ObjectCount,
		"shared_prefix_sha256": prefixRef.SHA256, "dag_nodes": len(dag.Nodes), "dag_edges": len(dag.Edges),
	}
	evidence, _ := json.Marshal(report)
	t.Log(string(evidence))
}

func workflowPlan(t *testing.T, demoPolicy capability.DemoCatalogPolicy, benchmarkPolicy capability.BenchmarkManifestPolicy, offline bool) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	var err error
	if offline {
		demoSpec, demoGrant, definitionErr := capability.DemoCatalogDefinition(demoPolicy)
		if definitionErr != nil {
			t.Fatal(definitionErr)
		}
		err = registry.Register(demoSpec, demoGrant, capability.NewPlaybackHandler())
		if err == nil {
			err = capability.RegisterBenchmarkManifestPlayback(registry, benchmarkPolicy)
		}
	} else {
		err = capability.RegisterDemoCatalog(registry, demoPolicy)
		if err == nil {
			err = capability.RegisterBenchmarkManifest(registry, benchmarkPolicy)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func runWorkflowParent(t *testing.T, artifact []byte, plan *capability.Plan, request []byte, binding branchWorkspaceBinding, playbackEntries []capability.TranscriptEntry, executionID string) (playback.Bundle, runtimeconfig.RunResponse, []capability.TranscriptEntry, []observe.Event) {
	t.Helper()
	var broker *capability.Broker
	factory := wazeroengine.Factory{
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			config := capability.Config{RunIdentity: executionID, Plan: plan}
			if playbackEntries != nil {
				config.Playback = &capability.PlaybackConfig{Entries: playbackEntries}
			}
			created, err := capability.NewBroker(config)
			if err == nil {
				broker = created
			}
			return created, err
		}, WorkspaceManager: binding.manager, WorkspaceRef: binding.ref, WorkspaceOwner: executionID,
	}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{AgentRunID: "combined-research", InvocationID: executionID, InvocationAttempt: 1, ExecutionID: executionID})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &observationRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, executionID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = enginecontract.WithObservationSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	payload, runErr := runner.Run(ctx, request, plan.PythonPrelude())
	closeErr := runner.Close(context.Background())
	if runErr != nil || closeErr != nil || broker == nil || broker.Finalize(true) != nil {
		t.Fatalf("run=%v close=%v broker=%+v", runErr, closeErr, broker)
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
	if err != nil {
		t.Fatal(err)
	}
	entries := broker.SnapshotTranscript()
	if playbackEntries != nil {
		entries = append([]capability.TranscriptEntry(nil), playbackEntries...)
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
	return bundle, response, entries, recorder.snapshot()
}
