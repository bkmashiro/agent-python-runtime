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

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	fanout "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetfanout"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const expectedSum = fanout.ExpectedSum

type record = fanout.Record
type report = fanout.Report

type usage struct{ minor, major int64 }

type runResult struct{ sum uint64 }

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, artifactSHA, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	fixture := researchdata.CanonicalFixture()
	file, err := os.CreateTemp("", "pysolate-prepared-data-*.npy")
	if err != nil {
		fail(err)
	}
	fixturePath := file.Name()
	defer os.Remove(fixturePath)
	if _, err := file.Write(fixture); err != nil {
		fail(err)
	}
	if err := file.Close(); err != nil {
		fail(err)
	}
	readStart := time.Now()
	readFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		fail(err)
	}
	readNanos := uint64(time.Since(readStart))
	decodeStart := time.Now()
	decoded, err := researchdata.Decode(readFixture)
	if err != nil {
		fail(err)
	}
	decodeNanos := uint64(time.Since(decodeStart))
	body := decoded.Body
	if digest(body) != researchdata.CanonicalBodySHA256 {
		fail(errors.New("fixture body drift"))
	}
	out := report{SchemaVersion: fanout.SchemaVersion, ArtifactSHA256: artifactSHA, FixtureBodySHA256: researchdata.CanonicalBodySHA256, FixtureBodyBytes: uint64(len(body)), PackagePrepareOnce: true, HostReadNanos: readNanos, HostDecodeNanos: decodeNanos, WarmupFreshGuests: 4}

	recompute, recomputeShard := newPackageEngine(wasm, profile)
	defer recompute.Close(context.Background())
	if _, err := run(recompute, "warmup-recompute", "import numpy as np\nresult={'sum':int(np.arange(1048576,dtype=np.int64).sum())}\n", ""); err != nil {
		fail(err)
	}
	out.Records = append(out.Records, runSchedules(recompute, "recompute", recomputeShard, 0, 0, 0, 0, "import numpy as np\nresult = {'sum': int(np.arange(1048576, dtype=np.int64).sum())}\n", "")...)

	privateCopy, privateShard := newPackageEngine(wasm, profile)
	defer privateCopy.Close(context.Background())
	privatePrepare := readNanos + decodeNanos
	copyPrepare := renderPrepare(decoded.Body)
	if _, err := run(privateCopy, "warmup-private-copy", "import numpy as np\nresult={'sum':int(dataset.sum())}\n", copyPrepare); err != nil {
		fail(err)
	}
	out.Records = append(out.Records, runSchedules(privateCopy, "private_copy", privateShard, privatePrepare, uint64(len(decoded.Body)), uint64(len(copyPrepare)), 0, "import numpy as np\nresult = {'sum': int(dataset.sum())}\n", copyPrepare)...)

	cow, cowShard := newPackageEngine(wasm, profile)
	defer cow.Close(context.Background())
	deriveStart := time.Now()
	if err := cow.DeriveSemanticRuntimeWithTrustedSource(context.Background(), renderPrepare(decoded.Body)); err != nil {
		fail(err)
	}
	cowPrepare := readNanos + decodeNanos + uint64(time.Since(deriveStart))
	if _, err := run(cow, "warmup-cow", "import numpy as np\nresult={'sum':int(dataset.sum())}\n", ""); err != nil {
		fail(err)
	}
	out.Records = append(out.Records, runSchedules(cow, "private_cow_pages", cowShard, cowPrepare, 0, 0, uint64(len(decoded.Body)), "import numpy as np\nresult = {'sum': int(dataset.sum())}\n", "")...)
	if _, err := run(cow, "cow-mutate", "import numpy as np\ndataset[0,0]=999\nresult={'sum':int(dataset.sum())}\n", ""); err != nil {
		fail(err)
	}
	postMutation, err := run(cow, "cow-post-mutate", "import numpy as np\nresult={'sum':int(dataset.sum())}\n", "")
	if err != nil {
		fail(err)
	}
	out.MutationIsolated = postMutation.sum == expectedSum
	if !out.MutationIsolated {
		fail(errors.New("COW mutation leaked"))
	}

	local, localShard := newPackageEngine(wasm, profile)
	defer local.Close(context.Background())
	localStart := time.Now()
	if err := local.DeriveSemanticRuntimeWithTrustedSource(context.Background(), renderPrepare(decoded.Body)); err != nil {
		fail(err)
	}
	localPrepare := readNanos + decodeNanos + uint64(time.Since(localStart))
	if _, err := run(local, "warmup-data-local", "import numpy as np\nresult={'sum':int(dataset.sum())}\n", ""); err != nil {
		fail(err)
	}
	out.Records = append(out.Records, dataLocalSchedules(local, recompute, localShard, localPrepare, uint64(len(decoded.Body)))...)

	for _, item := range out.Records {
		if !item.Parity || !item.Cleanup || item.ResultSum != expectedSum && item.Consumers > 0 || item.LogicalConsumers != uint64(item.Consumers) {
			fail(fmt.Errorf("record invariant: %+v", item))
		}
	}
	if err := fanout.Validate(out); err != nil {
		fail(err)
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

func newPackageEngine(wasm []byte, profile runtimeconfig.ExecutionProfile) (*wazeroengine.Engine, uint64) {
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
	started := time.Now()
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), "import numpy as np\n"); err != nil {
		fail(err)
	}
	return engine, uint64(time.Since(started))
}

