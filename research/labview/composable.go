package labview

import (
	"fmt"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

// ComposableProjection is the bounded Lab surface for a completed composable
// acceptance report. It deliberately excludes scenario tasks, child analyses,
// expected artifact bodies, workspace paths, and raw logs.
type ComposableProjection struct {
	Study StudySummary
	Runs  []RunDetail
}

func ProjectComposableAcceptance(report composableacceptance.Report, reportSHA256 string) (ComposableProjection, error) {
	if report.Validate() != nil || !digestRE.MatchString(reportSHA256) {
		return ComposableProjection{}, ErrInvalid
	}
	projection := ComposableProjection{Runs: make([]RunDetail, 0, len(report.Rows))}
	statusCount := map[string]uint32{"completed": 0, "failed": 0, "timed_out": 0, "unsupported": 0}
	workloads := map[string]struct{}{}
	treatments := map[composableacceptance.Treatment]struct{}{}
	for _, row := range report.Rows {
		status, oracle := "completed", "passed"
		switch row.Status {
		case "passed", "rejected":
		case "skipped":
			status, oracle = "unsupported", "not_run"
		default:
			return ComposableProjection{}, ErrInvalid
		}
		statusCount[status]++
		workloads[row.ScenarioID] = struct{}{}
		treatments[row.Treatment] = struct{}{}
		workspaceSHA := row.SelectedRootSHA256
		if workspaceSHA == "" {
			workspaceSHA = composableacceptance.ArtifactIdentity(row.ScenarioSHA256 + "/" + string(row.Treatment) + "/workspace-unavailable")
		}
		refs := []Ref{
			{Kind: "artifact", SHA256: report.GuestArtifactSHA256, Privacy: PrivacyPortable, Availability: AvailabilityAvailable},
			{Kind: "capability_plan", SHA256: row.ConformanceSHA256, Privacy: PrivacyPortable, Availability: AvailabilityAvailable},
			{Kind: "execution", SHA256: reportSHA256, Privacy: PrivacyPortable, Availability: AvailabilityAvailable},
			{Kind: "execution_profile", SHA256: row.ConformanceSHA256, Privacy: PrivacyPortable, Availability: AvailabilityAvailable},
			{Kind: "invocation", SHA256: row.ScenarioSHA256, Privacy: PrivacyPortable, Availability: AvailabilityAvailable},
			{Kind: "result", SHA256: row.OracleSHA256, Privacy: PrivacyPrivate, Availability: AvailabilityUnavailable},
			{Kind: "workspace_tree", SHA256: workspaceSHA, Privacy: PrivacyPrivate, Availability: AvailabilityUnavailable},
		}
		run := RunDetail{
			Header:     header(KindRunDetail, reportSHA256),
			RunID:      fmt.Sprintf("run-%s-%s", row.ScenarioID, row.Treatment),
			WorkloadID: row.ScenarioID, Treatment: string(row.Treatment), Status: status,
			OracleStatus: oracle, EvidenceClass: "qualified_workload", EvidenceCompleteness: Complete,
			Refs: refs, ProblemCodes: []string{},
		}
		if validateRun(run) != nil {
			return ComposableProjection{}, ErrInvalid
		}
		projection.Runs = append(projection.Runs, run)
	}
	projection.Study = StudySummary{
		Header: header(KindStudySummary, reportSHA256), StudyID: "study-spark-composable-runtime",
		EvidenceClass: "qualified_workload", WorkloadCount: uint32(len(workloads)), TreatmentCount: uint32(len(treatments)),
		StatusTotals:     []StatusTotal{{Status: "completed", Count: statusCount["completed"]}, {Status: "failed", Count: statusCount["failed"]}, {Status: "timed_out", Count: statusCount["timed_out"]}, {Status: "unsupported", Count: statusCount["unsupported"]}},
		ProhibitedClaims: evaluation.RequiredProhibitedClaims(),
		Storage:          StorageSummary{LogicalBytes: uint64(len(report.Rows)) * 7 * 64, StoredBytes: uint64(len(report.Rows)) * 7 * 64, ObjectCount: uint32(len(report.Rows) * 7)},
	}
	if validateStudy(projection.Study) != nil {
		return ComposableProjection{}, ErrInvalid
	}
	return projection, nil
}
