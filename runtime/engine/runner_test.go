package engine_test

import (
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestPropertiesBindOnlyVerifiedProfiles(t *testing.T) {
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeconfig.VerifiedArtifactIdentity{
		ProfileID:      "base",
		ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ImportRoots:    []string{"json"}, QualifiedImportRoots: []string{"json"},
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	properties := engine.Properties{
		Backend: "wazero", ExecutionProfileID: profile.ID(), AllowedImports: profile.AllowedImports(),
		AvailableImports: profile.AvailableImports(), QualifiedImports: profile.QualifiedImports(),
		ArtifactSHA256: profile.ArtifactSHA256(), ManifestSHA256: profile.ManifestSHA256(),
	}
	if err := properties.Validate(); err != nil {
		t.Fatal(err)
	}
	if reconstructed := properties.ExecutionProfile(); reconstructed == nil || reconstructed.ArtifactSHA256() != profile.ArtifactSHA256() {
		t.Fatalf("profile was not reconstructed: %#v", reconstructed)
	}
}

func TestPropertiesRejectIncompleteArtifactIdentity(t *testing.T) {
	properties := engine.Properties{Backend: "wazero", ExecutionProfileID: "base", AllowedImports: []string{"json"}, ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if properties.Validate() == nil {
		t.Fatal("incomplete artifact identity was accepted")
	}
}
