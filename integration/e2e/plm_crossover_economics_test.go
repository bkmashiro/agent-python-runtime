package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

const plmCrossoverEconomicsSchema = "pysolate.plm-crossover-economics.v1"

type plmCrossoverConfig struct {
	Output                string
	Runs                  int
	ReadCounts            []int
	DelaysMS              []int
	ZeroRead              bool
	TargetCommit          string
	SourceTree            string
	ArtifactSourceCommit  string
	EvaluationHostID      string
	EvaluationOrderOffset int
}

type plmCrossoverSample struct {
	Mode                   string                               `json:"mode"`
	Profile                string                               `json:"profile"`
	PairIteration          int                                  `json:"pair_iteration"`
	ReadCount              int                                  `json:"read_count"`
	DelayMS                int                                  `json:"delay_ms"`
	SourceSHA256           string                               `json:"source_sha256"`
	TotalNanos             uint64                               `json:"total_nanos"`
	EngineSetupNanos       uint64                               `json:"engine_setup_nanos"`
	AllocatedBytes         uint64                               `json:"allocated_bytes"`
	ProviderNanos          uint64                               `json:"provider_nanos"`
	ProviderStarts         uint32                               `json:"provider_starts"`
	ProviderMaxConcurrency uint32                               `json:"provider_max_concurrency"`
	CallCount              uint32                               `json:"call_count"`
	Result                 json.RawMessage                      `json:"result"`
	Lifecycle              wazeroengine.PLMRunLifecycleEvidence `json:"lifecycle"`
	Candidates             capability.SplitPhaseSnapshot        `json:"candidates"`
}

type plmCrossoverComparison struct {
	ReadCount           int     `json:"read_count"`
	DelayMS             int     `json:"delay_ms"`
	BaselineMedianNanos uint64  `json:"baseline_median_nanos"`
	PLMMedianNanos      uint64  `json:"plm_median_nanos"`
	DeltaPercent        float64 `json:"delta_percent"`
}

type plmCrossoverProfile struct {
	Name        string                   `json:"name"`
	Comparisons []plmCrossoverComparison `json:"comparisons"`
	Samples     []plmCrossoverSample     `json:"samples"`
}

type plmCrossoverEvidence struct {
	SchemaVersion         string                `json:"schema_version"`
	TargetCommit          string                `json:"target_commit"`
	SourceTree            string                `json:"source_tree"`
	ArtifactSourceCommit  string                `json:"artifact_source_commit"`
	ArtifactSHA256        string                `json:"artifact_sha256"`
	SourceSHA256          string                `json:"source_sha256"`
	RunsPerArm            int                   `json:"runs_per_arm"`
	ReadCounts            []int                 `json:"read_counts"`
	DelaysMS              []int                 `json:"delays_ms"`
	ZeroRead              bool                  `json:"zero_read"`
	EvaluationHostID      string                `json:"evaluation_host_id"`
	EvaluationOrderOffset int                   `json:"evaluation_order_offset"`
	Profiles              []plmCrossoverProfile `json:"profiles"`
}

func TestPLMCrossoverConfigDefaultsAndValidation(t *testing.T) {
	for _, name := range []string{
		"PLM_CROSSOVER_RUNS", "PLM_CROSSOVER_READ_COUNTS", "PLM_CROSSOVER_DELAYS_MS",
		"PLM_CROSSOVER_ZERO_READ", "PLM_TARGET_COMMIT", "PLM_SOURCE_TREE",
		"PLM_ARTIFACT_SOURCE_COMMIT", "EVALUATION_HOST_ID", "EVALUATION_ORDER_OFFSET",
	} {
		t.Setenv(name, "")
	}
	config, err := plmCrossoverConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runs != 5 || !equalInts(config.ReadCounts, []int{1, 2, 4, 8}) ||
		!equalInts(config.DelaysMS, []int{25, 75, 200}) || !config.ZeroRead || config.EvaluationOrderOffset != 0 {
		t.Fatalf("defaults=%+v", config)
	}

	t.Setenv("PLM_CROSSOVER_RUNS", "2")
	if _, err := plmCrossoverConfigFromEnv(); err == nil {
		t.Fatal("expected runs lower bound error")
	}
}

