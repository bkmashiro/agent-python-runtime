package capability_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPLMContractV1AcceptsOnlyExplicitTemporalCombinations(t *testing.T) {
	valid := []capability.PLMContract{
		plmValueContract(capability.TemporalImmutable),
		plmValueContract(capability.TemporalSnapshot),
		plmValueContract(capability.TemporalVersioned),
		plmValueContract(capability.TemporalLeased),
		{
			Version: capability.PLMContractVersionV1, Temporal: capability.TemporalCurrent,
			Resource:      capability.ResourceReference{Namespace: "workspace", Argument: "path"},
			PrepareEffect: capability.PrepareTransportOnly, Speculation: capability.SpeculationNever,
			Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
			ProviderNonInterferenceValidator: "pysolate.test.provider-noninterference.v1", CostUnits: 1,
		},
		{
			Version: capability.PLMContractVersionV1, Temporal: capability.TemporalWallclockObserving,
			PrepareEffect: capability.PrepareNone, Speculation: capability.SpeculationNever,
			Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
		},
	}
	for _, contract := range valid {
		if err := contract.Validate(); err != nil {
			t.Fatalf("valid contract %+v: %v", contract, err)
		}
	}

	invalid := []capability.PLMContract{
		{},
		plmValueContract(capability.TemporalCurrent),
		plmValueContract(capability.TemporalWallclockObserving),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalImmutable)
			contract.Authority = "bind_at_prepare"
			return contract
		}(),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalVersioned)
			contract.MaxResultBytes = 0
			return contract
		}(),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalVersioned)
			contract.Failure = capability.FailureStable
			contract.StableFailureValidator = ""
			return contract
		}(),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalVersioned)
			contract.TemporalValidator = ""
			return contract
		}(),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalVersioned)
			contract.ProviderNonInterferenceValidator = ""
			return contract
		}(),
		func() capability.PLMContract {
			contract := plmValueContract(capability.TemporalVersioned)
			contract.TemporalValidator = "not valid"
			return contract
		}(),
	}
	for _, contract := range invalid {
		if err := contract.Validate(); err == nil {
			t.Fatalf("invalid contract accepted: %+v", contract)
		}
	}
}

func TestPLMLinearizationAdoptsImmutableAndRejectsChangedBindings(t *testing.T) {
	contract := plmValueContract(capability.TemporalImmutable)
	binding := plmBinding()
	certificate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "git-object:abc"},
	}
	context := capability.LinearizationContext{
		Binding: binding, TemporalValidated: true, ProviderNonInterferenceValidated: true,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "git-object:abc"},
	}
	decision := capability.DecidePLMLinearization(contract, certificate, context)
	if decision.Action != capability.LinearizationAdopt || decision.Reason != capability.LinearizationValidCandidate {
		t.Fatalf("decision=%+v", decision)
	}

	mutations := map[string]func(*capability.LinearizationContext){
		"Run":              func(current *capability.LinearizationContext) { current.Binding.RunIdentity = "other-run" },
		"Plan":             func(current *capability.LinearizationContext) { current.Binding.PlanIdentity = "other-plan" },
		"source seal":      func(current *capability.LinearizationContext) { current.Binding.SourceSealIdentity = "other-seal" },
		"site":             func(current *capability.LinearizationContext) { current.Binding.SiteID = "site-2" },
		"occurrence":       func(current *capability.LinearizationContext) { current.Binding.Occurrence++ },
		"capability":       func(current *capability.LinearizationContext) { current.Binding.Capability = "other.read" },
		"handler":          func(current *capability.LinearizationContext) { current.Binding.HandlerIdentity = "handler-v2" },
		"arguments":        func(current *capability.LinearizationContext) { current.Binding.ArgumentsSHA256 = "sha256:other" },
		"authority":        func(current *capability.LinearizationContext) { current.Binding.AuthorityEpoch = "authority-2" },
		"provider session": func(current *capability.LinearizationContext) { current.Binding.ProviderSessionIdentity = "session-2" },
		"resource":         func(current *capability.LinearizationContext) { current.Temporal.ResourceIdentity = "git-object:def" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := context
			mutate(&changed)
			decision := capability.DecidePLMLinearization(contract, certificate, changed)
			if decision.Action != capability.LinearizationStartCanonical {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestPLMVersionedAndLeasedCandidatesRequireCurrentEvidence(t *testing.T) {
	binding := plmBinding()
	versioned := plmValueContract(capability.TemporalVersioned)
	versionCertificate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-42"},
	}
	current := capability.LinearizationContext{
		Binding: binding, TemporalValidated: true, ProviderNonInterferenceValidated: true,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-42"},
	}
	if got := capability.DecidePLMLinearization(versioned, versionCertificate, current); got.Action != capability.LinearizationAdopt {
		t.Fatalf("matching version=%+v", got)
	}
	current.Temporal.Version = "seq-43"
	if got := capability.DecidePLMLinearization(versioned, versionCertificate, current); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationTemporalInvalid {
		t.Fatalf("changed version=%+v", got)
	}

	leased := plmValueContract(capability.TemporalLeased)
	leaseCertificate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{
			Mode: capability.TemporalLeased, ResourceIdentity: "lease:tax-table", ClockEpoch: "clock-1", ValidUntilTick: 100,
		},
	}
	leaseContext := capability.LinearizationContext{
		Binding: binding, TemporalValidated: true, ProviderNonInterferenceValidated: true,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalLeased, ResourceIdentity: "lease:tax-table", ClockEpoch: "clock-1", NowTick: 100},
	}
	if got := capability.DecidePLMLinearization(leased, leaseCertificate, leaseContext); got.Action != capability.LinearizationAdopt {
		t.Fatalf("valid lease=%+v", got)
	}
	leaseContext.Temporal.NowTick = 101
	if got := capability.DecidePLMLinearization(leased, leaseCertificate, leaseContext); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationTemporalInvalid {
		t.Fatalf("expired lease=%+v", got)
	}
	leaseContext.Temporal.NowTick = 100
	leaseContext.Temporal.ClockEpoch = "clock-2"
	if got := capability.DecidePLMLinearization(leased, leaseCertificate, leaseContext); got.Action != capability.LinearizationStartCanonical {
		t.Fatalf("foreign clock epoch=%+v", got)
	}
}

