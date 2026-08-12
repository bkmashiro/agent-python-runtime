package evaluation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

const RawSchemaVersion = "pysolate.evaluation-raw.v1"

// PhaseMillis is diagnostic local wall time split by lifecycle phase. Setup is
// deliberately separate from execution and is not a universal performance claim.
type PhaseMillis struct {
	Setup     uint64 `json:"setup"`
	Execution uint64 `json:"execution"`
	Oracle    uint64 `json:"oracle"`
	Evidence  uint64 `json:"evidence"`
}

type RowMetrics struct {
	ReplayEquivalent  bool   `json:"replay_equivalent"`
	LogicalBytes      uint64 `json:"logical_bytes"`
	StoredBytes       uint64 `json:"stored_bytes"`
	ObjectCount       uint32 `json:"object_count"`
	ReusedObjectCount uint32 `json:"reused_object_count"`
}

type RawRow struct {
	RowID            string       `json:"row_id"`
	WorkloadID       string       `json:"workload_id"`
	Treatment        Treatment    `json:"treatment"`
	Repetition       uint32       `json:"repetition"`
	Started          bool         `json:"started"`
	Status           RowStatus    `json:"status"`
	OracleStatus     OracleStatus `json:"oracle_status"`
	EvidenceComplete bool         `json:"evidence_complete"`
	ProblemCode      string       `json:"problem_code,omitempty"`
	PhaseMillis      PhaseMillis  `json:"phase_millis"`
	Metrics          RowMetrics   `json:"metrics"`
}

type RawStudy struct {
	SchemaVersion string   `json:"schema_version"`
	CorpusSHA256  string   `json:"corpus_sha256"`
	PlanSHA256    string   `json:"plan_sha256"`
	Rows          []RawRow `json:"rows"`
}

type rowEvidence struct {
	SchemaVersion    string       `json:"schema_version"`
	RowID            string       `json:"row_id"`
	WorkloadID       string       `json:"workload_id"`
	Treatment        Treatment    `json:"treatment"`
	Repetition       uint32       `json:"repetition"`
	Status           RowStatus    `json:"status"`
	OracleStatus     OracleStatus `json:"oracle_status"`
	EvidenceComplete bool         `json:"evidence_complete"`
	CorpusSHA256     string       `json:"corpus_sha256"`
	PlanSHA256       string       `json:"plan_sha256"`
	PhaseMillis      PhaseMillis  `json:"phase_millis"`
	Metrics          RowMetrics   `json:"metrics"`
	ProblemCode      string       `json:"problem_code,omitempty"`
}

func (study RawStudy) Validate() error {
	if study.SchemaVersion != RawSchemaVersion || !digestPattern.MatchString(study.CorpusSHA256) || !digestPattern.MatchString(study.PlanSHA256) || study.Rows == nil || len(study.Rows) == 0 || len(study.Rows) > maxEvaluationRows {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(study.Rows))
	for _, row := range study.Rows {
		if err := validateRawRow(row); err != nil {
			return err
		}
		if _, exists := seen[row.RowID]; exists {
			return ErrInvalid
		}
		seen[row.RowID] = struct{}{}
	}
	return nil
}

func validateRawRow(row RawRow) error {
	if row.RowID != RowIdentity(row.WorkloadID, row.Treatment, row.Repetition) || !identifierPattern.MatchString(row.WorkloadID) || !validTreatment(row.Treatment) || !validRowStatus(row.Status) || !validOracleStatus(row.OracleStatus) || row.Metrics.StoredBytes > row.Metrics.LogicalBytes || row.Metrics.ReusedObjectCount > row.Metrics.ObjectCount {
		return ErrInvalid
	}
	switch row.Status {
	case RowCompleted:
		if !row.Started || row.OracleStatus != OraclePassed || !row.EvidenceComplete || row.ProblemCode != "" {
			return ErrInvalid
		}
	case RowFailed:
		if !row.Started || row.OracleStatus == OracleNotRun || row.ProblemCode == "" {
			return ErrInvalid
		}
	case RowTimedOut:
		if !row.Started || row.OracleStatus != OracleNotRun || row.ProblemCode == "" {
			return ErrInvalid
		}
	case RowUnsupported:
		if row.Started || row.OracleStatus != OracleNotRun || row.EvidenceComplete || row.ProblemCode == "" || row.PhaseMillis != (PhaseMillis{}) || row.Metrics != (RowMetrics{}) {
			return ErrInvalid
		}
	}
	return nil
}

func (row RawRow) BodyFreeEvidence(corpusSHA256, planSHA256 string) ([]byte, string, error) {
	if err := validateRawRow(row); err != nil || !digestPattern.MatchString(corpusSHA256) || !digestPattern.MatchString(planSHA256) {
		return nil, "", ErrInvalid
	}
	evidence := rowEvidence{
		SchemaVersion: "pysolate.evaluation-row-evidence.v1", RowID: row.RowID, WorkloadID: row.WorkloadID,
		Treatment: row.Treatment, Repetition: row.Repetition, Status: row.Status, OracleStatus: row.OracleStatus,
		EvidenceComplete: row.EvidenceComplete, CorpusSHA256: corpusSHA256, PlanSHA256: planSHA256,
		PhaseMillis: row.PhaseMillis, Metrics: row.Metrics, ProblemCode: row.ProblemCode,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil, "", ErrInvalid
	}
	encoded = append(encoded, '\n')
	digest := sha256.Sum256(encoded)
	return encoded, fmt.Sprintf("sha256:%x", digest[:]), nil
}

func RebuildReport(study RawStudy, planned []PlannedRow) (Report, []string, error) {
	if err := study.Validate(); err != nil || len(study.Rows) != len(planned) {
		return Report{}, nil, ErrInvalid
	}
	outcomes := make([]RowOutcome, len(planned))
	refs := make([]string, len(planned))
	for i, row := range study.Rows {
		if row.RowID != planned[i].RowID {
			return Report{}, nil, ErrInvalid
		}
		outcome := RowOutcome{RowID: row.RowID, Status: row.Status, OracleStatus: row.OracleStatus, EvidenceComplete: row.EvidenceComplete, ProblemCode: row.ProblemCode, EvidenceRefs: []string{}}
		if row.Status != RowUnsupported {
			_, identity, err := row.BodyFreeEvidence(study.CorpusSHA256, study.PlanSHA256)
			if err != nil {
				return Report{}, nil, err
			}
			refs[i] = identity
			outcome.EvidenceRefs = []string{identity}
		}
		outcomes[i] = outcome
	}
	report, err := BuildReport(study.CorpusSHA256, study.PlanSHA256, planned, outcomes)
	if err != nil {
		return Report{}, nil, err
	}
	return report, slices.Clone(refs), nil
}
