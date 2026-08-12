package evaluation

import "slices"

type PlannedRow struct {
	RowID             string
	WorkloadID        string
	Treatment         Treatment
	Repetition        uint32
	Supported         bool
	UnsupportedReason string
}

type RowOutcome struct {
	RowID            string
	Status           RowStatus
	OracleStatus     OracleStatus
	EvidenceComplete bool
	EvidenceRefs     []string
	ProblemCode      string
}

func ExpandPlanRows(corpus Corpus, plan Plan) ([]PlannedRow, error) {
	if err := validateCorpus(corpus); err != nil {
		return nil, err
	}
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	_, corpusID, err := EncodeCorpus(corpus)
	if err != nil || plan.CorpusSHA256 != corpusID {
		return nil, ErrInvalid
	}
	count := uint64(len(corpus.Workloads)) * uint64(len(plan.TreatmentOrder)) * uint64(plan.Repetitions)
	if count == 0 || count > uint64(plan.Ceilings.MaxRows) || count > maxEvaluationRows {
		return nil, ErrInvalid
	}
	rows := make([]PlannedRow, 0, count)
	for _, workload := range corpus.Workloads {
		for _, treatment := range plan.TreatmentOrder {
			supported := slices.Contains(workload.Treatments, treatment)
			reason := ""
			if !supported {
				reason = unsupportedReason(treatment)
			}
			for repetition := uint32(0); repetition < plan.Repetitions; repetition++ {
				rows = append(rows, PlannedRow{
					RowID: RowIdentity(workload.ID, treatment, repetition), WorkloadID: workload.ID,
					Treatment: treatment, Repetition: repetition, Supported: supported, UnsupportedReason: reason,
				})
			}
		}
	}
	return rows, nil
}

func unsupportedReason(treatment Treatment) string {
	switch treatment {
	case TreatmentCounterfactualBranch:
		return "no_captured_capability_boundary"
	case TreatmentDeterministicVerify:
		return "mounted_workspace_or_unqualified_scope"
	default:
		return "treatment_not_admitted"
	}
}

func BuildReport(corpusSHA256, planSHA256 string, planned []PlannedRow, outcomes []RowOutcome) (Report, error) {
	if !digestPattern.MatchString(corpusSHA256) || !digestPattern.MatchString(planSHA256) || len(planned) == 0 || len(planned) != len(outcomes) || len(planned) > maxEvaluationRows {
		return Report{}, ErrInvalid
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, EvidenceClass: EvidenceMechanismOnly,
		CorpusSHA256: corpusSHA256, PlanSHA256: planSHA256,
		ProhibitedClaims: RequiredProhibitedClaims(), Rows: make([]Row, len(planned)),
	}
	seen := make(map[string]struct{}, len(planned))
	for i, item := range planned {
		outcome := outcomes[i]
		if item.RowID == "" || outcome.RowID != item.RowID {
			return Report{}, ErrInvalid
		}
		if _, exists := seen[item.RowID]; exists {
			return Report{}, ErrInvalid
		}
		seen[item.RowID] = struct{}{}
		if !item.Supported && (outcome.Status != RowUnsupported || outcome.ProblemCode != item.UnsupportedReason) {
			return Report{}, ErrInvalid
		}
		refs := make([]string, len(outcome.EvidenceRefs))
		copy(refs, outcome.EvidenceRefs)
		report.Rows[i] = Row{
			RowID: item.RowID, WorkloadID: item.WorkloadID, Treatment: item.Treatment, Repetition: item.Repetition,
			Status: outcome.Status, OracleStatus: outcome.OracleStatus, EvidenceComplete: outcome.EvidenceComplete,
			CorpusSHA256: corpusSHA256, PlanSHA256: planSHA256,
			EvidenceRefs: refs, ProblemCode: outcome.ProblemCode,
		}
		switch outcome.Status {
		case RowCompleted:
			report.Summary.Completed++
		case RowFailed:
			report.Summary.Failed++
		case RowTimedOut:
			report.Summary.TimedOut++
		case RowUnsupported:
			report.Summary.Unsupported++
		default:
			return Report{}, ErrInvalid
		}
	}
	report.Summary.Offered = uint32(len(report.Rows))
	if err := validateReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}
