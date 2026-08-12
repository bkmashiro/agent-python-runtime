package labview

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

var (
	digestRE     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idRE         = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
	capabilityRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$`)
	cursorRE     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const maxPageItems = 256

func validHeader(h Header, kind Kind) bool {
	return h.SchemaVersion == "pysolate."+string(kind) && digestRE.MatchString(h.SourceSHA256) && h.GeneratedAtPolicy == "omitted"
}
func validPage(p Page, length int) bool {
	return length <= maxPageItems && p.Returned == uint32(length) && p.Total >= p.Returned && p.Truncated == (p.Total > p.Returned) && ((p.Truncated && cursorRE.MatchString(p.NextCursor)) || (!p.Truncated && p.NextCursor == "")) && (p.Cursor == "" || cursorRE.MatchString(p.Cursor))
}
func validRef(r Ref) bool {
	return slices.Contains([]string{"artifact", "capability_plan", "execution", "execution_profile", "invocation", "result", "workspace_tree"}, r.Kind) && digestRE.MatchString(r.SHA256) && (r.Privacy == PrivacyPortable || r.Privacy == PrivacyPrivate) && (r.Availability == AvailabilityAvailable || r.Availability == AvailabilityUnavailable) && !(r.Privacy == PrivacyPrivate && r.Availability == AvailabilityAvailable)
}
func validCompleteness(v Completeness) bool {
	return v == Complete || v == Incomplete || v == Truncated
}
func uniqueSorted(values []string) bool {
	for i, v := range values {
		if !idRE.MatchString(v) || (i > 0 && values[i-1] >= v) {
			return false
		}
	}
	return values != nil
}
func validPath(v string) bool {
	return v != "" && len(v) <= 4096 && !strings.ContainsAny(v, "\\:") && !strings.ContainsRune(v, 0) && !strings.HasPrefix(v, "/") && path.Clean(v) == v && v != "." && v != ".." && !strings.HasPrefix(v, "../")
}

func validateIndex(v Index) error {
	if !validHeader(v.Header, KindIndex) || v.Links == nil || v.Capabilities == nil || len(v.Links) != 8 || !validPage(v.Page, len(v.Links)) || !uniqueSorted(v.Capabilities) {
		return ErrInvalid
	}
	if !slices.Equal(v.Capabilities, []string{"branch_dag", "comparison", "timeline", "workspace_diff"}) {
		return ErrInvalid
	}
	relations := map[string]Kind{"study": KindStudySummary, "run": KindRunDetail, "timeline": KindTimelinePage, "branch": KindBranchDAG, "workspace": KindWorkspaceDiff, "comparison": KindRunComparison, "reference": KindObjectRef, "problem": KindProblem}
	seen := map[string]bool{}
	expectedRelations := []string{"study", "run", "timeline", "branch", "workspace", "comparison", "reference", "problem"}
	for index, link := range v.Links {
		if link.Rel != expectedRelations[index] || relations[link.Rel] != link.Kind || seen[link.Rel] || !digestRE.MatchString(link.SHA256) {
			return ErrInvalid
		}
		seen[link.Rel] = true
	}
	return nil
}
func validateStudy(v StudySummary) error {
	if !validHeader(v.Header, KindStudySummary) || !idRE.MatchString(v.StudyID) || !validEvidenceClass(v.EvidenceClass) || v.StatusTotals == nil || v.ProhibitedClaims == nil || !slices.Equal(v.ProhibitedClaims, evaluation.RequiredProhibitedClaims()) || v.Storage.ReusedObjectCount > v.Storage.ObjectCount {
		return ErrInvalid
	}
	if len(v.StatusTotals) != 4 {
		return ErrInvalid
	}
	for index, s := range v.StatusTotals {
		expected := []string{"completed", "failed", "timed_out", "unsupported"}
		if s.Status != expected[index] {
			return ErrInvalid
		}
	}
	return nil
}
func validateRun(v RunDetail) error {
	if !validHeader(v.Header, KindRunDetail) || !idRE.MatchString(v.RunID) || !idRE.MatchString(v.WorkloadID) || !validTreatment(v.Treatment) || !validStatus(v.Status) || !validOracleStatus(v.OracleStatus) || !validEvidenceClass(v.EvidenceClass) || !validCompleteness(v.EvidenceCompleteness) || v.Refs == nil || v.ProblemCodes == nil || len(v.Refs) > 32 || len(v.ProblemCodes) > 32 || !uniqueSorted(v.ProblemCodes) {
		return ErrInvalid
	}
	if !validProblemCodes(v.ProblemCodes) {
		return ErrInvalid
	}
	if !validRunOutcome(v.Status, v.OracleStatus) {
		return ErrInvalid
	}
	prev := ""
	expected := []string{"artifact", "capability_plan", "execution", "execution_profile", "invocation", "result", "workspace_tree"}
	if len(v.Refs) != len(expected) {
		return ErrInvalid
	}
	for index, r := range v.Refs {
		key := r.Kind + "@" + r.SHA256
		if !validRef(r) || r.Kind != expected[index] || (prev != "" && prev >= key) {
			return ErrInvalid
		}
		prev = key
	}
	return nil
}
func validateTimeline(v TimelinePage) error {
	if !validHeader(v.Header, KindTimelinePage) || !idRE.MatchString(v.RunID) || !validCompleteness(v.EvidenceCompleteness) || v.Events == nil || !validPage(v.Page, len(v.Events)) {
		return ErrInvalid
	}
	for i, e := range v.Events {
		if e.Sequence == 0 || e.ParentSequence >= e.Sequence || (i == 0 && v.Page.Cursor == "" && e.ParentSequence != 0) || (i > 0 && e.Sequence != v.Events[i-1].Sequence+1) || !validEvent(e) {
			return ErrInvalid
		}
	}
	return nil
}
func validateDAG(v BranchDAG) error {
	if !validHeader(v.Header, KindBranchDAG) || v.Nodes == nil || v.Edges == nil || !validPage(v.Page, len(v.Nodes)) {
		return ErrInvalid
	}
	nodes := map[string]bool{}
	for i, n := range v.Nodes {
		if !idRE.MatchString(n.RunID) || !validStatus(n.Status) || !validCompleteness(n.EvidenceCompleteness) || nodes[n.RunID] || (i > 0 && v.Nodes[i-1].RunID >= n.RunID) {
			return ErrInvalid
		}
		nodes[n.RunID] = true
	}
	parents := map[string]string{}
	for i, e := range v.Edges {
		if !nodes[e.ParentRunID] || !nodes[e.ChildRunID] || e.ParentRunID == e.ChildRunID || !digestRE.MatchString(e.BranchSHA256) || !validSuffix(e.SuffixMode) || (i > 0 && edgeKey(v.Edges[i-1]) >= edgeKey(e)) {
			return ErrInvalid
		}
		if _, exists := parents[e.ChildRunID]; exists {
			return ErrInvalid
		}
		parents[e.ChildRunID] = e.ParentRunID
	}
	for child := range parents {
		seen := map[string]bool{}
		for cur := child; cur != ""; cur = parents[cur] {
			if seen[cur] {
				return ErrInvalid
			}
			seen[cur] = true
		}
	}
	return nil
}
func validateWorkspace(v WorkspaceDiff) error {
	if !validHeader(v.Header, KindWorkspaceDiff) || !idRE.MatchString(v.RunID) || (v.BaseRunID != "" && !idRE.MatchString(v.BaseRunID)) || v.Changes == nil || !validPage(v.Page, len(v.Changes)) {
		return ErrInvalid
	}
	for i, c := range v.Changes {
		if !validPath(c.Path) || (i > 0 && v.Changes[i-1].Path >= c.Path) || !validChange(c) {
			return ErrInvalid
		}
	}
	return nil
}
func validateComparison(v RunComparison) error {
	if !validHeader(v.Header, KindRunComparison) || !idRE.MatchString(v.ComparisonID) || !idRE.MatchString(v.LeftRunID) || !idRE.MatchString(v.RightRunID) || v.LeftRunID == v.RightRunID || !uniqueSorted(v.SameDimensions) || !uniqueSorted(v.DifferentDimensions) || !uniqueSorted(v.ReasonCodes) || v.CallDeltas == nil || v.WorkspaceDeltas == nil || !validPage(v.Page, len(v.CallDeltas)+len(v.WorkspaceDeltas)) {
		return ErrInvalid
	}
	for _, reason := range v.ReasonCodes {
		if !slices.Contains([]string{"no_difference", "capability_result_changed", "workspace_digest_changed", "status_changed", "evidence_incomplete"}, reason) {
			return ErrInvalid
		}
	}
	for _, d := range v.SameDimensions {
		if slices.Contains(v.DifferentDimensions, d) || !validDimension(d) {
			return ErrInvalid
		}
	}
	for _, d := range v.DifferentDimensions {
		if !validDimension(d) {
			return ErrInvalid
		}
	}
	for i, d := range v.CallDeltas {
		if !validCallDelta(d) || (i > 0 && v.CallDeltas[i-1].OperationIndex >= d.OperationIndex) {
			return ErrInvalid
		}
	}
	for i, d := range v.WorkspaceDeltas {
		if !validPath(d.Path) || !validWorkspaceDelta(d.Kind) || (i > 0 && v.WorkspaceDeltas[i-1].Path >= d.Path) {
			return ErrInvalid
		}
	}
	return nil
}
func validateObjectRef(v ObjectRef) error {
	if !validHeader(v.Header, KindObjectRef) || !validRef(v.Ref) {
		return ErrInvalid
	}
	return nil
}
func validateProblem(v Problem) error {
	if !validHeader(v.Header, KindProblem) || !idRE.MatchString(v.ProblemID) || !validProblemCode(v.Code) || !slices.Contains([]string{"info", "warning", "error"}, v.Severity) || !slices.Contains([]string{"index", "study", "run", "timeline", "branch", "workspace", "comparison", "reference"}, v.Scope) || (v.RunID != "" && !idRE.MatchString(v.RunID)) || (v.RefSHA256 != "" && !digestRE.MatchString(v.RefSHA256)) {
		return ErrInvalid
	}
	if v.Code == "none" && v.Scope != "index" && v.Scope != "study" {
		return ErrInvalid
	}
	switch v.Scope {
	case "index", "study":
		if v.RunID != "" || v.RefSHA256 != "" {
			return ErrInvalid
		}
	case "reference":
		if v.RefSHA256 == "" {
			return ErrInvalid
		}
	default:
		if v.RunID == "" {
			return ErrInvalid
		}
	}
	return nil
}

func ValidateSet(s Set) error {
	for kind, value := range map[Kind]any{KindIndex: s.Index, KindStudySummary: s.Study, KindRunDetail: s.Run, KindTimelinePage: s.Timeline, KindBranchDAG: s.DAG, KindWorkspaceDiff: s.Workspace, KindRunComparison: s.Comparison, KindObjectRef: s.Refs, KindProblem: s.Problem} {
		if validateDocument(kind, value) != nil {
			return ErrInvalid
		}
	}
	source := s.Index.SourceSHA256
	for _, h := range []Header{s.Study.Header, s.Run.Header, s.Timeline.Header, s.DAG.Header, s.Workspace.Header, s.Comparison.Header, s.Refs.Header, s.Problem.Header} {
		if h.SourceSHA256 != source {
			return ErrInvalid
		}
	}
	runs := map[string]bool{}
	for _, n := range s.DAG.Nodes {
		runs[n.RunID] = true
		if n.RunID == s.Run.RunID && (n.Status != s.Run.Status || n.EvidenceCompleteness != s.Run.EvidenceCompleteness) {
			return ErrInvalid
		}
	}
	runs[s.Run.RunID] = true
	if s.Timeline.EvidenceCompleteness != s.Run.EvidenceCompleteness || s.Timeline.RunID != s.Run.RunID || s.Workspace.RunID != s.Run.RunID || (s.Workspace.BaseRunID != "" && !runs[s.Workspace.BaseRunID]) || !runs[s.Comparison.LeftRunID] || !runs[s.Comparison.RightRunID] {
		return ErrInvalid
	}
	if s.Problem.RunID != "" && !runs[s.Problem.RunID] {
		return ErrInvalid
	}
	if s.Problem.Code == "none" {
		if s.Problem.RunID != "" || s.Problem.RefSHA256 != "" {
			return ErrInvalid
		}
	} else if s.Problem.RunID == s.Run.RunID && !slices.Contains(s.Run.ProblemCodes, s.Problem.Code) {
		return ErrInvalid
	}
	if s.Problem.RefSHA256 != "" {
		found := s.Problem.RefSHA256 == s.Refs.Ref.SHA256
		for _, ref := range s.Run.Refs {
			found = found || ref.SHA256 == s.Problem.RefSHA256
		}
		if !found {
			return ErrInvalid
		}
	}
	fixtures := []struct {
		kind  Kind
		value any
	}{{KindStudySummary, s.Study}, {KindRunDetail, s.Run}, {KindTimelinePage, s.Timeline}, {KindBranchDAG, s.DAG}, {KindWorkspaceDiff, s.Workspace}, {KindRunComparison, s.Comparison}, {KindObjectRef, s.Refs}, {KindProblem, s.Problem}}
	if len(s.Index.Links) != len(fixtures) {
		return ErrInvalid
	}
	for i, item := range fixtures {
		_, id, err := Encode(item.kind, item.value)
		if err != nil || s.Index.Links[i].Kind != item.kind || s.Index.Links[i].SHA256 != id {
			return ErrInvalid
		}
	}
	return nil
}

func validEvidenceClass(v string) bool {
	return slices.Contains([]string{"current", "mechanism_only", "qualified_workload", "experimental_partial", "not_measured"}, v)
}
func validStatus(v string) bool {
	return slices.Contains([]string{"completed", "failed", "timed_out", "unsupported"}, v)
}
func validRunOutcome(status, oracle string) bool {
	if status == "completed" {
		return oracle == "passed"
	}
	if status == "failed" {
		return oracle == "failed"
	}
	return (status == "timed_out" || status == "unsupported") && oracle == "not_run"
}
func validOracleStatus(v string) bool {
	return slices.Contains([]string{"passed", "failed", "not_run"}, v)
}
func validTreatment(v string) bool {
	return slices.Contains([]string{"live_capture", "offline_replay", "counterfactual_branch", "deterministic_verification"}, v)
}
func validSuffix(v SuffixMode) bool {
	return slices.Contains([]SuffixMode{"override", "recorded_suffix", "live_suffix"}, v)
}
func edgeKey(e DAGEdge) string { return e.ParentRunID + "\x00" + e.ChildRunID }
func validEvent(e TimelineEvent) bool {
	if e.Type == "capability.call" {
		return capabilityRE.MatchString(e.Capability) && digestRE.MatchString(e.ArgumentsSHA256) && slices.Contains([]string{"ok", "error", "denied", "timeout"}, e.Outcome) && ((e.Outcome == "ok" && digestRE.MatchString(e.ResultSHA256)) || (e.Outcome != "ok" && e.ResultSHA256 == ""))
	}
	if e.Capability != "" || e.ArgumentsSHA256 != "" || e.ResultSHA256 != "" || e.OperationIndex != 0 {
		return false
	}
	switch e.Type {
	case "execution.started", "capability.plan_bound", "workspace.finalized":
		return e.Outcome == "none"
	case "execution.completed":
		return e.Outcome == "ok"
	case "execution.failed":
		return e.Outcome == "error"
	default:
		return false
	}
}
func validChange(c WorkspaceChange) bool {
	b, a := c.Before, c.After
	valid := func(s FileState) bool {
		return (!s.Present && s.SizeBytes == 0 && !s.Executable && s.SHA256 == "") || (s.Present && digestRE.MatchString(s.SHA256))
	}
	if !valid(b) || !valid(a) {
		return false
	}
	switch c.Kind {
	case "added":
		return !b.Present && a.Present
	case "removed":
		return b.Present && !a.Present
	case "modified":
		return b.Present && a.Present && (b != a)
	default:
		return false
	}
}
func validDimension(v string) bool {
	return slices.Contains([]string{"workload", "treatment", "status", "artifact", "profile", "plan", "result", "workspace", "evidence_class", "evidence_completeness", "capability_calls"}, v)
}
func validCallDelta(d CallDelta) bool {
	left, right := digestRE.MatchString(d.LeftSHA256), digestRE.MatchString(d.RightSHA256)
	switch d.Kind {
	case "same":
		return left && right && d.LeftSHA256 == d.RightSHA256
	case "changed":
		return left && right && d.LeftSHA256 != d.RightSHA256
	case "left_only":
		return left && d.RightSHA256 == ""
	case "right_only":
		return d.LeftSHA256 == "" && right
	case "unavailable":
		return d.LeftSHA256 == "" && d.RightSHA256 == ""
	default:
		return false
	}
}
func validWorkspaceDelta(v string) bool {
	return slices.Contains([]string{"same", "added", "removed", "modified", "unavailable"}, v)
}
func validProblemCodes(values []string) bool {
	for _, value := range values {
		if !validProblemCode(value) {
			return false
		}
	}
	return true
}
func validProblemCode(v string) bool {
	return slices.Contains([]string{"none", "evidence_incomplete", "projection_truncated", "object_unavailable", "empty_projection"}, v)
}
