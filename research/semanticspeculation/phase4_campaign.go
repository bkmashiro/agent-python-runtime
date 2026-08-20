package semanticspeculation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const Phase4TrialRecordSchemaVersion = "pysolate.semantic-speculation-phase4-trial.v1"

type Phase4CampaignConfig struct {
	Artifact      []byte
	RunConfig     runtimeconfig.RunConfig
	WorkspaceRoot string
	UseLinuxCOW   bool
}

type Phase4TrialRecord struct {
	SchemaVersion                string `json:"schema_version"`
	Profile                      string `json:"profile"`
	CaseID                       string `json:"case_id"`
	Treatment                    string `json:"treatment"`
	TrialIndex                   uint32 `json:"trial_index"`
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

type formalExecutionReporter interface{ FormalExecutionNanos() uint64 }

func RunPhase4CampaignCoordinate(ctx context.Context, config Phase4CampaignConfig, coordinate Phase4CampaignCoordinate) (Phase4TrialRecord, error) {
	if ctx == nil || len(config.Artifact) == 0 || config.RunConfig.ExecutionProfile == nil || config.WorkspaceRoot == "" {
		return Phase4TrialRecord{}, errors.New("invalid phase 4 campaign config")
	}
	var fixture SyntheticCase
	var matrixCoordinate Phase4SyntheticCoordinate
	found := false
	for _, candidate := range Phase4SyntheticCoordinates() {
		if candidate.Fixture.ID == coordinate.CaseID {
			fixture, matrixCoordinate, found = candidate.Fixture, candidate, true
			break
		}
	}
	if !found || coordinate.TrialIndex == 0 || coordinate.TrialIndex > Phase4TrialsPerTreatment || !containsString(phase4Profiles, coordinate.Profile) || !containsString(phase4Treatments, coordinate.Treatment) {
		return Phase4TrialRecord{}, errors.New("invalid phase 4 coordinate")
	}
	tracker := &campaignProviderTracker{}
	plan, err := NewPhase3CampaignPlan(capability.HandlerFunc(func(callCtx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		started := time.Now()
		defer func() { tracker.elapsedNanos.Add(uint64(time.Since(started))) }()
		tracker.attempts.Add(1)
		timer := time.NewTimer(time.Duration(matrixCoordinate.PhysicalDelayMillis) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-callCtx.Done():
			tracker.cancelled.Add(1)
			return nil, callCtx.Err()
		case <-timer.C:
		}
		response := json.RawMessage(`{"value":"weather"}`)
		tracker.successes.Add(1)
		tracker.resultBytes.Add(uint64(len(response)))
		tracker.costUnits.Add(1)
		return response, nil
	}))
	if err != nil {
		return Phase4TrialRecord{}, err
	}
	runID := phase4OpaqueRunID(coordinate)
	observation := func() ProviderObservation {
		return ProviderObservation{Attempts: tracker.attempts.Load(), ResultBytes: tracker.resultBytes.Load(), CostUnits: tracker.costUnits.Load(), ElapsedNanos: tracker.elapsedNanos.Load(), Dispositions: PhysicalDispositions{Consumed: tracker.successes.Load(), Cancelled: tracker.cancelled.Load()}}
	}
	brokerFactory := func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
	}
	workspaceRoot := filepath.Join(config.WorkspaceRoot, runID)
	var treatment ScheduledTreatment
	var semanticTreatment *SemanticPreDispatchTreatment
	var provisioningNanos uint64
	if coordinate.Treatment == "semantic_pre_dispatch" {
		semanticConfig := config.RunConfig
		var prepared *wazeroengine.Engine
		if coordinate.Profile == "preprovisioned_equivalent_capacity" {
			semanticConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, PreparedRuntime: true, MemoryCOW: config.UseLinuxCOW}
			provisionStarted := time.Now()
			prepared, err = wazeroengine.New(ctx, config.Artifact, semanticConfig)
			if err == nil {
				err = prepared.PrepareSemanticRuntime(ctx)
			}
			provisioningNanos = uint64(time.Since(provisionStarted))
			if err != nil {
				if prepared != nil {
					_ = prepared.Close(ctx)
				}
				return Phase4TrialRecord{}, err
			}
		}
		newAnalyzer := func(inner context.Context, artifact []byte, runConfig runtimeconfig.RunConfig) (*wazeroengine.Engine, error) {
			if prepared != nil {
				engine := prepared
				prepared = nil
				return engine, nil
			}
			return wazeroengine.New(inner, artifact, runConfig)
		}
		semanticTreatment, err = NewSemanticPreDispatchTreatment(SemanticPreDispatchTreatmentConfig{Artifact: config.Artifact, RunConfig: semanticConfig, NewAnalyzer: newAnalyzer, Plan: plan, ProviderObservation: observation, ImportClosureSHA256: digestBytes(config.Artifact), PhysicalReadBudget: 1, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
		treatment = semanticTreatment
	} else if coordinate.Treatment == "serial_whole_file" {
		runConfig := config.RunConfig
		runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
		treatment, err = NewSerialGuestTreatment(SerialGuestTreatmentConfig{Artifact: config.Artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory, ProviderObservation: observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
	} else {
		runConfig := config.RunConfig
		runConfig.Mechanisms = runtimeconfig.MechanismSet{Streaming: true, PrivateWorkspace: true}
		treatment, err = NewEagerGuestTreatment(EagerGuestTreatmentConfig{Artifact: config.Artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory, AllowedImportRoots: config.RunConfig.ExecutionProfile.AllowedImports(), ProviderObservation: observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
	}
	if err != nil {
		return Phase4TrialRecord{}, err
	}
	started := time.Now()
	scheduled, err := RunScheduledTreatment(ctx, fixture, treatment)
	total := uint64(time.Since(started))
	if err != nil {
		return Phase4TrialRecord{}, err
	}
	provider := observation()
	outcome := scheduled.Outcome
	record := Phase4TrialRecord{SchemaVersion: Phase4TrialRecordSchemaVersion, Profile: coordinate.Profile, CaseID: coordinate.CaseID, Treatment: coordinate.Treatment, TrialIndex: coordinate.TrialIndex, TotalElapsedNanos: total, ProvisioningNanos: provisioningNanos, ProviderNanos: provider.ElapsedNanos, LogicalCallCount: outcome.LogicalCalls, PhysicalAttemptCount: outcome.PhysicalAttempts, ReadyBeforeFinalize: outcome.ReadyBeforeFinalize, ResidentMemoryBytes: processResidentBytes(), AuthorityTerminalDisposition: outcome.AuthorityDisposition, WorkspaceTerminalDisposition: outcome.WorkspaceDisposition, FinalProgramOutcome: outcome.FinalProgramOutcome, ResultSHA256: outcome.ResultSHA256, ErrorClass: outcome.ErrorClass}
	if reporter, ok := treatment.(formalExecutionReporter); ok {
		record.FormalExecutionNanos = reporter.FormalExecutionNanos()
	}
	if semanticTreatment != nil {
		lifecycle := semanticTreatment.LifecycleEvidence()
		record.AdmissionNanos = lifecycle.AdmissionNanos
		record.AnalyzerInvocations = lifecycle.Analyzer.Invocations
		record.AnalyzerSessionCount = lifecycle.AnalyzerSessions
		record.RuntimeInitNanos = lifecycle.Analyzer.RuntimeInitNanos
		record.FormalExecutionNanos = lifecycle.FormalExecutionNanos
		record.PreparedOrCOWHitCount = lifecycle.Analyzer.PreparedHits + lifecycle.Analyzer.COWHits
		record.PreparedOrCOWFallbackCount = lifecycle.Analyzer.FreshFallbacks
		record.FormalGuestExecutions = lifecycle.FormalGuestExecutions
		record.VisiblePrefixes = lifecycle.VisiblePrefixes
		record.SkippedPrefixes = lifecycle.SkippedPrefixes
	}
	if outcome.PhysicalDispositions.Orphaned != 0 {
		record.OrphanedPhysicalCount = outcome.PhysicalDispositions.Orphaned
	}
	return record, nil
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
func phase4OpaqueRunID(c Phase4CampaignCoordinate) string {
	digest := sha256.Sum256([]byte(c.Profile + "\x00" + c.CaseID + "\x00" + c.Treatment + "\x00" + strconv.FormatUint(uint64(c.TrialIndex), 10)))
	return "phase4-" + hex.EncodeToString(digest[:12])
}
func processResidentBytes() uint64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}
