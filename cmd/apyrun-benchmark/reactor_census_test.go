package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRunMainWritesFailClosedReactorCensus(t *testing.T) {
	originalHostSource := reactorCensusHostSource
	reactorCensusHostSource = func() (hostSourceIdentity, error) {
		return hostSourceIdentity{Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
	}
	t.Cleanup(func() { reactorCensusHostSource = originalHostSource })
	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "guest.wasm")
	manifestPath := filepath.Join(directory, "manifest.json")
	outputPath := filepath.Join(directory, "census.json")
	wasm := censusFixedMemoryModule()
	if err := os.WriteFile(artifactPath, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wasm)
	manifest := map[string]any{
		"artifact_profile": "base",
		"artifact": map[string]any{
			"filename": "guest.wasm", "sha256": hex.EncodeToString(digest[:]), "size": len(wasm),
		},
		"build": map[string]any{
			"repository_commit": "0123456789012345678901234567890123456789",
			"compiler_target":   "wasm32-wasip1",
			"execution_model":   "reactor",
		},
		"target": "wasm32-wasip1",
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runMain([]string{
		"-kind", "reactor-census",
		"-artifact", artifactPath,
		"-manifest", manifestPath,
		"-output", outputPath,
	}); err != nil {
		t.Fatal(err)
	}
	resultBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var evidence reactorCensusEvidence
	if err := json.Unmarshal(resultBytes, &evidence); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(resultBytes, &instance); err != nil {
		t.Fatal(err)
	}
	if err := compileReactorCensusSchema(t).Validate(instance); err != nil {
		t.Fatal(err)
	}
	if !evidence.State.Memory.COWEligible || evidence.State.RestoreDecision != wazeroengine.ReactorRestoreSingleUseOnly {
		t.Fatalf("unexpected census evidence %#v", evidence)
	}
}

func censusFixedMemoryModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
		0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	}
}

func compileReactorCensusSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../benchmark/v1/reactor-census.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://github.com/bkmashiro/agent-python-runtime/benchmark/v1/reactor-census.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