func TestPLMCrossoverSourceKeeps750AssignmentsAndAddsReads(t *testing.T) {
	source := plmCrossoverSource(4)
	if got := strings.Count(source, " = "); got != 4+750+2 {
		t.Fatalf("assignment count=%d source suffix=%q", got, source[len(source)-120:])
	}
	if !strings.Contains(source, "x750 = x749 + 1\n") ||
		!strings.HasSuffix(source, "result = [value0, value1, value2, value3, x750]\n") {
		t.Fatalf("source=%q", source[len(source)-240:])
	}
	zero := plmCrossoverSource(0)
	if !strings.HasSuffix(zero, "result = [x750]\n") {
		t.Fatalf("zero-read source=%q", zero[len(zero)-120:])
	}
}

func TestPLMCrossoverOrderOffsetAlternatesBaselineAndPLM(t *testing.T) {
	if got := plmCrossoverOrder(0, 0); !equalStrings(got, []string{"baseline", "plm"}) {
		t.Fatalf("offset zero even=%v", got)
	}
	if got := plmCrossoverOrder(1, 0); !equalStrings(got, []string{"plm", "baseline"}) {
		t.Fatalf("offset zero odd=%v", got)
	}
	if got := plmCrossoverOrder(0, 1); !equalStrings(got, []string{"plm", "baseline"}) {
		t.Fatalf("offset one even=%v", got)
	}
}

func TestPLMCrossoverZeroReadRunsOncePerProfile(t *testing.T) {
	if got := plmCrossoverPairIterations(0, 20); got != 1 {
		t.Fatalf("zero-read control pairs = %d, want 1", got)
	}
	if got := plmCrossoverPairIterations(8, 20); got != 20 {
		t.Fatalf("non-zero cell pairs = %d, want 20", got)
	}
}

func plmCrossoverPairIterations(readCount, runs int) int {
	if readCount == 0 {
		return 1
	}
	return runs
}

