package runtime

import (
	"errors"
	"testing"
)

func TestShardProfileIdentityIsCanonicalAndDefensive(t *testing.T) {
	profile, err := NewShardProfile(ShardProfileConfig{
		ID:                     "plain",
		ExecutionProfileID:     "base",
		QualifiedImports:       []string{"sys", "json", "agent_runtime"},
		ArtifactSHA256:         "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256:         "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PreparedBaselineSHA256: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		IdlePolicy:             ShardIdleRetireWhenIdle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID() != "plain" || profile.ExecutionProfileID() != "base" || profile.Identity() == "" {
		t.Fatalf("profile=%+v", profile)
	}
	imports := profile.QualifiedImports()
	want := []string{"agent_runtime", "json", "sys"}
	for i := range want {
		if imports[i] != want[i] {
			t.Fatalf("imports=%v", imports)
		}
	}
	imports[0] = "tampered"
	if profile.QualifiedImports()[0] != "agent_runtime" {
		t.Fatal("imports alias internal state")
	}

	reordered, err := NewShardProfile(ShardProfileConfig{
		ID: "plain", ExecutionProfileID: "base",
		QualifiedImports: []string{"agent_runtime", "sys", "json"},
		ArtifactSHA256:   profile.ArtifactSHA256(), ManifestSHA256: profile.ManifestSHA256(),
		PreparedBaselineSHA256: profile.PreparedBaselineSHA256(), IdlePolicy: ShardIdleRetireWhenIdle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Identity() != profile.Identity() {
		t.Fatalf("identity changed with import order: %s != %s", reordered.Identity(), profile.Identity())
	}
}

func TestShardProfileRejectsUnboundOrArbitraryVocabulary(t *testing.T) {
	valid := ShardProfileConfig{
		ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"json"},
		ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IdlePolicy:     ShardIdleRetireWhenIdle,
	}
	cases := []ShardProfileConfig{
		{ID: "per-request-random", ExecutionProfileID: "base", QualifiedImports: []string{"json"}, ArtifactSHA256: valid.ArtifactSHA256, ManifestSHA256: valid.ManifestSHA256, IdlePolicy: valid.IdlePolicy},
		{ID: "plain", ExecutionProfileID: "numpy-core", QualifiedImports: []string{"numpy"}, ArtifactSHA256: valid.ArtifactSHA256, ManifestSHA256: valid.ManifestSHA256, IdlePolicy: valid.IdlePolicy},
		{ID: "plain", ExecutionProfileID: "base", QualifiedImports: []string{"json"}, ArtifactSHA256: valid.ArtifactSHA256, IdlePolicy: valid.IdlePolicy},
	}
	for i, candidate := range cases {
		if _, err := NewShardProfile(candidate); !errors.Is(err, ErrInvalidShardProfile) {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
}

func TestExecutionArtifactValidationIsBackendSpecific(t *testing.T) {
	wasm := ExecutionArtifact{
		SchemaVersion: ExecutionArtifactSchemaVersion, Backend: BackendPysolateWASM,
		Kind: ArtifactWASMDistribution, ProfileID: "base", ShardID: "plain", Target: "wasm32-wasip1",
		ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if err := wasm.Validate(); err != nil {
		t.Fatal(err)
	}
	native := ExecutionArtifact{
		SchemaVersion: ExecutionArtifactSchemaVersion, Backend: BackendNativeSandbox,
		Kind: ArtifactOCIImage, ProfileID: "native-python", Target: "linux/amd64",
		ImageDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	if err := native.Validate(); err != nil {
		t.Fatal(err)
	}
	native.ArtifactSHA256 = wasm.ArtifactSHA256
	if err := native.Validate(); !errors.Is(err, ErrInvalidExecutionArtifact) {
		t.Fatalf("native accepted WASM identity: %v", err)
	}
}

func TestNativeStateAndLeaseClassesAreExplicit(t *testing.T) {
	valid := []NativeStateClass{StatePortableValue, StateWorkspaceRef, StateProcessRef, StateOpaque}
	for _, state := range valid {
		if !state.Valid() {
			t.Fatalf("invalid state %q", state)
		}
	}
	if NativeStateClass("heap-maybe").Valid() {
		t.Fatal("unknown state accepted")
	}
	leases := []NativeLeaseClass{LeaseDestroyImmediately, LeaseWorkspaceGrace, LeaseLiveProcess}
	for _, lease := range leases {
		if !lease.Valid() {
			t.Fatalf("invalid lease %q", lease)
		}
	}
	if NativeLeaseClass("forever").Valid() {
		t.Fatal("unbounded lease accepted")
	}
}
