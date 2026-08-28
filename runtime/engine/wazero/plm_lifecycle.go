package wazero

import "sync"

const PLMRunLifecycleSchemaVersion = "pysolate.plm-run-lifecycle.v1"

type PLMRunLifecycleEvidence struct {
	SchemaVersion         string `json:"schema_version"`
	ModuleInstantiations  uint32 `json:"module_instantiations"`
	InitializeCalls       uint32 `json:"initialize_calls"`
	RuntimeInitCalls      uint32 `json:"runtime_init_calls"`
	SourceValidationCalls uint32 `json:"source_validation_calls"`
	LoweringCalls         uint32 `json:"lowering_calls"`
	SelectionCalls        uint32 `json:"selection_calls"`
	ExecuteCalls          uint32 `json:"execute_calls"`
	FreshModule           bool   `json:"fresh_module"`
	PreparedModule        bool   `json:"prepared_module"`
	InstantiateNanos      uint64 `json:"instantiate_nanos"`
	InitializeNanos       uint64 `json:"initialize_nanos"`
	RuntimeInitNanos      uint64 `json:"runtime_init_nanos"`
	SourceValidationNanos uint64 `json:"source_validation_nanos"`
	LoweringNanos         uint64 `json:"lowering_nanos"`
	SelectionNanos        uint64 `json:"selection_nanos"`
	PrepareNanos          uint64 `json:"prepare_nanos"`
	ExecuteNanos          uint64 `json:"execute_nanos"`
	TotalNanos            uint64 `json:"total_nanos"`
}

type plmRunLifecycleStore struct {
	mu       sync.Mutex
	evidence PLMRunLifecycleEvidence
}

func (store *plmRunLifecycleStore) set(evidence PLMRunLifecycleEvidence) {
	store.mu.Lock()
	store.evidence = evidence
	store.mu.Unlock()
}

func (store *plmRunLifecycleStore) get() PLMRunLifecycleEvidence {
	store.mu.Lock()
	defer store.mu.Unlock()
	evidence := store.evidence
	if evidence.SchemaVersion == "" {
		evidence.SchemaVersion = PLMRunLifecycleSchemaVersion
	}
	return evidence
}

func (engine *Engine) PLMRunLifecycleEvidence() PLMRunLifecycleEvidence {
	if engine == nil {
		return PLMRunLifecycleEvidence{SchemaVersion: PLMRunLifecycleSchemaVersion}
	}
	return engine.plmRunLifecycle.get()
}
