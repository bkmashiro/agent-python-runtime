package sourceboundpasses

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"sort"
)

const PreregistrationSchemaVersion = "pysolate.source-bound-pass-preregistration.v1"

type Stage string
type Outcome string

const (
	StagePrefixOverlay      Stage = "prefix_overlay"
	StageHybridPreparePatch Stage = "hybrid_prepare_patch"
	StageWholeProgramPatch  Stage = "whole_program_patch"
	StageMultiProgramPatch  Stage = "multi_program_patch"

	OutcomeApplied               Outcome = "applied"
	OutcomeDiscarded             Outcome = "discarded"
	OutcomePreparedAwaitingFinal Outcome = "prepared_awaiting_final"
	OutcomeRejected              Outcome = "rejected"
)

var (
	ErrInvalidPreregistration = errors.New("invalid source-bound pass preregistration")
	digestPattern             = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	tokenPattern              = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Bounds struct {
	MaxPasses           uint32 `json:"max_passes"`
	MaxASTNodes         uint32 `json:"max_ast_nodes"`
	MaxSourceBytes      uint32 `json:"max_source_bytes"`
	MaxPreparationBytes uint32 `json:"max_preparation_bytes"`
	MaxReanalyses       uint32 `json:"max_reanalyses"`
}

type Case struct {
	ID                    string  `json:"id"`
	Stage                 Stage   `json:"stage"`
	ExpectedOutcome       Outcome `json:"expected_outcome"`
	RejectionReason       string  `json:"rejection_reason,omitempty"`
	ExpectedLogicalEvents uint32  `json:"expected_logical_events"`
	ExpectedPhysicalWork  uint32  `json:"expected_physical_work"`
	ExpectedWrites        uint32  `json:"expected_writes"`
}

type Preregistration struct {
	SchemaVersion  string    `json:"schema_version"`
	IdentitySHA256 string    `json:"identity_sha256"`
	Stages         []Stage   `json:"stages"`
	Outcomes       []Outcome `json:"outcomes"`
	Bounds         Bounds    `json:"bounds"`
	Cases          []Case    `json:"cases"`
}

type preregistrationIdentity struct {
	SchemaVersion string    `json:"schema_version"`
	Stages        []Stage   `json:"stages"`
	Outcomes      []Outcome `json:"outcomes"`
	Bounds        Bounds    `json:"bounds"`
	Cases         []Case    `json:"cases"`
}

func PreregistrationV1() Preregistration {
	value := Preregistration{
		SchemaVersion: PreregistrationSchemaVersion,
		Stages:        []Stage{StagePrefixOverlay, StageHybridPreparePatch, StageWholeProgramPatch, StageMultiProgramPatch},
		Outcomes:      []Outcome{OutcomeApplied, OutcomeDiscarded, OutcomePreparedAwaitingFinal, OutcomeRejected},
		Bounds: Bounds{
			MaxPasses: 16, MaxASTNodes: 8192, MaxSourceBytes: 1 << 20,
			MaxPreparationBytes: 8 << 20, MaxReanalyses: 16,
		},
		Cases: []Case{
			{ID: "branch_not_taken", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeDiscarded, RejectionReason: "call_not_reached", ExpectedPhysicalWork: 1},
			{ID: "cancellation", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeDiscarded, RejectionReason: "cancelled", ExpectedPhysicalWork: 1},
			{ID: "earlier_exception", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeDiscarded, RejectionReason: "earlier_exception", ExpectedPhysicalWork: 1},
			{ID: "external_write", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeRejected, RejectionReason: "effect_not_speculative_safe"},
			{ID: "freshness_drift", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeRejected, RejectionReason: "freshness_mismatch"},
			{ID: "invalid_final_suffix", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeDiscarded, RejectionReason: "final_source_invalid", ExpectedPhysicalWork: 1},
			{ID: "mutable_alias", Stage: StageWholeProgramPatch, ExpectedOutcome: OutcomeRejected, RejectionReason: "identity_alias"},
			{ID: "plan_drift", Stage: StageWholeProgramPatch, ExpectedOutcome: OutcomeRejected, RejectionReason: "plan_mismatch"},
			{ID: "positive_prefix_overlay", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeApplied, ExpectedLogicalEvents: 1, ExpectedPhysicalWork: 1},
			{ID: "positive_pure_scalar_patch", Stage: StageWholeProgramPatch, ExpectedOutcome: OutcomeApplied},
			{ID: "privacy_drift", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeRejected, RejectionReason: "privacy_mismatch"},
			{ID: "unsupported_syntax", Stage: StageWholeProgramPatch, ExpectedOutcome: OutcomeRejected, RejectionReason: "unsupported_syntax"},
			{ID: "workspace_drift", Stage: StagePrefixOverlay, ExpectedOutcome: OutcomeRejected, RejectionReason: "workspace_mismatch"},
			{ID: "zero_iteration", Stage: StageWholeProgramPatch, ExpectedOutcome: OutcomeRejected, RejectionReason: "zero_iteration_exception_motion"},
		},
	}
	value.IdentitySHA256 = identitySHA256(value)
	return value
}

func (value Preregistration) Validate() error {
	if value.SchemaVersion != PreregistrationSchemaVersion || !digestPattern.MatchString(value.IdentitySHA256) ||
		!reflect.DeepEqual(value.Stages, []Stage{StagePrefixOverlay, StageHybridPreparePatch, StageWholeProgramPatch, StageMultiProgramPatch}) ||
		!reflect.DeepEqual(value.Outcomes, []Outcome{OutcomeApplied, OutcomeDiscarded, OutcomePreparedAwaitingFinal, OutcomeRejected}) ||
		value.Bounds != (Bounds{MaxPasses: 16, MaxASTNodes: 8192, MaxSourceBytes: 1 << 20, MaxPreparationBytes: 8 << 20, MaxReanalyses: 16}) ||
		len(value.Cases) == 0 || len(value.Cases) > 64 || identitySHA256(value) != value.IdentitySHA256 {
		return ErrInvalidPreregistration
	}
	if !sort.SliceIsSorted(value.Cases, func(i, j int) bool { return value.Cases[i].ID < value.Cases[j].ID }) {
		return ErrInvalidPreregistration
	}
	seen := make(map[string]struct{}, len(value.Cases))
	for _, row := range value.Cases {
		if !tokenPattern.MatchString(row.ID) || !validStage(row.Stage) || !validOutcome(row.ExpectedOutcome) || row.ExpectedLogicalEvents > 1 || row.ExpectedPhysicalWork > 1 || row.ExpectedWrites != 0 {
			return ErrInvalidPreregistration
		}
		if _, exists := seen[row.ID]; exists {
			return ErrInvalidPreregistration
		}
		seen[row.ID] = struct{}{}
		if row.ExpectedOutcome == OutcomeApplied {
			if row.RejectionReason != "" {
				return ErrInvalidPreregistration
			}
		} else if !tokenPattern.MatchString(row.RejectionReason) {
			return ErrInvalidPreregistration
		}
	}
	return nil
}

func EncodePreregistration(value Preregistration) ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func DecodePreregistration(raw []byte) (Preregistration, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Preregistration
	if err := decoder.Decode(&value); err != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	if err := ensureEOF(decoder); err != nil {
		return Preregistration{}, err
	}
	if err := value.Validate(); err != nil {
		return Preregistration{}, err
	}
	return value, nil
}

func identitySHA256(value Preregistration) string {
	identity := preregistrationIdentity{SchemaVersion: value.SchemaVersion, Stages: value.Stages, Outcomes: value.Outcomes, Bounds: value.Bounds, Cases: value.Cases}
	raw, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("pysolate.source-bound-pass-preregistration.identity.v1\x00"), raw...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validStage(value Stage) bool {
	switch value {
	case StagePrefixOverlay, StageHybridPreparePatch, StageWholeProgramPatch, StageMultiProgramPatch:
		return true
	default:
		return false
	}
}

func validOutcome(value Outcome) bool {
	switch value {
	case OutcomeApplied, OutcomeDiscarded, OutcomePreparedAwaitingFinal, OutcomeRejected:
		return true
	default:
		return false
	}
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidPreregistration
	}
	return nil
}
