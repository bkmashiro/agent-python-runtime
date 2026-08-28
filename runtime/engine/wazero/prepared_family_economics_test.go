package wazero

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const preparedFamilyEconomicsSchema = "pysolate.prepared-family-economics.v1"

type preparedFamilyResourceSample struct {
	PSSBytes          uint64 `json:"pss_bytes"`
	PrivateDirtyBytes uint64 `json:"private_dirty_bytes"`
}

type preparedFamilyEconomicsSample struct {
	Mode                  string                       `json:"mode"`
	Iteration             int                          `json:"iteration"`
	Fanout                int                          `json:"fanout"`
	FamilyPrepareNanos    uint64                       `json:"family_prepare_nanos"`
	RunnerCreateNanos     []uint64                     `json:"runner_create_nanos"`
	RunNanos              []uint64                     `json:"run_nanos"`
	RunnerCloseNanos      []uint64                     `json:"runner_close_nanos"`
	FamilyCloseNanos      uint64                       `json:"family_close_nanos"`
	BaselineResources     preparedFamilyResourceSample `json:"baseline_resources"`
	AfterCreateResources  preparedFamilyResourceSample `json:"after_create_resources"`
	AfterRunResources     preparedFamilyResourceSample `json:"after_run_resources"`
	PSSCreateDeltaBytes   int64                        `json:"pss_create_delta_bytes"`
	PSSRunDeltaBytes      int64                        `json:"pss_run_delta_bytes"`
	DirtyCreateDeltaBytes int64                        `json:"private_dirty_create_delta_bytes"`
	DirtyRunDeltaBytes    int64                        `json:"private_dirty_run_delta_bytes"`
	Result                int64                        `json:"result"`
}

type preparedFamilyEconomicsTreatment struct {
	Mode                        string                          `json:"mode"`
	FamilyPrepareMedianNanos    uint64                          `json:"family_prepare_median_nanos"`
	RunnerCreateMedianNanos     uint64                          `json:"runner_create_median_nanos"`
	RunMedianNanos              uint64                          `json:"run_median_nanos"`
	PSSCreateDeltaMedianBytes   int64                           `json:"pss_create_delta_median_bytes"`
	PSSRunDeltaMedianBytes      int64                           `json:"pss_run_delta_median_bytes"`
	DirtyCreateDeltaMedianBytes int64                           `json:"private_dirty_create_delta_median_bytes"`
	DirtyRunDeltaMedianBytes    int64                           `json:"private_dirty_run_delta_median_bytes"`
	Samples                     []preparedFamilyEconomicsSample `json:"samples"`
}

type preparedFamilyEconomicsEvidence struct {
	SchemaVersion       string                             `json:"schema_version"`
	SourceCommit        string                             `json:"source_commit"`
	SourceTree          string                             `json:"source_tree"`
	ArtifactSHA256      string                             `json:"artifact_sha256"`
	InputSHA256         string                             `json:"input_sha256"`
	InputBytes          uint64                             `json:"input_bytes"`
	RunsPerArm          int                                `json:"runs_per_arm"`
	Fanout              int                                `json:"fanout"`
	Isolation           string                             `json:"isolation"`
	ProcessMemorySource string                             `json:"process_memory_source"`
	Treatments          []preparedFamilyEconomicsTreatment `json:"treatments"`
}

