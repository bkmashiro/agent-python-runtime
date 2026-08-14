package verification

import (
	"errors"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	nativeengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/native"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
)

var ErrInvalidNativeEvidence = errors.New("invalid native execution evidence")

// VerifyNative validates a successfully published native execution.
func VerifyNative(plan placement.Plan, artifact runtimeconfig.ExecutionArtifact, evidence nativeengine.Evidence) error {
	return VerifyNativeAttempt(plan, artifact, evidence, true)
}

// VerifyNativeAttempt is deliberately separate from the native backend. It
// consumes only Host-authored identities, receipts, aggregate lifecycle data
// and cleanup dispositions; it never needs request/result bodies.
func VerifyNativeAttempt(plan placement.Plan, artifact runtimeconfig.ExecutionArtifact, evidence nativeengine.Evidence, succeeded bool) error {
	if plan.Decision.SchemaVersion != placement.DecisionSchemaVersion || plan.Decision.Status != placement.StatusSelected ||
		plan.Decision.Backend != runtimeconfig.BackendNativeSandbox || plan.Decision.Identity == "" ||
		artifact.Validate() != nil || artifact.Backend != runtimeconfig.BackendNativeSandbox || artifact.ProfileID != "native-python" ||
		evidence.Backend != string(runtimeconfig.BackendNativeSandbox) || evidence.DecisionID != plan.Decision.Identity ||
		evidence.ArtifactIdentity != artifact.Identity() || evidence.ImageDigest != artifact.ImageDigest || !evidence.ImageConfigVerified || evidence.RootFSSHA256 != artifact.RootFSSHA256 ||
		evidence.ExecutionID == "" || evidence.CapabilityPlanSHA256 == "" ||
		!evidence.DeleteReconciled || !evidence.CgroupReconciled || !evidence.ControlRootUnmounted || !evidence.ScratchRemoved || !evidence.WorkspaceLeaseReleased ||
		evidence.RunscStateEntriesAfter != 0 {
		return ErrInvalidNativeEvidence
	}
	for index, item := range evidence.CapabilityReceipts {
		if item.ReceiptID == "" || item.RunID != evidence.ExecutionID || item.CapabilityPlanSHA256 != evidence.CapabilityPlanSHA256 ||
			item.OperationIndex != uint32(index) || item.Capability == "" || item.RequestSHA256 == "" || item.ResponseSHA256 == "" || item.Outcome == "" {
			return ErrInvalidNativeEvidence
		}
	}
	lifecycle := evidence.Lifecycle()
	if err := lifecycle.Validate(); err != nil || lifecycle.ExecutionID != evidence.ExecutionID || lifecycle.ArtifactIdentity != artifact.Identity() {
		return ErrInvalidNativeEvidence
	}
	if succeeded {
		if !evidence.Ready || evidence.ExitStatus != 0 || lifecycle.TerminalStatus != "ok" {
			return ErrInvalidNativeEvidence
		}
	} else if evidence.Ready && evidence.ExitStatus == 0 || lifecycle.TerminalStatus != "error" {
		return ErrInvalidNativeEvidence
	}
	return nil
}
