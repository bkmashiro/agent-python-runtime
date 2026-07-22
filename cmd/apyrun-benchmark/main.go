package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const providerDelay = 2 * time.Millisecond

var lifecyclePhases = []string{"instantiate_guest", "_initialize", "runtime_init", "prepare", "execute"}

type lifecycleCollector struct {
	mutex        sync.Mutex
	observations []wazeroengine.Observation
}

func (collector *lifecycleCollector) observe(observation wazeroengine.Observation) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	collector.observations = append(collector.observations, observation)
}

func (collector *lifecycleCollector) drain() []wazeroengine.Observation {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	result := append([]wazeroengine.Observation(nil), collector.observations...)
	collector.observations = nil
	return result
}

type capabilityCollector struct {
	mutex        sync.Mutex
	observations []capability.Observation
}

func (collector *capabilityCollector) observe(observation capability.Observation) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	collector.observations = append(collector.observations, observation)
}

func (collector *capabilityCollector) drain() []capability.Observation {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	result := append([]capability.Observation(nil), collector.observations...)
	collector.observations = nil
	return result
}

type benchmarkOptions struct {
	ArtifactPath string
	ManifestPath string
	OutputPath   string
	Class        string
	Samples      int
}

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runMain(args []string) error {
	flags := flag.NewFlagSet("apyrun-benchmark", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	options := benchmarkOptions{}
	flags.StringVar(&options.ArtifactPath, "artifact", "", "verified guest artifact")
	flags.StringVar(&options.ManifestPath, "manifest", "", "matching artifact manifest")
	flags.StringVar(&options.OutputPath, "output", "", "JSON evidence output")
	flags.StringVar(&options.Class, "class", "production-safe", "production-safe or full")
	flags.IntVar(&options.Samples, "samples", 3, "sample count (3-20)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath == "" {
		return errors.New("usage: apyrun-benchmark -artifact <guest.wasm> -manifest <manifest.json> -output <evidence.json> [-class production-safe|full] [-samples 3]")
	}
	if options.Class != "production-safe" && options.Class != "full" {
		return errors.New("benchmark class must be production-safe or full")
	}
	if options.Samples < 3 || options.Samples > 20 {
		return errors.New("samples must be between 3 and 20")
	}
	evidence, err := runBenchmark(options)
	if err != nil {
		return err
	}
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate benchmark evidence: %w", err)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"output\":%q,\"source_commit\":%q}\n", options.OutputPath, evidence.Artifact.SourceCommit)
	return nil
}

func runBenchmark(options benchmarkOptions) (benchmarkEvidence, error) {
	identity, wasm, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return benchmarkEvidence{}, err
	}
	hostSource, err := currentHostSource()
	if err != nil {
		return benchmarkEvidence{}, err
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
		Name:               capability.FetchManyCapability,
		MaxCalls:           1,
		MaxRequestsPerCall: uint32(operations),
		MaxTotalRequests:   uint32(operations),
		MaxConcurrency:     uint32(min(operations, 8)),
		MaxResponseBytes:   1024 * 1024,
		PerRequestTimeout:  5 * time.Second,
		Targets: map[string]capability.TargetGrant{
			"fixture": {BaseURL: server.URL},
		},
	}
	factory := wazeroengine.Factory{
		Observer: lifecycle.observe,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			return capability.NewBroker(capability.Config{
				RunIdentity: fmt.Sprintf("benchmark-run-%d", runIdentity.Add(1)),
				Grants:      map[string]capability.Grant{grant.Name: grant},
				Observer:    capabilities.observe,
			}, capability.NewHTTPFetcher(server.Client()))
		},
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 30 * time.Second
	runner, err := factory.New(context.Background(), wasm, config)
	if err != nil {
		return benchmarkEvidence{}, fmt.Errorf("create benchmark runner: %w", err)
	}
	defer runner.Close(context.Background())

	compile, err := compileFromObservations(lifecycle.drain())
	if err != nil {
		return benchmarkEvidence{}, err
	}
	evidence := benchmarkEvidence{
		SchemaVersion: 1,
		EvidenceClass: options.Class,
		Artifact:      identity,
		HostSource:    hostSource,
		Backend: backendIdentity{
			Name:      runner.Properties().Backend,
			ResetMode: string(runner.Properties().ResetMode),
		},
		Environment: environmentIdentity{
			GOOS:      goruntime.GOOS,
			GOARCH:    goruntime.GOARCH,
			GoVersion: goruntime.Version(),
		},
		Fixture: fixtureIdentity{
			Samples:                  options.Samples,
			CapabilityOperations:     operations,
			ProviderDelayNanoseconds: providerDelay.Nanoseconds(),
		},
		CompileOnce: compile,
		Limitations: []string{
			"Local synthetic IP-loopback provider; excludes production DNS, TCP, TLS, and provider behavior.",
			"Compile is measured once; every workload sample still uses a fresh guest instance.",
			"Measurements are evidence for this artifact, backend, host, class, and command only.",
		},
	}

	for sample := 0; sample < options.Samples; sample++ {
		executeRequest, err := makeRequest(
			fmt.Sprintf("execute-%d", sample),
			`result = {"prepared": prepared, "sum": sum(range(inputs["integer_work"]))}`,
			map[string]any{"integer_work": integerWork},
		)
		if err != nil {
			return benchmarkEvidence{}, err
		}
		executeSample, err := runSample(runner, lifecycle, capabilities, executeRequest, "prepared = 41", false)
		if err != nil {
			return benchmarkEvidence{}, err
		}
		evidence.Workloads.Execute = append(evidence.Workloads.Execute, executeSample)

		capabilityRequest, err := makeRequest(
			fmt.Sprintf("capability-%d", sample),
			`from agent_runtime import tools
requests = [{"request_id": f"r{index}", "target": "fixture", "path": f"/value?index={index}"} for index in range(inputs["operations"])]
items = tools.fetch_many(requests)
result = {"prepared": prepared, "sum": sum(__import__("json").loads(item["body"])["value"] for item in items)}`,
			map[string]any{"operations": operations},
		)
		if err != nil {
			return benchmarkEvidence{}, err
		}
		capabilitySample, err := runSample(runner, lifecycle, capabilities, capabilityRequest, "prepared = 41", true)
		if err != nil {
			return benchmarkEvidence{}, err
		}
		evidence.Workloads.Capability = append(evidence.Workloads.Capability, capabilitySample)
	}
	if providerCalls.Load() != int64(options.Samples*operations) {
		return benchmarkEvidence{}, fmt.Errorf("provider calls=%d, want %d", providerCalls.Load(), options.Samples*operations)
	}
	return evidence, nil
}

