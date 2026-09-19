package semanticspeculation

// Historical record schema; the legacy phase-4 execution campaign is retired.
const Phase4TrialRecordSchemaVersion = "pysolate.semantic-speculation-phase4-trial.v2"

type Phase4TrialRecord struct {
	SchemaVersion                string `json:"schema_version"`
	Profile                      string `json:"profile"`
	CaseID                       string `json:"case_id"`
	Treatment                    string `json:"treatment"`
	TrialIndex                   uint32 `json:"trial_index"`
	ExecutionTimeoutNanos        uint64 `json:"execution_timeout_nanos"`
	TotalElapsedNanos            uint64 `json:"total_elapsed_nanos"`
	ProvisioningNanos            uint64 `json:"provisioning_nanos"`
	AdmissionNanos               uint64 `json:"admission_nanos"`
	AnalyzerInvocations          uint32 `json:"analyzer_invocations"`
	AnalyzerSessionCount         uint32 `json:"analyzer_session_count"`
	RuntimeInitNanos             uint64 `json:"runtime_init_nanos"`
	FormalExecutionNanos         uint64 `json:"formal_execution_nanos"`
	ProviderNanos                uint64 `json:"provider_nanos"`
	LogicalCallCount             uint32 `json:"logical_call_count"`
	PhysicalAttemptCount         uint32 `json:"physical_attempt_count"`
	OrphanedPhysicalCount        uint32 `json:"orphaned_physical_count"`
	ReadyBeforeFinalize          uint32 `json:"ready_before_finalize"`
	PreparedOrCOWHitCount        uint32 `json:"prepared_or_cow_hit_count"`
	PreparedOrCOWFallbackCount   uint32 `json:"prepared_or_cow_fallback_count"`
	DiscardedCapacityBytes       uint64 `json:"discarded_capacity_bytes"`
	ResidentMemoryBytes          uint64 `json:"resident_memory_bytes"`
	AuthorityTerminalDisposition string `json:"authority_terminal_disposition"`
	WorkspaceTerminalDisposition string `json:"workspace_terminal_disposition"`
	FinalProgramOutcome          string `json:"final_program_outcome"`
	ResultSHA256                 string `json:"result_sha256"`
	ErrorClass                   string `json:"error_class"`
	FormalGuestExecutions        uint32 `json:"formal_guest_executions"`
	VisiblePrefixes              uint32 `json:"visible_prefixes"`
	SkippedPrefixes              uint32 `json:"skipped_prefixes"`
}
