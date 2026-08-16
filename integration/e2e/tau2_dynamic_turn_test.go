package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTau2DynamicModelTurnThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	sourcePath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_SOURCE_FILE")
	outputPath := os.Getenv("PYSOLATE_TAU2_DYNAMIC_OUTPUT_FILE")
	capabilityName := os.Getenv("PYSOLATE_TAU2_EXPECTED_CAPABILITY")
	argumentsText := os.Getenv("PYSOLATE_TAU2_EXPECTED_ARGUMENTS")
	if python == "" || sourceRoot == "" || sourcePath == "" || outputPath == "" || capabilityName == "" || argumentsText == "" {
		t.Skip("dynamic tau2 turn environment is required")
	}
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(outputPath) || sourcePath == outputPath {
		t.Fatal("dynamic paths must be distinct absolute paths")
	}
	for _, path := range []string{sourcePath, filepath.Dir(outputPath)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private dynamic path has group/other permissions: %s", path)
		}
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) == 0 || len(source) > 16*1024 {
		t.Fatal("dynamic source size is outside the bounded contract")
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(argumentsText), &arguments); err != nil {
		t.Fatal(err)
	}
	canonicalArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}

	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2ReadPlan(t, python, sourceRoot)
	profile := tau2CanaryProfile(t, wasm)
	run := runTau2SourceBoundTurn(t, wasm, profile, plan, "model", capabilityName, string(canonicalArguments), string(source))
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version":  "pysolate.tau2-dynamic-turn-private.v1",
		"content":         run.Content,
		"request_sha256":  tau2Digest(run.Request),
		"response_sha256": tau2Digest(run.Payload),
		"receipt":         run.Receipt,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(outputPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outputPath, 0o600); err != nil {
		t.Fatal(err)
	}
}