func TestRealGuestPLMCrossoverEconomicsFixture(t *testing.T) {
	output := strings.TrimSpace(os.Getenv("PLM_CROSSOVER_OUTPUT"))
	if output == "" {
		t.Skip("set PLM_CROSSOVER_OUTPUT to run the parameterized crossover fixture")
	}
	config, err := plmCrossoverConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.TargetCommit == "" || config.SourceTree == "" || config.ArtifactSourceCommit == "" || config.EvaluationHostID == "" {
		t.Fatal("PLM_TARGET_COMMIT, PLM_SOURCE_TREE, PLM_ARTIFACT_SOURCE_COMMIT, and EVALUATION_HOST_ID are required")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}

	profiles := make([]plmCrossoverProfile, 0, 2)
	for _, profile := range []string{"cold_end_to_end", "engine_precompiled"} {
		samples := make([]plmCrossoverSample, 0, 2*config.Runs*len(config.ReadCounts)*len(config.DelaysMS)+2)
		if config.ZeroRead {
			for pairIteration := 0; pairIteration < plmCrossoverPairIterations(0, config.Runs); pairIteration++ {
				samples = append(samples, runPLMCrossoverPair(t, artifact, profile, pairIteration, 0, 0, config.EvaluationOrderOffset)...)
			}
		}
		for _, readCount := range config.ReadCounts {
			for _, delayMS := range config.DelaysMS {
				for pairIteration := 0; pairIteration < plmCrossoverPairIterations(readCount, config.Runs); pairIteration++ {
					samples = append(samples, runPLMCrossoverPair(t, artifact, profile, pairIteration, readCount, delayMS, config.EvaluationOrderOffset)...)
				}
			}
		}
		comparisons := make([]plmCrossoverComparison, 0, len(config.ReadCounts)*len(config.DelaysMS)+1)
		if config.ZeroRead {
			baselineMedian := plmCrossoverMedian(samples, "baseline", 0, 0)
			plmMedian := plmCrossoverMedian(samples, "plm", 0, 0)
			comparisons = append(comparisons, plmCrossoverComparison{
				ReadCount: 0, DelayMS: 0, BaselineMedianNanos: baselineMedian, PLMMedianNanos: plmMedian,
				DeltaPercent: 100 * (float64(plmMedian) - float64(baselineMedian)) / float64(baselineMedian),
			})
		}
		for _, readCount := range config.ReadCounts {
			for _, delayMS := range config.DelaysMS {
				baselineMedian := plmCrossoverMedian(samples, "baseline", readCount, delayMS)
				plmMedian := plmCrossoverMedian(samples, "plm", readCount, delayMS)
				comparisons = append(comparisons, plmCrossoverComparison{
					ReadCount: readCount, DelayMS: delayMS,
					BaselineMedianNanos: baselineMedian, PLMMedianNanos: plmMedian,
					DeltaPercent: 100 * (float64(plmMedian) - float64(baselineMedian)) / float64(baselineMedian),
				})
			}
		}
		profiles = append(profiles, plmCrossoverProfile{Name: profile, Comparisons: comparisons, Samples: samples})
	}

	artifactDigest := sha256.Sum256(artifact)
	maxReadCount := 0
	for _, readCount := range config.ReadCounts {
		if readCount > maxReadCount {
			maxReadCount = readCount
		}
	}
	evidence := plmCrossoverEvidence{
		SchemaVersion: plmCrossoverEconomicsSchema,
		TargetCommit:  config.TargetCommit, SourceTree: config.SourceTree,
		ArtifactSourceCommit: config.ArtifactSourceCommit,
		ArtifactSHA256:       fmt.Sprintf("sha256:%x", artifactDigest[:]),
		SourceSHA256:         plmCrossoverDigest(plmCrossoverSource(maxReadCount)),
		RunsPerArm:           config.Runs, ReadCounts: config.ReadCounts, DelaysMS: config.DelaysMS,
		ZeroRead: config.ZeroRead, EvaluationHostID: config.EvaluationHostID,
		EvaluationOrderOffset: config.EvaluationOrderOffset, Profiles: profiles,
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("PLM_CROSSOVER %s", encoded)
}

func plmCrossoverConfigFromEnv() (plmCrossoverConfig, error) {
	config := plmCrossoverConfig{Output: strings.TrimSpace(os.Getenv("PLM_CROSSOVER_OUTPUT"))}
	var err error
	config.Runs, err = plmCrossoverIntEnv("PLM_CROSSOVER_RUNS", 5, 3, 20)
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.ReadCounts, err = plmCrossoverListEnv("PLM_CROSSOVER_READ_COUNTS", "1,2,4,8", map[int]bool{1: true, 2: true, 4: true, 8: true})
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.DelaysMS, err = plmCrossoverListEnv("PLM_CROSSOVER_DELAYS_MS", "25,75,200", map[int]bool{25: true, 75: true, 200: true})
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.ZeroRead, err = plmCrossoverBoolEnv("PLM_CROSSOVER_ZERO_READ", true)
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.TargetCommit, err = plmCrossoverIdentityEnv("PLM_TARGET_COMMIT")
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.SourceTree, err = plmCrossoverIdentityEnv("PLM_SOURCE_TREE")
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.ArtifactSourceCommit, err = plmCrossoverIdentityEnv("PLM_ARTIFACT_SOURCE_COMMIT")
	if err != nil {
		return plmCrossoverConfig{}, err
	}
	config.EvaluationHostID = strings.TrimSpace(os.Getenv("EVALUATION_HOST_ID"))
	offset := strings.TrimSpace(os.Getenv("EVALUATION_ORDER_OFFSET"))
	if offset == "" {
		config.EvaluationOrderOffset = 0
	} else if config.EvaluationOrderOffset, err = strconv.Atoi(offset); err != nil || config.EvaluationOrderOffset < 0 {
		return plmCrossoverConfig{}, fmt.Errorf("EVALUATION_ORDER_OFFSET must be a non-negative integer")
	}
	return config, nil
}

func plmCrossoverIntEnv(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be in [%d,%d]", name, minimum, maximum)
	}
	return parsed, nil
}

func plmCrossoverListEnv(name, fallback string, allowed map[int]bool) ([]int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		value = fallback
	}
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		parsed, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || !allowed[parsed] || seen[parsed] {
			return nil, fmt.Errorf("%s must contain unique allowed values", name)
		}
		seen[parsed] = true
		result = append(result, parsed)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	return result, nil
}