func TestPreparedFamilyEconomicsFixture(t *testing.T) {
	output := os.Getenv("PYSOLATE_PREPARED_FAMILY_ECONOMICS_OUTPUT")
	if output == "" {
		t.Skip("set PYSOLATE_PREPARED_FAMILY_ECONOMICS_OUTPUT to run the bounded economics fixture")
	}
	if goruntime.GOOS != "linux" {
		t.Fatal("prepared-family economics requires Linux")
	}
	runs := preparedFamilyEconomicsBoundedInt(t, "PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS", 3, 20)
	fanout := preparedFamilyEconomicsBoundedInt(t, "PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT", 1, 8)
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".prepared-family-economics-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temporary)

	samplesByMode := map[string][]preparedFamilyEconomicsSample{"private_copy": {}, "private_cow": {}}
	for iteration := 0; iteration < runs; iteration++ {
		order := []string{"private_copy", "private_cow"}
		if iteration%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, mode := range order {
			workerOutput := filepath.Join(temporary, fmt.Sprintf("%02d-%s.json", iteration, mode))
			command := exec.Command(os.Args[0], "-test.run=^TestPreparedFamilyEconomicsWorker$", "-test.count=1")
			command.Env = append(os.Environ(),
				"PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER=1",
				"PYSOLATE_PREPARED_FAMILY_ECONOMICS_MODE="+mode,
				"PYSOLATE_PREPARED_FAMILY_ECONOMICS_ITERATION="+strconv.Itoa(iteration),
				"PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT="+strconv.Itoa(fanout),
				"PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER_OUTPUT="+workerOutput,
			)
			var trace bytes.Buffer
			command.Stdout = &trace
			command.Stderr = &trace
			if err := command.Run(); err != nil {
				t.Fatalf("worker mode=%s iteration=%d: %v\n%s", mode, iteration, err, trace.String())
			}
			encoded, err := os.ReadFile(workerOutput)
			if err != nil {
				t.Fatal(err)
			}
			var sample preparedFamilyEconomicsSample
			if err := json.Unmarshal(encoded, &sample); err != nil || sample.Mode != mode || sample.Iteration != iteration || sample.Fanout != fanout {
				t.Fatalf("worker sample=%+v err=%v", sample, err)
			}
			samplesByMode[mode] = append(samplesByMode[mode], sample)
		}
	}

	artifact, profile := realPreparedGuest(t)
	input := preparedFamilyEconomicsInput(t, profile)
	artifactDigest := sha256.Sum256(artifact)
	evidence := preparedFamilyEconomicsEvidence{
		SchemaVersion:       preparedFamilyEconomicsSchema,
		SourceCommit:        os.Getenv("PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT"),
		SourceTree:          os.Getenv("PYSOLATE_PREPARED_FAMILY_SOURCE_TREE"),
		ArtifactSHA256:      fmt.Sprintf("sha256:%x", artifactDigest[:]),
		InputSHA256:         input.IdentitySHA256(),
		InputBytes:          8 << 20,
		RunsPerArm:          runs,
		Fanout:              fanout,
		Isolation:           "one fresh test subprocess per treatment and repetition; treatment order alternates",
		ProcessMemorySource: "/proc/self/smaps_rollup",
	}
	for _, mode := range []string{"private_copy", "private_cow"} {
		samples := samplesByMode[mode]
		evidence.Treatments = append(evidence.Treatments, preparedFamilyEconomicsTreatment{
			Mode:                     mode,
			FamilyPrepareMedianNanos: preparedFamilyEconomicsMedian(samples, func(sample preparedFamilyEconomicsSample) uint64 { return sample.FamilyPrepareNanos }),
			RunnerCreateMedianNanos: preparedFamilyEconomicsMedian(samples, func(sample preparedFamilyEconomicsSample) uint64 {
				return sumPreparedFamilyDurations(sample.RunnerCreateNanos)
			}),
			RunMedianNanos:              preparedFamilyEconomicsMedian(samples, func(sample preparedFamilyEconomicsSample) uint64 { return sumPreparedFamilyDurations(sample.RunNanos) }),
			PSSCreateDeltaMedianBytes:   preparedFamilyEconomicsSignedMedian(samples, func(sample preparedFamilyEconomicsSample) int64 { return sample.PSSCreateDeltaBytes }),
			PSSRunDeltaMedianBytes:      preparedFamilyEconomicsSignedMedian(samples, func(sample preparedFamilyEconomicsSample) int64 { return sample.PSSRunDeltaBytes }),
			DirtyCreateDeltaMedianBytes: preparedFamilyEconomicsSignedMedian(samples, func(sample preparedFamilyEconomicsSample) int64 { return sample.DirtyCreateDeltaBytes }),
			DirtyRunDeltaMedianBytes:    preparedFamilyEconomicsSignedMedian(samples, func(sample preparedFamilyEconomicsSample) int64 { return sample.DirtyRunDeltaBytes }),
			Samples:                     samples,
		})
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("PREPARED_FAMILY_ECONOMICS %s", encoded)
}

func TestPreparedFamilyEconomicsWorker(t *testing.T) {
	if os.Getenv("PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER") != "1" {
		t.Skip("worker is started by TestPreparedFamilyEconomicsFixture")
	}
	mode := os.Getenv("PYSOLATE_PREPARED_FAMILY_ECONOMICS_MODE")
	iteration, err := strconv.Atoi(os.Getenv("PYSOLATE_PREPARED_FAMILY_ECONOMICS_ITERATION"))
	if err != nil || (mode != "private_copy" && mode != "private_cow") {
		t.Fatal("invalid worker coordinates")
	}
	fanout := preparedFamilyEconomicsBoundedInt(t, "PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT", 1, 8)
	output := os.Getenv("PYSOLATE_PREPARED_FAMILY_ECONOMICS_WORKER_OUTPUT")
	if output == "" {
		t.Fatal("worker output is required")
	}

	artifact, profile := realPreparedGuest(t)
	input := preparedFamilyEconomicsInput(t, profile)
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 2 * time.Minute
	config.ExecutionProfile = profile
	goruntime.GC()
	baseline := readPreparedFamilyProcessMemory(t)

	familyMode := PreparedFamilyPrivateCopy
	if mode == "private_cow" {
		familyMode = PreparedFamilyPrivateCOW
	}
	started := time.Now()
	family, err := PrepareNumpyFamily(context.Background(), artifact, PreparedFamilyConfig{
		ImageConfig: config, MaxConsumers: uint32(fanout), MaxActive: 1, Mode: familyMode,
	}, input)
	familyPrepare := uint64(time.Since(started))
	if err != nil {
		t.Fatal(err)
	}

	runners := make([]interface {
		Run(context.Context, []byte, string) ([]byte, error)
		Close(context.Context) error
	}, 0, fanout)
	sample := preparedFamilyEconomicsSample{Mode: mode, Iteration: iteration, Fanout: fanout, FamilyPrepareNanos: familyPrepare, BaselineResources: baseline}
	for index := 0; index < fanout; index++ {
		runID := preparedFamilyEconomicsRunID(mode, iteration, index)
		started = time.Now()
		runner, err := family.NewRunner(context.Background(), PreparedRunnerConfig{
			RunConfig: config,
			InvocationRef: runtimeconfig.InvocationRef{
				AgentRunID: "prepared-family-economics", InvocationID: fmt.Sprintf("inv-%s-%d-%d", mode, iteration, index),
				InvocationAttempt: 1, ExecutionID: runID,
			},
		})
		sample.RunnerCreateNanos = append(sample.RunnerCreateNanos, uint64(time.Since(started)))
		if err != nil {
			t.Fatal(err)
		}
		runners = append(runners, runner)
	}
	sample.AfterCreateResources = readPreparedFamilyProcessMemory(t)
	sample.PSSCreateDeltaBytes = int64(sample.AfterCreateResources.PSSBytes) - int64(baseline.PSSBytes)
	sample.DirtyCreateDeltaBytes = int64(sample.AfterCreateResources.PrivateDirtyBytes) - int64(baseline.PrivateDirtyBytes)

	for index, runner := range runners {
		runID := preparedFamilyEconomicsRunID(mode, iteration, index)
		request := runtimeconfig.RunRequest{
			RunID:         runID,
			Code:          "import numpy\ndataset.flat[0] = dataset.flat[0] + 1\nresult = int(dataset.sum())\n",
			Inputs:        json.RawMessage(`{}`),
			Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}},
		}
		raw, err := runtimeconfig.EncodeRunRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		started = time.Now()
		response, err := runner.Run(context.Background(), raw, "")
		sample.RunNanos = append(sample.RunNanos, uint64(time.Since(started)))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := runtimeconfig.DecodeAndValidateRunResponse(request, response)
		if err != nil || decoded.Status != runtimeconfig.RunResponseOK {
			t.Fatalf("response=%s err=%v", response, err)
		}
		var result int64
		if err := json.Unmarshal(decoded.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result != 1 {
			t.Fatalf("unexpected consumer result: got=%d want=1", result)
		}
		if index == 0 {
			sample.Result = result
		} else if result != sample.Result {
			t.Fatalf("consumer result drift: first=%d current=%d", sample.Result, result)
		}
	}
	sample.AfterRunResources = readPreparedFamilyProcessMemory(t)
	sample.PSSRunDeltaBytes = int64(sample.AfterRunResources.PSSBytes) - int64(baseline.PSSBytes)
	sample.DirtyRunDeltaBytes = int64(sample.AfterRunResources.PrivateDirtyBytes) - int64(baseline.PrivateDirtyBytes)

	for _, runner := range runners {
		started = time.Now()
		if err := runner.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		sample.RunnerCloseNanos = append(sample.RunnerCloseNanos, uint64(time.Since(started)))
	}
	started = time.Now()
	if err := family.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	sample.FamilyCloseNanos = uint64(time.Since(started))

	encoded, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedFamilyEconomicsMedian(t *testing.T) {
	samples := []preparedFamilyEconomicsSample{{FamilyPrepareNanos: 9, PSSRunDeltaBytes: -2}, {FamilyPrepareNanos: 1, PSSRunDeltaBytes: 5}, {FamilyPrepareNanos: 5, PSSRunDeltaBytes: 3}}
	if got := preparedFamilyEconomicsMedian(samples, func(sample preparedFamilyEconomicsSample) uint64 { return sample.FamilyPrepareNanos }); got != 5 {
		t.Fatalf("median=%d", got)
	}
	if got := preparedFamilyEconomicsSignedMedian(samples, func(sample preparedFamilyEconomicsSample) int64 { return sample.PSSRunDeltaBytes }); got != 3 {
		t.Fatalf("signed median=%d", got)
	}
}

func TestPreparedFamilyEconomicsRunID(t *testing.T) {
	if got := preparedFamilyEconomicsRunID("private_cow", 2, 3); got != "run-private_cow-2-3" {
		t.Fatalf("run id=%q", got)
	}
}

func preparedFamilyEconomicsInput(t *testing.T, profile *runtimeconfig.ExecutionProfile) PreparedNumpyInput {
	t.Helper()
	body := make([]byte, 8<<20)
	return realPreparedInputRaw(t, profile, "<i8", []uint64{1024, 1024}, body)
}

func preparedFamilyEconomicsBoundedInt(t *testing.T, name string, minimum, maximum int) int {
	t.Helper()
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < minimum || value > maximum {
		t.Fatalf("%s must be in [%d,%d]", name, minimum, maximum)
	}
	return value
}

func readPreparedFamilyProcessMemory(t *testing.T) preparedFamilyResourceSample {
	t.Helper()
	file, err := os.Open("/proc/self/smaps_rollup")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var sample preparedFamilyResourceSample
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "Pss:":
			sample.PSSBytes = value * 1024
		case "Private_Dirty:":
			sample.PrivateDirtyBytes = value * 1024
		}
	}
	if err := scanner.Err(); err != nil || sample.PSSBytes == 0 {
		t.Fatalf("smaps_rollup sample=%+v err=%v", sample, err)
	}
	return sample
}

func preparedFamilyEconomicsRunID(mode string, iteration, index int) string {
	return fmt.Sprintf("run-%s-%d-%d", mode, iteration, index)
}

func preparedFamilyEconomicsMedian(samples []preparedFamilyEconomicsSample, selectValue func(preparedFamilyEconomicsSample) uint64) uint64 {
	values := make([]uint64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, selectValue(sample))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func preparedFamilyEconomicsSignedMedian(samples []preparedFamilyEconomicsSample, selectValue func(preparedFamilyEconomicsSample) int64) int64 {
	values := make([]int64, 0, len(samples))
	for _, sample := range samples {
		values = append(values, selectValue(sample))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

func sumPreparedFamilyDurations(values []uint64) uint64 {
	var total uint64
	for _, value := range values {
		total += value
	}
	return total
}
