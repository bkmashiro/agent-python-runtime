package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const trustedPrepare = "import numpy as np\n"

type envelope struct {
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	ResultPresent bool            `json:"result_present"`
}

type report struct {
	SchemaVersion            string                          `json:"schema_version"`
	ArtifactSHA256           string                          `json:"artifact_sha256"`
	TrustedPrepareSHA256     string                          `json:"trusted_prepare_sha256"`
	Image                    wazeroengine.PreparedImageState `json:"image"`
	FirstResult              json.RawMessage                 `json:"first_result"`
	SecondResult             json.RawMessage                 `json:"second_result"`
	PreparedAliasVisible     bool                            `json:"prepared_alias_visible"`
	ConsumerMutationIsolated bool                            `json:"consumer_mutation_isolated"`
	PrivateCOWSelected       bool                            `json:"private_cow_selected"`
	Fallback                 bool                            `json:"fallback"`
	PrepareNanos             uint64                          `json:"prepare_nanos"`
	FirstRunNanos            uint64                          `json:"first_run_nanos"`
	SecondRunNanos           uint64                          `json:"second_run_nanos"`
}

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core artifact root")
	flag.Parse()
	if *root == "" {
		fail(errors.New("artifact root is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	wasm, profile, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 5 * time.Minute
	config.MaxRequestBytes = 16 << 20
	config.MaxResponseBytes = 16 << 20
	config.MemoryLimitPages = 16384
	config.ExecutionProfile = &profile
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	engine, err := wazeroengine.New(ctx, wasm, config)
	if err != nil {
		fail(err)
	}
	defer engine.Close(ctx)
	started := time.Now()
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(ctx, trustedPrepare); err != nil {
		fail(err)
	}
	prepareNanos := uint64(time.Since(started))
	started = time.Now()
	first, err := run(ctx, engine, "np._pysolate_private_probe = 1\nresult = {'version': np.__version__, 'alias_visible': True}")
	if err != nil {
		fail(err)
	}
	firstRunNanos := uint64(time.Since(started))
	started = time.Now()
	second, err := run(ctx, engine, "result = {'version': np.__version__, 'alias_visible': True, 'mutation_visible': hasattr(np, '_pysolate_private_probe')}")
	if err != nil {
		fail(err)
	}
	secondRunNanos := uint64(time.Since(started))
	var firstValue struct {
		AliasVisible bool `json:"alias_visible"`
	}
	var secondValue struct {
		AliasVisible    bool `json:"alias_visible"`
		MutationVisible bool `json:"mutation_visible"`
	}
	if json.Unmarshal(first, &firstValue) != nil || json.Unmarshal(second, &secondValue) != nil {
		fail(errors.New("invalid result bodies"))
	}
	probe := engine.COWProbe()
	state := engine.PreparedImageState()
	digest := sha256.Sum256([]byte(trustedPrepare))
	out := report{
		SchemaVersion: "pysolate.prepared-data-shard-probe.v1", ArtifactSHA256: profile.ArtifactSHA256(),
		TrustedPrepareSHA256: fmt.Sprintf("sha256:%x", digest[:]), Image: state,
		FirstResult: first, SecondResult: second, PreparedAliasVisible: firstValue.AliasVisible && secondValue.AliasVisible,
		ConsumerMutationIsolated: !secondValue.MutationVisible, PrivateCOWSelected: probe.COWSelected, Fallback: probe.Fallback,
		PrepareNanos: prepareNanos, FirstRunNanos: firstRunNanos, SecondRunNanos: secondRunNanos,
	}
	if !out.PreparedAliasVisible || !out.ConsumerMutationIsolated || !out.PrivateCOWSelected || out.Fallback || state.TrustedPrepareSHA256 != out.TrustedPrepareSHA256 {
		fail(errors.New("package-ready private-COW invariants failed"))
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

func run(ctx context.Context, engine *wazeroengine.Engine, code string) (json.RawMessage, error) {
	request, _ := json.Marshal(map[string]any{"run_id": fmt.Sprintf("prepared-shard-%x", sha256.Sum256([]byte(code))), "code": code, "inputs": map[string]any{}})
	raw, err := engine.Run(ctx, request, "")
	if err != nil {
		return nil, err
	}
	var value envelope
	if json.Unmarshal(raw, &value) != nil || value.Status != "ok" || !value.ResultPresent {
		return nil, errors.New("Guest response rejected")
	}
	return append(json.RawMessage(nil), value.Result...), nil
}

func loadArtifact(root string) ([]byte, runtimeconfig.ExecutionProfile, error) {
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, "dist", name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return nil, runtimeconfig.ExecutionProfile{}, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return wasm, profile, err
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