func TestPLMSnapshotAndProviderProofsAreRequiredAtLinearization(t *testing.T) {
	binding := plmBinding()
	contract := plmValueContract(capability.TemporalSnapshot)
	candidate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "database:orders", SnapshotIdentity: "snapshot-7"},
	}
	current := capability.LinearizationContext{
		Binding:  binding,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "database:orders", SnapshotIdentity: "snapshot-7"},
	}
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationProofMissing {
		t.Fatalf("missing validator proof=%+v", got)
	}
	current.TemporalValidated = true
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationProofMissing {
		t.Fatalf("missing provider proof=%+v", got)
	}
	current.ProviderNonInterferenceValidated = true
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationAdopt {
		t.Fatalf("validated snapshot=%+v", got)
	}
	current.Temporal.SnapshotIdentity = "snapshot-8"
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationTemporalInvalid {
		t.Fatalf("changed snapshot=%+v", got)
	}
}

func TestPLMProviderVisibleQuotaChangeRejectsCandidate(t *testing.T) {
	binding := plmBinding()
	contract := plmValueContract(capability.TemporalImmutable)
	candidate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "git-object:abc"},
	}
	current := capability.LinearizationContext{
		Binding: binding, TemporalValidated: true,
		ProviderNonInterferenceValidated: false, // Provider quota epoch changed after prepare.
		Temporal:                         capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "git-object:abc"},
	}
	if got := capability.DecidePLMLinearization(contract, candidate, current); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationProofMissing {
		t.Fatalf("quota-affecting candidate=%+v", got)
	}
}

func TestPLMCurrentAndPrepareFailuresRestartAtLinearization(t *testing.T) {
	binding := plmBinding()
	currentContract := capability.PLMContract{
		Version: capability.PLMContractVersionV1, Temporal: capability.TemporalCurrent,
		Resource:      capability.ResourceReference{Namespace: "workspace", Argument: "path"},
		PrepareEffect: capability.PrepareTransportOnly, Speculation: capability.SpeculationNever,
		Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
		ProviderNonInterferenceValidator: "pysolate.test.provider-noninterference.v1", CostUnits: 1,
	}
	candidate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalCurrent, ResourceIdentity: "market:AAPL"},
	}
	context := capability.LinearizationContext{
		Binding: binding, Temporal: candidate.Temporal,
		TemporalValidated: true, ProviderNonInterferenceValidated: true,
	}
	if got := capability.DecidePLMLinearization(currentContract, candidate, context); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationTransportOnly {
		t.Fatalf("current candidate=%+v", got)
	}

	immutable := plmValueContract(capability.TemporalImmutable)
	candidate.Outcome = capability.CandidateFailure
	candidate.Temporal.Mode = capability.TemporalImmutable
	context.Temporal.Mode = capability.TemporalImmutable
	if got := capability.DecidePLMLinearization(immutable, candidate, context); got.Action != capability.LinearizationStartCanonical || got.Reason != capability.LinearizationRetryFailure {
		t.Fatalf("retry failure=%+v", got)
	}
	immutable.Failure = capability.FailureStable
	immutable.StableFailureValidator = "pysolate.test.stable-failure.v1"
	if got := capability.DecidePLMLinearization(immutable, candidate, context); got.Action != capability.LinearizationStartCanonical {
		t.Fatalf("unvalidated stable failure=%+v", got)
	}
	context.StableFailureValidated = true
	if got := capability.DecidePLMLinearization(immutable, candidate, context); got.Action != capability.LinearizationAdopt {
		t.Fatalf("validated stable failure=%+v", got)
	}
}

