package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
)

const plmEconomicsSchema = "pysolate.plm-economics.v1"

type plmEconomicsSample struct {
	Mode             string                               `json:"mode"`
	Profile          string                               `json:"profile"`
	Iteration        int                                  `json:"iteration"`
	TotalNanos       uint64                               `json:"total_nanos"`
	EngineSetupNanos uint64                               `json:"engine_setup_nanos"`
	AllocatedBytes   uint64                               `json:"allocated_bytes"`
	ProviderNanos    uint64                               `json:"provider_nanos"`
	Lifecycle        wazeroengine.PLMRunLifecycleEvidence `json:"lifecycle"`
	Candidates       capability.SplitPhaseSnapshot        `json:"candidates"`
}

type plmEconomicsProfile struct {
	Name                string               `json:"name"`
	BaselineMedianNanos uint64               `json:"baseline_median_nanos"`
	PLMMedianNanos      uint64               `json:"plm_median_nanos"`
	DeltaPercent        float64              `json:"delta_percent"`
	Samples             []plmEconomicsSample `json:"samples"`
}

type plmEconomicsEvidence struct {
	SchemaVersion  string                `json:"schema_version"`
	TargetCommit   string                `json:"target_commit"`
	ArtifactSHA256 string                `json:"artifact_sha256"`
	SourceSHA256   string                `json:"source_sha256"`
	RunsPerArm     int                   `json:"runs_per_arm"`
	ProviderDelay  string                `json:"provider_delay"`
	Profiles       []plmEconomicsProfile `json:"profiles"`
}

func TestRealGuestPLMEconomicsFixture(t *testing.T) {
	output := os.Getenv("PLM_ECONOMICS_OUTPUT")
	if output == "" {
		t.Skip("set PLM_ECONOMICS_OUTPUT to run the bounded matched fixture")
	}
	runs, err := strconv.Atoi(os.Getenv("PLM_ECONOMICS_RUNS"))
	if err != nil || runs < 3 || runs > 20 {
		t.Fatalf("PLM_ECONOMICS_RUNS must be in [3,20]")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	source := plmEconomicsSource(750)
	profiles := make([]plmEconomicsProfile, 0, 2)
	for _, profile := range []string{"cold_end_to_end", "engine_precompiled"} {
		samples := make([]plmEconomicsSample, 0, 2*runs)
		for iteration := 0; iteration < runs; iteration++ {
			order := []string{"baseline", "plm"}
			if iteration%2 == 1 {
				order[0], order[1] = order[1], order[0]
			}
			for _, mode := range order {
				samples = append(samples, runPLMEconomicsSample(t, artifact, source, mode, profile, iteration))
			}
		}
		baselineMedian := plmMedian(samples, "baseline")
		plmMedianNanos := plmMedian(samples, "plm")
		profiles = append(profiles, plmEconomicsProfile{
			Name: profile, BaselineMedianNanos: baselineMedian, PLMMedianNanos: plmMedianNanos,
			DeltaPercent: 100 * (float64(plmMedianNanos) - float64(baselineMedian)) / float64(baselineMedian), Samples: samples,
		})
	}
	artifactDigest := sha256.Sum256(artifact)
	sourceDigest := sha256.Sum256([]byte(source))
	evidence := plmEconomicsEvidence{
		SchemaVersion: plmEconomicsSchema, TargetCommit: os.Getenv("PLM_TARGET_COMMIT"),
		ArtifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]), SourceSHA256: fmt.Sprintf("sha256:%x", sourceDigest[:]),
		RunsPerArm: runs, ProviderDelay: "75ms", Profiles: profiles,
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(output, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("PLM_ECONOMICS %s", encoded)
}

func runPLMEconomicsSample(t *testing.T, artifact []byte, source, mode, profile string, iteration int) plmEconomicsSample {
	t.Helper()
	runID := fmt.Sprintf("plm-economics-%s-%s-%d", profile, mode, iteration)
	var providerNanos atomic.Uint64
	adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		started := time.Now()
		time.Sleep(75 * time.Millisecond)
		providerNanos.Add(uint64(time.Since(started)))
		return json.RawMessage(`{"body":"fixture"}`), nil
	})}
	plan := plmE2EPlan(t, 1, adapter)
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	plugins := unifiedPassCatalog(t)
	if mode == "plm" {
		plugins, err = plugins.Enable(sourcepatch.PLMCapabilityCallsName)
		if err != nil {
			t.Fatal(err)
		}
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	var beforeSetup goruntime.MemStats
	goruntime.ReadMemStats(&beforeSetup)
	setupStarted := time.Now()
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	setupNanos := uint64(time.Since(setupStarted))
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	var beforeTimed goruntime.MemStats
	goruntime.ReadMemStats(&beforeTimed)
	timedStarted := time.Now()
	if profile == "cold_end_to_end" {
		timedStarted = setupStarted
	}
	if mode == "plm" {
		execution, runErr := plugins.ExecuteCapabilityHostScheduled(context.Background(), sourcepatch.PLMCapabilityCallsName, engine, request,
			plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan))
		if runErr != nil || !execution.Applied {
			t.Fatalf("mode=%s execution=%+v err=%v", mode, execution, runErr)
		}
		if _, runErr = decodeSuccessfulGuestResult(execution.Payload); runErr != nil {
			t.Fatal(runErr)
		}
	} else {
		payload, runErr := engine.Run(context.Background(), request, plan.PythonPrelude())
		if runErr != nil {
			t.Fatal(runErr)
		}
		if _, runErr = decodeSuccessfulGuestResult(payload); runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	totalNanos := uint64(time.Since(timedStarted))
	var after goruntime.MemStats
	goruntime.ReadMemStats(&after)
	allocated := after.TotalAlloc - beforeTimed.TotalAlloc
	if profile == "cold_end_to_end" {
		allocated = after.TotalAlloc - beforeSetup.TotalAlloc
	}
	if broker == nil || broker.CallCount() != 1 {
		t.Fatalf("mode=%s broker=%v", mode, broker)
	}
	return plmEconomicsSample{
		Mode: mode, Profile: profile, Iteration: iteration, TotalNanos: totalNanos, EngineSetupNanos: setupNanos,
		AllocatedBytes: allocated, ProviderNanos: providerNanos.Load(), Lifecycle: engine.PLMRunLifecycleEvidence(), Candidates: engine.SplitPhaseEvidence(),
	}
}

func plmEconomicsSource(statements int) string {
	var source strings.Builder
	source.WriteString("x0 = 0\n")
	for index := 1; index <= statements; index++ {
		fmt.Fprintf(&source, "x%d = x%d + 1\n", index, index-1)
	}
	fmt.Fprintf(&source, "value = sources.read(\"alpha\")\nresult = [value, x%d]\n", statements)
	return source.String()
}

func plmMedian(samples []plmEconomicsSample, mode string) uint64 {
	values := make([]uint64, 0, len(samples)/2)
	for _, sample := range samples {
		if sample.Mode == mode {
			values = append(values, sample.TotalNanos)
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	return values[len(values)/2]
}
