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

func compileRepositoryEvidenceSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
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
	return schema
}

func compilePreparedEvidenceSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../benchmark/v1/prepared-evidence.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://github.com/bkmashiro/agent-python-runtime/benchmark/v1/prepared-evidence.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestPreparedEvidenceSeparatesReadyFirstSteadyAndNoCopy(t *testing.T) {
	sample := preparedSampleEvidence{
		PoolHitNS: 1, PrepareNS: 1, ExecuteNS: 1, RunTotalNS: 1,
		RefillInstantiateGuestNS: 1, RefillInitializeNS: 1, RefillRuntimeInitNS: 1,
		RequestBytes: 1, ResultBytes: 1, RetainedGuestMemoryBytes: 128 * 1024 * 1024,
	}
	capabilitySample := sample
	capabilitySample.CapabilityNS = 1
	evidence := preparedBenchmarkEvidence{
		SchemaVersion: 1, EvidenceKind: "single-use-preinitialized", EvidenceClass: "production-safe",
		Artifact:    artifactIdentity{Filename: "guest.wasm", SHA256: strings.Repeat("a", 64), Size: 1, SourceCommit: strings.Repeat("b", 40), Target: "wasm32-wasip1", Execution: "reactor"},
		HostSource:  hostSourceIdentity{Revision: strings.Repeat("c", 40)},
		Backend:     backendIdentity{Name: "wazero", ResetMode: "fresh-instance"},
		Environment: environmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.24"},
		Fixture:     preparedFixtureIdentity{Samples: 3, CapabilityOperations: 1, ProviderDelayNanoseconds: 2_000_000, PreparedCapacity: 1},
		CompileOnce: compileEvidence{InstantiateHostNS: 1, CompileNS: 1},
		Readiness:   preparedReadinessEvidence{FactoryNewTotalNS: 1, InstantiateGuestNS: 1, InitializeNS: 1, RuntimeInitNS: 1, ReadyInstances: 1, RetainedGuestMemoryBytes: 128 * 1024 * 1024},
		StateCopy:   stateCopyEvidence{Applicable: false, Reason: "single-use instances are never restored"},
		Workloads:   preparedWorkloadEvidence{FirstExecute: sample, SteadyExecute: []preparedSampleEvidence{sample, sample, sample}, SteadyCapability: []preparedSampleEvidence{capabilitySample, capabilitySample, capabilitySample}},
		Limitations: []string{"fixture"},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(evidence)
	var instance any
	_ = json.Unmarshal(encoded, &instance)
	if err := compilePreparedEvidenceSchema(t).Validate(instance); err != nil {
		t.Fatal(err)
	}
	invalid := evidence
	invalid.StateCopy.Applicable = true
	if err := invalid.Validate(); err == nil {
		t.Fatal("prepared evidence claimed an unsupported state copy")
	}
}

func TestPreparedBenchmarkWithRealGuestArtifact(t *testing.T) {
	artifact := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifact == "" {
		t.Skip("AGENT_RUNTIME_GUEST is not set")
	}
	evidence, err := runPreparedBenchmarkWithHostSource(benchmarkOptions{
		ArtifactPath: artifact,
		ManifestPath: filepath.Join(filepath.Dir(artifact), "manifest.json"),
		Class:        "production-safe", Strategy: "single-use-preinitialized", Samples: 3,
	}, hostSourceIdentity{Revision: strings.Repeat("d", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.Readiness.RetainedGuestMemoryBytes != 128*1024*1024 || evidence.Backend.ResetMode != "fresh-instance" || evidence.StateCopy.Applicable {
		t.Fatalf("prepared evidence widened or drifted: %#v", evidence)
	}
	encoded, _ := json.Marshal(evidence)
	var instance any
	_ = json.Unmarshal(encoded, &instance)
	if err := compilePreparedEvidenceSchema(t).Validate(instance); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryJSONSchemaAcceptsCanonicalEvidence(t *testing.T) {
	schema := compileRepositoryEvidenceSchema(t)
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

func TestCheckedInBenchmarkEvidenceValidates(t *testing.T) {
	paths, err := filepath.Glob("../../docs/benchmarks/runtime-*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("checked-in benchmark evidence missing: paths=%v err=%v", paths, err)
	}
	schema := compileRepositoryEvidenceSchema(t)
	for _, path := range paths {
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var evidence benchmarkEvidence
		if err := json.Unmarshal(encoded, &evidence); err != nil {
			t.Fatal(err)
		}
		if err := evidence.Validate(); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var instance any
		if err := json.Unmarshal(encoded, &instance); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(instance); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
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
