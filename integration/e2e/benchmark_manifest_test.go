package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type fixedCapturedSource struct {
	result json.RawMessage
	calls  atomic.Uint32
}

func (source *fixedCapturedSource) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := source.CallWithEvidence(ctx, arguments)
	return result, err
}

func (source *fixedCapturedSource) CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, capability.TransportEvidence, error) {
	source.calls.Add(1)
	digest := sha256.Sum256(source.result)
	return append(json.RawMessage(nil), source.result...), capability.TransportEvidence{
		Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(source.result)),
		BodySHA256: fmt.Sprintf("sha256:%x", digest[:]),
	}, nil
}

func TestRealGuestCapturesAndReplaysBenchmarkManifestWithDemoCatalog(t *testing.T) {
	manifest := readBenchmarkFixture(t, "benchmark-manifest.v1.json")
	expected := readBenchmarkFixture(t, "benchmark-manifest-analysis.v1.json")
	demoPolicy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	benchmarkPolicy := capability.BenchmarkManifestPolicy{Endpoint: "http://127.0.0.1:1/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}

	liveRegistry := capability.NewRegistry()
	demoSpec, demoGrant, err := capability.DemoCatalogDefinition(demoPolicy)
	if err != nil {
		t.Fatal(err)
	}
	demoHandler := &fixedCapturedSource{result: json.RawMessage(`{"items":[{"id":"alpha","title":"Alpha","score":7}]}`)}
	if err := liveRegistry.Register(demoSpec, demoGrant, demoHandler); err != nil {
		t.Fatal(err)
	}
	benchmarkSpec, benchmarkGrant, err := capability.BenchmarkManifestDefinition(benchmarkPolicy)
	if err != nil {
		t.Fatal(err)
	}
	benchmarkHandler := &fixedCapturedSource{result: manifest}
	if err := liveRegistry.Register(benchmarkSpec, benchmarkGrant, benchmarkHandler); err != nil {
		t.Fatal(err)
	}
	livePlan, err := liveRegistry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}

	request := []byte(`{"run_id":"benchmark-manifest-e2e","code":"import builtins\nfresh_guest=not hasattr(builtins, '_pysolate_benchmark_marker')\nbuiltins._pysolate_benchmark_marker=True\ncatalog=sources.demo_catalog()\nmanifest=sources.benchmark_manifest()\nmatrix=[{'case_id':case['id'],'task_class':case['task_class'],'metric_ids':[metric['id'] for metric in case['metrics']]} for case in manifest['cases']]\nresult={'fresh_guest':fresh_guest,'suite':manifest['suite']['id']+'@'+manifest['suite']['version'],'catalog_titles':[item['title'] for item in catalog],'experiment_matrix':matrix}","inputs":{}}`)
	liveRunner, liveBroker := newSourceRunner(t, livePlan, nil, "source-e2e")
	livePayload, err := liveRunner.Run(context.Background(), request, livePlan.PythonPrelude())
	if err != nil {
		t.Fatal(err)
	}
	if err := liveRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	liveResponse := assertBenchmarkAnalysis(t, livePayload, expected, livePlan.Identity())
	if demoHandler.calls.Load() != 1 || benchmarkHandler.calls.Load() != 1 {
		t.Fatalf("live source calls demo=%d benchmark=%d", demoHandler.calls.Load(), benchmarkHandler.calls.Load())
	}
	transcript := liveBroker().SnapshotTranscript()
	if len(transcript) != 2 || transcript[0].Capability != "sources.demo_catalog" || transcript[1].Capability != "sources.benchmark_manifest" {
		t.Fatalf("captured transcript=%+v", transcript)
	}
	encodedTranscript, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{demoPolicy.Endpoint, benchmarkPolicy.Endpoint, "Authorization"} {
		if bytes.Contains(encodedTranscript, []byte(private)) {
			t.Fatalf("captured transcript leaked private transport policy %q", private)
		}
	}

	offlineRegistry := capability.NewRegistry()
	if err := offlineRegistry.Register(demoSpec, demoGrant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	if err := capability.RegisterBenchmarkManifestPlayback(offlineRegistry, benchmarkPolicy); err != nil {
		t.Fatal(err)
	}
	offlinePlan, err := offlineRegistry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil || offlinePlan.Identity() != livePlan.Identity() {
		t.Fatalf("offline plan=%q live plan=%q err=%v", offlinePlan.Identity(), livePlan.Identity(), err)
	}
	offlineRunner, offlineBroker := newSourceRunner(t, offlinePlan, transcript, "source-e2e")
	offlinePayload, err := offlineRunner.Run(context.Background(), request, offlinePlan.PythonPrelude())
	if err != nil {
		t.Fatal(err)
	}
	if err := offlineRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	offlineResponse := assertBenchmarkAnalysis(t, offlinePayload, expected, offlinePlan.Identity())
	if !reflect.DeepEqual(liveResponse.Result, offlineResponse.Result) || !reflect.DeepEqual(liveResponse.Receipts, offlineResponse.Receipts) {
		t.Fatalf("fresh offline Guest semantic evidence diverged:\nlive=%s\noffline=%s", livePayload, offlinePayload)
	}
	if broker := offlineBroker(); broker == nil || broker.Calls() != 2 || broker.Finalize(true) != nil {
		t.Fatalf("offline broker=%+v", broker)
	}
	analysis, err := json.Marshal(liveResponse.Result)
	if err != nil {
		t.Fatal(err)
	}
	analysisDigest := sha256.Sum256(analysis)
	t.Logf("artifact=%s plan=%s transcript_entries=%d result_sha256=sha256:%x fresh_live=%v fresh_offline=%v", guestArtifact(t), livePlan.Identity(), len(transcript), analysisDigest[:], liveResponse.Result.(map[string]any)["fresh_guest"], offlineResponse.Result.(map[string]any)["fresh_guest"])
}

func newSourceRunner(t *testing.T, plan *capability.Plan, entries []capability.TranscriptEntry, runIdentity string) (enginecontract.Runner, func() *capability.Broker) {
	t.Helper()
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var created *capability.Broker
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		config := capability.Config{RunIdentity: runIdentity, Plan: plan}
		if entries != nil {
			config.Playback = &capability.PlaybackConfig{Entries: entries}
		}
		broker, err := capability.NewBroker(config)
		if err == nil {
			created = broker
		}
		return broker, err
	}}
	runner, err := factory.New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	return runner, func() *capability.Broker { return created }
}

func assertBenchmarkAnalysis(t *testing.T, payload, expected []byte, planIdentity string) guestResponse {
	t.Helper()
	var response guestResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	var expectedResult any
	if err := json.Unmarshal(expected, &expectedResult); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" || !reflect.DeepEqual(response.Result, expectedResult) || response.CapabilityPlanSHA256 != planIdentity || len(response.Receipts) != 2 {
		t.Fatalf("response=%+v expected=%+v plan=%s", response, expectedResult, planIdentity)
	}
	return response
}

func readBenchmarkFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "runtime", "capability", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
