package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const (
	rows      = 1024
	columns   = 1024
	bodyBytes = rows * columns * 8
)

type report struct {
	SchemaVersion            string                          `json:"schema_version"`
	ArtifactSHA256           string                          `json:"artifact_sha256"`
	BodyBytes                uint64                          `json:"body_bytes"`
	BodySHA256               string                          `json:"body_sha256"`
	TrustedPrepareSHA256     string                          `json:"trusted_prepare_sha256"`
	Image                    wazeroengine.PreparedImageState `json:"image"`
	PrepareNanos             uint64                          `json:"prepare_nanos"`
	FirstRunNanos            uint64                          `json:"first_run_nanos"`
	SecondRunNanos           uint64                          `json:"second_run_nanos"`
	BodyCopyBytesPerConsumer uint64                          `json:"body_copy_bytes_per_consumer"`
	FirstMinorFaults         int64                           `json:"first_minor_faults"`
	FirstMajorFaults         int64                           `json:"first_major_faults"`
	SecondMinorFaults        int64                           `json:"second_minor_faults"`
	SecondMajorFaults        int64                           `json:"second_major_faults"`
	FirstResult              json.RawMessage                 `json:"first_result"`
	SecondResult             json.RawMessage                 `json:"second_result"`
	ConsumerMutationIsolated bool                            `json:"consumer_mutation_isolated"`
	PrivateCOWSelected       bool                            `json:"private_cow_selected"`
	Fallback                 bool                            `json:"fallback"`
}

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, artifactSHA, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	body := fixtureBody()
	bodyDigest := digest(body)
	if bodyDigest != "sha256:a78cee677876b925402c15818acd3fc020a47754d9d1c26688914ea09070f8d0" {
		fail(errors.New("fixture body identity drifted"))
	}
	prepareSource := renderPrepare(body)
	prepareDigest := digest([]byte(prepareSource))
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 180 * time.Second
	config.MaxRequestBytes = 16 << 20
	config.MaxResponseBytes = 16 << 20
	config.ExecutionProfile = &profile
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	engine, err := wazeroengine.New(context.Background(), wasm, config)
	if err != nil {
		fail(err)
	}
	defer engine.Close(context.Background())
	started := time.Now()
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), prepareSource); err != nil {
		fail(err)
	}
	prepareNanos := uint64(time.Since(started))
	beforeFirst := sampleUsage()
	started = time.Now()
	first, err := run(engine, "cow-data-a", "import numpy as np\ndataset[0, 0] = 999\nresult = {'first': int(dataset[0, 0]), 'sum': int(dataset.sum())}\n")
	if err != nil {
		fail(err)
	}
	firstNanos := uint64(time.Since(started))
	afterFirst := sampleUsage()
	started = time.Now()
	second, err := run(engine, "cow-data-b", "import numpy as np\nresult = {'first': int(dataset[0, 0]), 'sum': int(dataset.sum()), 'shape': list(dataset.shape)}\n")
	if err != nil {
		fail(err)
	}
	secondNanos := uint64(time.Since(started))
	afterSecond := sampleUsage()
	var secondValue struct {
		First int64 `json:"first"`
		Sum   int64 `json:"sum"`
	}
	if json.Unmarshal(second, &secondValue) != nil {
		fail(errors.New("decode second result"))
	}
	probe := engine.COWProbe()
	out := report{
		SchemaVersion: "pysolate.prepared-data-derived-cow-probe.v1", ArtifactSHA256: artifactSHA,
		BodyBytes: uint64(len(body)), BodySHA256: bodyDigest, TrustedPrepareSHA256: prepareDigest,
		Image: engine.PreparedImageState(), PrepareNanos: prepareNanos, FirstRunNanos: firstNanos, SecondRunNanos: secondNanos,
		BodyCopyBytesPerConsumer: 0,
		FirstMinorFaults:         afterFirst.minor - beforeFirst.minor, FirstMajorFaults: afterFirst.major - beforeFirst.major,
		SecondMinorFaults: afterSecond.minor - afterFirst.minor, SecondMajorFaults: afterSecond.major - afterFirst.major,
		FirstResult: first, SecondResult: second, ConsumerMutationIsolated: secondValue.First == 0 && secondValue.Sum == 549755289600,
		PrivateCOWSelected: probe.COWSelected, Fallback: probe.Fallback,
	}
	if !out.ConsumerMutationIsolated || !out.PrivateCOWSelected || out.Fallback || out.Image.TrustedPrepareSHA256 != prepareDigest {
		fail(errors.New("derived COW invariants failed"))
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

type usage struct {
	minor int64
	major int64
}

func sampleUsage() usage {
	var value syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &value) != nil {
		return usage{}
	}
	return usage{minor: value.Minflt, major: value.Majflt}
}

func fixtureBody() []byte {
	body := make([]byte, bodyBytes)
	for value := uint64(0); value < rows*columns; value++ {
		base := value * 8
		for index := uint(0); index < 8; index++ {
			body[base+uint64(index)] = byte(value >> (8 * index))
		}
	}
	return body
}

func renderPrepare(body []byte) string {
	return "import base64 as _pd_base64, numpy as np\n" +
		"_pd_body = bytearray(_pd_base64.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(body)) + "))\n" +
		"dataset = np.frombuffer(_pd_body, dtype=np.dtype('<i8')).reshape((1024, 1024))\n"
}

func run(engine *wazeroengine.Engine, runID, code string) (json.RawMessage, error) {
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		return nil, err
	}
	response, err := engine.Run(context.Background(), request, "")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" || len(envelope.Result) == 0 {
		return nil, fmt.Errorf("Guest response=%s", response)
	}
	return envelope.Result, nil
}

func loadArtifact(root string) ([]byte, runtimeconfig.ExecutionProfile, string, error) {
	var zero runtimeconfig.ExecutionProfile
	if root == "" {
		return nil, zero, "", errors.New("artifact root is required")
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return nil, zero, "", err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, zero, "", err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return nil, zero, "", err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return nil, zero, "", err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return nil, zero, "", err
	}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return nil, zero, "", err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return wasm, profile, identity.ArtifactSHA256, err
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
