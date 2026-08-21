package prepareddataset

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

const claimTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func sealedClaimObject(t *testing.T) *StagedObject {
	t.Helper()
	object, err := NewStagedObject(HostReceipt{
		ContractSHA256: claimTestDigest, PreparationSHA256: claimTestDigest,
		FileSHA256: CanonicalFileSHA256, BodySHA256: CanonicalBodySHA256,
		ExecutionProfileSHA256: claimTestDigest, PrivacyPartition: "run-private", Freshness: "plan_epoch:stream-1",
		BudgetReservationSHA256: claimTestDigest, MaxFileBytes: CanonicalFileBytes, MaxBodyBytes: CanonicalBodyBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := object.IssueRead(CanonicalFileBytes); err != nil {
		t.Fatal(err)
	}
	if err := object.VerifySource(CanonicalFileSHA256); err != nil {
		t.Fatal(err)
	}
	if err := object.Decode(CanonicalFixture()); err != nil {
		t.Fatal(err)
	}
	if err := object.Seal(); err != nil {
		t.Fatal(err)
	}
	return object
}

func claimDecisionAndCapsule(t *testing.T) (preparedregion.PreparedRegionDecision, preparedregion.PreparedRegionCapsule) {
	t.Helper()
	_, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: claimTestDigest, ASTSHA256: claimTestDigest, AnalysisSHA256: claimTestDigest,
		RegionID: claimTestDigest, RegionSpan: preparedregion.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 1},
		RegionSourceSHA256: CanonicalBodySHA256, LiveInsSHA256: claimTestDigest, EnvironmentSHA256: claimTestDigest,
		ExecutionProfileSHA256: claimTestDigest, ImportClosureSHA256: claimTestDigest, CapabilityPlanSHA256: claimTestDigest,
		PassConfigSHA256: claimTestDigest, Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "dataset",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := preparedregion.SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`true`))
	if err != nil {
		t.Fatal(err)
	}
	return decision, capsule
}

func TestPreparedDataClaimGuardClaimsExactSealedObjectOnce(t *testing.T) {
	object := sealedClaimObject(t)
	decision, capsule := claimDecisionAndCapsule(t)
	guard, err := NewPreparedDataClaimGuard(object, decision, capsule)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.ClaimPreparedRegion(decision, capsule); err != nil {
		t.Fatal(err)
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateClaimed || snapshot.Counters.LogicalClaims != 1 || snapshot.Counters.LogicalClaimBytes != CanonicalBodyBytes || snapshot.BodyBytes != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := guard.ClaimPreparedRegion(decision, capsule); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second claim=%v", err)
	}
}

func TestPreparedDataClaimGuardRejectsIdentityDriftWithoutConsumingObject(t *testing.T) {
	object := sealedClaimObject(t)
	decision, capsule := claimDecisionAndCapsule(t)
	guard, err := NewPreparedDataClaimGuard(object, decision, capsule)
	if err != nil {
		t.Fatal(err)
	}
	drifted := decision
	drifted.IdentitySHA256 = claimTestDigest
	if err := guard.ClaimPreparedRegion(drifted, capsule); !errors.Is(err, ErrPreparedDataClaimBinding) {
		t.Fatalf("drifted claim=%v", err)
	}
	if snapshot := object.Snapshot(); snapshot.State != StateSealed || snapshot.Counters.LogicalClaims != 0 {
		t.Fatalf("drift consumed object: %+v", snapshot)
	}
}
