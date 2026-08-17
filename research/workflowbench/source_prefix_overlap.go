package workflowbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

const SourcePrefixScheduleSchema = "pysolate.source-prefix-schedule.v1"
const SourcePrefixExperimentContractSchema = "pysolate.source-prefix-experiment-contract.v1"
const SourcePrefixEvidenceSchema = "pysolate.source-prefix-evidence.v1"
const SourcePrefixClaimBoundary = "mechanism_fixture_bounded"

var sourcePrefixCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const (
	maxSourcePrefixChunks = 32
	maxSourcePrefixBytes  = 64 * 1024
)

type SourcePrefixTreatment string

const (
	SourcePrefixBaseline  SourcePrefixTreatment = "generate_then_execute"
	SourcePrefixStreaming SourcePrefixTreatment = "stream_while_generating"
)

type TimedSourceChunk struct {
	OffsetMS uint32 `json:"offset_ms"`
	Source   string `json:"source"`
}

type SourcePrefixSchedule struct {
	SchemaVersion     string             `json:"schema_version"`
	CaseID            string             `json:"case_id"`
	Chunks            []TimedSourceChunk `json:"chunks"`
	MaxBufferedChunks uint32             `json:"max_buffered_chunks"`
	MaxBufferedBytes  uint32             `json:"max_buffered_bytes"`
}

func (schedule SourcePrefixSchedule) Validate() error {
	if schedule.SchemaVersion != SourcePrefixScheduleSchema || !evidenceID.MatchString(schedule.CaseID) || len(schedule.Chunks) == 0 || len(schedule.Chunks) > maxSourcePrefixChunks {
		return errors.New("invalid source-prefix schedule identity or chunk count")
	}
	if schedule.MaxBufferedChunks == 0 || schedule.MaxBufferedChunks > maxSourcePrefixChunks || uint32(len(schedule.Chunks)) > schedule.MaxBufferedChunks || schedule.MaxBufferedBytes == 0 || schedule.MaxBufferedBytes > maxSourcePrefixBytes {
		return errors.New("invalid source-prefix queue bounds")
	}
	total := 0
	for index, chunk := range schedule.Chunks {
		if chunk.Source == "" || !utf8.ValidString(chunk.Source) || (index > 0 && chunk.OffsetMS <= schedule.Chunks[index-1].OffsetMS) {
			return errors.New("invalid source-prefix chunk")
		}
		total += len(chunk.Source)
		if total > int(schedule.MaxBufferedBytes) {
			return errors.New("source-prefix byte budget exceeded")
		}
	}
	return nil
}