func TestPLMFakeWorldInvalidationReturnsTheLinearizationPointValue(t *testing.T) {
	world := plmFakeWorld{resource: "market:AAPL", version: "seq-42", value: 100}
	binding := plmBinding()
	candidateValue := world.value
	candidate := capability.CandidateCertificate{
		Binding: binding, Outcome: capability.CandidateValue,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: world.resource, Version: world.version},
	}

	world.version, world.value = "seq-43", 101
	current := capability.LinearizationContext{
		Binding: binding, TemporalValidated: true, ProviderNonInterferenceValidated: true,
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: world.resource, Version: world.version},
	}
	decision := capability.DecidePLMLinearization(plmValueContract(capability.TemporalVersioned), candidate, current)
	if decision.Action != capability.LinearizationStartCanonical {
		t.Fatalf("stale candidate adopted: %+v", decision)
	}
	result := candidateValue
	if decision.Action == capability.LinearizationStartCanonical {
		result = world.value
	}
	if result != 101 {
		t.Fatalf("result=%d want linearization-point value 101", result)
	}
}

type plmFakeWorld struct {
	resource string
	version  string
	value    int
}

func TestPLMCandidateAndJobStateMachinesHaveExplicitTerminalDisposition(t *testing.T) {
	candidateTransitions := [][2]capability.CandidateState{
		{capability.CandidatePrepared, capability.CandidateRunning},
		{capability.CandidatePrepared, capability.CandidateCancelled},
		{capability.CandidatePrepared, capability.CandidateDiscarded},
		{capability.CandidateRunning, capability.CandidateReady},
		{capability.CandidateRunning, capability.CandidateFailed},
		{capability.CandidateRunning, capability.CandidateCancelled},
		{capability.CandidateReady, capability.CandidateAdopted},
		{capability.CandidateReady, capability.CandidateDiscarded},
		{capability.CandidateFailed, capability.CandidateAdopted},
		{capability.CandidateFailed, capability.CandidateDiscarded},
	}
	for _, transition := range candidateTransitions {
		if !capability.ValidCandidateTransition(transition[0], transition[1]) {
			t.Fatalf("candidate transition rejected: %q -> %q", transition[0], transition[1])
		}
	}
	for _, terminal := range []capability.CandidateState{capability.CandidateAdopted, capability.CandidateDiscarded, capability.CandidateCancelled} {
		if capability.ValidCandidateTransition(terminal, capability.CandidateReady) {
			t.Fatalf("candidate terminal state reopened: %q", terminal)
		}
	}

	jobTransitions := [][2]capability.JobState{
		{capability.JobPending, capability.JobCompleted},
		{capability.JobPending, capability.JobFailed},
		{capability.JobPending, capability.JobCancelled},
		{capability.JobCompleted, capability.JobMaterialized},
		{capability.JobFailed, capability.JobMaterialized},
	}
	for _, transition := range jobTransitions {
		if !capability.ValidJobTransition(transition[0], transition[1]) {
			t.Fatalf("job transition rejected: %q -> %q", transition[0], transition[1])
		}
	}
	for _, terminal := range []capability.JobState{capability.JobMaterialized, capability.JobCancelled} {
		if capability.ValidJobTransition(terminal, capability.JobCompleted) {
			t.Fatalf("job terminal state reopened: %q", terminal)
		}
	}
}

func plmValueContract(mode capability.TemporalMode) capability.PLMContract {
	return capability.PLMContract{
		Version: capability.PLMContractVersionV1, Temporal: mode,
		PrepareEffect: capability.PrepareSilentRead, Speculation: capability.SpeculationBudgeted,
		Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
		Resource:                         capability.ResourceReference{Namespace: "workspace", Argument: "path"},
		TemporalValidator:                "pysolate.test.temporal-validator.v1",
		ProviderNonInterferenceValidator: "pysolate.test.provider-noninterference.v1",
		MaxResultBytes:                   1 << 20, CostUnits: 1,
	}
}

func plmBinding() capability.CandidateBinding {
	return capability.CandidateBinding{
		RunIdentity: "run-1", PlanIdentity: "plan-1", SourceSealIdentity: "seal-1",
		SiteID: "site-1", Occurrence: 3, Capability: "fixture.read", HandlerIdentity: "handler-v1",
		ArgumentsSHA256: "sha256:arguments", AuthorityEpoch: "authority-1", ProviderSessionIdentity: "session-1",
	}
}
