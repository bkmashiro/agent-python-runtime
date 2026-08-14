package verification

import (
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	nativeengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/native"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func verificationFixture() (placement.Plan, runtimeconfig.ExecutionArtifact, nativeengine.Evidence) {
	d := func(value byte) string {
		buffer := make([]byte, 64)
		for index := range buffer {
			buffer[index] = value
		}
		return "sha256:" + string(buffer)
	}
	artifact := runtimeconfig.ExecutionArtifact{SchemaVersion: runtimeconfig.ExecutionArtifactSchemaVersion, Backend: runtimeconfig.BackendNativeSandbox, Kind: runtimeconfig.ArtifactOCIImage, ProfileID: "native-python", Target: "linux/arm64", ImageDigest: d('a'), RootFSSHA256: d('b')}
	decision := placement.Decision{SchemaVersion: placement.DecisionSchemaVersion, Status: placement.StatusSelected, Backend: runtimeconfig.BackendNativeSandbox, Reason: placement.ReasonRequiredNativeFeature, AnalyzerVersion: placement.AnalyzerStaticV1, RequestSHA256: d('c'), StateClass: runtimeconfig.StatePortableValue, Identity: d('d')}
	evidence := nativeengine.Evidence{SchemaVersion: nativeengine.EvidenceSchemaVersion, Backend: string(runtimeconfig.BackendNativeSandbox), ImageDigest: artifact.ImageDigest, ImageConfigVerified: true, RootFSSHA256: artifact.RootFSSHA256, ArtifactIdentity: artifact.Identity(), DecisionID: decision.Identity, ExecutionID: "native-exec", CapabilityPlanSHA256: d('e'), Ready: true, ExitStatus: 0, WallNanoseconds: 1, RootFSVerifyNanoseconds: 1, DeleteReconciled: true, CgroupReconciled: true, ControlRootUnmounted: true, ScratchRemoved: true, WorkspaceLeaseReleased: true, CapabilityReceipts: []receipt.Receipt{{ReceiptID: "rcpt_1", RunID: "native-exec", CapabilityPlanSHA256: d('e'), Capability: "math.double", OperationIndex: 0, RequestSHA256: "request", ResponseSHA256: "response", Outcome: "ok"}}}
	return placement.Plan{Decision: decision}, artifact, evidence
}

func TestVerifyNativeAndRejectIdentityMutations(t *testing.T) {
	plan, artifact, evidence := verificationFixture()
	if err := VerifyNative(plan, artifact, evidence); err != nil {
		t.Fatal(err)
	}
	failed := evidence
	failed.Ready = false
	failed.ExitStatus = 137
	failed.CapabilityReceipts = nil
	if err := VerifyNativeAttempt(plan, artifact, failed, false); err != nil {
		t.Fatalf("valid failed attempt rejected: %v", err)
	}
	if err := VerifyNativeAttempt(plan, artifact, evidence, false); !errors.Is(err, ErrInvalidNativeEvidence) {
		t.Fatalf("successful evidence accepted as failure: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*placement.Plan, *runtimeconfig.ExecutionArtifact, *nativeengine.Evidence)
	}{
		{"schema", func(_ *placement.Plan, _ *runtimeconfig.ExecutionArtifact, e *nativeengine.Evidence) {
			e.SchemaVersion = "pysolate.native-lifecycle.v0"
		}},
		{"decision", func(p *placement.Plan, _ *runtimeconfig.ExecutionArtifact, _ *nativeengine.Evidence) {
			p.Decision.Identity = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		}},
		{"artifact", func(_ *placement.Plan, a *runtimeconfig.ExecutionArtifact, _ *nativeengine.Evidence) {
			a.ImageDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		}},
		{"receipt", func(_ *placement.Plan, _ *runtimeconfig.ExecutionArtifact, e *nativeengine.Evidence) {
			e.CapabilityReceipts[0].RunID = "other"
		}},
		{"cleanup", func(_ *placement.Plan, _ *runtimeconfig.ExecutionArtifact, e *nativeengine.Evidence) {
			e.CgroupReconciled = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, a, e := verificationFixture()
			test.mutate(&p, &a, &e)
			if !errors.Is(VerifyNative(p, a, e), ErrInvalidNativeEvidence) {
				t.Fatal("mutation accepted")
			}
		})
	}
}
