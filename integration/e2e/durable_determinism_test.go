package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const (
	durableChildEnv = "PYSOLATE_DURABLE_DETERMINISM_CHILD"
	durableCaseEnv  = "PYSOLATE_DURABLE_DETERMINISM_CASE"
	durableSeedEnv  = "PYSOLATE_DURABLE_DETERMINISM_SEED"
)

type durableCase struct {
	name string
	code string
}

type durableObservation struct {
	Payload         json.RawMessage `json:"payload,omitempty"`
	RunError        string          `json:"run_error,omitempty"`
	ArtifactSHA256  string          `json:"artifact_sha256"`
	ProfileArtifact string          `json:"profile_artifact_sha256"`
}

func TestDurableDeterminismAcrossIndependentProcessRestarts(t *testing.T) {
	guestArtifact(t)
	for _, testCase := range durableCases() {
		t.Run(testCase.name, func(t *testing.T) {
			first, err := runDurableProcess(testCase.name, "durable-e2e-seed")
			if err != nil {
				t.Fatal(err)
			}
			second, err := runDurableProcess(testCase.name, "durable-e2e-seed")
			if err != nil {
				t.Fatal(err)
			}
			if first.RunError != "" || second.RunError != "" {
				t.Fatalf("run errors: %q / %q", first.RunError, second.RunError)
			}
			if !bytes.Equal(first.Payload, second.Payload) {
				t.Fatalf("restart changed response:\n%s\n%s", first.Payload, second.Payload)
			}
			var response struct {
				Status string `json:"status"`
				Error  struct {
					Type string `json:"error_type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(first.Payload, &response); err != nil {
				t.Fatal(err)
			}
			if testCase.name == "exception" {
				if response.Status != "error" || response.Error.Type != "ValueError" {
					t.Fatalf("unexpected response: %s", first.Payload)
				}
			} else if response.Status != "ok" {
				t.Fatalf("unexpected response: %s", first.Payload)
			}
		})
	}
	for _, name := range []string{"python-random-default-instance", "numpy-default-rng", "os-urandom"} {
		t.Run(name+"-different-seed", func(t *testing.T) {
			first, err := runDurableProcess(name, "durable-e2e-seed-a")
			if err != nil {
				t.Fatal(err)
			}
			second, err := runDurableProcess(name, "durable-e2e-seed-b")
			if err != nil {
				t.Fatal(err)
			}
			if first.RunError != "" || second.RunError != "" {
				t.Fatalf("run errors: %q / %q", first.RunError, second.RunError)
			}
			if bytes.Equal(first.Payload, second.Payload) {
				t.Fatalf("different seeds produced the same response: %s", first.Payload)
			}
		})
	}
}

func TestDurableDeterminismProcessHelper(t *testing.T) {
	if os.Getenv(durableChildEnv) != "1" {
		t.Skip("process helper")
	}
	caseName := os.Getenv(durableCaseEnv)
	var selected durableCase
	for _, testCase := range durableCases() {
		if testCase.name == caseName {
			selected = testCase
			break
		}
	}
	if selected.name == "" {
		t.Fatalf("unknown deterministic case %q", caseName)
	}
	seed := os.Getenv(durableSeedEnv)
	if seed == "" {
		t.Fatal("deterministic seed is required")
	}

	wasm, identity := durableArtifact(t)
	config := durableConfig(t, identity, seed)
	runner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()

	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
		RunID: "durable-determinism-" + selected.name, Code: selected.code,
		Inputs: json.RawMessage(`{"seed":417}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, runErr := runner.Run(context.Background(), request, "")
	observation := durableObservation{Payload: canonicalDurableJSON(payload), ArtifactSHA256: identity.ArtifactSHA256, ProfileArtifact: config.ExecutionProfile.ArtifactSHA256()}
	if runErr != nil {
		observation.RunError = runErr.Error()
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func durableCases() []durableCase {
	return []durableCase{
		{name: "python-random-explicit-seed", code: `import random
random.seed(inputs["seed"])
result = {"random": [random.random(), random.getrandbits(32), random.randrange(1000000)]}`},
		{name: "python-random-default-instance", code: `import random
rng = random.Random()
result = {"random": [rng.random(), rng.getrandbits(32), rng.randrange(1000000)]}`},
		{name: "os-urandom", code: `import os
result = {"urandom_hex": os.urandom(24).hex()}`},
		{name: "logical-time", code: `import time
result = {"time_ns": time.time_ns(), "monotonic_ns": time.monotonic_ns(), "time_again_ns": time.time_ns()}`},
		{name: "set-iteration", code: `values = {"alpha", "beta", "gamma", "delta"}
result = {"iteration": list(values)}`},
		{name: "numpy-seeded-random-and-compute", code: `import numpy as np
rng = np.random.default_rng(inputs["seed"])
left = np.array([[1, 2], [3, 4]], dtype=np.int64)
right = np.array([5, 6], dtype=np.int64)
result = {"random": rng.integers(0, 1000000, size=6).tolist(), "dot": (left @ right).tolist(), "sum": int(left.sum())}`},
		{name: "numpy-default-rng", code: `import numpy as np
rng = np.random.default_rng()
result = {"random": rng.integers(0, 1000000, size=6).tolist()}`},
		{name: "exception", code: `raise ValueError("durable deterministic exception")`},
	}
}

func durableArtifact(t *testing.T) ([]byte, runtimeconfig.VerifiedArtifactIdentity) {
	artifactPath := guestArtifact(t)
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read Guest %s: %v", artifactPath, err)
	}
	root := filepath.Dir(artifactPath)
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := os.ReadFile(filepath.Join(root, "import-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := os.ReadFile(filepath.Join(root, "import-qualification.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), wasm, manifest, inventory, qualification)
	if err != nil {
		t.Fatalf("verify Guest distribution metadata: %v", err)
	}
	return wasm, identity
}

func durableConfig(t *testing.T, identity runtimeconfig.VerifiedArtifactIdentity, seed string) runtimeconfig.RunConfig {
	// The artifact qualification covers its NumPy extension. Standard Python
	// runs do not opt into the optional extension compatibility declaration.
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", identity.QualifiedImportRoots)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(identity.ArtifactSHA256, seed)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	config.ExecutionProfile = &profile
	config.DeterministicVerification = &deterministic
	return config
}

func runDurableProcess(caseName, seed string) (durableObservation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestDurableDeterminismProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), durableChildEnv+"=1", durableCaseEnv+"="+caseName, durableSeedEnv+"="+seed)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return durableObservation{}, fmt.Errorf("child process: %w: stdout=%s stderr=%s", err, stdout.Bytes(), stderr.Bytes())
	}
	line := bytes.SplitN(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'}, 2)[0]
	var observation durableObservation
	if err := json.Unmarshal(line, &observation); err != nil {
		return durableObservation{}, fmt.Errorf("decode child observation %q: %w", stdout.String(), err)
	}
	return observation, nil
}

func canonicalDurableJSON(payload []byte) json.RawMessage {
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return payload
	}
	return compact.Bytes()
}