func plmCrossoverBoolEnv(name string, fallback bool) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "" {
		return fallback, nil
	}
	switch value {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", name)
	}
}

func plmCrossoverIdentityEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value != "" && !isPLMCrossoverGitIdentity(value) {
		return "", fmt.Errorf("%s must be a full Git identity", name)
	}
	return value, nil
}

func isPLMCrossoverGitIdentity(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func plmCrossoverSource(readCount int) string {
	var source strings.Builder
	source.WriteString("x0 = 0\n")
	for index := 1; index <= 750; index++ {
		fmt.Fprintf(&source, "x%d = x%d + 1\n", index, index-1)
	}
	values := make([]string, 0, readCount+1)
	for index := 0; index < readCount; index++ {
		name := fmt.Sprintf("value%d", index)
		fmt.Fprintf(&source, "%s = sources.read(\"fixture-%d\")\n", name, index)
		values = append(values, name)
	}
	values = append(values, "x750")
	fmt.Fprintf(&source, "result = [%s]\n", strings.Join(values, ", "))
	return source.String()
}

func plmCrossoverOrder(pairIteration, offset int) []string {
	if (pairIteration+offset)%2 == 0 {
		return []string{"baseline", "plm"}
	}
	return []string{"plm", "baseline"}
}

func runPLMCrossoverPair(t *testing.T, artifact []byte, profile string, pairIteration, readCount, delayMS, orderOffset int) []plmCrossoverSample {
	t.Helper()
	samples := make([]plmCrossoverSample, 0, 2)
	for _, mode := range plmCrossoverOrder(pairIteration, orderOffset) {
		samples = append(samples, runPLMCrossoverSample(t, artifact, profile, mode, pairIteration, readCount, delayMS))
	}
	return samples
}

func runPLMCrossoverSample(t *testing.T, artifact []byte, profile, mode string, pairIteration, readCount, delayMS int) plmCrossoverSample {
	t.Helper()
	runID := fmt.Sprintf("plm-crossover-%s-%s-r%d-d%d-p%d", profile, mode, readCount, delayMS, pairIteration)
	provider := &plmCrossoverProvider{delay: time.Duration(delayMS) * time.Millisecond}
	adapter := &e2ePLMAdapter{handler: provider}
	planCalls := uint32(readCount)
	if planCalls == 0 {
		planCalls = 1
	}
	plan := plmE2EPlan(t, planCalls, adapter)
	source := plmCrossoverSource(readCount)
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

	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	var beforeSetup goruntime.MemStats
	goruntime.ReadMemStats(&beforeSetup)
	setupStarted := time.Now()
	var broker *capability.Broker
	runner, err := (wazeroengine.Factory{Passes: plugins, BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
		broker = created
		return created, createErr
	}}).New(context.Background(), artifact, config)
	setupNanos := uint64(time.Since(setupStarted))
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = runner.Close(context.Background())
		}
	}()
	engine := trustedSemanticRunner(t, runner)
	var beforeTimed goruntime.MemStats
	goruntime.ReadMemStats(&beforeTimed)
	timedStarted := time.Now()
	if profile == "cold_end_to_end" {
		timedStarted = setupStarted
	}
	var result []byte
	if mode == "plm" {
		execution, runErr := plugins.ExecuteCapabilityHostScheduled(context.Background(), sourcepatch.PLMCapabilityCallsName, engine, request,
			plan.PythonPrelude(), passplugin.PLMCapabilityProjections(plan))
		if runErr != nil || execution.PassError != nil || (readCount > 0 && !execution.Applied) {
			t.Fatalf("mode=%s read_count=%d execution=%+v err=%v", mode, readCount, execution, runErr)
		}
		if result, runErr = decodeSuccessfulGuestResult(execution.Payload); runErr != nil {
			t.Fatal(runErr)
		}
	} else {
		payload, runErr := engine.Run(context.Background(), request, plan.PythonPrelude())
		if runErr != nil {
			t.Fatal(runErr)
		}
		if result, runErr = decodeSuccessfulGuestResult(payload); runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed = true
	totalNanos := uint64(time.Since(timedStarted))
	var after goruntime.MemStats
	goruntime.ReadMemStats(&after)
	allocated := after.TotalAlloc - beforeTimed.TotalAlloc
	if profile == "cold_end_to_end" {
		allocated = after.TotalAlloc - beforeSetup.TotalAlloc
	}
	if broker == nil || broker.CallCount() != uint32(readCount) {
		t.Fatalf("mode=%s read_count=%d broker=%v", mode, readCount, broker)
	}
	expected := plmCrossoverExpectedResult(readCount)
	if string(result) != string(expected) {
		t.Fatalf("mode=%s read_count=%d result=%s expected=%s", mode, readCount, result, expected)
	}
	candidates := engine.SplitPhaseEvidence()
	if mode == "plm" && (candidates.CandidatesPrepared != uint32(readCount) || candidates.CandidatesAdopted != uint32(readCount) || candidates.MaximumConcurrent != uint32(readCount)) {
		t.Fatalf("mode=%s read_count=%d candidates=%+v", mode, readCount, candidates)
	}
	if readCount == 0 && (provider.starts.Load() != 0 || provider.maxConcurrency.Load() != 0 || candidates.CandidatesPrepared != 0 || candidates.CandidatesAdopted != 0 || candidates.MaximumConcurrent != 0) {
		t.Fatalf("zero-read mode=%s provider=%+v candidates=%+v", mode, provider, candidates)
	}
	wantProviderMax := uint32(0)
	if readCount > 0 {
		wantProviderMax = 1
		if mode == "plm" {
			wantProviderMax = uint32(readCount)
		}
	}
	if provider.starts.Load() != uint32(readCount) || provider.maxConcurrency.Load() != wantProviderMax {
		t.Fatalf("mode=%s read_count=%d starts=%d max_concurrency=%d want=%d", mode, readCount, provider.starts.Load(), provider.maxConcurrency.Load(), wantProviderMax)
	}
	return plmCrossoverSample{
		Mode: mode, Profile: profile, PairIteration: pairIteration, ReadCount: readCount, DelayMS: delayMS,
		SourceSHA256: plmCrossoverDigest(source), TotalNanos: totalNanos, EngineSetupNanos: setupNanos,
		AllocatedBytes: allocated, ProviderNanos: provider.totalNanos.Load(), ProviderStarts: provider.starts.Load(),
		ProviderMaxConcurrency: provider.maxConcurrency.Load(), CallCount: broker.CallCount(),
		Result:    append(json.RawMessage(nil), result...),
		Lifecycle: engine.PLMRunLifecycleEvidence(), Candidates: candidates,
	}
}