func runSchedules(engine *wazeroengine.Engine, treatment string, shard, dataset, copyPerConsumer, encodedPerConsumer, mappedPerConsumer uint64, code, prepare string) []record {
	out := make([]record, 0, 4)
	for _, consumers := range []int{0, 1, 2, 4} {
		before := sampleUsage()
		started := time.Now()
		var sum uint64
		for index := 0; index < consumers; index++ {
			observed, err := run(engine, fmt.Sprintf("%s-%d-%d", treatment, consumers, index), code, prepare)
			if err != nil {
				fail(err)
			}
			sum = observed.sum
		}
		elapsed := uint64(time.Since(started))
		after := sampleUsage()
		if consumers == 0 {
			sum = expectedSum
		}
		producer := boolCount(treatment != "recompute")
		orphan := uint64(0)
		if consumers == 0 && producer == 1 {
			orphan = researchdata.CanonicalBodyBytes
		}
		out = append(out, record{Treatment: treatment, Consumers: consumers, ShardPrepareNanos: shard, DatasetPrepareNanos: dataset, ConsumerNanos: elapsed, CriticalPathNanos: dataset + elapsed, BodyCopyBytes: copyPerConsumer * uint64(consumers), EncodedBytes: encodedPerConsumer * uint64(consumers), MappedBodyBytes: mappedPerConsumer * uint64(consumers), OrphanBytes: orphan, PhysicalProducers: producer, LogicalConsumers: uint64(consumers), FreshGuests: uint64(consumers), MinorFaults: after.minor - before.minor, MajorFaults: after.major - before.major, ResultSum: sum, Parity: sum == expectedSum, Cleanup: true})
	}
	return out
}

func dataLocalSchedules(dataEngine, scalarEngine *wazeroengine.Engine, shard, dataset, mapped uint64) []record {
	out := make([]record, 0, 4)
	for _, consumers := range []int{0, 1, 2, 4} {
		before := sampleUsage()
		started := time.Now()
		var sum uint64 = expectedSum
		fresh := uint64(0)
		if consumers > 0 {
			computed, err := run(dataEngine, fmt.Sprintf("local-compute-%d", consumers), "import numpy as np\nresult={'sum':int(dataset.sum())}\n", "")
			if err != nil {
				fail(err)
			}
			sum = computed.sum
			fresh++
			for index := 0; index < consumers; index++ {
				code := fmt.Sprintf("import numpy as np\nresult={'sum':int(%d)}\n", sum)
				observed, err := run(scalarEngine, fmt.Sprintf("local-scalar-%d-%d", consumers, index), code, "")
				if err != nil || observed.sum != sum {
					fail(errors.New("scalar fanout mismatch"))
				}
				fresh++
			}
		}
		elapsed := uint64(time.Since(started))
		after := sampleUsage()
		mappedBytes, orphan := uint64(0), uint64(0)
		if consumers > 0 {
			mappedBytes = mapped
		} else {
			orphan = researchdata.CanonicalBodyBytes
		}
		out = append(out, record{Treatment: "data_local_compute", Consumers: consumers, ShardPrepareNanos: shard, DatasetPrepareNanos: dataset, ConsumerNanos: elapsed, CriticalPathNanos: dataset + elapsed, BodyCopyBytes: 0, MappedBodyBytes: mappedBytes, OrphanBytes: orphan, PhysicalProducers: 1, LogicalConsumers: uint64(consumers), FreshGuests: fresh, MinorFaults: after.minor - before.minor, MajorFaults: after.major - before.major, ResultSum: sum, Parity: sum == expectedSum, Cleanup: true})
	}
	return out
}

func run(engine *wazeroengine.Engine, runID, code, prepare string) (runResult, error) {
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		return runResult{}, err
	}
	response, err := engine.Run(context.Background(), request, prepare)
	if err != nil {
		return runResult{}, err
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			Sum uint64 `json:"sum"`
		} `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
		return runResult{}, fmt.Errorf("Guest response=%s", response)
	}
	return runResult{sum: envelope.Result.Sum}, nil
}

func renderPrepare(body []byte) string {
	return "import base64 as _pd_base64, numpy as np\n" +
		"_pd_body = bytearray(_pd_base64.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(body)) + "))\n" +
		"dataset = np.frombuffer(_pd_body, dtype=np.dtype('<i8')).reshape((1024, 1024))\n"
}

func sampleUsage() usage {
	var value syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &value) != nil {
		return usage{}
	}
	return usage{minor: value.Minflt, major: value.Majflt}
}
func boolCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
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
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
