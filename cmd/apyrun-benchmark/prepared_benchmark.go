package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type preparedBenchmarkRunner interface {
	enginecontract.Runner
	PreparedReady() int
	PreparedRetainedGuestMemoryBytes() uint64
}

func runPreparedBenchmark(options benchmarkOptions) (preparedBenchmarkEvidence, error) {
	hostSource, err := currentHostSource()
	if err != nil {
		return preparedBenchmarkEvidence{}, err
	}
	return runPreparedBenchmarkWithHostSource(options, hostSource)
}

func runPreparedBenchmarkWithHostSource(options benchmarkOptions, hostSource hostSourceIdentity) (preparedBenchmarkEvidence, error) {
	identity, wasm, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return preparedBenchmarkEvidence{}, err
	}
	operations := 1
	integerWork := 1_000
	if options.Class == "full" {
		operations = 20
		integerWork = 100_000
	}

	var providerCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		time.Sleep(providerDelay)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"value":1}`))
	}))
	defer server.Close()

	lifecycle := &lifecycleCollector{}
	capabilities := &capabilityCollector{}
	var runIdentity atomic.Uint64
	grant := capability.Grant{
		Name: capability.FetchManyCapability, MaxCalls: 1,
		MaxRequestsPerCall: uint32(operations), MaxTotalRequests: uint32(operations),
		MaxConcurrency: uint32(min(operations, 8)), MaxResponseBytes: 1024 * 1024,
		PerRequestTimeout: 5 * time.Second,
		Targets:           map[string]capability.TargetGrant{"fixture": {BaseURL: server.URL}},
	}
	factory := wazeroengine.Factory{
		PreparedCapacity: 1,
		Observer:         lifecycle.observe,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{
				RunIdentity: fmt.Sprintf("prepared-benchmark-run-%d", runIdentity.Add(1)),
				Grants:      map[string]capability.Grant{grant.Name: grant}, Observer: capabilities.observe,
			}, capability.NewHTTPFetcher(server.Client()))
		},
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 60 * time.Second
	factoryStarted := time.Now()
	neutralRunner, err := factory.New(context.Background(), wasm, config)
	factoryTotal := time.Since(factoryStarted)
	if err != nil {
		return preparedBenchmarkEvidence{}, fmt.Errorf("create prepared benchmark runner: %w", err)
	}
	defer neutralRunner.Close(context.Background())
	runner, ok := neutralRunner.(preparedBenchmarkRunner)
	if !ok {
		return preparedBenchmarkEvidence{}, fmt.Errorf("wazero prepared diagnostics are unavailable")
	}
	compile, readiness, err := preparedStartupEvidence(lifecycle.drain(), factoryTotal, runner)
	if err != nil {
		return preparedBenchmarkEvidence{}, err
	}

	evidence := preparedBenchmarkEvidence{
		SchemaVersion: 1, EvidenceKind: "single-use-preinitialized", EvidenceClass: options.Class,
		Artifact: identity, HostSource: hostSource,
		Backend:     backendIdentity{Name: runner.Properties().Backend, ResetMode: string(runner.Properties().ResetMode)},
		Environment: environmentIdentity{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH, GoVersion: goruntime.Version()},
		Fixture:     preparedFixtureIdentity{Samples: options.Samples, CapabilityOperations: operations, ProviderDelayNanoseconds: providerDelay.Nanoseconds(), PreparedCapacity: 1},
		CompileOnce: compile, Readiness: readiness,
		StateCopy: stateCopyEvidence{Applicable: false, Reason: "single-use instances are never restored or copied after serving a Run"},
		Limitations: []string{
			"Local synthetic IP-loopback provider; excludes production DNS, TCP, TLS, and provider behavior.",
			"Queued-memory evidence covers guest linear memory only; it excludes compiled code, Go runtime, WASI, and other Host overhead.",
			"Single-use preparation moves trusted initialization off the Run path but still pays one full refill per served instance.",
			"No state copy or restore exists, so copy cost is not applicable rather than measured as zero.",
			"Measurements are evidence for this artifact, backend, host, class, and command only.",
		},
	}

	executeRequest, err := makeRequest("prepared-first-execute", `result = {"prepared": prepared, "sum": sum(range(inputs["integer_work"]))}`, map[string]any{"integer_work": integerWork})
	if err != nil {
		return preparedBenchmarkEvidence{}, err
	}
	evidence.Workloads.FirstExecute, err = runPreparedSample(runner, lifecycle, capabilities, executeRequest, "prepared = 41", false)
	if err != nil {
		return preparedBenchmarkEvidence{}, err
	}
	for sample := 0; sample < options.Samples; sample++ {
		executeRequest, err = makeRequest(fmt.Sprintf("prepared-steady-execute-%d", sample), `result = {"prepared": prepared, "sum": sum(range(inputs["integer_work"]))}`, map[string]any{"integer_work": integerWork})
		if err != nil {
			return preparedBenchmarkEvidence{}, err
		}
		executeSample, err := runPreparedSample(runner, lifecycle, capabilities, executeRequest, "prepared = 41", false)
		if err != nil {
			return preparedBenchmarkEvidence{}, err
		}
		evidence.Workloads.SteadyExecute = append(evidence.Workloads.SteadyExecute, executeSample)

		capabilityRequest, err := makeRequest(fmt.Sprintf("prepared-steady-capability-%d", sample), `from agent_runtime import tools
