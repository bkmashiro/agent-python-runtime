package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

func TestDensitySweepSpecsUseCanonicalOrderAndFreshProcessPerSample(t *testing.T) {
	specs, err := densitySweepSpecs("fresh", 2, 512*1024*1024, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wantSlots := []uint32{1, 1, 2, 2, 4, 4, 8, 8, 16, 16}
	if len(specs) != len(wantSlots) {
		t.Fatalf("spec count=%d, want %d", len(specs), len(wantSlots))
	}
	for index, spec := range specs {
		if spec.SampleIndex != uint32(index) || spec.RequestedSlots != wantSlots[index] || spec.RepeatIndex != uint32(index%2) {
			t.Fatalf("spec %d drifted: %#v", index, spec)
		}
		if spec.Strategy != "fresh-instance" || spec.MaxRSSBytes != 512*1024*1024 || spec.Timeout != 30*time.Second {
			t.Fatalf("spec %d lost execution bounds: %#v", index, spec)
		}
	}
}

func TestDensitySweepSpecsAcceptSharedCacheOnlyAsExplicitStrategy(t *testing.T) {
	specs, err := densitySweepSpecs("single-use-preinitialized-shared-cache", 1, 512*1024*1024, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if spec.Strategy != "single-use-preinitialized-shared-cache" {
			t.Fatalf("shared-cache strategy drifted: %#v", spec)
		}
	}
}

func TestDensitySweepSpecsAcceptCOWReadySingleUse(t *testing.T) {
	specs, err := densitySweepSpecs("cow-ready-single-use", 1, 512*1024*1024, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if spec.Strategy != "cow-ready-single-use" {
			t.Fatalf("COW strategy drifted: %#v", spec)
		}
	}
}

func TestNumPyDensitySweepSpecsExtendTo64AndBindWarmup(t *testing.T) {
	specs, err := numpyDensitySweepSpecs("cow-ready-single-use", 2, 8*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantSlots := []uint32{1, 2, 4, 8, 16, 32, 64}
	if len(specs) != len(wantSlots)*2 {
		t.Fatalf("spec count=%d, want %d", len(specs), len(wantSlots)*2)
	}
	for index, spec := range specs {
		if spec.RequestedSlots != wantSlots[index/2] || spec.RepeatIndex != uint32(index%2) || spec.WarmupProfile != wazeroengine.COWWarmupNumPyReadyV1 {
			t.Fatalf("NumPy density spec %d drifted: %#v", index, spec)
		}
	}
}

func TestPreparedDensityShardsPreserveRealStrategyTopology(t *testing.T) {
	cow, err := preparedDensityShardCapacitiesForStrategy(64, "cow-ready-single-use", true)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cow) != "[64]" {
		t.Fatalf("COW topology=%v, want one 64-slot canonical image", cow)
	}
	nonCOW, err := preparedDensityShardCapacitiesForStrategy(64, "single-use-preinitialized", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonCOW) != 16 {
		t.Fatalf("non-COW shards=%d, want 16 hard-bounded runtimes", len(nonCOW))
	}
	for _, capacity := range nonCOW {
		if capacity != 4 {
			t.Fatalf("non-COW shard capacity=%d, want 4", capacity)
		}
	}
}

func TestDensitySweepSpecsRejectMissingBoundsAndUnknownStrategy(t *testing.T) {
	for name, invoke := range map[string]func() error{
		"unknown strategy": func() error {
			_, err := densitySweepSpecs("cow", 1, 1, time.Second)
			return err
		},
		"zero repeats": func() error {
			_, err := densitySweepSpecs("fresh", 0, 1, time.Second)
			return err
		},
		"zero memory guard": func() error {
			_, err := densitySweepSpecs("fresh", 1, 0, time.Second)
			return err
		},
		"zero timeout": func() error {
			_, err := densitySweepSpecs("fresh", 1, 1, 0)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := invoke(); err == nil {
				t.Fatal("invalid density sweep accepted")
			}
		})
	}
}

