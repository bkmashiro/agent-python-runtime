package evaluation

import "math"

const MeasurementSchemaVersion = "pysolate.evaluation-measurements.v1"

type MeasurementSummary struct {
	SchemaVersion    string        `json:"schema_version"`
	EvidenceClass    EvidenceClass `json:"evidence_class"`
	CorpusSHA256     string        `json:"corpus_sha256"`
	PlanSHA256       string        `json:"plan_sha256"`
	ProhibitedClaims []string      `json:"prohibited_claims"`
	Offered          uint32        `json:"offered"`
	Started          uint32        `json:"started"`
	Completed        uint32        `json:"completed"`
	Failed           uint32        `json:"failed"`
	TimedOut         uint32        `json:"timed_out"`
	Unsupported      uint32        `json:"unsupported"`
	OraclePassed     uint32        `json:"oracle_passed"`
	EvidenceComplete uint32        `json:"evidence_complete"`
	ReplayChecked    uint32        `json:"replay_checked"`
	ReplayEquivalent uint32        `json:"replay_equivalent"`
	BranchChecked    uint32        `json:"branch_checked"`
	BranchDiverged   uint32        `json:"branch_diverged"`
	ObjectPuts       uint64        `json:"object_puts"`
	ReusedPuts       uint64        `json:"reused_puts"`
	LogicalBytes     uint64        `json:"logical_bytes"`
	StoredBytes      uint64        `json:"stored_bytes"`
}

func EncodeMeasurementSummary(summary MeasurementSummary) ([]byte, string, error) {
	return encodeCanonical(summary, validateMeasurementSummary)
}

func DecodeMeasurementSummary(data []byte) (MeasurementSummary, string, error) {
	return decodeStrict(data, validateMeasurementSummary)
}

func DeriveMeasurementSummary(study RawStudy) (MeasurementSummary, []byte, string, error) {
	if err := study.Validate(); err != nil {
		return MeasurementSummary{}, nil, "", err
	}
	summary := MeasurementSummary{SchemaVersion: MeasurementSchemaVersion, EvidenceClass: EvidenceMechanismOnly, CorpusSHA256: study.CorpusSHA256, PlanSHA256: study.PlanSHA256, ProhibitedClaims: RequiredProhibitedClaims(), Offered: uint32(len(study.Rows))}
	for _, row := range study.Rows {
		if row.Started {
			summary.Started++
		}
		switch row.Status {
		case RowCompleted:
			summary.Completed++
		case RowFailed:
			summary.Failed++
		case RowTimedOut:
			summary.TimedOut++
		case RowUnsupported:
			summary.Unsupported++
		}
		if row.OracleStatus == OraclePassed {
			summary.OraclePassed++
		}
		if row.EvidenceComplete {
			summary.EvidenceComplete++
		}
		if row.Metrics.ReplayChecked {
			summary.ReplayChecked++
		}
		if row.Metrics.ReplayEquivalent {
			summary.ReplayEquivalent++
		}
		if row.Metrics.BranchChecked {
			summary.BranchChecked++
		}
		if row.Metrics.BranchDiverged {
			summary.BranchDiverged++
		}
		if math.MaxUint64-summary.ObjectPuts < uint64(row.Metrics.ObjectCount) || math.MaxUint64-summary.ReusedPuts < uint64(row.Metrics.ReusedObjectCount) || math.MaxUint64-summary.LogicalBytes < row.Metrics.LogicalBytes || math.MaxUint64-summary.StoredBytes < row.Metrics.StoredBytes {
			return MeasurementSummary{}, nil, "", ErrInvalid
		}
		summary.ObjectPuts += uint64(row.Metrics.ObjectCount)
		summary.ReusedPuts += uint64(row.Metrics.ReusedObjectCount)
		summary.LogicalBytes += row.Metrics.LogicalBytes
		summary.StoredBytes += row.Metrics.StoredBytes
	}
	encoded, identity, err := EncodeMeasurementSummary(summary)
	if err != nil {
		return MeasurementSummary{}, nil, "", err
	}
	return summary, encoded, identity, nil
}

func validateMeasurementSummary(summary MeasurementSummary) error {
	if summary.SchemaVersion != MeasurementSchemaVersion || summary.EvidenceClass != EvidenceMechanismOnly || !digestPattern.MatchString(summary.CorpusSHA256) || !digestPattern.MatchString(summary.PlanSHA256) || !equalStrings(summary.ProhibitedClaims, RequiredProhibitedClaims()) || summary.Offered == 0 || summary.Offered > maxEvaluationRows ||
		uint64(summary.Offered) != uint64(summary.Completed)+uint64(summary.Failed)+uint64(summary.TimedOut)+uint64(summary.Unsupported) ||
		uint64(summary.Started) != uint64(summary.Completed)+uint64(summary.Failed)+uint64(summary.TimedOut) || summary.OraclePassed > summary.Started || summary.EvidenceComplete > summary.Started || summary.ReplayEquivalent > summary.ReplayChecked || summary.BranchDiverged > summary.BranchChecked || summary.ReusedPuts > summary.ObjectPuts {
		return ErrInvalid
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
