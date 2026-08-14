package native

import (
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestImageIdentityMustBindConfigToArtifact(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	config := Config{ImageDigest: digest, Artifact: runtimeconfig.ExecutionArtifact{ImageDigest: digest}}
	if !imageIdentityBound(config) {
		t.Fatal("equal image identities were not bound")
	}
	config.Artifact.ImageDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if imageIdentityBound(config) {
		t.Fatal("mismatched config/artifact image identities were accepted")
	}
	config.ImageDigest = ""
	if imageIdentityBound(config) {
		t.Fatal("empty image identity was accepted")
	}
}

func TestRandomPhysicalIdentifiersAreFreshAndBounded(t *testing.T) {
	first, err := randomIdentifier("native")
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomIdentifier("native")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != len("native-")+32 || len(second) != len(first) {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

func TestLifecycleProjectionPreservesMeasuredResources(t *testing.T) {
	e := Evidence{Backend: "native_sandbox", ArtifactIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExecutionID: "native-1", RootFSVerifyNanoseconds: 3, WallNanoseconds: 7, ExitStatus: 0, Ready: true, DeleteReconciled: true, CgroupReconciled: true, ControlRootUnmounted: true, ScratchRemoved: true, WorkspaceLeaseReleased: true, ResourceSamples: 4, CgroupMemoryPeakBytes: 11, PSSPeakBytes: 9, PrivateDirtyPeakBytes: 5, ReadBytes: 2, WriteBytes: 1, PidsPeak: 3}
	projected := e.Lifecycle()
	if err := projected.Validate(); err != nil {
		t.Fatal(err)
	}
	if projected.Resources.Samples != 4 || projected.Resources.PSSPeakBytes != 9 || projected.TerminalStatus != "ok" {
		t.Fatalf("projected=%+v", projected)
	}
}
