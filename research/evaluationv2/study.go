package evaluationv2

import (
	"math"
	"reflect"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

const (
	StudySchemaVersion           = "pysolate.evaluation-study.v2"
	SummarySchemaVersion         = "pysolate.evaluation-summary.v2"
	ExpandedStudySchemaVersion   = "pysolate.evaluation-study.v2.1"
	ExpandedSummarySchemaVersion = "pysolate.evaluation-summary.v2.1"
)

type Status string

type OracleStatus string

const (
	StatusCompleted Status       = "completed"
	StatusFailed    Status       = "failed"
	StatusTimedOut  Status       = "timed_out"
	OraclePassed    OracleStatus = "passed"
	OracleFailed    OracleStatus = "failed"
	OracleNotRun    OracleStatus = "not_run"
)

type PilotMetrics struct {
	ControllerBoundaries    uint32 `json:"controller_boundaries"`
	ControllerRequestBytes  uint64 `json:"controller_request_bytes"`
	ControllerResponseBytes uint64 `json:"controller_response_bytes"`
	CapabilityCalls         uint32 `json:"capability_calls"`
	CapabilityArgumentBytes uint64 `json:"capability_argument_bytes"`
	CapabilityResultBytes   uint64 `json:"capability_result_bytes"`
	Receipts                uint32 `json:"receipts"`
	TranscriptEntries       uint32 `json:"transcript_entries"`
}

type PilotRow struct {
	RowID                string       `json:"row_id"`
	WorkloadID           string       `json:"workload_id"`
	Condition            Condition    `json:"condition"`
	Repetition           uint32       `json:"repetition"`
	Status               Status       `json:"status"`
	OracleStatus         OracleStatus `json:"oracle_status"`
	EvidenceComplete     bool         `json:"evidence_complete"`
	CapabilityPlanSHA256 string       `json:"capability_plan_sha256"`
	Metrics              PilotMetrics `json:"metrics"`
	ProblemCode          string       `json:"problem_code,omitempty"`
}

type PilotStudy struct {
	SchemaVersion    string     `json:"schema_version"`
	EvidenceClass    string     `json:"evidence_class"`
	CorpusSHA256     string     `json:"corpus_sha256"`
	PlanSHA256       string     `json:"plan_sha256"`
	ProhibitedClaims []string   `json:"prohibited_claims"`
	Rows             []PilotRow `json:"rows"`
}

type Summary struct {
	SchemaVersion                 string   `json:"schema_version"`
	EvidenceClass                 string   `json:"evidence_class"`
	CorpusSHA256                  string   `json:"corpus_sha256"`
	PlanSHA256                    string   `json:"plan_sha256"`
	StudySHA256                   string   `json:"study_sha256"`
	ProhibitedClaims              []string `json:"prohibited_claims"`
	Offered                       uint32   `json:"offered"`
	Completed                     uint32   `json:"completed"`
	Failed                        uint32   `json:"failed"`
	TimedOut                      uint32   `json:"timed_out"`
	OraclePassed                  uint32   `json:"oracle_passed"`
	EvidenceComplete              uint32   `json:"evidence_complete"`
	DirectRows                    uint32   `json:"direct_rows"`
	GuestRows                     uint32   `json:"guest_rows"`
	DirectControllerBoundaries    uint64   `json:"direct_controller_boundaries"`
	GuestControllerBoundaries     uint64   `json:"guest_controller_boundaries"`
	DirectControllerRequestBytes  uint64   `json:"direct_controller_request_bytes"`
	GuestControllerRequestBytes   uint64   `json:"guest_controller_request_bytes"`
	DirectControllerResponseBytes uint64   `json:"direct_controller_response_bytes"`
	GuestControllerResponseBytes  uint64   `json:"guest_controller_response_bytes"`
	DirectCapabilityCalls         uint64   `json:"direct_capability_calls"`
	GuestCapabilityCalls          uint64   `json:"guest_capability_calls"`
	DirectCapabilityArgumentBytes uint64   `json:"direct_capability_argument_bytes"`
	GuestCapabilityArgumentBytes  uint64   `json:"guest_capability_argument_bytes"`
	DirectCapabilityResultBytes   uint64   `json:"direct_capability_result_bytes"`
	GuestCapabilityResultBytes    uint64   `json:"guest_capability_result_bytes"`
}

func RequiredProhibitedClaims() []string { return evaluation.RequiredProhibitedClaims() }

func definitionsForStudySchema(schema string) ([]Definition, string, error) {
	switch schema {
	case StudySchemaVersion:
		definitions, err := PilotDefinitions()
		return definitions, SummarySchemaVersion, err
	case ExpandedStudySchemaVersion:
		definitions, err := ExpandedDefinitions()
		return definitions, ExpandedSummarySchemaVersion, err
	default:
		return nil, "", ErrInvalid
	}
}

func EncodeStudy(value PilotStudy) ([]byte, string, error) {
	return encodeCanonical(value, validateStudy)
}
func DecodeStudy(data []byte) (PilotStudy, string, error) { return decodeStrict(data, validateStudy) }

func ValidateStudyAgainst(study PilotStudy, corpus Corpus, plan Plan) error {
	if err := validateStudy(study); err != nil {
		return err
	}
	_, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		return err
	}
	_, planID, err := EncodePlan(plan)
	if err != nil || plan.CorpusSHA256 != corpusID || study.CorpusSHA256 != corpusID || study.PlanSHA256 != planID {
		return ErrInvalid
	}
	planned, err := ExpandRows(corpus, plan)
	if err != nil || len(planned) != len(study.Rows) {
		return ErrInvalid
	}
	for i := range planned {
		if planned[i].RowID != study.Rows[i].RowID || planned[i].WorkloadID != study.Rows[i].WorkloadID || planned[i].Condition != study.Rows[i].Condition || planned[i].Repetition != study.Rows[i].Repetition {
			return ErrInvalid
		}
	}
	return nil
}

