// Package evaluationlab projects strict evaluation reports into read-only Lab v1 sets.
package evaluationlab

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

// Project selects one report row and emits a complete, body-free Lab v1 set.
// Relations absent from the report are represented by distinct unavailable-marker identities.
func Project(reportBytes []byte, rowID string) (labview.Set, error) {
	report, source, err := evaluation.DecodeReport(reportBytes)
	if err != nil || rowID == "" {
		return labview.Set{}, labview.ErrInvalid
	}
	selected := -1
	for i := range report.Rows {
		if report.Rows[i].RowID == rowID {
			selected = i
			break
		}
	}
	if selected < 0 || len(report.Rows) < 2 {
		return labview.Set{}, labview.ErrInvalid
	}
	row := report.Rows[selected]
	other := report.Rows[0]
	if other.RowID == row.RowID {
		other = report.Rows[1]
	}
	header := func(kind labview.Kind) labview.Header {
		return labview.Header{SchemaVersion: "pysolate." + string(kind), SourceSHA256: source, GeneratedAtPolicy: "omitted"}
	}
	page := func(n int) labview.Page { return labview.Page{Returned: uint32(n), Total: uint32(n)} }
	kinds := []string{"artifact", "capability_plan", "execution", "execution_profile", "invocation", "result", "workspace_tree"}
	refs := make([]labview.Ref, len(kinds))
	for i, kind := range kinds {
		refs[i] = labview.Ref{Kind: kind, SHA256: marker(source, row, kind), Privacy: labview.PrivacyPrivate, Availability: labview.AvailabilityUnavailable}
	}
	completeness := labview.Incomplete
	problemCodes := []string{"evidence_incomplete"}
	run := labview.RunDetail{Header: header(labview.KindRunDetail), RunID: row.RowID, WorkloadID: row.WorkloadID, Treatment: string(row.Treatment), Status: string(row.Status), OracleStatus: string(row.OracleStatus), EvidenceClass: string(report.EvidenceClass), EvidenceCompleteness: completeness, Refs: refs, ProblemCodes: problemCodes}
	timeline := labview.TimelinePage{Header: header(labview.KindTimelinePage), RunID: row.RowID, EvidenceCompleteness: completeness, Events: []labview.TimelineEvent{}, Page: page(0)}
	nodes := []labview.DAGNode{{RunID: other.RowID, Status: string(other.Status), EvidenceCompleteness: labview.Incomplete}, {RunID: row.RowID, Status: string(row.Status), EvidenceCompleteness: completeness}}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].RunID < nodes[j].RunID })
	dag := labview.BranchDAG{Header: header(labview.KindBranchDAG), Nodes: nodes, Edges: []labview.DAGEdge{}, Page: page(len(nodes))}
	workspace := labview.WorkspaceDiff{Header: header(labview.KindWorkspaceDiff), RunID: row.RowID, BaseRunID: other.RowID, Changes: []labview.WorkspaceChange{}, Page: page(0)}
	comparison := labview.RunComparison{Header: header(labview.KindRunComparison), ComparisonID: "comparison-" + shortID(source), LeftRunID: other.RowID, RightRunID: row.RowID, SameDimensions: []string{}, DifferentDimensions: []string{}, CallDeltas: []labview.CallDelta{}, WorkspaceDeltas: []labview.WorkspaceDelta{}, ReasonCodes: []string{"evidence_incomplete"}, Page: page(0)}
	object := labview.ObjectRef{Header: header(labview.KindObjectRef), Ref: refs[5]}
	problem := labview.Problem{Header: header(labview.KindProblem), ProblemID: "problem-" + shortID(source), Code: "evidence_incomplete", Severity: "warning", Scope: "run", RunID: row.RowID}
	workloads, treatments := map[string]bool{}, map[string]bool{}
	for _, candidate := range report.Rows {
		workloads[candidate.WorkloadID] = true
		treatments[string(candidate.Treatment)] = true
	}
	study := labview.StudySummary{Header: header(labview.KindStudySummary), StudyID: "study-" + shortID(source), EvidenceClass: string(report.EvidenceClass), WorkloadCount: uint32(len(workloads)), TreatmentCount: uint32(len(treatments)), StatusTotals: []labview.StatusTotal{{Status: "completed", Count: report.Summary.Completed}, {Status: "failed", Count: report.Summary.Failed}, {Status: "timed_out", Count: report.Summary.TimedOut}, {Status: "unsupported", Count: report.Summary.Unsupported}}, ProhibitedClaims: evaluation.RequiredProhibitedClaims(), Storage: labview.StorageSummary{}}
	set := labview.Set{Study: study, Run: run, Timeline: timeline, DAG: dag, Workspace: workspace, Comparison: comparison, Refs: object, Problem: problem}
	items := []struct {
		rel   string
		kind  labview.Kind
		value any
	}{{"study", labview.KindStudySummary, study}, {"run", labview.KindRunDetail, run}, {"timeline", labview.KindTimelinePage, timeline}, {"branch", labview.KindBranchDAG, dag}, {"workspace", labview.KindWorkspaceDiff, workspace}, {"comparison", labview.KindRunComparison, comparison}, {"reference", labview.KindObjectRef, object}, {"problem", labview.KindProblem, problem}}
	links := make([]labview.Link, len(items))
	for i, item := range items {
		_, id, e := labview.Encode(item.kind, item.value)
		if e != nil {
			return labview.Set{}, labview.ErrInvalid
		}
		links[i] = labview.Link{Rel: item.rel, Kind: item.kind, SHA256: id}
	}
	set.Index = labview.Index{Header: header(labview.KindIndex), Links: links, Capabilities: []string{"branch_dag", "comparison", "timeline", "workspace_diff"}, Page: page(len(links))}
	if err := labview.ValidateSet(set); err != nil {
		return labview.Set{}, err
	}
	return set, nil
}

func marker(source string, row evaluation.Row, kind string) string {
	raw, _ := json.Marshal(struct {
		Domain, Source, RowID, Kind string
		EvidenceRefs                []string
	}{"pysolate.lab.unavailable-relation.v1", source, row.RowID, kind, row.EvidenceRefs})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}
func shortID(digest string) string { return strings.TrimPrefix(digest, "sha256:")[:16] }
