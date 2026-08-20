package wazero

import "sync"

const SemanticAnalysisLifecycleSchemaVersion = "pysolate.semantic-analysis-lifecycle.v2"

// SemanticAnalysisLifecycleEvidence is body-free cumulative evidence for exact
// target-Guest analyzer invocations owned by one Engine.
type SemanticAnalysisLifecycleEvidence struct {
	SchemaVersion             string `json:"schema_version"`
	Invocations               uint32 `json:"invocations"`
	ModuleInstantiations      uint32 `json:"module_instantiations"`
	InitializeCalls           uint32 `json:"initialize_calls"`
	RuntimeInitCalls          uint32 `json:"runtime_init_calls"`
	Successes                 uint32 `json:"successes"`
	Failures                  uint32 `json:"failures"`
	PreparedProvisions        uint32 `json:"prepared_provisions"`
	PreparedProvisionFailures uint32 `json:"prepared_provision_failures"`
	PreparedHits              uint32 `json:"prepared_hits"`
	COWHits                   uint32 `json:"cow_hits"`
	FreshFallbacks            uint32 `json:"fresh_fallbacks"`
	InstantiateNanos          uint64 `json:"instantiate_nanos"`
	InitializeNanos           uint64 `json:"initialize_nanos"`
	RuntimeInitNanos          uint64 `json:"runtime_init_nanos"`
	AnalyzeNanos              uint64 `json:"analyze_nanos"`
	CloseNanos                uint64 `json:"close_nanos"`
	PreparedProvisionNanos    uint64 `json:"prepared_provision_nanos"`
	COWCloneNanos             uint64 `json:"cow_clone_nanos"`
}

type semanticAnalysisLifecycleStore struct {
	mu    sync.Mutex
	value SemanticAnalysisLifecycleEvidence
}

func (store *semanticAnalysisLifecycleStore) add(delta SemanticAnalysisLifecycleEvidence) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.value.SchemaVersion = SemanticAnalysisLifecycleSchemaVersion
	store.value.Invocations += delta.Invocations
	store.value.ModuleInstantiations += delta.ModuleInstantiations
	store.value.InitializeCalls += delta.InitializeCalls
	store.value.RuntimeInitCalls += delta.RuntimeInitCalls
	store.value.Successes += delta.Successes
	store.value.Failures += delta.Failures
	store.value.PreparedProvisions += delta.PreparedProvisions
	store.value.PreparedProvisionFailures += delta.PreparedProvisionFailures
	store.value.PreparedHits += delta.PreparedHits
	store.value.COWHits += delta.COWHits
	store.value.FreshFallbacks += delta.FreshFallbacks
	store.value.InstantiateNanos += delta.InstantiateNanos
	store.value.InitializeNanos += delta.InitializeNanos
	store.value.RuntimeInitNanos += delta.RuntimeInitNanos
	store.value.AnalyzeNanos += delta.AnalyzeNanos
	store.value.CloseNanos += delta.CloseNanos
	store.value.PreparedProvisionNanos += delta.PreparedProvisionNanos
	store.value.COWCloneNanos += delta.COWCloneNanos
}

func (store *semanticAnalysisLifecycleStore) get() SemanticAnalysisLifecycleEvidence {
	store.mu.Lock()
	defer store.mu.Unlock()
	value := store.value
	value.SchemaVersion = SemanticAnalysisLifecycleSchemaVersion
	return value
}

func (engine *Engine) SemanticAnalysisLifecycleEvidence() SemanticAnalysisLifecycleEvidence {
	if engine == nil {
		return SemanticAnalysisLifecycleEvidence{SchemaVersion: SemanticAnalysisLifecycleSchemaVersion}
	}
	return engine.semanticLifecycle.get()
}