func (schedule SourcePrefixSchedule) Identity() (string, error) {
	if err := schedule.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(schedule)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

type SourcePrefixExperimentContract struct {
	SchemaVersion        string               `json:"schema_version"`
	ExperimentID         string               `json:"experiment_id"`
	Schedule             SourcePrefixSchedule `json:"schedule"`
	Repetitions          uint32               `json:"repetitions"`
	ToolDelayMS          uint32               `json:"tool_delay_ms"`
	ExpectedResultSHA256 string               `json:"expected_result_sha256"`
	OracleSHA256         string               `json:"oracle_sha256"`
	LaneConfigSHA256     string               `json:"lane_config_sha256"`
	ClaimBoundary        string               `json:"claim_boundary"`
}

func (contract SourcePrefixExperimentContract) Validate() error {
	if contract.SchemaVersion != SourcePrefixExperimentContractSchema || !evidenceID.MatchString(contract.ExperimentID) || contract.Repetitions == 0 || contract.Repetitions > 20 || contract.ToolDelayMS == 0 || contract.ClaimBoundary != SourcePrefixClaimBoundary {
		return errors.New("invalid source-prefix experiment contract")
	}
	if contract.Schedule.Validate() != nil || !evidenceSHA256.MatchString(contract.ExpectedResultSHA256) || !evidenceSHA256.MatchString(contract.OracleSHA256) || !evidenceSHA256.MatchString(contract.LaneConfigSHA256) {
		return errors.New("source-prefix experiment lacks frozen identities")
	}
	return nil
}

func DecodeSourcePrefixExperimentContract(raw []byte) (SourcePrefixExperimentContract, error) {
	var contract SourcePrefixExperimentContract
	if rejectDuplicateJSON(raw) != nil {
		return contract, errors.New("invalid source-prefix contract JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&contract) != nil || decoder.Decode(&struct{}{}) != io.EOF || contract.Validate() != nil {
		return SourcePrefixExperimentContract{}, errors.New("invalid source-prefix experiment contract")
	}
	return contract, nil
}

func (contract SourcePrefixExperimentContract) Identity() (string, error) {
	if err := contract.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

type SourcePrefixRuntimeIdentities struct {
	ArtifactSHA256         string
	ArtifactSourceCommit   string
	HarnessSourceCommit    string
	ExecutionProfileSHA256 string
	CapabilityPlanSHA256   string
	CapabilitySpecSHA256   string
	HandlerSHA256          string
}

func (identities SourcePrefixRuntimeIdentities) validate() error {
	if !evidenceSHA256.MatchString(identities.ArtifactSHA256) || !sourcePrefixCommitPattern.MatchString(identities.ArtifactSourceCommit) || !sourcePrefixCommitPattern.MatchString(identities.HarnessSourceCommit) || !evidenceSHA256.MatchString(identities.ExecutionProfileSHA256) || !evidenceSHA256.MatchString(identities.CapabilityPlanSHA256) || !evidenceSHA256.MatchString(identities.CapabilitySpecSHA256) || !evidenceSHA256.MatchString(identities.HandlerSHA256) {
		return errors.New("invalid source-prefix runtime identities")
	}
	return nil
}

type SourcePrefixLaneExecutor func(context.Context, uint32, uint32, SourcePrefixTreatment) (SourcePrefixRow, error)

func ExecuteSourcePrefixPairs(ctx context.Context, contract SourcePrefixExperimentContract, identities SourcePrefixRuntimeIdentities, execute SourcePrefixLaneExecutor) (SourcePrefixEvidence, error) {
	contractSHA, err := contract.Identity()
	if err != nil || identities.validate() != nil || execute == nil {
		return SourcePrefixEvidence{}, errors.New("invalid source-prefix pair execution")
	}
	rows := make([]SourcePrefixRow, 0, contract.Repetitions*2)
	baseline := make([]int64, 0, contract.Repetitions)
	streaming := make([]int64, 0, contract.Repetitions)
	for pair := uint32(0); pair < contract.Repetitions; pair++ {
		for order, treatment := range SourcePrefixTreatmentOrder(int(pair)) {
			if err := ctx.Err(); err != nil {
				return SourcePrefixEvidence{}, err
			}
			row, err := execute(ctx, pair, uint32(order), treatment)
			if err != nil {
				return SourcePrefixEvidence{}, err
			}
			if row.WallNS <= 0 {
				return SourcePrefixEvidence{}, errors.New("source-prefix lane returned a non-positive wall time")
			}
			rows = append(rows, row)
			if treatment == SourcePrefixBaseline {
				baseline = append(baseline, row.WallNS)
			} else {
				streaming = append(streaming, row.WallNS)
			}
		}
	}
	baselineMedian, streamingMedian := medianInt64(baseline), medianInt64(streaming)
	evidence := SourcePrefixEvidence{
		SchemaVersion: SourcePrefixEvidenceSchema, ExperimentSHA256: contractSHA,
		ArtifactSHA256: identities.ArtifactSHA256, ArtifactSourceCommit: identities.ArtifactSourceCommit, HarnessSourceCommit: identities.HarnessSourceCommit,
		ExecutionProfileSHA256: identities.ExecutionProfileSHA256, CapabilityPlanSHA256: identities.CapabilityPlanSHA256,
		CapabilitySpecSHA256: identities.CapabilitySpecSHA256, HandlerSHA256: identities.HandlerSHA256,
		Rows: rows, BaselineMedianNS: baselineMedian, StreamingMedianNS: streamingMedian,
		MedianSpeedupMilli: uint64(baselineMedian) * 1000 / uint64(streamingMedian), SpeedupSupported: baselineMedian > streamingMedian,
		ClaimBoundary: SourcePrefixClaimBoundary,
	}
	if err := ValidateSourcePrefixEvidence(contract, evidence); err != nil {
		return SourcePrefixEvidence{}, err
	}
	return evidence, nil
}

type SourcePrefixRow struct {
	Pair                 uint32                `json:"pair"`
	LaneOrder            uint32                `json:"lane_order"`
	Treatment            SourcePrefixTreatment `json:"treatment"`
	WallNS               int64                 `json:"wall_ns"`
	GenerationCompleteNS int64                 `json:"generation_complete_ns"`
	ToolStartedNS        int64                 `json:"tool_started_ns"`
	ToolEndedNS          int64                 `json:"tool_ended_ns"`
	RunEndedNS           int64                 `json:"run_ended_ns"`
	ResultSHA256         string                `json:"result_sha256"`
	OraclePassed         bool                  `json:"oracle_passed"`
	LogicalCalls         uint32                `json:"logical_calls"`
	PhysicalDispatches   uint32                `json:"physical_dispatches"`
	GuestStarts          uint32                `json:"guest_starts"`
	Fallback             bool                  `json:"fallback"`
	WorkspaceDisposition string                `json:"workspace_disposition"`
}

type SourcePrefixEvidence struct {
	SchemaVersion          string            `json:"schema_version"`
	ExperimentSHA256       string            `json:"experiment_sha256"`
	ArtifactSHA256         string            `json:"artifact_sha256"`
	ArtifactSourceCommit   string            `json:"artifact_source_commit"`
	HarnessSourceCommit    string            `json:"harness_source_commit"`
	ExecutionProfileSHA256 string            `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string            `json:"capability_plan_sha256"`
	CapabilitySpecSHA256   string            `json:"capability_spec_sha256"`
	HandlerSHA256          string            `json:"handler_sha256"`
	Rows                   []SourcePrefixRow `json:"rows"`
	BaselineMedianNS       int64             `json:"baseline_median_ns"`
	StreamingMedianNS      int64             `json:"streaming_median_ns"`
	MedianSpeedupMilli     uint64            `json:"median_speedup_milli"`
	SpeedupSupported       bool              `json:"speedup_supported"`
	ClaimBoundary          string            `json:"claim_boundary"`
}

func ValidateSourcePrefixEvidence(contract SourcePrefixExperimentContract, evidence SourcePrefixEvidence) error {
	contractSHA, err := contract.Identity()
	if err != nil {
		return err
	}
	if evidence.SchemaVersion != SourcePrefixEvidenceSchema || evidence.ExperimentSHA256 != contractSHA || evidence.ClaimBoundary != SourcePrefixClaimBoundary || len(evidence.Rows) != int(contract.Repetitions*2) {
		return errors.New("invalid source-prefix evidence identity or row count")
	}
	if !evidenceSHA256.MatchString(evidence.ArtifactSHA256) || !sourcePrefixCommitPattern.MatchString(evidence.ArtifactSourceCommit) || !sourcePrefixCommitPattern.MatchString(evidence.HarnessSourceCommit) || !evidenceSHA256.MatchString(evidence.ExecutionProfileSHA256) || !evidenceSHA256.MatchString(evidence.CapabilityPlanSHA256) || !evidenceSHA256.MatchString(evidence.CapabilitySpecSHA256) || !evidenceSHA256.MatchString(evidence.HandlerSHA256) {
		return errors.New("source-prefix evidence lacks runtime identities")
	}
	generationFloor := int64(contract.Schedule.Chunks[len(contract.Schedule.Chunks)-1].OffsetMS) * int64(time.Millisecond)
	seen := make(map[[2]uint32]SourcePrefixTreatment, len(evidence.Rows))
	baseline := make([]int64, 0, contract.Repetitions)
	streaming := make([]int64, 0, contract.Repetitions)
	for _, row := range evidence.Rows {
		if row.Pair >= contract.Repetitions || row.LaneOrder > 1 || row.Treatment != SourcePrefixTreatmentOrder(int(row.Pair))[row.LaneOrder] {
			return errors.New("source-prefix row has invalid pair or treatment order")
		}
		key := [2]uint32{row.Pair, row.LaneOrder}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate source-prefix lane")
		}
		seen[key] = row.Treatment
		if row.WallNS <= 0 || row.WallNS != row.RunEndedNS || row.GenerationCompleteNS < generationFloor || row.ToolStartedNS < 0 || row.ToolEndedNS <= row.ToolStartedNS || row.RunEndedNS < row.ToolEndedNS || row.RunEndedNS < row.GenerationCompleteNS {
			return errors.New("source-prefix row has an invalid timeline")
		}
		if row.ResultSHA256 != contract.ExpectedResultSHA256 || !row.OraclePassed || row.LogicalCalls != 1 || row.PhysicalDispatches != 1 || row.GuestStarts != 1 || row.Fallback || row.WorkspaceDisposition != "published" {
			return errors.New("source-prefix row failed correctness or matched-call gates")
		}
		switch row.Treatment {
		case SourcePrefixBaseline:
			if row.ToolStartedNS < row.GenerationCompleteNS {
				return errors.New("baseline dispatched before generation completed")
			}
			baseline = append(baseline, row.WallNS)
		case SourcePrefixStreaming:
			if row.ToolStartedNS >= row.GenerationCompleteNS {
				return errors.New("streaming lane did not overlap generation")
			}
			streaming = append(streaming, row.WallNS)
		default:
			return errors.New("unknown source-prefix treatment")
		}
	}
	if len(baseline) != int(contract.Repetitions) || len(streaming) != int(contract.Repetitions) {
		return errors.New("source-prefix evidence is not pair-complete")
	}
	baselineMedian := medianInt64(baseline)
	streamingMedian := medianInt64(streaming)
	if baselineMedian != evidence.BaselineMedianNS || streamingMedian != evidence.StreamingMedianNS || streamingMedian <= 0 {
		return errors.New("source-prefix summary does not match rows")
	}
	speedup := uint64(baselineMedian) * 1000 / uint64(streamingMedian)
	supported := baselineMedian > streamingMedian
	if evidence.MedianSpeedupMilli != speedup || evidence.SpeedupSupported != supported {
		return errors.New("source-prefix speedup claim does not match rows")
	}
	return nil
}

func medianInt64(values []int64) int64 {
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
}

func EncodeSourcePrefixEvidence(contract SourcePrefixExperimentContract, evidence SourcePrefixEvidence) ([]byte, error) {
	if err := ValidateSourcePrefixEvidence(contract, evidence); err != nil {
		return nil, err
	}
	return json.Marshal(evidence)
}

func DecodeSourcePrefixEvidence(raw []byte, contract SourcePrefixExperimentContract) (SourcePrefixEvidence, error) {
	var evidence SourcePrefixEvidence
	if rejectDuplicateJSON(raw) != nil {
		return evidence, errors.New("invalid source-prefix evidence JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&evidence) != nil || decoder.Decode(&struct{}{}) != io.EOF || ValidateSourcePrefixEvidence(contract, evidence) != nil {
		return SourcePrefixEvidence{}, errors.New("invalid source-prefix evidence")
	}
	return evidence, nil
}

func SourcePrefixTreatmentOrder(pair int) []SourcePrefixTreatment {
	if pair%2 == 0 {
		return []SourcePrefixTreatment{SourcePrefixBaseline, SourcePrefixStreaming}
	}
	return []SourcePrefixTreatment{SourcePrefixStreaming, SourcePrefixBaseline}
}

type TimedSourceEvent struct {
	Ordinal         uint32
	ScheduledOffset time.Duration
	Source          string
}

type WaitUntil func(context.Context, time.Duration) error

func ProduceTimedSource(ctx context.Context, schedule SourcePrefixSchedule, treatment SourcePrefixTreatment, wait WaitUntil) (<-chan TimedSourceEvent, <-chan error, error) {
	if err := schedule.Validate(); err != nil || wait == nil || (treatment != SourcePrefixBaseline && treatment != SourcePrefixStreaming) {
		return nil, nil, errors.New("invalid timed source producer configuration")
	}
	events := make(chan TimedSourceEvent, len(schedule.Chunks))
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		emit := func(index int) bool {
			chunk := schedule.Chunks[index]
			select {
			case events <- TimedSourceEvent{Ordinal: uint32(index), ScheduledOffset: time.Duration(chunk.OffsetMS) * time.Millisecond, Source: chunk.Source}:
				return true
			case <-ctx.Done():
				failures <- ctx.Err()
				return false
			}
		}
		if treatment == SourcePrefixBaseline {
			last := schedule.Chunks[len(schedule.Chunks)-1]
			if err := wait(ctx, time.Duration(last.OffsetMS)*time.Millisecond); err != nil {
				failures <- err
				return
			}
			for index := range schedule.Chunks {
				if !emit(index) {
					return
				}
			}
			return
		}
		for index, chunk := range schedule.Chunks {
			if err := wait(ctx, time.Duration(chunk.OffsetMS)*time.Millisecond); err != nil {
				failures <- err
				return
			}
			if !emit(index) {
				return
			}
		}
	}()
	return events, failures, nil
}
