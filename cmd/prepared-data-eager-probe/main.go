package main

import (
	"context"

	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	campaign "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetcampaign"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, artifact, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	fixture := researchdata.CanonicalFixture()
	file, err := os.CreateTemp("", "pysolate-eager-*.npy")
	if err != nil {
		fail(err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(fixture); err != nil {
		fail(err)
	}
	if err := file.Close(); err != nil {
		fail(err)
	}
	started := time.Now()
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	readNanos := uint64(time.Since(started))
	started = time.Now()
	decoded, err := researchdata.Decode(raw)
	if err != nil {
		fail(err)
	}
	decodeNanos := uint64(time.Since(started))
	prepare := renderPrepare(decoded.Body)
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
	started = time.Now()
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), "import numpy as np\n"); err != nil {
		fail(err)
	}
	shard := uint64(time.Since(started))
	if _, _, err := run(engine, "eager-warmup", 1, prepare); err != nil {
		fail(err)
	}
	report := campaign.EagerReport{SchemaVersion: campaign.EagerSchemaVersion, ArtifactSHA256: artifact, FixtureBodySHA256: researchdata.CanonicalBodySHA256, HostReadNanos: readNanos, HostDecodeNanos: decodeNanos, ShardPrepareNanos: shard, WarmupFreshGuests: 1}
	for _, consumers := range []int{1, 2, 4} {
		sum, nanos, err := run(engine, fmt.Sprintf("eager-%d", consumers), consumers, prepare)
		if err != nil {
			fail(err)
		}
		report.Records = append(report.Records, campaign.EagerRecord{Consumers: consumers, ConsumerNanos: nanos, BodyCopyBytes: researchdata.CanonicalBodyBytes, EncodedBytes: uint64(len(prepare)), LogicalConsumers: uint64(consumers), FreshGuests: 1, ResultSum: sum, Parity: sum == 549755289600, Cleanup: true})
	}
	if campaign.ValidateEager(report) != nil {
		fail(campaign.ErrInvalidCampaign)
	}
	encoded, _ := json.Marshal(report)
	fmt.Println(string(encoded))
}

func run(engine *wazeroengine.Engine, id string, consumers int, prepare string) (uint64, uint64, error) {
	code := fmt.Sprintf("import numpy as np\n_value=0\nfor _index in range(%d):\n    _value=int(dataset.sum())\nresult={'sum':_value}\n", consumers)
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: id, Code: code, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		return 0, 0, err
	}
	started := time.Now()
	response, err := engine.Run(context.Background(), request, prepare)
	nanos := uint64(time.Since(started))
	if err != nil {
		return 0, 0, err
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			Sum uint64 `json:"sum"`
		} `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
		return 0, 0, fmt.Errorf("Guest response=%s", response)
	}
	return envelope.Result.Sum, nanos, nil
}
func renderPrepare(body []byte) string {
	return "import base64 as _b, numpy as np\n_pd=bytearray(_b.b64decode(" + fmt.Sprintf("%q", base64.StdEncoding.EncodeToString(body)) + "))\ndataset=np.frombuffer(_pd,dtype=np.dtype('<i8')).reshape((1024,1024))\n"
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