func TestValidateLifecycleDensityCLIOptionsSeparatesParentAndChild(t *testing.T) {
	parent := benchmarkOptions{
		ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "evidence.json",
		Kind: "lifecycle-density", Class: "production-safe", Strategy: "single-use-preinitialized", Samples: 1,
		MaxRSSBytes: 4 * 1024 * 1024 * 1024, ChildTimeout: 2 * time.Minute,
	}
	if err := validateLifecycleDensityOptions(parent, false, "linux"); err != nil {
		t.Fatal(err)
	}
	cow := parent
	cow.Strategy = "cow-ready-single-use"
	if err := validateLifecycleDensityOptions(cow, false, "linux"); err != nil {
		t.Fatalf("production-safe COW density rejected: %v", err)
	}
	spike := parent
	spike.Class = "preinitialization-spike"
	if err := validateLifecycleDensityOptions(spike, false, "linux"); err != nil {
		t.Fatalf("preinitialization spike density rejected: %v", err)
	}
	sharedCacheSpike := spike
	sharedCacheSpike.Strategy = "single-use-preinitialized-shared-cache"
	if err := validateLifecycleDensityOptions(sharedCacheSpike, false, "linux"); err != nil {
		t.Fatalf("preinitialization shared-cache density rejected: %v", err)
	}
	sharedCacheProduction := parent
	sharedCacheProduction.Strategy = "single-use-preinitialized-shared-cache"
	if err := validateLifecycleDensityOptions(sharedCacheProduction, false, "linux"); err == nil {
		t.Fatal("production-safe density accepted experimental shared cache")
	}
	numpyExperiment := parent
	numpyExperiment.Class = "profile-candidate"
	numpyExperiment.PreparedWarmupProfile = wazeroengine.COWWarmupNumPyReadyV1
	if err := validateLifecycleDensityOptions(numpyExperiment, false, "linux"); err != nil {
		t.Fatalf("NumPy density experiment rejected: %v", err)
	}
	numpyChild := numpyExperiment
	numpyChild.OutputPath = ""
	numpyChild.LifecycleDensityChild = true
	numpyChild.DensitySlots = 64
	if err := validateLifecycleDensityOptions(numpyChild, true, "linux"); err != nil {
		t.Fatalf("NumPy 64-slot child rejected: %v", err)
	}
	unknownWarmup := numpyExperiment
	unknownWarmup.PreparedWarmupProfile = "unknown-v1"
	if err := validateLifecycleDensityOptions(unknownWarmup, false, "linux"); err == nil {
		t.Fatal("unknown prepared warmup accepted")
	}
	child := parent
	child.OutputPath = ""
	child.LifecycleDensityChild = true
	child.DensitySlots = 2
	if err := validateLifecycleDensityOptions(child, true, "linux"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*benchmarkOptions){
		"non-Linux":       func(*benchmarkOptions) {},
		"fresh strategy":  func(value *benchmarkOptions) { value.Strategy = "fresh" },
		"missing guard":   func(value *benchmarkOptions) { value.MaxRSSBytes = 0 },
		"missing output":  func(value *benchmarkOptions) { value.OutputPath = "" },
		"invalid repeats": func(value *benchmarkOptions) { value.Samples = 0 },
		"invalid class":   func(value *benchmarkOptions) { value.Class = "full" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := parent
			goos := "linux"
			if name == "non-Linux" {
				goos = "darwin"
			}
			mutate(&candidate)
			if err := validateLifecycleDensityOptions(candidate, false, goos); err == nil {
				t.Fatal("invalid lifecycle-density parent options accepted")
			}
		})
	}
}

