package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
)

const Phase5TrialRecordSchemaVersion = "pysolate.semantic-speculation-phase5-trial.v1"
const Phase5StageMeasured = "measured"
const Phase5StagePreclock = "preclock"
const Phase5StageNotApplicable = "not_applicable"

var phase5TimingStages = []string{
	"finalization_gap",
	"analyzer_provision",
	"analysis",
	"patch_emission",
	"scratch_guest_provision",
	"scratch_execution",
	"capsule_seal_transport",
	"final_selection_validation",
	"final_guest_provision",
	"final_patch_compile_load",
	"final_execution",
	"teardown",
}

func Phase5TimingStageNames() []string {
	return append([]string(nil), phase5TimingStages...)
}

type Phase5StageObservation struct {
	Name               string `json:"name"`
	Disposition        string `json:"disposition"`
	StartedOffsetNanos uint64 `json:"started_offset_nanos"`
	EndedOffsetNanos   uint64 `json:"ended_offset_nanos"`
	DurationNanos      uint64 `json:"duration_nanos"`
	OnCriticalPath     bool   `json:"on_critical_path"`
}

type Phase5TrialRecord struct {
	SchemaVersion                  string                   `json:"schema_version"`
	StudyID                        string                   `json:"study_id"`
	CaseMatrixIdentity             string                   `json:"case_matrix_identity"`
	PreregistrationIdentity        string                   `json:"preregistration_identity"`
	HarnessIdentity                string                   `json:"harness_identity"`
	GuestArtifactSHA256            string                   `json:"guest_artifact_sha256"`
	RunID                          string                   `json:"run_id"`
	Profile                        string                   `json:"profile"`
	CaseID                         string                   `json:"case_id"`
	Treatment                      string                   `json:"treatment"`
	TrialIndex                     uint32                   `json:"trial_index"`
	SourceSHA256                   string                   `json:"source_sha256"`
	RegionSourceSHA256             string                   `json:"region_source_sha256"`
	DecisionSHA256                 string                   `json:"decision_sha256"`
	PatchSHA256                    string                   `json:"patch_sha256"`
	CapsuleSHA256                  string                   `json:"capsule_sha256"`
	SelectionSHA256                string                   `json:"selection_sha256"`
	DerivedASTSHA256               string                   `json:"derived_ast_sha256"`
	ExpectedDisposition            string                   `json:"expected_disposition"`
	ExpectedOutcome                string                   `json:"expected_outcome"`
	ActualDisposition              string                   `json:"actual_disposition"`
	ActualOutcome                  string                   `json:"actual_outcome"`
	ResultSHA256                   string                   `json:"result_sha256"`
	ErrorClass                     string                   `json:"error_class"`
	ErrorMessageSHA256             string                   `json:"error_message_sha256"`
	TracebackSHA256                string                   `json:"traceback_sha256"`
	LogsSHA256                     string                   `json:"logs_sha256"`
	CriticalPathStartedOffsetNanos uint64                   `json:"critical_path_started_offset_nanos"`
	TrialEndedOffsetNanos          uint64                   `json:"trial_ended_offset_nanos"`
	TotalCriticalPathNanos         uint64                   `json:"total_critical_path_nanos"`
	UnattributedCriticalPathNanos  uint64                   `json:"unattributed_critical_path_nanos"`
	Stages                         []Phase5StageObservation `json:"stages"`
	AnalyzerSessionCount           uint32                   `json:"analyzer_session_count"`
	AnalyzerRuntimeInitCount       uint32                   `json:"analyzer_runtime_init_count"`
	ScratchGuestExecutions         uint32                   `json:"scratch_guest_executions"`
	ScratchRuntimeInitCount        uint32                   `json:"scratch_runtime_init_count"`
	FormalGuestExecutions          uint32                   `json:"formal_guest_executions"`
	FinalRuntimeInitCount          uint32                   `json:"final_runtime_init_count"`
	HelperClaimCount               uint32                   `json:"helper_claim_count"`
	CapsuleConsumedCount           uint32                   `json:"capsule_consumed_count"`
	CapsuleRejectedClaimCount      uint32                   `json:"capsule_rejected_claim_count"`
	CapsuleDiscardedCount          uint32                   `json:"capsule_discarded_count"`
	CapsuleBytes                   uint32                   `json:"capsule_bytes"`
	LogicalCallCount               uint32                   `json:"logical_call_count"`
	OrphanedPhysicalCount          uint32                   `json:"orphaned_physical_count"`
	PeakResidentMemoryBytes        uint64                   `json:"peak_resident_memory_bytes"`
	DiscardedCapacityBytes         uint64                   `json:"discarded_capacity_bytes"`
	AuthorityTerminalDisposition   string                   `json:"authority_terminal_disposition"`
	WorkspaceTerminalDisposition   string                   `json:"workspace_terminal_disposition"`
}

