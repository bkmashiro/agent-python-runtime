package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBenchmarkManifestIsTypedExactEndpointSource(t *testing.T) {
	fixture := benchmarkManifestFixtureJSON(t)
	var calls atomic.Uint32
	transport := sourceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/manifests/current" || request.URL.RawQuery != "track=stable" ||
			request.Header.Get("Accept") != "application/json" || request.Header.Get("Accept-Encoding") != "identity" ||
			request.Header.Get("User-Agent") != "pysolate-source-adapter/1" || len(request.Header.Values("Authorization")) != 0 {
			t.Errorf("unexpected request method=%s url=%s headers=%v", request.Method, request.URL.String(), request.Header)
		}
		return sourceHTTPResponse(http.StatusOK, "application/json; charset=utf-8", []byte(fixture)), nil
	})

	policy := BenchmarkManifestPolicy{Endpoint: "https://source.test/manifests/current?track=stable", Timeout: time.Second, MaxResponseBytes: 16 << 10}
	registry := NewRegistry()
	if err := RegisterBenchmarkManifest(registry, policy); err != nil {
		t.Fatal(err)
	}
	installSourceTransport(t, registry, benchmarkManifestCapability, transport)
	plan, err := registry.Seal(PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	specs := plan.Specs()
	if len(specs) != 1 || specs[0].Name != "sources.benchmark_manifest" || specs[0].Version != "pysolate.sources.benchmark-manifest.v1" || specs[0].EffectClass != EffectExternalRead || specs[0].Playback != PlaybackCaptured || specs[0].Python == nil || specs[0].Python.Module != "sources" || specs[0].Python.Method != "benchmark_manifest" || specs[0].Python.GlobalAlias != "" || specs[0].Python.ResultField != "" {
		t.Fatalf("specs=%#v", specs)
	}
	if qualification, ok := plan.PreDispatch("sources.benchmark_manifest"); ok || qualification.Eligible() {
		t.Fatalf("live manifest source received unsupported pre-dispatch qualification: %+v", qualification)
	}
	if strings.Contains(string(mustBenchmarkJSON(t, specs[0])), policy.Endpoint) || strings.Contains(plan.PythonPrelude(), "endpoint") || strings.Contains(plan.PythonPrelude(), "headers") || strings.Contains(plan.PythonPrelude(), "method") {
		t.Fatalf("Agent surface leaked transport authority: spec=%s prelude=%s", mustBenchmarkJSON(t, specs[0]), plan.PythonPrelude())
	}

	broker, err := NewBroker(Config{RunIdentity: "benchmark-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.benchmark_manifest","arguments":{}}`))
	if err != nil || !strings.Contains(string(response), `"status":"ok"`) || !strings.Contains(string(response), `"workspace-summary"`) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	transcript := broker.SnapshotTranscript()
	if calls.Load() != 1 || len(transcript) != 1 || transcript[0].Capability != "sources.benchmark_manifest" {
		t.Fatalf("calls=%d transcript=%+v", calls.Load(), transcript)
	}
	encodedTranscript, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedTranscript, []byte(policy.Endpoint)) || bytes.Contains(encodedTranscript, []byte("Authorization")) {
		t.Fatalf("transcript leaked private transport policy: %s", encodedTranscript)
	}

	response, err = broker.Call(context.Background(), []byte(`{"call_id":"two","capability":"sources.benchmark_manifest","arguments":{"url":"https://agent.invalid/","method":"POST","headers":{"Authorization":"secret"}}}`))
	if err != nil || !strings.Contains(string(response), `"status":"denied"`) || calls.Load() != 1 {
		t.Fatalf("Agent transport controls were accepted: response=%s err=%v calls=%d", response, err, calls.Load())
	}
}

func TestBenchmarkManifestGrantBindsExactHostPolicy(t *testing.T) {
	base := BenchmarkManifestPolicy{Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: 4096}
	spec, grant, err := BenchmarkManifestDefinition(base)
	if err != nil {
		t.Fatal(err)
	}
	if spec.HandlerIdentity == "" || strings.Contains(string(mustBenchmarkJSON(t, spec)), base.Endpoint) {
		t.Fatalf("invalid or endpoint-bearing spec=%#v", spec)
	}
	seal := func(spec Spec, grant Grant) *Plan {
		registry := NewRegistry()
		if err := registry.Register(spec, grant, NewPlaybackHandler()); err != nil {
			t.Fatal(err)
		}
		plan, err := registry.Seal(PlanConfig{MaxCalls: 1})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	basePlan := seal(spec, grant)
	mutations := []func(*BenchmarkManifestPolicy){
		func(policy *BenchmarkManifestPolicy) { policy.Endpoint = "https://other.test/manifest" },
		func(policy *BenchmarkManifestPolicy) { policy.Timeout = 2 * time.Second },
		func(policy *BenchmarkManifestPolicy) { policy.MaxResponseBytes++ },
	}
	for index, mutate := range mutations {
		changed := base
		mutate(&changed)
		changedSpec, changedGrant, err := BenchmarkManifestDefinition(changed)
		if err != nil || changedGrant.Identity() == grant.Identity() {
			t.Fatalf("mutation %d grant=%q err=%v", index, changedGrant.Identity(), err)
		}
		if changedPlan := seal(changedSpec, changedGrant); changedPlan.Identity() == basePlan.Identity() || changedPlan.Grants()[0].PolicySHA256 == basePlan.Grants()[0].PolicySHA256 {
			t.Fatalf("mutation %d was not bound into the sealed Plan", index)
		}
	}
}

func TestBenchmarkManifestRejectsInvalidHostPolicy(t *testing.T) {
	for name, policy := range map[string]BenchmarkManifestPolicy{
		"relative":       {Endpoint: "/manifest", Timeout: time.Second, MaxResponseBytes: 4096},
		"credentials":    {Endpoint: "https://user:pass@source.test/manifest", Timeout: time.Second, MaxResponseBytes: 4096},
		"fragment":       {Endpoint: "https://source.test/manifest#private", Timeout: time.Second, MaxResponseBytes: 4096},
		"scheme":         {Endpoint: "file:///tmp/manifest", Timeout: time.Second, MaxResponseBytes: 4096},
		"zero timeout":   {Endpoint: "https://source.test/manifest", Timeout: 0, MaxResponseBytes: 4096},
		"long timeout":   {Endpoint: "https://source.test/manifest", Timeout: 31 * time.Second, MaxResponseBytes: 4096},
		"partial millis": {Endpoint: "https://source.test/manifest", Timeout: time.Second + time.Nanosecond, MaxResponseBytes: 4096},
		"zero bytes":     {Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: 0},
		"too many bytes": {Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: 1<<20 + 1},
		"invalid UTF-8":  {Endpoint: "https://source.test/" + string([]byte{0xff}), Timeout: time.Second, MaxResponseBytes: 4096},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := BenchmarkManifestDefinition(policy); err == nil {
				t.Fatal("invalid benchmark manifest policy accepted")
			}
		})
	}
}

func TestBenchmarkManifestRejectsInvalidNestedResearchSchema(t *testing.T) {
	valid := compactJSON(t, benchmarkManifestFixtureJSON(t))
	cases := map[string]string{
		"unknown root field":       strings.Replace(valid, `"cases":`, `"unknown":true,"cases":`, 1),
		"unknown suite field":      strings.Replace(valid, `"title":"Pysolate Core Research"`, `"title":"Pysolate Core Research","unknown":true`, 1),
		"unknown case field":       strings.Replace(valid, `"task_class":"workspace_transform"`, `"task_class":"workspace_transform","unknown":true`, 1),
		"unknown artifact field":   strings.Replace(valid, `"kind":"dataset"`, `"kind":"dataset","unknown":true`, 1),
		"unknown metric field":     strings.Replace(valid, `"direction":"maximize"`, `"direction":"maximize","unknown":true`, 1),
		"unknown bounds field":     strings.Replace(valid, `"bounds":{"maximum":100,"minimum":0}`, `"bounds":{"maximum":100,"minimum":0,"unknown":1}`, 1),
		"folded direction alias":   strings.Replace(valid, `"direction":"maximize"`, `"Direction":"maximize"`, 1),
		"unsupported version":      strings.Replace(valid, `pysolate.benchmark-manifest.v1`, `pysolate.benchmark-manifest.v2`, 1),
		"invalid direction":        strings.Replace(valid, `"direction":"maximize"`, `"direction":"sideways"`, 1),
		"invalid unit":             strings.Replace(valid, `"unit":"score"`, `"unit":"tokens-per-dollar"`, 1),
		"reversed bounds":          strings.Replace(valid, `"bounds":{"maximum":100,"minimum":0}`, `"bounds":{"maximum":100,"minimum":101}`, 1),
		"ratio outside unit bound": strings.Replace(strings.Replace(valid, `"unit":"score"`, `"unit":"ratio"`, 1), `"bounds":{"maximum":100,"minimum":0}`, `"bounds":{"maximum":2,"minimum":0}`, 1),
		"duplicate case id":        strings.Replace(valid, `"cases":[`, `"cases":[{"id":"workspace-summary","title":"Other","task_class":"reasoning","input_artifacts":[{"id":"prompt","kind":"prompt","sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","media_type":"text/plain","bytes":1}],"metrics":[{"id":"accuracy","title":"Accuracy","direction":"maximize","unit":"ratio","bounds":{"minimum":0,"maximum":1}}]},`, 1),
		"duplicate artifact id":    strings.Replace(valid, `}],"metrics"`, `},{"id":"metrics-input","kind":"dataset","sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","media_type":"application/json","bytes":1}],"metrics"`, 1),
		"duplicate metric id":      strings.Replace(valid, `}],"tags"`, `},{"id":"quality","title":"Other quality","direction":"minimize","unit":"count","bounds":{"minimum":0,"maximum":1}}],"tags"`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response, hits := callBenchmarkManifestServer(t, []byte(body), "application/json", http.StatusOK, uint32(len(body)+1))
			if !strings.Contains(string(response), `"status":"error"`) || hits != 1 {
				t.Fatalf("invalid manifest accepted: response=%s hits=%d", response, hits)
			}
		})
	}
}

func TestBenchmarkManifestSharesStrictTransportValidation(t *testing.T) {
	valid := compactJSON(t, benchmarkManifestFixtureJSON(t))
	tests := []struct {
		name        string
		body        []byte
		contentType string
		status      int
		maximum     uint32
	}{
		{name: "status", body: []byte(valid), contentType: "application/json", status: http.StatusNoContent, maximum: 32 << 10},
		{name: "media type", body: []byte(valid), contentType: "text/plain", status: http.StatusOK, maximum: 32 << 10},
		{name: "charset", body: []byte(valid), contentType: "application/json; charset=iso-8859-1", status: http.StatusOK, maximum: 32 << 10},
		{name: "invalid UTF-8", body: append([]byte(`{"schema_version":"`), 0xff), contentType: "application/json", status: http.StatusOK, maximum: 32 << 10},
		{name: "duplicate JSON key", body: []byte(strings.Replace(valid, `"schema_version":`, `"schema_version":"pysolate.benchmark-manifest.v1","schema_version":`, 1)), contentType: "application/json", status: http.StatusOK, maximum: 32 << 10},
		{name: "trailing JSON", body: []byte(valid + `{}`), contentType: "application/json", status: http.StatusOK, maximum: 32 << 10},
		{name: "size", body: []byte(valid), contentType: "application/json", status: http.StatusOK, maximum: uint32(len(valid) - 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, hits := callBenchmarkManifestServer(t, test.body, test.contentType, test.status, test.maximum)
			if !strings.Contains(string(response), `"status":"error"`) || hits != 1 {
				t.Fatalf("transport failure accepted: response=%s hits=%d", response, hits)
			}
		})
	}
	t.Run("request failure", func(t *testing.T) {
		if response := callBenchmarkManifestTransportError(t); !strings.Contains(string(response), `"status":"error"`) {
			t.Fatalf("transport error accepted: response=%s", response)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var hits atomic.Uint32
		registry := NewRegistry()
		if err := RegisterBenchmarkManifest(registry, BenchmarkManifestPolicy{Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
			t.Fatal(err)
		}
		installSourceTransport(t, registry, benchmarkManifestCapability, sourceRoundTripFunc(func(*http.Request) (*http.Response, error) {
			hits.Add(1)
			response := sourceHTTPResponse(http.StatusFound, "application/json", []byte(valid))
			response.Header.Set("Location", "https://other.test/manifest")
			return response, nil
		}))
		plan, err := registry.Seal(PlanConfig{MaxCalls: 1})
		if err != nil {
			t.Fatal(err)
		}
		broker, err := NewBroker(Config{RunIdentity: "redirect-benchmark", Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.benchmark_manifest","arguments":{}}`))
		if err != nil || !strings.Contains(string(response), `"status":"error"`) || hits.Load() != 1 {
			t.Fatalf("redirect accepted: response=%s err=%v hits=%d", response, err, hits.Load())
		}
	})
}

func TestBenchmarkManifestOfflinePlaybackRevalidatesSemanticIDs(t *testing.T) {
	policy := BenchmarkManifestPolicy{Endpoint: "http://127.0.0.1:1/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}
	registry := NewRegistry()
	if err := RegisterBenchmarkManifestPlayback(registry, policy); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(compactJSON(t, strings.Replace(compactJSON(t, benchmarkManifestFixtureJSON(t)), `}],"tags"`, `},{"id":"quality","title":"Other","direction":"minimize","unit":"count","bounds":{"minimum":0,"maximum":1}}],"tags"`, 1)))
	arguments := json.RawMessage(`{}`)
	entry := TranscriptEntry{
		OperationIndex: 0, Capability: "sources.benchmark_manifest",
		Arguments: arguments, ArgumentsSHA256: testSHA256(arguments),
		Result: result, ResultSHA256: testSHA256(result),
		Evidence: TransportEvidence{Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: testSHA256(result)},
	}
	broker, err := NewBroker(Config{RunIdentity: "offline-benchmark", Plan: plan, Playback: &PlaybackConfig{Entries: []TranscriptEntry{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"offline","capability":"sources.benchmark_manifest","arguments":{}}`))
	if err != nil || !strings.Contains(string(response), `"code":"invalid_result"`) || broker.Finalize(false) == nil {
		t.Fatalf("semantic-invalid playback accepted: response=%s err=%v", response, err)
	}
}

func TestDemoCatalogAndBenchmarkManifestShareOneSealedSourceModule(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterDemoCatalog(registry, DemoCatalogPolicy{Endpoint: "https://demo.test/catalog", Timeout: time.Second, MaxResponseBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterBenchmarkManifest(registry, BenchmarkManifestPolicy{Endpoint: "https://benchmark.test/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
		t.Fatal(err)
	}
	installSourceTransport(t, registry, demoCatalogCapability, sourceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://demo.test/catalog" {
			t.Fatalf("demo URL=%s", request.URL)
		}
		return sourceHTTPResponse(http.StatusOK, "application/json", []byte(`{"items":[{"id":"a","title":"Alpha","score":1}]}`)), nil
	}))
	installSourceTransport(t, registry, benchmarkManifestCapability, sourceRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://benchmark.test/manifest" {
			t.Fatalf("benchmark URL=%s", request.URL)
		}
		return sourceHTTPResponse(http.StatusOK, "application/json", []byte(benchmarkManifestFixtureJSON(t))), nil
	}))
	plan, err := registry.Seal(PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if specs := plan.Specs(); len(specs) != 2 || !strings.Contains(plan.PythonPrelude(), "sources.demo_catalog") || !strings.Contains(plan.PythonPrelude(), "sources.benchmark_manifest") {
		t.Fatalf("multi-source plan=%#v prelude=%s", specs, plan.PythonPrelude())
	}
	broker, err := NewBroker(Config{RunIdentity: "multi-source", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		`{"call_id":"demo","capability":"sources.demo_catalog","arguments":{}}`,
		`{"call_id":"benchmark","capability":"sources.benchmark_manifest","arguments":{}}`,
	} {
		response, err := broker.Call(context.Background(), []byte(call))
		if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
			t.Fatalf("call=%s response=%s err=%v", call, response, err)
		}
	}
	transcript := broker.SnapshotTranscript()
	if len(transcript) != 2 || transcript[0].Capability != "sources.demo_catalog" || transcript[1].Capability != "sources.benchmark_manifest" {
		t.Fatalf("transcript=%+v", transcript)
	}

	offlineRegistry := NewRegistry()
	demoPolicy := DemoCatalogPolicy{Endpoint: "https://demo.test/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	demoSpec, demoGrant, err := DemoCatalogDefinition(demoPolicy)
	if err != nil || offlineRegistry.Register(demoSpec, demoGrant, NewPlaybackHandler()) != nil {
		t.Fatalf("register offline demo source: %v", err)
	}
	benchmarkPolicy := BenchmarkManifestPolicy{Endpoint: "https://benchmark.test/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}
	if err := RegisterBenchmarkManifestPlayback(offlineRegistry, benchmarkPolicy); err != nil {
		t.Fatal(err)
	}
	offlinePlan, err := offlineRegistry.Seal(PlanConfig{MaxCalls: 2})
	if err != nil || offlinePlan.Identity() != plan.Identity() {
		t.Fatalf("offline plan=%v live=%v err=%v", offlinePlan.Identity(), plan.Identity(), err)
	}
	offline, err := NewBroker(Config{RunIdentity: "multi-source-offline", Plan: offlinePlan, Playback: &PlaybackConfig{Entries: transcript}})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		`{"call_id":"offline-demo","capability":"sources.demo_catalog","arguments":{}}`,
		`{"call_id":"offline-benchmark","capability":"sources.benchmark_manifest","arguments":{}}`,
	} {
		response, err := offline.Call(context.Background(), []byte(call))
		if err != nil || !strings.Contains(string(response), `"status":"ok"`) {
			t.Fatalf("offline call=%s response=%s err=%v", call, response, err)
		}
	}
	if err := offline.Finalize(true); err != nil {
		t.Fatal(err)
	}
}

func callBenchmarkManifestServer(t *testing.T, body []byte, contentType string, status int, maximum uint32) ([]byte, uint32) {
	t.Helper()
	var hits atomic.Uint32
	transport := sourceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		hits.Add(1)
		return sourceHTTPResponse(status, contentType, body), nil
	})
	registry := NewRegistry()
	if err := RegisterBenchmarkManifest(registry, BenchmarkManifestPolicy{Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: maximum}); err != nil {
		t.Fatal(err)
	}
	installSourceTransport(t, registry, benchmarkManifestCapability, transport)
	plan, err := registry.Seal(PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewBroker(Config{RunIdentity: "invalid-benchmark", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.benchmark_manifest","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	return response, hits.Load()
}

func compactJSON(t *testing.T, raw string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func benchmarkManifestFixtureJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/benchmark-manifest.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type sourceRoundTripFunc func(*http.Request) (*http.Response, error)

func (function sourceRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func sourceHTTPResponse(status int, contentType string, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func installSourceTransport(t *testing.T, registry *Registry, capabilityName string, transport http.RoundTripper) {
	t.Helper()
	registered, ok := registry.registrations[capabilityName]
	if !ok {
		t.Fatalf("source %s is not registered", capabilityName)
	}
	switch handler := registered.handler.(type) {
	case *demoCatalogHandler:
		handler.source.client.Transport = transport
	case *benchmarkManifestHandler:
		handler.source.client.Transport = transport
	default:
		t.Fatalf("source %s has handler %T", capabilityName, registered.handler)
	}
}

func mustBenchmarkJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func callBenchmarkManifestTransportError(t *testing.T) []byte {
	t.Helper()
	registry := NewRegistry()
	if err := RegisterBenchmarkManifest(registry, BenchmarkManifestPolicy{Endpoint: "https://source.test/manifest", Timeout: time.Second, MaxResponseBytes: 32 << 10}); err != nil {
		t.Fatal(err)
	}
	installSourceTransport(t, registry, benchmarkManifestCapability, sourceRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("source unavailable")
	}))
	plan, err := registry.Seal(PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewBroker(Config{RunIdentity: "transport-error", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.benchmark_manifest","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func testSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}