requests = [{"request_id": f"r{index}", "target": "fixture", "path": f"/value?index={index}"} for index in range(inputs["operations"])]
items = tools.fetch_many(requests)
result = {"prepared": prepared, "sum": sum(__import__("json").loads(item["body"])["value"] for item in items)}`, map[string]any{"operations": operations})
		if err != nil {
			return preparedBenchmarkEvidence{}, err
		}
		capabilitySample, err := runPreparedSample(runner, lifecycle, capabilities, capabilityRequest, "prepared = 41", true)
		if err != nil {
			return preparedBenchmarkEvidence{}, err
		}
		evidence.Workloads.SteadyCapability = append(evidence.Workloads.SteadyCapability, capabilitySample)
	}
	if providerCalls.Load() != int64(options.Samples*operations) {
		return preparedBenchmarkEvidence{}, fmt.Errorf("provider calls=%d, want %d", providerCalls.Load(), options.Samples*operations)
	}
	return evidence, nil
}

func preparedStartupEvidence(observations []wazeroengine.Observation, total time.Duration, runner preparedBenchmarkRunner) (compileEvidence, preparedReadinessEvidence, error) {
	want := []string{"instantiate_host", "compile", "pool_prepare_instantiate_guest", "pool_prepare__initialize", "pool_prepare_runtime_init"}
	if len(observations) != len(want) {
		return compileEvidence{}, preparedReadinessEvidence{}, fmt.Errorf("unexpected prepared startup observations: %#v", observations)
	}
	durations := map[string]int64{}
	for index, observation := range observations {
		if observation.Phase != want[index] || !observation.Success {
			return compileEvidence{}, preparedReadinessEvidence{}, fmt.Errorf("unexpected prepared startup observations: %#v", observations)
		}
		durations[observation.Phase] = observation.Duration.Nanoseconds()
	}
	if runner.PreparedReady() != 1 || runner.PreparedRetainedGuestMemoryBytes() == 0 {
		return compileEvidence{}, preparedReadinessEvidence{}, fmt.Errorf("prepared candidate is not ready")
	}
	return compileEvidence{InstantiateHostNS: durations["instantiate_host"], CompileNS: durations["compile"]}, preparedReadinessEvidence{
		FactoryNewTotalNS: total.Nanoseconds(), InstantiateGuestNS: durations["pool_prepare_instantiate_guest"],
		InitializeNS: durations["pool_prepare__initialize"], RuntimeInitNS: durations["pool_prepare_runtime_init"],
		ReadyInstances: runner.PreparedReady(), RetainedGuestMemoryBytes: runner.PreparedRetainedGuestMemoryBytes(),
	}, nil
}

func runPreparedSample(runner preparedBenchmarkRunner, lifecycle *lifecycleCollector, capabilities *capabilityCollector, request []byte, prepare string, requireCapability bool) (preparedSampleEvidence, error) {
	if runner.PreparedReady() != 1 {
		return preparedSampleEvidence{}, fmt.Errorf("prepared candidate missing before sample")
	}
	lifecycle.drain()
	capabilities.drain()
	started := time.Now()
	response, err := runner.Run(context.Background(), request, prepare)
	runTotal := time.Since(started)
	if err != nil {
		return preparedSampleEvidence{}, err
	}
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
		return preparedSampleEvidence{}, fmt.Errorf("prepared benchmark workload failed: %s", response)
	}
	refillWaitStarted := time.Now()
	deadline := time.Now().Add(60 * time.Second)
	for runner.PreparedReady() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	refillWait := time.Since(refillWaitStarted)
	if runner.PreparedReady() != 1 || runner.PreparedRetainedGuestMemoryBytes() == 0 {
		return preparedSampleEvidence{}, fmt.Errorf("prepared pool did not refill")
	}

	want := map[string]bool{
		"pool_hit": false, "prepare": false, "execute": false,
		"pool_prepare_instantiate_guest": false, "pool_prepare__initialize": false, "pool_prepare_runtime_init": false,
	}
	durations := map[string]int64{}
	observations := lifecycle.drain()
	for _, observation := range observations {
		seen, expected := want[observation.Phase]
		if !expected || seen || !observation.Success {
			return preparedSampleEvidence{}, fmt.Errorf("unexpected prepared lifecycle observations: %#v", observations)
		}
		want[observation.Phase] = true
		durations[observation.Phase] = observation.Duration.Nanoseconds()
	}
	for phase, seen := range want {
		if !seen {
			return preparedSampleEvidence{}, fmt.Errorf("prepared lifecycle phase %s is missing: %#v", phase, observations)
		}
	}
	capabilityNS := int64(0)
	capabilityObservations := capabilities.drain()
	if requireCapability {
		if len(capabilityObservations) != 1 || !capabilityObservations[0].Success || capabilityObservations[0].Capability != capability.FetchManyCapability {
			return preparedSampleEvidence{}, fmt.Errorf("unexpected capability observations: %#v", capabilityObservations)
		}
		capabilityNS = capabilityObservations[0].Duration.Nanoseconds()
	} else if len(capabilityObservations) != 0 {
		return preparedSampleEvidence{}, fmt.Errorf("execute-only prepared workload used capability: %#v", capabilityObservations)
	}
	return preparedSampleEvidence{
		PoolHitNS: durations["pool_hit"], PrepareNS: durations["prepare"], ExecuteNS: durations["execute"], CapabilityNS: capabilityNS,
		RunTotalNS: runTotal.Nanoseconds(), RefillInstantiateGuestNS: durations["pool_prepare_instantiate_guest"],
		RefillInitializeNS: durations["pool_prepare__initialize"], RefillRuntimeInitNS: durations["pool_prepare_runtime_init"],
		RefillReadyAfterRunNS: refillWait.Nanoseconds(), RequestBytes: len(request), ResultBytes: len(response),
		RetainedGuestMemoryBytes: runner.PreparedRetainedGuestMemoryBytes(),
	}, nil
}