type plmCrossoverProvider struct {
	delay          time.Duration
	starts         atomic.Uint32
	active         atomic.Uint32
	maxConcurrency atomic.Uint32
	totalNanos     atomic.Uint64
}

func (provider *plmCrossoverProvider) Call(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	started := time.Now()
	provider.starts.Add(1)
	active := provider.active.Add(1)
	for {
		maximum := provider.maxConcurrency.Load()
		if active <= maximum || provider.maxConcurrency.CompareAndSwap(maximum, active) {
			break
		}
	}
	defer provider.active.Add(^uint32(0))
	if provider.delay > 0 {
		timer := time.NewTimer(provider.delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			provider.totalNanos.Add(uint64(time.Since(started)))
			return nil, ctx.Err()
		}
	}
	provider.totalNanos.Add(uint64(time.Since(started)))
	return json.RawMessage(`{"body":"fixture"}`), nil
}

func plmCrossoverExpectedResult(readCount int) []byte {
	values := make([]any, 0, readCount+1)
	for index := 0; index < readCount; index++ {
		values = append(values, "fixture")
	}
	values = append(values, 750)
	encoded, _ := json.Marshal(values)
	return encoded
}

func plmCrossoverMedian(samples []plmCrossoverSample, mode string, readCount, delayMS int) uint64 {
	values := make([]uint64, 0)
	for _, sample := range samples {
		if sample.Mode == mode && sample.ReadCount == readCount && sample.DelayMS == delayMS {
			values = append(values, sample.TotalNanos)
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	return values[len(values)/2]
}

func plmCrossoverDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
