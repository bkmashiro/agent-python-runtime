package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestLoadArtifactIdentityBindsManifestToBytes(t *testing.T) {
	directory := t.TempDir()
	wasm := []byte("fixture-wasm")
	artifactPath := filepath.Join(directory, "guest.wasm")
	manifestPath := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(artifactPath, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wasm)
	manifest := map[string]any{
		"artifact": map[string]any{"filename": "guest.wasm", "sha256": hex.EncodeToString(digest[:]), "size": len(wasm)},
		"build":    map[string]any{"repository_commit": "0123456789012345678901234567890123456789", "compiler_target": "wasm32-wasip1", "execution_model": "reactor"},
		"target":   "wasm32-wasip1",
	}
	encoded, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, artifact, err := loadArtifactIdentity(artifactPath, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact) != string(wasm) || identity.SHA256 != hex.EncodeToString(digest[:]) || identity.SourceCommit != manifest["build"].(map[string]any)["repository_commit"] {
		t.Fatalf("identity mismatch: %#v", identity)
	}
	if err := os.WriteFile(artifactPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadArtifactIdentity(artifactPath, manifestPath); err == nil {
		t.Fatal("tampered artifact was accepted")
	}
}

func TestRepositoryJSONSchemaAcceptsCanonicalEvidence(t *testing.T) {
	schemaBytes, err := os.ReadFile("../../benchmark/v1/evidence.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://github.com/bkmashiro/agent-python-runtime/benchmark/v1/evidence.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	sample := sampleEvidence{
		InstantiateGuestNS: 1, InitializeNS: 1, RuntimeInitNS: 1, PrepareNS: 1,
		ExecuteNS: 1, RunTotalNS: 1, RequestBytes: 1, ResultBytes: 1,
	}
	capabilitySample := sample
	capabilitySample.CapabilityNS = 1
	evidence := benchmarkEvidence{
		SchemaVersion: 1,
		EvidenceClass: "production-safe",
		Artifact: artifactIdentity{
			Filename: "guest.wasm", SHA256: strings.Repeat("a", 64), Size: 1,
			SourceCommit: strings.Repeat("b", 40), Target: "wasm32-wasip1", Execution: "reactor",
		},
		HostSource:  hostSourceIdentity{Revision: strings.Repeat("c", 40)},
		Backend:     backendIdentity{Name: "wazero", ResetMode: "fresh-instance"},
		Environment: environmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.24"},
		Fixture:     fixtureIdentity{Samples: 3, CapabilityOperations: 1, ProviderDelayNanoseconds: 2_000_000},
		CompileOnce: compileEvidence{InstantiateHostNS: 1, CompileNS: 1},
		Workloads: workloadEvidence{
			Execute:    []sampleEvidence{sample, sample, sample},
			Capability: []sampleEvidence{capabilitySample, capabilitySample, capabilitySample},
		},
		Limitations: []string{"fixture"},
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceValidationRequiresCanonicalPhasesAndClasses(t *testing.T) {
	sample := sampleEvidence{
		InstantiateGuestNS: 1,
		InitializeNS:       1,
		RuntimeInitNS:      1,
		PrepareNS:          1,
		ExecuteNS:          1,
		CapabilityNS:       0,
		RunTotalNS:         1,
		RequestBytes:       1,
		ResultBytes:        1,
	}
	capabilitySample := sample
	capabilitySample.CapabilityNS = 1
	evidence := benchmarkEvidence{
		SchemaVersion: 1,
		EvidenceClass: "production-safe",
		Artifact:      artifactIdentity{SHA256: "digest", Size: 1, SourceCommit: "commit", Target: "wasm32-wasip1"},
		HostSource:    hostSourceIdentity{Revision: "host-commit"},
		Backend:       backendIdentity{Name: "wazero", ResetMode: "fresh-instance"},
		Fixture:       fixtureIdentity{Samples: 3, CapabilityOperations: 1, ProviderDelayNanoseconds: 2_000_000},
		CompileOnce:   compileEvidence{InstantiateHostNS: 1, CompileNS: 1},
		Workloads: workloadEvidence{
			Execute:    []sampleEvidence{sample, sample, sample},
			Capability: []sampleEvidence{capabilitySample, capabilitySample, capabilitySample},
		},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalidClass := range []string{"", "production", "unsafe"} {
		invalid := evidence
		invalid.EvidenceClass = invalidClass
		if err := invalid.Validate(); err == nil {
			t.Fatalf("class %q was accepted", invalidClass)
		}
	}
	invalid := evidence
	invalid.Workloads.Execute = []sampleEvidence{{RunTotalNS: 1, RequestBytes: 1, ResultBytes: 1}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("missing lifecycle phases were accepted")
	}
}
