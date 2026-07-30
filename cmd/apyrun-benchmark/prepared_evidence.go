package main

import (
	"errors"
	"fmt"
)

type preparedFixtureIdentity struct {
	Workload                 string `json:"workload,omitempty"`
	Samples                  int    `json:"samples"`
	CapabilityOperations     int    `json:"capability_operations"`
	ProviderDelayNanoseconds int64  `json:"provider_delay_ns_per_operation"`
	PreparedCapacity         int    `json:"prepared_capacity"`
}

type preparedReadinessEvidence struct {
	FactoryNewTotalNS        int64  `json:"factory_new_total_ns"`
	InstantiateGuestNS       int64  `json:"instantiate_guest_ns"`
	InitializeNS             int64  `json:"_initialize_ns"`
	RuntimeInitNS            int64  `json:"runtime_init_ns"`
	AttachHostCallsNS        int64  `json:"attach_host_calls_ns,omitempty"`
	ReadyInstances           int    `json:"ready_instances"`
	RetainedGuestMemoryBytes uint64 `json:"retained_guest_memory_bytes"`
}

type stateCopyEvidence struct {
	Applicable bool   `json:"applicable"`
	Reason     string `json:"reason"`
}

type preparedSampleEvidence struct {
	PoolHitNS                int64  `json:"pool_hit_ns"`
	PrepareNS                int64  `json:"prepare_ns"`
	ExecuteNS                int64  `json:"execute_ns"`
	CapabilityNS             int64  `json:"capability_ns"`
	RunTotalNS               int64  `json:"run_total_ns"`
	RefillInstantiateGuestNS int64  `json:"refill_instantiate_guest_ns"`
	RefillInitializeNS       int64  `json:"refill__initialize_ns"`
	RefillRuntimeInitNS      int64  `json:"refill_runtime_init_ns"`
	RefillAttachHostCallsNS  int64  `json:"refill_attach_host_calls_ns,omitempty"`
	RefillReadyAfterRunNS    int64  `json:"refill_ready_after_run_ns"`
	RequestBytes             int    `json:"request_bytes"`
	ResultBytes              int    `json:"result_bytes"`
	RetainedGuestMemoryBytes uint64 `json:"retained_guest_memory_bytes"`
}

type preparedWorkloadEvidence struct {
	FirstExecute     preparedSampleEvidence   `json:"first_execute"`
	SteadyExecute    []preparedSampleEvidence `json:"steady_execute"`
	SteadyCapability []preparedSampleEvidence `json:"steady_capability"`
}

type preparedBenchmarkEvidence struct {
	SchemaVersion int                       `json:"schema_version"`
	EvidenceKind  string                    `json:"evidence_kind"`
	EvidenceClass string                    `json:"evidence_class"`
	Artifact      artifactIdentity          `json:"artifact"`
	HostSource    hostSourceIdentity        `json:"host_source"`
	Backend       backendIdentity           `json:"backend"`
	Environment   environmentIdentity       `json:"environment"`
	Fixture       preparedFixtureIdentity   `json:"fixture"`
	CompileOnce   compileEvidence           `json:"compile_once"`
	Readiness     preparedReadinessEvidence `json:"readiness"`
	StateCopy     stateCopyEvidence         `json:"state_copy"`
	Workloads     preparedWorkloadEvidence  `json:"workloads"`
	Limitations   []string                  `json:"limitations"`
}

func (evidence preparedBenchmarkEvidence) Validate() error {
	if evidence.SchemaVersion != 1 || evidence.EvidenceKind != "single-use-preinitialized" {
		return errors.New("unsupported prepared benchmark schema or kind")
	}
	if err := validateEvidenceClassProfile(evidence.EvidenceClass, evidence.Artifact.ArtifactProfile); err != nil {
		return err
	}
	if evidence.Artifact.SHA256 == "" || evidence.Artifact.Size <= 0 || evidence.Artifact.SourceCommit == "" || evidence.Artifact.Target != "wasm32-wasip1" || evidence.Artifact.Execution != "reactor" {
		return errors.New("artifact identity is incomplete")
	}
	if evidence.HostSource.Revision == "" || evidence.HostSource.Modified || evidence.Backend.Name != "wazero" || evidence.Backend.ResetMode != "fresh-instance" {
		return errors.New("Host source or backend identity is invalid")
	}
	if evidence.CompileOnce.InstantiateHostNS <= 0 || evidence.CompileOnce.CompileNS <= 0 ||
		evidence.Readiness.FactoryNewTotalNS <= 0 || evidence.Readiness.InstantiateGuestNS <= 0 ||
		evidence.Readiness.InitializeNS <= 0 || evidence.Readiness.RuntimeInitNS <= 0 ||
		evidence.Readiness.ReadyInstances != 1 || evidence.Readiness.RetainedGuestMemoryBytes == 0 {
		return errors.New("compile or readiness evidence is incomplete")
	}
	if evidence.StateCopy.Applicable || evidence.StateCopy.Reason == "" {
		return errors.New("single-use strategy must record state copy as not applicable")
	}
	if evidence.Fixture.Samples < 3 || evidence.Fixture.Samples > 20 || evidence.Fixture.PreparedCapacity != 1 ||
		evidence.Fixture.ProviderDelayNanoseconds != 2_000_000 ||
		len(evidence.Workloads.SteadyExecute) != evidence.Fixture.Samples ||
		len(evidence.Workloads.SteadyCapability) != evidence.Fixture.Samples {
		return errors.New("prepared fixture or sample count drifted")
	}
	wantOperations := 1
	if evidence.EvidenceClass == "full" {
		wantOperations = 20
	}
	if evidence.Fixture.CapabilityOperations != wantOperations {
		return errors.New("fixture operation count does not match evidence class")
	}
	if err := validatePreparedSample(evidence.Workloads.FirstExecute, false); err != nil {
		return fmt.Errorf("first execute sample: %w", err)
	}
	for _, sample := range evidence.Workloads.SteadyExecute {
		if err := validatePreparedSample(sample, false); err != nil {
			return fmt.Errorf("steady execute sample: %w", err)
		}
	}
	for _, sample := range evidence.Workloads.SteadyCapability {
		if err := validatePreparedSample(sample, true); err != nil {
			return fmt.Errorf("steady capability sample: %w", err)
		}
	}
	if len(evidence.Limitations) == 0 {
		return errors.New("prepared benchmark limitations are missing")
	}
	return nil
}

func validatePreparedSample(sample preparedSampleEvidence, requireCapability bool) error {
	if sample.PoolHitNS <= 0 || sample.PrepareNS <= 0 || sample.ExecuteNS <= 0 || sample.RunTotalNS <= 0 ||
		sample.RefillInstantiateGuestNS <= 0 || sample.RefillInitializeNS <= 0 || sample.RefillRuntimeInitNS <= 0 ||
		sample.RefillReadyAfterRunNS < 0 || sample.RequestBytes <= 0 || sample.ResultBytes <= 0 || sample.RetainedGuestMemoryBytes == 0 {
		return errors.New("prepared lifecycle, refill, memory, or byte evidence is incomplete")
	}
	if requireCapability && sample.CapabilityNS <= 0 {
		return errors.New("capability phase is missing")
	}
	if !requireCapability && sample.CapabilityNS != 0 {
		return errors.New("execute-only sample unexpectedly used a capability")
	}
	return nil
}