func EncodeSummary(value Summary) ([]byte, string, error) {
	return encodeCanonical(value, validateSummary)
}
func DecodeSummary(data []byte) (Summary, string, error) { return decodeStrict(data, validateSummary) }

func ValidateSummaryAgainst(summary Summary, study PilotStudy) error {
	derived, _, _, err := DeriveSummary(study)
	if err != nil || !reflect.DeepEqual(summary, derived) {
		return ErrInvalid
	}
	return nil
}

func validateStudy(study PilotStudy) error {
	definitions, summaryVersion, err := definitionsForStudySchema(study.SchemaVersion)
	if err != nil || study.EvidenceClass != EvidenceClass || !digestPattern.MatchString(study.CorpusSHA256) || !digestPattern.MatchString(study.PlanSHA256) || !sameClaims(study.ProhibitedClaims) || len(study.Rows) != len(definitions)*2 || summaryVersion == "" {
		return ErrInvalid
	}
	seen := map[string]struct{}{}
	for index, row := range study.Rows {
		definition := definitions[index/2]
		condition := ConditionDirect
		if index%2 == 1 {
			condition = ConditionGuest
		}
		if row.WorkloadID != definition.Workload.ID || row.Condition != condition || row.RowID != RowIdentity(row.WorkloadID, row.Condition, row.Repetition) || row.Repetition != 0 || !identifierPattern.MatchString(row.WorkloadID) || !digestPattern.MatchString(row.CapabilityPlanSHA256) {
			return ErrInvalid
		}
		if _, exists := seen[row.RowID]; exists {
			return ErrInvalid
		}
		seen[row.RowID] = struct{}{}
		if row.Status == StatusCompleted && row.Metrics.CapabilityCalls != definition.Workload.ExpectedCapabilityCalls {
			return ErrInvalid
		}
		switch row.Status {
		case StatusCompleted:
			if row.Metrics.CapabilityCalls == 0 || row.Metrics.CapabilityCalls != row.Metrics.Receipts || row.Metrics.CapabilityCalls != row.Metrics.TranscriptEntries || row.Metrics.ControllerBoundaries == 0 || row.Metrics.ControllerRequestBytes == 0 || row.Metrics.ControllerResponseBytes == 0 || row.Metrics.CapabilityArgumentBytes == 0 || row.Metrics.CapabilityResultBytes == 0 || row.Condition == ConditionDirect && row.Metrics.ControllerBoundaries != row.Metrics.CapabilityCalls || row.Condition == ConditionGuest && row.Metrics.ControllerBoundaries != 1 || row.OracleStatus != OraclePassed || !row.EvidenceComplete || row.ProblemCode != "" {
				return ErrInvalid
			}
		case StatusFailed:
			if row.Metrics != (PilotMetrics{}) || row.OracleStatus == OracleNotRun || row.EvidenceComplete || row.ProblemCode == "" {
				return ErrInvalid
			}
		case StatusTimedOut:
			if row.Metrics != (PilotMetrics{}) || row.OracleStatus != OracleNotRun || row.EvidenceComplete || row.ProblemCode == "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	for i := 0; i < len(study.Rows); i += 2 {
		if study.Rows[i].CapabilityPlanSHA256 != study.Rows[i+1].CapabilityPlanSHA256 {
			return ErrInvalid
		}
	}
	return nil
}

func DeriveSummary(study PilotStudy) (Summary, []byte, string, error) {
	if validateStudy(study) != nil {
		return Summary{}, nil, "", ErrInvalid
	}
	_, studyID, err := EncodeStudy(study)
	if err != nil {
		return Summary{}, nil, "", err
	}
	_, summaryVersion, err := definitionsForStudySchema(study.SchemaVersion)
	if err != nil {
		return Summary{}, nil, "", ErrInvalid
	}
	summary := Summary{SchemaVersion: summaryVersion, EvidenceClass: EvidenceClass, CorpusSHA256: study.CorpusSHA256, PlanSHA256: study.PlanSHA256, StudySHA256: studyID, ProhibitedClaims: RequiredProhibitedClaims(), Offered: uint32(len(study.Rows))}
	for _, row := range study.Rows {
		switch row.Status {
		case StatusCompleted:
			summary.Completed++
		case StatusFailed:
			summary.Failed++
		case StatusTimedOut:
			summary.TimedOut++
		}
		if row.OracleStatus == OraclePassed {
			summary.OraclePassed++
		}
		if row.EvidenceComplete {
			summary.EvidenceComplete++
		}
		if row.Condition == ConditionDirect {
			summary.DirectRows++
			for target, value := range map[*uint64]uint64{
				&summary.DirectControllerBoundaries:    uint64(row.Metrics.ControllerBoundaries),
				&summary.DirectControllerRequestBytes:  row.Metrics.ControllerRequestBytes,
				&summary.DirectControllerResponseBytes: row.Metrics.ControllerResponseBytes,
				&summary.DirectCapabilityCalls:         uint64(row.Metrics.CapabilityCalls),
				&summary.DirectCapabilityArgumentBytes: row.Metrics.CapabilityArgumentBytes,
				&summary.DirectCapabilityResultBytes:   row.Metrics.CapabilityResultBytes,
			} {
				if addOverflow(target, value) {
					return Summary{}, nil, "", ErrInvalid
				}
			}
		} else {
			summary.GuestRows++
			for target, value := range map[*uint64]uint64{
				&summary.GuestControllerBoundaries:    uint64(row.Metrics.ControllerBoundaries),
				&summary.GuestControllerRequestBytes:  row.Metrics.ControllerRequestBytes,
				&summary.GuestControllerResponseBytes: row.Metrics.ControllerResponseBytes,
				&summary.GuestCapabilityCalls:         uint64(row.Metrics.CapabilityCalls),
				&summary.GuestCapabilityArgumentBytes: row.Metrics.CapabilityArgumentBytes,
				&summary.GuestCapabilityResultBytes:   row.Metrics.CapabilityResultBytes,
			} {
				if addOverflow(target, value) {
					return Summary{}, nil, "", ErrInvalid
				}
			}
		}
	}
	encoded, identity, err := EncodeSummary(summary)
	if err != nil {
		return Summary{}, nil, "", err
	}
	return summary, encoded, identity, nil
}

func validateSummary(summary Summary) error {
	wantRows := uint32(4)
	if summary.SchemaVersion == ExpandedSummarySchemaVersion {
		wantRows = 10
	} else if summary.SchemaVersion != SummarySchemaVersion {
		return ErrInvalid
	}
	if summary.EvidenceClass != EvidenceClass || !digestPattern.MatchString(summary.CorpusSHA256) || !digestPattern.MatchString(summary.PlanSHA256) || !digestPattern.MatchString(summary.StudySHA256) || !sameClaims(summary.ProhibitedClaims) || summary.Offered != wantRows || summary.Completed > summary.Offered || summary.Failed > summary.Offered || summary.TimedOut > summary.Offered || summary.Offered != summary.Completed+summary.Failed+summary.TimedOut || summary.OraclePassed > summary.Completed || summary.EvidenceComplete > summary.Completed || summary.DirectRows != wantRows/2 || summary.GuestRows != wantRows/2 {
		return ErrInvalid
	}
	if summary.Completed > 0 && (summary.DirectControllerBoundaries+summary.GuestControllerBoundaries == 0 || summary.DirectControllerRequestBytes+summary.GuestControllerRequestBytes == 0 || summary.DirectControllerResponseBytes+summary.GuestControllerResponseBytes == 0 || summary.DirectCapabilityCalls+summary.GuestCapabilityCalls == 0 || summary.DirectCapabilityArgumentBytes+summary.GuestCapabilityArgumentBytes == 0 || summary.DirectCapabilityResultBytes+summary.GuestCapabilityResultBytes == 0) {
		return ErrInvalid
	}
	return nil
}

func sameClaims(claims []string) bool {
	want := RequiredProhibitedClaims()
	if len(claims) != len(want) {
		return false
	}
	for i := range want {
		if claims[i] != want[i] {
			return false
		}
	}
	return true
}

func addOverflow(target *uint64, value uint64) bool {
	if math.MaxUint64-*target < value {
		return true
	}
	*target += value
	return false
}
