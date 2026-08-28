package capability_test

import (
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPLMSmallCoreDecisionProperties(t *testing.T) {
	modes := []capability.TemporalMode{
		capability.TemporalImmutable,
		capability.TemporalSnapshot,
		capability.TemporalVersioned,
		capability.TemporalLeased,
	}
	outcomes := []capability.CandidateOutcome{
		capability.CandidateValue,
		capability.CandidateFailure,
	}
	for _, mode := range modes {
		for _, outcome := range outcomes {
			for mask := 0; mask < 16; mask++ {
				bindingOK := mask&1 != 0
				temporalProof := mask&2 != 0
				providerProof := mask&4 != 0
				temporalOK := mask&8 != 0
				name := fmt.Sprintf("%s/%s/%02x", mode, outcome, mask)
				t.Run(name, func(t *testing.T) {
					contract := plmValueContract(mode)
					candidate := capability.CandidateCertificate{Binding: plmBinding(), Temporal: validTemporalEvidence(mode), Outcome: outcome}
					current := capability.LinearizationContext{
						Binding: candidate.Binding, Temporal: candidate.Temporal,
						TemporalValidated: temporalProof, ProviderNonInterferenceValidated: providerProof,
					}
					if !bindingOK {
						current.Binding.AuthorityEpoch = "authority-other"
					}
					if !temporalOK {
						invalidateTemporalEvidence(&current.Temporal)
					}
					decision := capability.DecidePLMLinearization(contract, candidate, current)
					expectAdopt := bindingOK && temporalProof && providerProof && temporalOK && outcome != capability.CandidateFailure
					if (decision.Action == capability.LinearizationAdopt) != expectAdopt {
						t.Fatalf("decision=%+v expect_adopt=%t", decision, expectAdopt)
					}
				})
			}
		}
	}
}

func TestPLMSmallCoreStableFailureAndNonStageableModes(t *testing.T) {
	binding := plmBinding()
	contract := plmValueContract(capability.TemporalImmutable)
	contract.Failure = capability.FailureStable
	contract.StableFailureValidator = "pysolate.test.stable-failure.v1"
	candidate := capability.CandidateCertificate{Binding: binding, Temporal: validTemporalEvidence(capability.TemporalImmutable), Outcome: capability.CandidateFailure}
	current := capability.LinearizationContext{
		Binding: binding, Temporal: candidate.Temporal, TemporalValidated: true,
		ProviderNonInterferenceValidated: true, StableFailureValidated: true,
	}
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationAdopt {
		t.Fatalf("stable failure=%+v", got)
	}
	current.StableFailureValidated = false
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical {
		t.Fatalf("unproved stable failure=%+v", got)
	}

	for _, mode := range []capability.TemporalMode{capability.TemporalCurrent, capability.TemporalWallclockObserving} {
		contract := plmValueContract(mode)
		contract.Speculation = capability.SpeculationNever
		contract.TemporalValidator = ""
		candidateTemporal := capability.TemporalEvidence{Mode: mode, ResourceIdentity: "resource-1"}
		if mode == capability.TemporalCurrent {
			contract.PrepareEffect = capability.PrepareTransportOnly
			contract.MaxResultBytes = 0
		} else {
			contract.PrepareEffect = capability.PrepareNone
			contract.ProviderNonInterferenceValidator = ""
			contract.Resource = capability.ResourceReference{}
			contract.MaxResultBytes = 0
			contract.CostUnits = 0
		}
		if err := contract.Validate(); err != nil {
			t.Fatalf("mode=%s contract=%v", mode, err)
		}
		candidate := capability.CandidateCertificate{Binding: binding, Temporal: candidateTemporal, Outcome: capability.CandidateValue}
		current := capability.LinearizationContext{Binding: binding, Temporal: candidate.Temporal, TemporalValidated: true, ProviderNonInterferenceValidated: true}
		if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical {
			t.Fatalf("mode=%s decision=%+v", mode, got)
		}
	}
}

func validTemporalEvidence(mode capability.TemporalMode) capability.TemporalEvidence {
	evidence := capability.TemporalEvidence{Mode: mode, ResourceIdentity: "resource-1"}
	switch mode {
	case capability.TemporalSnapshot:
		evidence.SnapshotIdentity = "snapshot-1"
	case capability.TemporalVersioned:
		evidence.Version = "version-1"
	case capability.TemporalLeased:
		evidence.ClockEpoch = "clock-1"
		evidence.NowTick = 4
		evidence.ValidUntilTick = 5
	}
	return evidence
}

func invalidateTemporalEvidence(evidence *capability.TemporalEvidence) {
	switch evidence.Mode {
	case capability.TemporalImmutable:
		evidence.ResourceIdentity = "resource-other"
	case capability.TemporalSnapshot:
		evidence.SnapshotIdentity = "snapshot-other"
	case capability.TemporalVersioned:
		evidence.Version = "version-other"
	case capability.TemporalLeased:
		evidence.ClockEpoch = "clock-other"
	}
}
