package prepareddataset

import (
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

var ErrPreparedDataClaimBinding = errors.New("prepared data claim binding mismatch")

// PreparedDataClaimGuard atomically joins one prepared-region token to one
// sealed Host-owned dataset object. It is deliberately fixed to the canonical
// numpy_npy_c_v1 research fixture rather than exposing a generic blob claim.
type PreparedDataClaimGuard struct {
	object   *StagedObject
	decision preparedregion.PreparedRegionDecision
	capsule  preparedregion.PreparedRegionCapsule
}

func NewPreparedDataClaimGuard(object *StagedObject, decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule) (*PreparedDataClaimGuard, error) {
	if object == nil || capsule.ValidateDecision(decision) != nil {
		return nil, ErrPreparedDataClaimBinding
	}
	snapshot := object.Snapshot()
	if snapshot.State != StateSealed || !snapshot.Receipt.valid() ||
		decision.LiveInsSHA256 != snapshot.Receipt.PreparationSHA256 ||
		decision.EnvironmentSHA256 != snapshot.Receipt.ContractSHA256 ||
		decision.RegionSourceSHA256 != snapshot.Receipt.BodySHA256 ||
		decision.ExecutionProfileSHA256 != snapshot.Receipt.ExecutionProfileSHA256 ||
		snapshot.SourceIdentity != snapshot.Receipt.FileSHA256 ||
		snapshot.Metadata.BodySHA256 != snapshot.Receipt.BodySHA256 ||
		snapshot.Metadata.FileSHA256 != snapshot.Receipt.FileSHA256 ||
		snapshot.BodyBytes != snapshot.Receipt.MaxBodyBytes {
		return nil, ErrPreparedDataClaimBinding
	}
	return &PreparedDataClaimGuard{object: object, decision: decision, capsule: capsule}, nil
}

func (guard *PreparedDataClaimGuard) ClaimPreparedRegion(decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule) error {
	if guard == nil || guard.object == nil || decision != guard.decision || capsule.IdentitySHA256 != guard.capsule.IdentitySHA256 || capsule.ValidateDecision(decision) != nil {
		return ErrPreparedDataClaimBinding
	}
	if err := guard.object.ClaimBoundMaterialization(guard.decision.RegionSourceSHA256, CanonicalBodyBytes); err != nil {
		return err
	}
	return nil
}