func compileFromObservations(observations []wazeroengine.Observation) (compileEvidence, error) {
	if len(observations) != 2 || observations[0].Phase != "instantiate_host" || observations[1].Phase != "compile" ||
		!observations[0].Success || !observations[1].Success {
		return compileEvidence{}, fmt.Errorf("unexpected compile observations: %#v", observations)
	}
	return compileEvidence{
		InstantiateHostNS: observations[0].Duration.Nanoseconds(),
		CompileNS:         observations[1].Duration.Nanoseconds(),
	}, nil
}

func runSample(
	runner interface {
		Run(context.Context, []byte, string) ([]byte, error)
	},
	lifecycle *lifecycleCollector,
	capabilities *capabilityCollector,
	request []byte,
	prepare string,
	requireCapability bool,
) (sampleEvidence, error) {
	lifecycle.drain()
	capabilities.drain()
	started := time.Now()
	response, err := runner.Run(context.Background(), request, prepare)
	runTotal := time.Since(started)
	if err != nil {
		return sampleEvidence{}, err
	}
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
		return sampleEvidence{}, fmt.Errorf("benchmark workload failed: %s", response)
	}
	observations := lifecycle.drain()
	if len(observations) != len(lifecyclePhases) {
		return sampleEvidence{}, fmt.Errorf("lifecycle observations=%#v", observations)
	}
	durations := map[string]int64{}
	for index, observation := range observations {
		if observation.Phase != lifecyclePhases[index] || !observation.Success {
			return sampleEvidence{}, fmt.Errorf("unexpected lifecycle observation: %#v", observations)
		}
		durations[observation.Phase] = observation.Duration.Nanoseconds()
	}
	capabilityNS := int64(0)
	capabilityObservations := capabilities.drain()
	if requireCapability {
		if len(capabilityObservations) != 1 || !capabilityObservations[0].Success || capabilityObservations[0].Capability != capability.FetchManyCapability {
			return sampleEvidence{}, fmt.Errorf("unexpected capability observations: %#v", capabilityObservations)
		}
		capabilityNS = capabilityObservations[0].Duration.Nanoseconds()
	} else if len(capabilityObservations) != 0 {
		return sampleEvidence{}, fmt.Errorf("execute-only workload used capability: %#v", capabilityObservations)
	}
	return sampleEvidence{
		InstantiateGuestNS: durations["instantiate_guest"],
		InitializeNS:       durations["_initialize"],
		RuntimeInitNS:      durations["runtime_init"],
		PrepareNS:          durations["prepare"],
		ExecuteNS:          durations["execute"],
		CapabilityNS:       capabilityNS,
		RunTotalNS:         runTotal.Nanoseconds(),
		RequestBytes:       len(request),
		ResultBytes:        len(response),
	}, nil
}

func makeRequest(runID, code string, inputs any) ([]byte, error) {
	return json.Marshal(map[string]any{"run_id": runID, "code": code, "inputs": inputs})
}

func currentHostSource() (hostSourceIdentity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return hostSourceIdentity{}, errors.New("Host VCS build information is unavailable")
	}
	identity := hostSourceIdentity{}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			identity.Revision = setting.Value
		case "vcs.modified":
			modified, err := strconv.ParseBool(setting.Value)
			if err != nil {
				return hostSourceIdentity{}, errors.New("Host VCS modified flag is invalid")
			}
			identity.Modified = modified
		}
	}
	if identity.Revision == "" {
		return hostSourceIdentity{}, errors.New("Host VCS revision is unavailable")
	}
	if identity.Modified {
		return hostSourceIdentity{}, errors.New("benchmark evidence requires a clean Host source revision")
	}
	return identity, nil
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
