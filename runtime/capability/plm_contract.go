package capability

import "errors"

const PLMContractVersionV1 = "pysolate.plm-contract.v1"

type TemporalMode string

type PrepareEffectMode string

type SpeculationMode string

type FailureMode string

type AuthorityMode string

const (
	TemporalImmutable          TemporalMode = "immutable"
	TemporalSnapshot           TemporalMode = "snapshot"
	TemporalVersioned          TemporalMode = "versioned"
	TemporalLeased             TemporalMode = "leased"
	TemporalCurrent            TemporalMode = "current"
	TemporalWallclockObserving TemporalMode = "wallclock_observing"

	PrepareNone          PrepareEffectMode = "none"
	PrepareSilentRead    PrepareEffectMode = "silent_read"
	PrepareTransportOnly PrepareEffectMode = "transport_only"

	SpeculationNever    SpeculationMode = "never"
	SpeculationBudgeted SpeculationMode = "budgeted"

	FailureRetryAtLinearize FailureMode = "retry_at_linearize"
	FailureStable           FailureMode = "stable_failure"

	AuthorityRecheckAtLinearize AuthorityMode = "recheck_at_linearize"
)

var ErrInvalidPLMContract = errors.New("invalid PLM contract")

// PLMContract is Host-authored policy for physical preparation before a
// synchronous capability call reaches its original logical position. V1 does
// not permit delayed materialization or prepared writes.
type PLMContract struct {
	Version                          string            `json:"version"`
	Temporal                         TemporalMode      `json:"temporal"`
	PrepareEffect                    PrepareEffectMode `json:"prepare_effect"`
	Speculation                      SpeculationMode   `json:"speculation"`
	Failure                          FailureMode       `json:"failure"`
	Authority                        AuthorityMode     `json:"authority"`
	TemporalValidator                string            `json:"temporal_validator,omitempty"`
	ProviderNonInterferenceValidator string            `json:"provider_noninterference_validator,omitempty"`
	StableFailureValidator           string            `json:"stable_failure_validator,omitempty"`
	MaxResultBytes                   uint64            `json:"max_result_bytes,omitempty"`
	CostUnits                        uint32            `json:"cost_units,omitempty"`
}

func (contract PLMContract) Validate() error {
	if contract.Version != PLMContractVersionV1 || contract.Authority != AuthorityRecheckAtLinearize {
		return ErrInvalidPLMContract
	}
	if contract.Speculation != SpeculationNever && contract.Speculation != SpeculationBudgeted {
		return ErrInvalidPLMContract
	}
	if contract.Failure != FailureRetryAtLinearize && contract.Failure != FailureStable {
		return ErrInvalidPLMContract
	}
	if contract.Failure == FailureStable {
		if !validHandlerIdentity(contract.StableFailureValidator) || contract.PrepareEffect != PrepareSilentRead {
			return ErrInvalidPLMContract
		}
	} else if contract.StableFailureValidator != "" {
		return ErrInvalidPLMContract
	}

	switch contract.Temporal {
	case TemporalImmutable, TemporalSnapshot, TemporalVersioned, TemporalLeased:
		if contract.PrepareEffect != PrepareSilentRead || contract.MaxResultBytes == 0 || contract.MaxResultBytes > maxCallBytes ||
			contract.CostUnits == 0 || contract.CostUnits > maxPreDispatchCostUnits || !validHandlerIdentity(contract.TemporalValidator) ||
			!validHandlerIdentity(contract.ProviderNonInterferenceValidator) {
			return ErrInvalidPLMContract
		}
	case TemporalCurrent:
		if contract.PrepareEffect != PrepareTransportOnly || contract.Speculation != SpeculationNever ||
			contract.Failure != FailureRetryAtLinearize || contract.MaxResultBytes != 0 || contract.CostUnits == 0 ||
			contract.CostUnits > maxPreDispatchCostUnits || contract.TemporalValidator != "" ||
			!validHandlerIdentity(contract.ProviderNonInterferenceValidator) {
			return ErrInvalidPLMContract
		}
	case TemporalWallclockObserving:
		if contract.PrepareEffect != PrepareNone || contract.Speculation != SpeculationNever ||
			contract.Failure != FailureRetryAtLinearize || contract.MaxResultBytes != 0 || contract.CostUnits != 0 ||
			contract.TemporalValidator != "" || contract.ProviderNonInterferenceValidator != "" {
			return ErrInvalidPLMContract
		}
	default:
		return ErrInvalidPLMContract
	}
	return nil
}

func (contract PLMContract) CanPrepareValueCandidate() bool {
	return contract.Validate() == nil && contract.PrepareEffect == PrepareSilentRead
}

type CandidateBinding struct {
	RunIdentity             string
	PlanIdentity            string
	SourceSealIdentity      string
	SiteID                  string
	Occurrence              uint32
	Capability              string
	HandlerIdentity         string
	ArgumentsSHA256         string
	AuthorityEpoch          string
	ProviderSessionIdentity string
}

func (binding CandidateBinding) valid() bool {
	return binding.RunIdentity != "" && binding.PlanIdentity != "" && binding.SourceSealIdentity != "" &&
		binding.SiteID != "" && binding.Capability != "" && binding.HandlerIdentity != "" &&
		binding.ArgumentsSHA256 != "" && binding.AuthorityEpoch != "" && binding.ProviderSessionIdentity != ""
}

