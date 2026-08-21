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
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

const (
	digestA        = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	trustedPrepare = "import numpy as np\n"
)

type probeCase struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Result    any    `json:"result,omitempty"`
	Claims    uint32 `json:"claims"`
	Consumed  uint32 `json:"consumed"`
	Discarded uint32 `json:"discarded"`
}

type report struct {
	SchemaVersion  string      `json:"schema_version"`
	ArtifactSHA256 string      `json:"artifact_sha256"`
	Cases          []probeCase `json:"cases"`
}

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, artifactSHA, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	cases := []struct {
		id, code                                string
		wantStatus                              string
		wantClaims, wantConsumed, wantDiscarded uint32
	}{
		{"reached", "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = {'sum': int(dataset.sum()), 'shape': list(dataset.shape)}\n", "ok", 1, 1, 0},
		{"branch_not_taken", "import numpy as np\nif False:\n    dataset = np.load('/workspace/input.npy', allow_pickle=False)\nresult = 'unreached'\n", "ok", 0, 0, 1},
		{"earlier_exception", "import numpy as np\nraise ValueError('before load')\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\n", "error", 0, 0, 1},
	}
	out := report{SchemaVersion: "pysolate.prepared-data-claim-probe.v1", ArtifactSHA256: artifactSHA}
	for _, candidate := range cases {
		observed, err := runCase(wasm, profile, candidate.id, candidate.code)
		if err != nil {
			fail(fmt.Errorf("%s: %w", candidate.id, err))
		}
		if observed.Status != candidate.wantStatus || observed.Claims != candidate.wantClaims || observed.Consumed != candidate.wantConsumed || observed.Discarded != candidate.wantDiscarded {
			fail(fmt.Errorf("%s invariant mismatch: %+v", candidate.id, observed))
		}
		out.Cases = append(out.Cases, observed)
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

func runCase(wasm []byte, profile runtimeconfig.ExecutionProfile, id, code string) (probeCase, error) {
	decision, capsule, err := token()
	if err != nil {
		return probeCase{}, err
	}
	table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		return probeCase{}, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.MaxRequestBytes = 16 << 20
	config.MaxResponseBytes = 16 << 20
	config.ExecutionProfile = &profile
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	config.Mechanisms.SemanticAnalysis = true
	runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(context.Background(), wasm, config)
	if err != nil {
		return probeCase{}, err
	}
	defer runner.Close(context.Background())
	engine, ok := runner.(*wazeroengine.Engine)
	if !ok {
		return probeCase{}, errors.New("wazero factory did not return concrete engine")
	}
	if err := engine.PrepareNumpyCOWShard(context.Background()); err != nil {
		return probeCase{}, err
	}
	prepare := monkeypatchSource(decision.IdentitySHA256)
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-data-claim-" + id, Code: code, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		return probeCase{}, err
	}
	response, runErr := runner.Run(context.Background(), request, prepare)
	if runErr != nil {
		return probeCase{}, runErr
	}
	var envelope struct {
		Status string `json:"status"`
		Result any    `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || (envelope.Status != "ok" && envelope.Status != "error") {
		return probeCase{}, errors.New("invalid Guest response")
	}
	evidence := table.Evidence()
	return probeCase{ID: id, Status: envelope.Status, Result: envelope.Result, Claims: evidence.Claims, Consumed: evidence.Consumed, Discarded: evidence.Discarded}, nil
}

func token() (preparedregion.PreparedRegionDecision, preparedregion.PreparedRegionCapsule, error) {
	_, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: digestA, ASTSHA256: digestA, AnalysisSHA256: digestA, RegionID: digestA,
		RegionSpan:         preparedregion.SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 1},
		RegionSourceSHA256: digestA, LiveInsSHA256: digestA, EnvironmentSHA256: digestA,
		ExecutionProfileSHA256: digestA, ImportClosureSHA256: digestA, CapabilityPlanSHA256: digestA,
		PassConfigSHA256: digestA, Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "dataset",
	})
	if err != nil {
		return preparedregion.PreparedRegionDecision{}, preparedregion.PreparedRegionCapsule{}, err
	}
	_, capsule, err := preparedregion.SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`true`))
	return decision, capsule, err
}

func monkeypatchSource(decision string) string {
	body := make([]byte, 6*8)
	for index := uint64(0); index < 6; index++ {
		for byteIndex := uint(0); byteIndex < 8; byteIndex++ {
			body[index*8+uint64(byteIndex)] = byte(index >> (8 * byteIndex))
		}
	}
	payload := base64.StdEncoding.EncodeToString(body)
	return strings.Join([]string{
		"import base64 as _pd_base64, json as _pd_json, numpy as np, _agent_runtime_host as _pd_host",
		"_pd_body = _pd_base64.b64decode(" + fmt.Sprintf("%q", payload) + ")",
		"def _pd_load(path, *, allow_pickle=False):",
		"    if path != '/workspace/input.npy' or allow_pickle is not False:",
		"        raise RuntimeError('prepared data call mismatch')",
		"    if _pd_json.loads(_pd_host.materialize_value(" + fmt.Sprintf("%q", decision) + ")) is not True:",
		"        raise RuntimeError('prepared data claim token mismatch')",
		"    return np.frombuffer(_pd_body, dtype=np.dtype('<i8')).reshape((2, 3)).copy(order='C')",
		"np.load = _pd_load",
	}, "\n") + "\n"
}

func loadArtifact(root string) ([]byte, runtimeconfig.ExecutionProfile, string, error) {
	var zero runtimeconfig.ExecutionProfile
	if root == "" {
		return nil, zero, "", errors.New("artifact root is required")
	}
	wasm, err := os.ReadFile(filepath.Join(root, "agent-python-runtime-numpy-core.wasm"))
	if err != nil {
		return nil, zero, "", err
	}
	manifestRaw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, zero, "", err
	}
	inventoryRaw, err := os.ReadFile(filepath.Join(root, "import-inventory.json"))
	if err != nil {
		return nil, zero, "", err
	}
	qualificationRaw, err := os.ReadFile(filepath.Join(root, "import-qualification.json"))
	if err != nil {
		return nil, zero, "", err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifestRaw, inventoryRaw, qualificationRaw)
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