func EncodePhase5TrialRecord(record Phase5TrialRecord) ([]byte, error) {
	if err := validatePhase5TrialRecord(record, ""); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func DecodePhase5TrialRecord(raw []byte, expectedHarnessIdentity string) (Phase5TrialRecord, error) {
	if len(raw) == 0 || len(raw) > 64*1024 || !digestPattern.MatchString(expectedHarnessIdentity) {
		return Phase5TrialRecord{}, errors.New("invalid phase 5 trial record")
	}
	var record Phase5TrialRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Phase5TrialRecord{}, errors.New("invalid phase 5 trial record")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Phase5TrialRecord{}, errors.New("invalid phase 5 trial record")
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(raw, canonical) || validatePhase5TrialRecord(record, expectedHarnessIdentity) != nil {
		return Phase5TrialRecord{}, errors.New("invalid phase 5 trial record")
	}
	return record, nil
}

func validatePhase5TrialRecord(record Phase5TrialRecord, expectedHarnessIdentity string) error {
	if record.SchemaVersion != Phase5TrialRecordSchemaVersion || record.StudyID != Phase5StudyID || record.CaseMatrixIdentity != Phase5CaseMatrixIdentity || record.PreregistrationIdentity != Phase5PreregistrationIdentity || record.GuestArtifactSHA256 != Phase5GuestArtifactSHA256 || !digestPattern.MatchString(record.HarnessIdentity) || (expectedHarnessIdentity != "" && record.HarnessIdentity != expectedHarnessIdentity) {
		return errors.New("phase 5 trial identity drift")
	}
	coordinate := Phase5CampaignCoordinate{Profile: record.Profile, CaseID: record.CaseID, Treatment: record.Treatment, TrialIndex: record.TrialIndex}
	if record.RunID != phase5OpaqueRunID(coordinate) || !phase5CampaignContains(coordinate) {
		return errors.New("invalid phase 5 trial coordinate")
	}
	var candidate Phase5Case
	for _, item := range Phase5Cases() {
		if item.ID == record.CaseID {
			candidate = item
			break
		}
	}
	if !candidate.EconomicsEligible || record.SourceSHA256 != candidate.SourceSHA256 || record.RegionSourceSHA256 != candidate.RegionSourceSHA256 || record.ExpectedDisposition != candidate.ExpectedDisposition || record.ExpectedOutcome != candidate.ExpectedOutcome {
		return errors.New("phase 5 trial case binding drift")
	}
	if record.CriticalPathStartedOffsetNanos > record.TrialEndedOffsetNanos || record.TotalCriticalPathNanos != record.TrialEndedOffsetNanos-record.CriticalPathStartedOffsetNanos || len(record.Stages) != len(phase5TimingStages) {
		return errors.New("invalid phase 5 critical path")
	}
	for index, stage := range record.Stages {
		if stage.Name != phase5TimingStages[index] {
			return errors.New("phase 5 stage order drift")
		}
		switch stage.Disposition {
		case Phase5StageNotApplicable:
			if stage.StartedOffsetNanos != 0 || stage.EndedOffsetNanos != 0 || stage.DurationNanos != 0 || stage.OnCriticalPath {
				return errors.New("invalid not-applicable phase 5 stage")
			}
		case Phase5StagePreclock:
			if stage.EndedOffsetNanos < stage.StartedOffsetNanos || stage.EndedOffsetNanos > record.CriticalPathStartedOffsetNanos || stage.DurationNanos != stage.EndedOffsetNanos-stage.StartedOffsetNanos || stage.OnCriticalPath {
				return errors.New("invalid preclock phase 5 stage")
			}
		case Phase5StageMeasured:
			if stage.EndedOffsetNanos < stage.StartedOffsetNanos || stage.StartedOffsetNanos < record.CriticalPathStartedOffsetNanos || stage.EndedOffsetNanos > record.TrialEndedOffsetNanos || stage.DurationNanos != stage.EndedOffsetNanos-stage.StartedOffsetNanos || !stage.OnCriticalPath {
				return errors.New("invalid measured phase 5 stage")
			}
		default:
			return errors.New("invalid phase 5 stage disposition")
		}
	}
	union := phase5CriticalStageUnion(record.Stages, record.CriticalPathStartedOffsetNanos, record.TrialEndedOffsetNanos)
	minimumGapNanos := uint64(candidate.FinalizationGapMillis) * 1_000_000
	if record.Stages[0].DurationNanos < minimumGapNanos {
		return errors.New("phase 5 finalization gap shorter than frozen coordinate")
	}
	if union > record.TotalCriticalPathNanos || record.UnattributedCriticalPathNanos != record.TotalCriticalPathNanos-union {
		return errors.New("phase 5 critical path does not reconcile")
	}
	if err := validatePhase5PositiveOutcome(record, candidate); err != nil {
		return err
	}
	return nil
}

func validatePhase5PositiveOutcome(record Phase5TrialRecord, candidate Phase5Case) error {
	if record.ActualDisposition != candidate.ExpectedDisposition || record.ActualOutcome != candidate.ExpectedOutcome || record.ResultSHA256 != syntheticDigest(candidate.ExpectedResult) || record.ErrorClass != "" || record.ErrorMessageSHA256 != "" || record.TracebackSHA256 != "" || !digestPattern.MatchString(record.LogsSHA256) || record.AuthorityTerminalDisposition != "none" || record.WorkspaceTerminalDisposition != "unmounted" || record.LogicalCallCount != 0 || record.OrphanedPhysicalCount != 0 || record.CapsuleRejectedClaimCount != 0 || record.CapsuleDiscardedCount != 0 || record.PeakResidentMemoryBytes == 0 || record.FinalRuntimeInitCount != 1 {
		return errors.New("phase 5 positive outcome drift")
	}
	identityFields := []string{record.DecisionSHA256, record.PatchSHA256, record.CapsuleSHA256, record.SelectionSHA256, record.DerivedASTSHA256}
	if record.Treatment == "original_unchanged" {
		for _, identity := range identityFields {
			if identity != "" {
				return errors.New("original trial carries derived identity")
			}
		}
		if record.AnalyzerSessionCount != 0 || record.AnalyzerRuntimeInitCount != 0 || record.ScratchGuestExecutions != 0 || record.ScratchRuntimeInitCount != 0 || record.HelperClaimCount != 0 || record.CapsuleConsumedCount != 0 || record.CapsuleBytes != 0 || record.FormalGuestExecutions != 1 {
			return errors.New("original trial lifecycle drift")
		}
	} else if record.Treatment == "prepared_region_derived" {
		for _, identity := range identityFields {
			if !digestPattern.MatchString(identity) {
				return errors.New("derived trial identity missing")
			}
		}
		if record.AnalyzerSessionCount != 1 || record.AnalyzerRuntimeInitCount != 1 || record.ScratchGuestExecutions != 1 || record.ScratchRuntimeInitCount != 1 || record.HelperClaimCount != 1 || record.CapsuleConsumedCount != 1 || record.CapsuleBytes == 0 || record.CapsuleBytes > 256 || record.FormalGuestExecutions != 2 {
			return errors.New("derived trial lifecycle drift")
		}
	} else {
		return errors.New("invalid phase 5 treatment")
	}
	return validatePhase5StageApplicability(record)
}

func validatePhase5StageApplicability(record Phase5TrialRecord) error {
	stages := map[string]Phase5StageObservation{}
	for _, stage := range record.Stages {
		stages[stage.Name] = stage
	}
	derivedOnly := []string{"analyzer_provision", "analysis", "patch_emission", "scratch_guest_provision", "scratch_execution", "capsule_seal_transport", "final_selection_validation", "final_patch_compile_load"}
	if record.Treatment == "original_unchanged" {
		for _, name := range derivedOnly {
			if stages[name].Disposition != Phase5StageNotApplicable {
				return fmt.Errorf("original trial measured %s", name)
			}
		}
	} else {
		for _, name := range []string{"analysis", "patch_emission", "scratch_execution", "capsule_seal_transport", "final_selection_validation", "final_patch_compile_load"} {
			if stages[name].Disposition != Phase5StageMeasured {
				return fmt.Errorf("derived trial did not measure %s", name)
			}
		}
	}
	for _, name := range []string{"finalization_gap", "final_execution", "teardown"} {
		if stages[name].Disposition != Phase5StageMeasured {
			return fmt.Errorf("phase 5 trial did not measure %s", name)
		}
	}
	if record.Profile == "cold_end_to_end" {
		if stages["final_guest_provision"].Disposition != Phase5StageMeasured || (record.Treatment == "prepared_region_derived" && (stages["analyzer_provision"].Disposition != Phase5StageMeasured || stages["scratch_guest_provision"].Disposition != Phase5StageMeasured)) {
			return errors.New("cold phase 5 provisioning drift")
		}
	} else if record.Profile == "preprovisioned_equivalent_capacity" {
		if stages["final_guest_provision"].Disposition != Phase5StagePreclock || (record.Treatment == "prepared_region_derived" && (stages["analyzer_provision"].Disposition != Phase5StagePreclock || stages["scratch_guest_provision"].Disposition != Phase5StagePreclock)) {
			return errors.New("preprovisioned phase 5 capacity drift")
		}
	} else {
		return errors.New("invalid phase 5 profile")
	}
	return nil
}

func phase5CriticalStageUnion(stages []Phase5StageObservation, start, end uint64) uint64 {
	type interval struct{ start, end uint64 }
	intervals := make([]interval, 0, len(stages))
	for _, stage := range stages {
		if !stage.OnCriticalPath || stage.Disposition != Phase5StageMeasured || stage.EndedOffsetNanos <= start || stage.StartedOffsetNanos >= end {
			continue
		}
		left, right := stage.StartedOffsetNanos, stage.EndedOffsetNanos
		if left < start {
			left = start
		}
		if right > end {
			right = end
		}
		intervals = append(intervals, interval{left, right})
	}
	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].start < intervals[j].start || (intervals[i].start == intervals[j].start && intervals[i].end < intervals[j].end)
	})
	var total, left, right uint64
	for index, current := range intervals {
		if index == 0 {
			left, right = current.start, current.end
			continue
		}
		if current.start <= right {
			if current.end > right {
				right = current.end
			}
			continue
		}
		total += right - left
		left, right = current.start, current.end
	}
	if len(intervals) != 0 {
		total += right - left
	}
	return total
}

func phase5CampaignContains(target Phase5CampaignCoordinate) bool {
	for _, coordinate := range Phase5CampaignCoordinates() {
		if coordinate == target {
			return true
		}
	}
	return false
}

func phase5OpaqueRunID(coordinate Phase5CampaignCoordinate) string {
	digest := sha256.Sum256([]byte(coordinate.Profile + "\x00" + coordinate.CaseID + "\x00" + coordinate.Treatment + "\x00" + strconv.FormatUint(uint64(coordinate.TrialIndex), 10)))
	return "phase5-" + hex.EncodeToString(digest[:12])
}