type CandidateOutcome string

const (
	CandidateValue   CandidateOutcome = "value"
	CandidateFailure CandidateOutcome = "failure"
)

// TemporalEvidence is produced by an operation-specific Host adapter. Generic
// PLM code compares identities only; it does not infer freshness from a TTL.
type TemporalEvidence struct {
	Mode             TemporalMode
	ResourceIdentity string
	SnapshotIdentity string
	Version          string
	ClockEpoch       string
	NowTick          uint64
	ValidUntilTick   uint64
}

type CandidateCertificate struct {
	Binding  CandidateBinding
	Outcome  CandidateOutcome
	Temporal TemporalEvidence
}

type LinearizationContext struct {
	Binding                          CandidateBinding
	Temporal                         TemporalEvidence
	TemporalValidated                bool
	ProviderNonInterferenceValidated bool
	StableFailureValidated           bool
}

type LinearizationAction string

type LinearizationReason string

const (
	LinearizationAdopt          LinearizationAction = "adopt_candidate"
	LinearizationStartCanonical LinearizationAction = "start_canonical"

	LinearizationValidCandidate  LinearizationReason = "valid_candidate"
	LinearizationInvalidContract LinearizationReason = "invalid_contract"
	LinearizationBindingMismatch LinearizationReason = "binding_mismatch"
	LinearizationTemporalInvalid LinearizationReason = "temporal_invalid"
	LinearizationProofMissing    LinearizationReason = "proof_missing"
	LinearizationTransportOnly   LinearizationReason = "transport_only"
	LinearizationRetryFailure    LinearizationReason = "retry_prepare_failure"
)

type LinearizationDecision struct {
	Action LinearizationAction
	Reason LinearizationReason
}

func DecidePLMLinearization(contract PLMContract, candidate CandidateCertificate, current LinearizationContext) LinearizationDecision {
	restart := func(reason LinearizationReason) LinearizationDecision {
		return LinearizationDecision{Action: LinearizationStartCanonical, Reason: reason}
	}
	if contract.Validate() != nil || !candidate.Binding.valid() || !current.Binding.valid() ||
		(candidate.Outcome != CandidateValue && candidate.Outcome != CandidateFailure) {
		return restart(LinearizationInvalidContract)
	}
	if candidate.Binding != current.Binding {
		return restart(LinearizationBindingMismatch)
	}
	if contract.PrepareEffect != PrepareSilentRead {
		return restart(LinearizationTransportOnly)
	}
	if !current.TemporalValidated || !current.ProviderNonInterferenceValidated {
		return restart(LinearizationProofMissing)
	}
	if candidate.Temporal.Mode != contract.Temporal || current.Temporal.Mode != contract.Temporal ||
		candidate.Temporal.ResourceIdentity == "" || candidate.Temporal.ResourceIdentity != current.Temporal.ResourceIdentity {
		return restart(LinearizationTemporalInvalid)
	}

	validTemporal := false
	switch contract.Temporal {
	case TemporalImmutable:
		validTemporal = true
	case TemporalSnapshot:
		validTemporal = candidate.Temporal.SnapshotIdentity != "" &&
			candidate.Temporal.SnapshotIdentity == current.Temporal.SnapshotIdentity
	case TemporalVersioned:
		validTemporal = candidate.Temporal.Version != "" && candidate.Temporal.Version == current.Temporal.Version
	case TemporalLeased:
		validTemporal = candidate.Temporal.ClockEpoch != "" &&
			candidate.Temporal.ClockEpoch == current.Temporal.ClockEpoch &&
			candidate.Temporal.ValidUntilTick != 0 && current.Temporal.NowTick <= candidate.Temporal.ValidUntilTick
	}
	if !validTemporal {
		return restart(LinearizationTemporalInvalid)
	}
	if candidate.Outcome == CandidateFailure {
		if contract.Failure != FailureStable {
			return restart(LinearizationRetryFailure)
		}
		if !current.StableFailureValidated {
			return restart(LinearizationTemporalInvalid)
		}
	}
	return LinearizationDecision{Action: LinearizationAdopt, Reason: LinearizationValidCandidate}
}

type CandidateState string

const (
	CandidatePreparing CandidateState = "preparing"
	CandidateReady     CandidateState = "ready"
	CandidateFailed    CandidateState = "failed"
	CandidateAdopted   CandidateState = "adopted"
	CandidateDiscarded CandidateState = "discarded"
)

func ValidCandidateTransition(from, to CandidateState) bool {
	switch from {
	case CandidatePreparing:
		return to == CandidateReady || to == CandidateFailed || to == CandidateDiscarded
	case CandidateReady, CandidateFailed:
		return to == CandidateAdopted || to == CandidateDiscarded
	default:
		return false
	}
}

type JobState string

const (
	JobPending      JobState = "pending"
	JobCompleted    JobState = "completed"
	JobFailed       JobState = "failed"
	JobMaterialized JobState = "materialized"
	JobCancelled    JobState = "cancelled"
)

func ValidJobTransition(from, to JobState) bool {
	switch from {
	case JobPending:
		return to == JobCompleted || to == JobFailed || to == JobCancelled
	case JobCompleted, JobFailed:
		return to == JobMaterialized
	default:
		return false
	}
}
