// Package passpipeline provides the small Host-owned routing and outcome shell
// shared by source-bound optimization passes. It does not execute transforms.
package passpipeline

import (
	"errors"
	"regexp"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

const OutcomeRecordSchemaVersion = "pysolate.source-bound-pass-outcome.v1"

type Stage string
type Outcome string
type RejectionReason string

const (
	StagePrefixOverlay      Stage = "prefix_overlay"
	StageHybridPreparePatch Stage = "hybrid_prepare_patch"
	StageWholeProgramPatch  Stage = "whole_program_patch"
	StageMultiProgramPatch  Stage = "multi_program_patch"

	OutcomeApplied               Outcome = "applied"
	OutcomeDiscarded             Outcome = "discarded"
	OutcomePreparedAwaitingFinal Outcome = "prepared_awaiting_final"
	OutcomeRejected              Outcome = "rejected"

	RejectPassDisabled RejectionReason = "pass_disabled"
)

var (
	ErrInvalidConfig   = errors.New("invalid pass pipeline configuration")
	ErrDuplicatePass   = errors.New("duplicate pass pipeline registration")
	ErrAllOff          = errors.New("pass pipeline is all-off")
	ErrStageMismatch   = errors.New("pass pipeline stage mismatch")
	ErrInvalidOutcome  = errors.New("invalid pass pipeline outcome")
	ErrInvalidRecord   = errors.New("invalid pass pipeline record")
	ErrBindingMismatch = errors.New("pass pipeline binding mismatch")
	ErrBoundsExceeded  = errors.New("pass pipeline bounds exceeded")

	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	tokenPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

var currentStages = map[passregistration.Name]Stage{
	passregistration.SemanticPreDispatch: StagePrefixOverlay,
	passregistration.PreparedPureRegion:  StageWholeProgramPatch,
}

type Limits struct {
	MaxPasses            uint32 `json:"max_passes"`
	MaxSourceGrowthBytes uint64 `json:"max_source_growth_bytes"`
	MaxASTGrowthNodes    uint32 `json:"max_ast_growth_nodes"`
	MaxPreparationBytes  uint64 `json:"max_preparation_bytes"`
	MaxReanalyses        uint32 `json:"max_reanalyses"`
}

func DefaultLimits() Limits {
	return Limits{
		MaxPasses: 16, MaxSourceGrowthBytes: 1 << 20, MaxASTGrowthNodes: 8192,
		MaxPreparationBytes: 8 << 20, MaxReanalyses: 16,
	}
}

type Usage struct {
	OriginalSourceBytes uint64 `json:"original_source_bytes"`
	DerivedSourceBytes  uint64 `json:"derived_source_bytes"`
	OriginalASTNodes    uint32 `json:"original_ast_nodes"`
	DerivedASTNodes     uint32 `json:"derived_ast_nodes"`
	PreparationBytes    uint64 `json:"preparation_bytes"`
	Reanalyses          uint32 `json:"reanalyses"`
}

type Entry struct {
	Registration passregistration.Registration
	Stage        Stage
	Enabled      bool
}

// CurrentEntry maps a closed pass registration onto its source-bound stage
// without changing registration identity.
func CurrentEntry(registration passregistration.Registration, enabled bool) (Entry, error) {
	if registration.IdentitySHA256() == "" {
		return Entry{}, ErrInvalidConfig
	}
	stage, ok := currentStages[registration.Name()]
	if !ok || !validStageForConsumer(stage, registration.Consumer()) {
		return Entry{}, ErrInvalidConfig
	}
	return Entry{Registration: registration, Stage: stage, Enabled: enabled}, nil
}

type RecordInput struct {
	PassName             passregistration.Name
	Outcome              Outcome
	RejectionReason      RejectionReason
	OriginalSourceSHA256 string
	DerivedSourceSHA256  string
	OriginalASTSHA256    string
	DerivedASTSHA256     string
	Bindings             map[passregistration.Binding]string
	Usage                Usage
}

type OutcomeRecord struct {
	SchemaVersion        string                              `json:"schema_version"`
	PassName             passregistration.Name               `json:"pass_name"`
	RegistrationSHA256   string                              `json:"registration_sha256"`
	Stage                Stage                               `json:"stage"`
	Outcome              Outcome                             `json:"outcome"`
	RejectionReason      RejectionReason                     `json:"rejection_reason"`
	PassOrder            uint32                              `json:"pass_order"`
	OriginalSourceSHA256 string                              `json:"original_source_sha256"`
	DerivedSourceSHA256  string                              `json:"derived_source_sha256"`
	OriginalASTSHA256    string                              `json:"original_ast_sha256"`
	DerivedASTSHA256     string                              `json:"derived_ast_sha256"`
	Bindings             map[passregistration.Binding]string `json:"bindings"`
	Usage                Usage                               `json:"usage"`
}

type Pipeline struct {
	entries map[passregistration.Name]Entry
	limits  Limits
	allOff  bool
	mu      sync.Mutex
	records []OutcomeRecord
}

func New(entries []Entry, limits Limits) (*Pipeline, error) {
	maximum := DefaultLimits()
	if limits.MaxPasses > maximum.MaxPasses || limits.MaxSourceGrowthBytes > maximum.MaxSourceGrowthBytes ||
		limits.MaxASTGrowthNodes > maximum.MaxASTGrowthNodes || limits.MaxPreparationBytes > maximum.MaxPreparationBytes ||
		limits.MaxReanalyses > maximum.MaxReanalyses {
		return nil, ErrBoundsExceeded
	}
	if limits.MaxSourceGrowthBytes == 0 || limits.MaxASTGrowthNodes == 0 || limits.MaxPreparationBytes == 0 || limits.MaxReanalyses == 0 {
		return nil, ErrInvalidConfig
	}
	if uint64(len(entries)) > uint64(limits.MaxPasses) {
		return nil, ErrBoundsExceeded
	}
	pipeline := &Pipeline{entries: make(map[passregistration.Name]Entry, len(entries)), limits: limits, allOff: true, records: []OutcomeRecord{}}
	for _, entry := range entries {
		name := entry.Registration.Name()
		expectedStage, known := currentStages[name]
		if name == "" || entry.Registration.IdentitySHA256() == "" || !known || entry.Stage != expectedStage ||
			!validStageForConsumer(entry.Stage, entry.Registration.Consumer()) {
			return nil, ErrStageMismatch
		}
		if _, exists := pipeline.entries[name]; exists {
			return nil, ErrDuplicatePass
		}
		pipeline.entries[name] = entry
		pipeline.allOff = pipeline.allOff && !entry.Enabled
	}
	return pipeline, nil
}

func (pipeline *Pipeline) AllOff() bool { return pipeline == nil || pipeline.allOff }

func (pipeline *Pipeline) RecordPrefixOverlay(input RecordInput) (OutcomeRecord, error) {
	return pipeline.record(StagePrefixOverlay, input)
}

func (pipeline *Pipeline) RecordHybridPreparePatch(input RecordInput) (OutcomeRecord, error) {
	return pipeline.record(StageHybridPreparePatch, input)
}

func (pipeline *Pipeline) RecordWholeProgramPatch(input RecordInput) (OutcomeRecord, error) {
	return pipeline.record(StageWholeProgramPatch, input)
}

func (pipeline *Pipeline) RecordMultiProgramPatch(input RecordInput) (OutcomeRecord, error) {
	return pipeline.record(StageMultiProgramPatch, input)
}

func (pipeline *Pipeline) Records() []OutcomeRecord {
	if pipeline == nil {
		return []OutcomeRecord{}
	}
	pipeline.mu.Lock()
	defer pipeline.mu.Unlock()
	result := make([]OutcomeRecord, len(pipeline.records))
	for index := range pipeline.records {
		result[index] = cloneRecord(pipeline.records[index])
	}
	return result
}

func (pipeline *Pipeline) record(stage Stage, input RecordInput) (OutcomeRecord, error) {
	if pipeline == nil {
		return OutcomeRecord{}, ErrInvalidConfig
	}
	if pipeline.allOff {
		return OutcomeRecord{}, ErrAllOff
	}
	entry, exists := pipeline.entries[input.PassName]
	if !exists || entry.Stage != stage {
		return OutcomeRecord{}, ErrStageMismatch
	}
	if !entry.Enabled {
		input.Outcome = OutcomeRejected
		input.RejectionReason = RejectPassDisabled
	}
	if !validOutcome(stage, input.Outcome) {
		return OutcomeRecord{}, ErrInvalidOutcome
	}
	if err := validateInput(entry.Registration, input, pipeline.limits); err != nil {
		return OutcomeRecord{}, err
	}
	record := OutcomeRecord{
		SchemaVersion: OutcomeRecordSchemaVersion, PassName: input.PassName,
		RegistrationSHA256: entry.Registration.IdentitySHA256(), Stage: stage,
		Outcome: input.Outcome, RejectionReason: input.RejectionReason,
		OriginalSourceSHA256: input.OriginalSourceSHA256, DerivedSourceSHA256: input.DerivedSourceSHA256,
		OriginalASTSHA256: input.OriginalASTSHA256, DerivedASTSHA256: input.DerivedASTSHA256,
		Bindings: cloneBindings(input.Bindings), Usage: input.Usage,
	}
	pipeline.mu.Lock()
	defer pipeline.mu.Unlock()
	record.PassOrder = uint32(len(pipeline.records) + 1)
	pipeline.records = append(pipeline.records, record)
	return cloneRecord(record), nil
}

func validateInput(registration passregistration.Registration, input RecordInput, limits Limits) error {
	if input.PassName != registration.Name() || !digestPattern.MatchString(input.OriginalSourceSHA256) || !digestPattern.MatchString(input.OriginalASTSHA256) {
		return ErrInvalidRecord
	}
	if input.Outcome == OutcomeApplied || input.Outcome == OutcomePreparedAwaitingFinal {
		if input.RejectionReason != "" {
			return ErrInvalidRecord
		}
	} else if !reasonPattern.MatchString(string(input.RejectionReason)) {
		return ErrInvalidRecord
	}
	if registration.Consumer() == passregistration.ExecutionPatch && input.Outcome == OutcomeApplied {
		if !digestPattern.MatchString(input.DerivedSourceSHA256) || !digestPattern.MatchString(input.DerivedASTSHA256) {
			return ErrInvalidRecord
		}
	} else if (input.DerivedSourceSHA256 != "" && !digestPattern.MatchString(input.DerivedSourceSHA256)) ||
		(input.DerivedASTSHA256 != "" && !digestPattern.MatchString(input.DerivedASTSHA256)) {
		return ErrInvalidRecord
	}
	if err := validateBindings(registration, input.Bindings); err != nil {
		return err
	}
	if input.Usage.DerivedSourceBytes > input.Usage.OriginalSourceBytes &&
		input.Usage.DerivedSourceBytes-input.Usage.OriginalSourceBytes > limits.MaxSourceGrowthBytes {
		return ErrBoundsExceeded
	}
	if input.Usage.DerivedASTNodes > input.Usage.OriginalASTNodes &&
		input.Usage.DerivedASTNodes-input.Usage.OriginalASTNodes > limits.MaxASTGrowthNodes {
		return ErrBoundsExceeded
	}
	if input.Usage.PreparationBytes > limits.MaxPreparationBytes || input.Usage.Reanalyses > limits.MaxReanalyses {
		return ErrBoundsExceeded
	}
	return nil
}

func validateBindings(registration passregistration.Registration, values map[passregistration.Binding]string) error {
	required := registration.RequiredBindings()
	if len(values) != len(required) {
		return ErrBindingMismatch
	}
	for _, binding := range required {
		value, exists := values[binding]
		if !exists {
			return ErrBindingMismatch
		}
		if binding == passregistration.OccurrenceID {
			if !tokenPattern.MatchString(value) {
				return ErrBindingMismatch
			}
		} else if !digestPattern.MatchString(value) {
			return ErrBindingMismatch
		}
	}
	return nil
}

func validStageForConsumer(stage Stage, consumer passregistration.Consumer) bool {
	if consumer == passregistration.OverlayOnly {
		return stage == StagePrefixOverlay
	}
	if consumer == passregistration.ExecutionPatch {
		return stage == StageHybridPreparePatch || stage == StageWholeProgramPatch || stage == StageMultiProgramPatch
	}
	return false
}

func validOutcome(stage Stage, outcome Outcome) bool {
	switch stage {
	case StagePrefixOverlay:
		return outcome == OutcomeApplied || outcome == OutcomeDiscarded || outcome == OutcomeRejected
	case StageHybridPreparePatch:
		return outcome == OutcomeApplied || outcome == OutcomeDiscarded || outcome == OutcomePreparedAwaitingFinal || outcome == OutcomeRejected
	case StageWholeProgramPatch, StageMultiProgramPatch:
		return outcome == OutcomeApplied || outcome == OutcomeRejected
	default:
		return false
	}
}

func cloneBindings(values map[passregistration.Binding]string) map[passregistration.Binding]string {
	result := make(map[passregistration.Binding]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneRecord(record OutcomeRecord) OutcomeRecord {
	record.Bindings = cloneBindings(record.Bindings)
	return record
}