func TestPreparedDensityShardCapacitiesPreserveProductionHardCap(t *testing.T) {
	for slots, want := range map[uint32][]uint32{
		1:  {1},
		2:  {2},
		4:  {4},
		8:  {4, 4},
		16: {4, 4, 4, 4},
	} {
		got, err := preparedDensityShardCapacities(slots)
		if err != nil {
			t.Fatalf("slots=%d: %v", slots, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("slots=%d capacities=%v, want %v", slots, got, want)
		}
	}
	for _, slots := range []uint32{0, 3, 17} {
		if _, err := preparedDensityShardCapacities(slots); err == nil {
			t.Fatalf("noncanonical slots=%d accepted", slots)
		}
	}
}

func TestPreparedDensitySharedCacheWarmsBeforeFollowers(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	followerStarted := make(chan int, 3)
	done := make(chan error, 1)
	go func() {
		_, err := createPreparedDensityShards(
			[]uint32{4, 4, 4, 4},
			"single-use-preinitialized-shared-cache",
			func(index int, capacity uint32) preparedDensityShardResult {
				if index == 0 {
					close(firstStarted)
					<-releaseFirst
				} else {
					followerStarted <- index
				}
				return preparedDensityShardResult{capacity: capacity}
			},
		)
		done <- err
	}()
	<-firstStarted
	select {
	case index := <-followerStarted:
		t.Fatalf("follower %d started before cache warm-up completed", index)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)
	for range 3 {
		<-followerStarted
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPreparedDensityDefaultStartsShardsConcurrently(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	followerStarted := make(chan int, 1)
	done := make(chan error, 1)
	go func() {
		_, err := createPreparedDensityShards(
			[]uint32{4, 4},
			"single-use-preinitialized",
			func(index int, capacity uint32) preparedDensityShardResult {
				if index == 0 {
					close(firstStarted)
					<-releaseFirst
				} else {
					followerStarted <- index
				}
				return preparedDensityShardResult{capacity: capacity}
			},
		)
		done <- err
	}()
	<-firstStarted
	select {
	case <-followerStarted:
	case <-time.After(time.Second):
		t.Fatal("default density strategy serialized shards")
	}
	close(releaseFirst)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDensityChildEnvelopeBindsArtifactStrategyAndRequestedSlots(t *testing.T) {
	specs, err := densitySweepSpecs("single-use-preinitialized", 1, 4*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[1]
	artifact := artifactIdentity{SHA256: strings.Repeat("a", 64), ArtifactProfile: "base"}
	base := validDensityChildEnvelope(spec, artifact)
	if err := validateDensityChildEnvelope(base, spec, artifact); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*densityChildEnvelope){
		"artifact": func(value *densityChildEnvelope) { value.ArtifactSHA256 = strings.Repeat("b", 64) },
		"profile":  func(value *densityChildEnvelope) { value.ArtifactProfile = "numpy-core" },
		"strategy": func(value *densityChildEnvelope) { value.Strategy.Active = "fresh-instance" },
		"slots":    func(value *densityChildEnvelope) { value.Sample.RequestedSlots++ },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := validateDensityChildEnvelope(candidate, spec, artifact); err == nil {
				t.Fatal("drifted density child envelope accepted")
			}
		})
	}
}

func TestAssembleLifecycleDensityEvidenceValidatesCanonicalChildSweep(t *testing.T) {
	artifactBytes := []byte("fixture-wasm")
	digest := sha256.Sum256(artifactBytes)
	artifact := artifactIdentity{
		Filename: "guest.wasm", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(artifactBytes)),
		SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", Execution: "reactor",
	}
	specs, err := densitySweepSpecs("single-use-preinitialized-shared-cache", 1, 4*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	invocations := 0
	evidence, encoded, err := assembleLifecycleDensityEvidence(
		context.Background(), artifact, artifactBytes,
		hostSourceIdentity{Revision: strings.Repeat("b", 40)}, "preinitialization-spike", specs,
		[]byte("01234567890123456789012345678901"),
		func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			invocations++
			return densityChildInvocation{
				Envelope: validDensityChildEnvelope(spec, artifact),
				Process:  boundedChildResult{PID: 100 + invocations, StartedAtUnixNS: int64(invocations), MaxObservedRSSBytes: 1},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if invocations != 5 || len(evidence.Samples) != 5 || evidence.Summary.SampleCount != 5 {
		t.Fatalf("canonical child sweep incomplete: invocations=%d evidence=%#v", invocations, evidence)
	}
	if !strings.Contains(evidence.Limitations[len(evidence.Limitations)-1], "does not approve") {
		t.Fatal("preinitialization lifecycle density lacks its experimental limitation")
	}
	sharedCacheBoundary := false
	for _, limitation := range evidence.Limitations {
		if strings.Contains(limitation, "first shard") && strings.Contains(limitation, "separate wazero runtime") {
			sharedCacheBoundary = true
		}
	}
	if !sharedCacheBoundary {
		t.Fatal("shared-cache lifecycle density lacks its ownership and warm-up limitation")
	}
	if err := runtimeevidence.ValidateLifecycleDensityJSON(encoded); err != nil {
		t.Fatal(err)
	}
	if evidence.Samples[0].ProcessInstanceSHA256 == evidence.Samples[1].ProcessInstanceSHA256 {
		t.Fatal("parent reused process-instance identity")
	}
	directory := t.TempDir()
	artifactPath := filepath.Join(directory, "guest.wasm")
	manifestPath := filepath.Join(directory, "manifest.json")
	evidencePath := filepath.Join(directory, "evidence.json")
	manifest := fmt.Sprintf(`{"artifact_profile":"base","artifact":{"filename":"guest.wasm","sha256":"%s","size":%d},"build":{"repository_commit":"%s","compiler_target":"wasm32-wasip1","execution_model":"reactor"},"target":"wasm32-wasip1"}`,
		artifact.SHA256, len(artifactBytes), artifact.SourceCommit)
	for path, data := range map[string][]byte{
		artifactPath: artifactBytes, manifestPath: []byte(manifest), evidencePath: encoded,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	schemaPath := filepath.Join(directory, "lifecycle-density.schema.json")
	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "benchmark", "v1", "lifecycle-density.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, schemaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	options := benchmarkOptions{InputPath: evidencePath, SchemaPath: schemaPath, ArtifactPath: artifactPath, ManifestPath: manifestPath}
	verdict, err := validateLifecycleDensityInput(options)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Valid || verdict.ArtifactSHA256 != artifact.SHA256 || verdict.Samples != len(evidence.Samples) {
		t.Fatalf("standalone validation verdict drifted: %#v", verdict)
	}
	var structuralDocument map[string]any
	if err := json.Unmarshal(encoded, &structuralDocument); err != nil {
		t.Fatal(err)
	}
	structuralSummary, ok := structuralDocument["summary"].(map[string]any)
	if !ok {
		t.Fatal("fixture summary is not an object")
	}
	delete(structuralSummary, "estimated_fixed_bytes")
	delete(structuralSummary, "estimated_per_slot_bytes")
	structuralSummary["estimated_fixed_bytes"] = nil
	structuralBytes, err := json.Marshal(structuralDocument)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeevidence.ValidateLifecycleDensityJSON(structuralBytes); err != nil {
		t.Fatalf("structural-only fixture unexpectedly failed semantic validation: %v", err)
	}
	if err := os.WriteFile(evidencePath, structuralBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateLifecycleDensityInput(options); err == nil || !strings.Contains(err.Error(), "JSON Schema") {
		t.Fatalf("standalone validator did not enforce structural schema: %v", err)
	}
	duplicate := append([]byte(`{"schema_version":1,`), encoded[1:]...)
	if err := os.WriteFile(evidencePath, duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateLifecycleDensityInput(options); err == nil {
		t.Fatal("standalone validator accepted duplicate JSON key")
	}
}

func TestAssembleNumPyLifecycleDensityBindsSameArtifactWarmupAndRealTopology(t *testing.T) {
	artifactBytes := []byte("fixture-wasm")
	artifactDigest := sha256.Sum256(artifactBytes)
	artifact := artifactIdentity{
		Filename: "numpy.wasm", SHA256: hex.EncodeToString(artifactDigest[:]), Size: int64(len(artifactBytes)),
		SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "numpy-core", Target: "wasm32-wasip1", Execution: "reactor",
	}
	warmupDigest := sha256.New()
	_, _ = warmupDigest.Write(artifactBytes)
	_, _ = warmupDigest.Write([]byte{0})
	_, _ = warmupDigest.Write([]byte(wazeroengine.COWWarmupNumPyReadyV1))
	expectedGeneration := hex.EncodeToString(warmupDigest.Sum(nil))
	for _, strategy := range []string{"cow-ready-single-use", "single-use-preinitialized"} {
		t.Run(strategy, func(t *testing.T) {
			specs, err := numpyDensitySweepSpecs(strategy, 1, 8*1024*1024*1024, 2*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			evidence, encoded, err := assembleLifecycleDensityEvidence(
				context.Background(), artifact, artifactBytes,
				hostSourceIdentity{Revision: strings.Repeat("b", 40)}, "profile-candidate", specs,
				[]byte("01234567890123456789012345678901"),
				func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
					envelope := validDensityChildEnvelope(spec, artifact)
					envelope.Warmup.GenerationSHA256 = expectedGeneration
					return densityChildInvocation{
						Envelope: envelope,
						Process:  boundedChildResult{PID: 100 + int(spec.SampleIndex), StartedAtUnixNS: int64(spec.SampleIndex + 1), MaxObservedRSSBytes: 1},
					}, nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.SchemaVersion != 2 || evidence.Warmup == nil || evidence.Warmup.GenerationSHA256 != expectedGeneration ||
				len(evidence.Samples) != 7 || evidence.Plan.SlotCounts[6] != 64 {
				t.Fatalf("NumPy lifecycle evidence drifted: %#v", evidence)
			}
			if err := runtimeevidence.ValidateLifecycleDensityJSON(encoded); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAssembleNumPyLifecycleDensityRejectsWarmupGenerationDrift(t *testing.T) {
	artifactBytes := []byte("fixture-wasm")
	digest := sha256.Sum256(artifactBytes)
	artifact := artifactIdentity{
		Filename: "numpy.wasm", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(artifactBytes)),
		SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "numpy-core", Target: "wasm32-wasip1", Execution: "reactor",
	}
	specs, err := numpyDensitySweepSpecs("single-use-preinitialized", 1, 8*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = assembleLifecycleDensityEvidence(
		context.Background(), artifact, artifactBytes,
		hostSourceIdentity{Revision: strings.Repeat("b", 40)}, "profile-candidate", specs,
		[]byte("01234567890123456789012345678901"),
		func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			envelope := validDensityChildEnvelope(spec, artifact)
			return densityChildInvocation{Envelope: envelope, Process: boundedChildResult{PID: 100 + int(spec.SampleIndex), StartedAtUnixNS: int64(spec.SampleIndex + 1), MaxObservedRSSBytes: 1}}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "artifact-bound") {
		t.Fatalf("warmup generation drift was accepted: %v", err)
	}
}

func TestAssembleCOWLifecycleDensityRequiresMappingAttribution(t *testing.T) {
	artifactBytes := []byte("fixture-wasm")
	digest := sha256.Sum256(artifactBytes)
	artifact := artifactIdentity{
		Filename: "guest.wasm", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(artifactBytes)),
		SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", Execution: "reactor",
	}
	specs, err := densitySweepSpecs("cow-ready-single-use", 1, 4*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evidence, encoded, err := assembleLifecycleDensityEvidence(
		context.Background(), artifact, artifactBytes,
		hostSourceIdentity{Revision: strings.Repeat("b", 40)}, "production-safe", specs,
		[]byte("01234567890123456789012345678901"),
		func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			return densityChildInvocation{
				Envelope: validDensityChildEnvelope(spec, artifact),
				Process:  boundedChildResult{PID: 100 + int(spec.SampleIndex), StartedAtUnixNS: int64(spec.SampleIndex + 1), MaxObservedRSSBytes: 1},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtimeevidence.ValidateLifecycleDensityJSON(encoded); err != nil {
		t.Fatal(err)
	}

	evidence.Samples[0].COWMappings = nil
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("missing COW mapping attribution was accepted: %v", err)
	}
}

func TestAssembleLifecycleDensityEvidenceRejectsEnvironmentDrift(t *testing.T) {
	artifactBytes := []byte("fixture-wasm")
	digest := sha256.Sum256(artifactBytes)
	artifact := artifactIdentity{
		Filename: "guest.wasm", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(artifactBytes)),
		SourceCommit: strings.Repeat("a", 40), ArtifactProfile: "base", Target: "wasm32-wasip1", Execution: "reactor",
	}
	specs, err := densitySweepSpecs("single-use-preinitialized", 1, 4*1024*1024*1024, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	invocations := 0
	_, _, err = assembleLifecycleDensityEvidence(
		context.Background(), artifact, artifactBytes,
		hostSourceIdentity{Revision: strings.Repeat("b", 40)}, "production-safe", specs,
		[]byte("01234567890123456789012345678901"),
		func(_ context.Context, spec densitySweepSpec) (densityChildInvocation, error) {
			invocations++
			envelope := validDensityChildEnvelope(spec, artifact)
			if invocations == 2 {
				envelope.Environment.KernelRelease = "drifted"
			}
			return densityChildInvocation{
				Envelope: envelope,
				Process:  boundedChildResult{PID: 100 + invocations, StartedAtUnixNS: int64(invocations), MaxObservedRSSBytes: 1},
			}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("child environment drift was not rejected: %v", err)
	}
}

func TestProcessInstanceSHA256BindsLaunchIdentity(t *testing.T) {
	nonce := []byte("01234567890123456789012345678901")
	first := processInstanceSHA256(nonce, boundedChildResult{PID: 11, StartedAtUnixNS: 100})
	second := processInstanceSHA256(nonce, boundedChildResult{PID: 12, StartedAtUnixNS: 100})
	if len(first) != 64 || first == second {
		t.Fatalf("process identities are not distinct lower-hex digests: %q %q", first, second)
	}
}

func TestPreparedDensityPhasesRequireEveryReadySlot(t *testing.T) {
	observations := []wazeroengine.Observation{
		{Phase: "instantiate_host", Duration: time.Nanosecond, Success: true},
		{Phase: "compile", Duration: 2 * time.Nanosecond, Success: true},
	}
	for range 2 {
		observations = append(observations,
			wazeroengine.Observation{Phase: "pool_prepare_instantiate_guest", Duration: 3 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare__initialize", Duration: 5 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare_runtime_init", Duration: 7 * time.Nanosecond, Success: true},
		)
	}
	phases, err := preparedDensityPhases([]preparedDensityShardResult{{capacity: 2, observations: observations}}, 20*time.Nanosecond, "single-use-preinitialized")
	if err != nil {
		t.Fatal(err)
	}
	if phases.CompileNS == nil || *phases.CompileNS.Value != 2 || *phases.InstantiateNS.Value != 6 ||
		*phases.InitializeNS.Value != 10 || *phases.RuntimeInitNS.Value != 14 || *phases.TotalNS.Value != 20 {
		t.Fatalf("prepared phase aggregation drifted: %#v", phases)
	}
	missing := observations[:len(observations)-1]
	if _, err := preparedDensityPhases([]preparedDensityShardResult{{capacity: 2, observations: missing}}, 20*time.Nanosecond, "single-use-preinitialized"); err == nil {
		t.Fatal("incomplete prepared observations accepted")
	}
}

func TestPreparedDensityPhasesBindEveryNonCOWWarmup(t *testing.T) {
	observations := []wazeroengine.Observation{
		{Phase: "instantiate_host", Duration: time.Nanosecond, Success: true},
		{Phase: "compile", Duration: 2 * time.Nanosecond, Success: true},
	}
	for range 2 {
		observations = append(observations,
			wazeroengine.Observation{Phase: "pool_prepare_instantiate_guest", Duration: 3 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare__initialize", Duration: 5 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare_runtime_init", Duration: 7 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare_warmup", Duration: 11 * time.Nanosecond, Success: true},
		)
	}
	phases, err := preparedDensityPhasesWithWarmup(
		[]preparedDensityShardResult{{capacity: 2, observations: observations}}, 50*time.Nanosecond,
		"single-use-preinitialized", wazeroengine.COWWarmupNumPyReadyV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if phases.WarmupNS == nil || phases.WarmupNS.Value == nil || *phases.WarmupNS.Value != 22 {
		t.Fatalf("non-COW warmup phase drifted: %#v", phases.WarmupNS)
	}
	for _, candidate := range [][]wazeroengine.Observation{
		observations[:len(observations)-1],
		append(append([]wazeroengine.Observation(nil), observations...), wazeroengine.Observation{Phase: "pool_prepare_warmup", Duration: time.Nanosecond, Success: true}),
	} {
		if _, err := preparedDensityPhasesWithWarmup(
			[]preparedDensityShardResult{{capacity: 2, observations: candidate}}, 50*time.Nanosecond,
			"single-use-preinitialized", wazeroengine.COWWarmupNumPyReadyV1,
		); err == nil {
			t.Fatal("missing or duplicate non-COW warmup observation was accepted")
		}
	}
}

func TestCOWDensityPhasesBindCanonicalImageAndEveryRestore(t *testing.T) {
	observations := []wazeroengine.Observation{
		{Phase: "instantiate_host", Duration: time.Nanosecond, Success: true},
		{Phase: "compile", Duration: 2 * time.Nanosecond, Success: true},
		{Phase: "cow_image_instantiate_guest", Duration: 3 * time.Nanosecond, Success: true},
		{Phase: "cow_image__initialize", Duration: 5 * time.Nanosecond, Success: true},
		{Phase: "cow_image_runtime_init", Duration: 7 * time.Nanosecond, Success: true},
	}
	for range 2 {
		observations = append(observations,
			wazeroengine.Observation{Phase: "pool_prepare_instantiate_guest", Duration: 11 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare__initialize", Duration: 13 * time.Nanosecond, Success: true},
			wazeroengine.Observation{Phase: "pool_prepare_cow_restore", Duration: 17 * time.Nanosecond, Success: true},
		)
	}
	phases, err := preparedDensityPhases(
		[]preparedDensityShardResult{{capacity: 2, observations: observations}},
		60*time.Nanosecond,
		"cow-ready-single-use",
	)
	if err != nil {
		t.Fatal(err)
	}
	if phases.CompileNS == nil || *phases.CompileNS.Value != 2 || *phases.InstantiateNS.Value != 25 ||
		*phases.InitializeNS.Value != 31 || *phases.RuntimeInitNS.Value != 7 ||
		*phases.PrepareNS.Value != 34 || *phases.TotalNS.Value != 60 {
		t.Fatalf("COW phase aggregation drifted: %#v", phases)
	}
}

func TestCollectPreparedDensityChildWithRealGuestArtifact(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("lifecycle-density collection is Linux-only")
	}
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is not set")
	}
	manifestPath := filepath.Join(filepath.Dir(artifactPath), "manifest.json")
	artifact, _, err := loadArtifactIdentity(artifactPath, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := densitySweepSpec{RequestedSlots: 1, Strategy: "single-use-preinitialized", MaxRSSBytes: 2 * 1024 * 1024 * 1024, Timeout: 2 * time.Minute}
	envelope, err := collectPreparedDensityChild(context.Background(), artifactPath, manifestPath, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDensityChildEnvelope(envelope, spec, artifact); err != nil {
		t.Fatal(err)
	}
	if envelope.Sample.Pool.Ready != 1 || envelope.Sample.Process.RSSBytes.Status != runtimeevidence.MetricMeasured {
		t.Fatalf("real prepared density sample is incomplete: %#v", envelope.Sample)
	}
}

func TestBoundedChildRunnerUsesDistinctOSProcesses(t *testing.T) {
	runner := boundedChildRunner{
		executable:   os.Args[0],
		pollInterval: time.Millisecond,
		readRSSBytes: func(int) (uint64, error) { return 1, nil },
	}
	var pids []int
	for invocation := 0; invocation < 2; invocation++ {
		result, err := runner.run(context.Background(), boundedChildSpec{
			args:        []string{"-test.run=^TestDensityHelperProcess$"},
			environment: []string{"APYRUN_DENSITY_HELPER=emit"},
			timeout:     5 * time.Second,
			maxRSSBytes: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
		marker := fmt.Sprintf("helper-pid=%d", result.PID)
		if !strings.Contains(string(result.Stdout), marker) {
			t.Fatalf("child output lacks process marker %q: %q", marker, result.Stdout)
		}
		if result.StartedAtUnixNS <= 0 || result.MaxObservedRSSBytes == 0 {
			t.Fatalf("child process evidence is incomplete: %#v", result)
		}
		pids = append(pids, result.PID)
	}
	if pids[0] == pids[1] {
		t.Fatalf("density samples reused PID %d", pids[0])
	}
}

func TestBoundedChildRunnerKillsTimeoutAndRSSOverflow(t *testing.T) {
	for name, testCase := range map[string]struct {
		reader func(int) (uint64, error)
		want   string
	}{
		"timeout": {
			reader: func(int) (uint64, error) { return 1, nil },
			want:   "timeout",
		},
		"rss guard": {
			reader: func(int) (uint64, error) { return 2048, nil },
			want:   "safety guard",
		},
	} {
		t.Run(name, func(t *testing.T) {
			timeout := 5 * time.Second
			if name == "timeout" {
				timeout = 20 * time.Millisecond
			}
			runner := boundedChildRunner{executable: os.Args[0], pollInterval: time.Millisecond, readRSSBytes: testCase.reader}
			_, err := runner.run(context.Background(), boundedChildSpec{
				args:        []string{"-test.run=^TestDensityHelperProcess$"},
				environment: []string{"APYRUN_DENSITY_HELPER=sleep"},
				timeout:     timeout,
				maxRSSBytes: 1024,
			})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("got %v, want %q failure", err, testCase.want)
			}
		})
	}
}

func TestBoundedChildRunnerRejectsOversizedOutput(t *testing.T) {
	runner := boundedChildRunner{
		executable:   os.Args[0],
		pollInterval: time.Millisecond,
		readRSSBytes: func(int) (uint64, error) { return 1, nil },
		stdoutLimit:  32,
	}
	_, err := runner.run(context.Background(), boundedChildSpec{
		args:        []string{"-test.run=^TestDensityHelperProcess$"},
		environment: []string{"APYRUN_DENSITY_HELPER=oversize"},
		timeout:     5 * time.Second,
		maxRSSBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "stdout limit") {
		t.Fatalf("oversized child output was not rejected: %v", err)
	}
}

func TestBoundedChildRunnerReportsFastExitBeforeInitialRSS(t *testing.T) {
	runner := boundedChildRunner{
		executable:   "/bin/sh",
		readRSSBytes: func(int) (uint64, error) { return 0, errors.New("process disappeared") },
	}
	_, err := runner.run(context.Background(), boundedChildSpec{
		args: []string{"-c", "printf boom >&2; exit 3"}, timeout: 5 * time.Second, maxRSSBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "stderr tail: boom") {
		t.Fatalf("fast child exit lost its diagnostic: %v", err)
	}
}

func TestBoundedChildRunnerTreatsFinalRSSDisappearanceAsProcessExit(t *testing.T) {
	reads := 0
	signalPath := filepath.Join(t.TempDir(), "child-exiting")
	runner := boundedChildRunner{
		executable:   "/bin/sh",
		pollInterval: time.Millisecond,
		readRSSBytes: func(int) (uint64, error) {
			reads++
			if reads == 1 {
				return 1, nil
			}
			for {
				if _, err := os.Stat(signalPath); err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			return 0, errors.New("process disappeared")
		},
	}
	result, err := runner.run(context.Background(), boundedChildSpec{
		args:        []string{"-c", "printf 'helper-pid=%s\\n' $$; printf exiting > \"$1\"", "density-helper", signalPath},
		timeout:     5 * time.Second,
		maxRSSBytes: 1024,
	})
	if err != nil {
		t.Fatalf("normal child exit lost to final RSS race: %v", err)
	}
	if !strings.Contains(string(result.Stdout), "helper-pid=") {
		t.Fatalf("normal child output missing: %q", result.Stdout)
	}
}

func TestDensityHelperProcess(t *testing.T) {
	mode := os.Getenv("APYRUN_DENSITY_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "emit":
		fmt.Printf("helper-pid=%s\n", strconv.Itoa(os.Getpid()))
	case "sleep":
		time.Sleep(30 * time.Second)
	case "oversize":
		fmt.Print(strings.Repeat("x", 4096))
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q", mode)
		os.Exit(2)
	}
}

func validDensityChildEnvelope(spec densitySweepSpec, artifact artifactIdentity) densityChildEnvelope {
	measured := func(value uint64) runtimeevidence.Metric {
		return runtimeevidence.Metric{Status: runtimeevidence.MetricMeasured, Value: &value}
	}
	skipped := func(reason runtimeevidence.UnavailableReason) runtimeevidence.Metric {
		return runtimeevidence.Metric{Status: runtimeevidence.MetricSkipped, ReasonCode: reason}
	}
	unsupported := func(reason runtimeevidence.UnavailableReason) runtimeevidence.Metric {
		return runtimeevidence.Metric{Status: runtimeevidence.MetricUnsupported, ReasonCode: reason}
	}
	cgroupMetric := unsupported(runtimeevidence.ReasonNotApplicable)
	envelope := densityChildEnvelope{
		ProtocolVersion: 1,
		ArtifactSHA256:  artifact.SHA256,
		ArtifactProfile: artifact.ArtifactProfile,
		Backend:         runtimeevidence.BackendIdentity{Name: "wazero", Version: "v1.11.0", ResetMode: "fresh-instance"},
		Environment:     runtimeevidence.EnvironmentIdentity{GOOS: "linux", GOARCH: "amd64", GoVersion: "go1.24", KernelRelease: "6.8", PageSizeBytes: 4096, CgroupVersion: "none"},
		Strategy:        runtimeevidence.StrategyIdentity{Requested: spec.Strategy, Active: spec.Strategy},
		Sample: runtimeevidence.LifecycleDensitySample{
			RequestedSlots:   spec.RequestedSlots,
			RuntimeShards:    1,
			ObservedAtUnixNS: runtimeevidence.Metric{Status: runtimeevidence.MetricTimestampObserved, Value: ptrForTest(uint64(1))},
			Pool:             runtimeevidence.PoolState{TargetCapacity: spec.RequestedSlots, Ready: spec.RequestedSlots, AccountedSlots: spec.RequestedSlots},
			Phases: runtimeevidence.PhaseTimings{
				QueueNS: skipped(runtimeevidence.ReasonNotApplicable), CompileNS: ptrForTest(measured(1)), InstantiateNS: measured(1), InitializeNS: measured(1),
				RuntimeInitNS: measured(1), PrepareNS: skipped(runtimeevidence.ReasonWorkloadNotRun), ExecuteNS: skipped(runtimeevidence.ReasonWorkloadNotRun),
				CapabilityNS: skipped(runtimeevidence.ReasonWorkloadNotRun), TotalNS: measured(1),
			},
			GoRuntime: runtimeevidence.GoRuntimeMetrics{
				HeapLiveBytes: measured(1), HeapGoalBytes: measured(1), GCCyclesTotal: measured(1), GCPauseTotalNS: measured(1), Goroutines: measured(1),
				SchedulerLatency: runtimeevidence.Histogram{Status: runtimeevidence.MetricMeasured, UpperBoundsNS: []uint64{1}, Counts: []uint64{1}},
			},
			Process: runtimeevidence.ProcessMetrics{
				RSSBytes: measured(1), VirtualBytes: measured(1), PSSBytes: measured(1), PrivateCleanBytes: measured(1), PrivateDirtyBytes: measured(1),
				SwapBytes: measured(0), MinorFaults: measured(1), MajorFaults: measured(0), FDCount: measured(1), VMACount: measured(1),
			},
			Cgroup: runtimeevidence.CgroupMetrics{
				Version: "none", Scope: "unverified", MemoryCurrentBytes: cgroupMetric, MemoryPeakBytes: cgroupMetric, MemorySwapCurrentBytes: cgroupMetric,
				MemoryEventsHighTotal: cgroupMetric, MemoryEventsOOMTotal: cgroupMetric, MemoryEventsOOMKillTotal: cgroupMetric,
				PressureSomeTotalUS: cgroupMetric, PressureFullTotalUS: cgroupMetric,
			},
		},
	}
	if spec.WarmupProfile != "" {
		envelope.Warmup = &runtimeevidence.PreparedWarmupIdentity{Profile: spec.WarmupProfile, GenerationSHA256: strings.Repeat("e", 64)}
		warmup := measured(1)
		envelope.Sample.Phases.WarmupNS = &warmup
		if spec.Strategy == "cow-ready-single-use" {
			envelope.Sample.RuntimeShards = 1
		} else {
			envelope.Sample.RuntimeShards = (spec.RequestedSlots + 3) / 4
		}
	}
	if spec.Strategy == "cow-ready-single-use" {
		envelope.Sample.Phases.PrepareNS = measured(1)
		envelope.Sample.COWMappings = &runtimeevidence.MappingMetrics{
			Name: "memfd:apyrun-cow-image", MappingCount: spec.RequestedSlots,
			VirtualBytes: measured(1), RSSBytes: measured(1), PSSBytes: measured(1),
			SharedCleanBytes: measured(1), SharedDirtyBytes: measured(0),
			PrivateCleanBytes: measured(0), PrivateDirtyBytes: measured(0),
			ReferencedBytes: measured(1), AnonymousBytes: measured(0),
		}
	}
	return envelope
}

func ptrForTest[T any](value T) *T { return &value }
