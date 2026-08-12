package labview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

func CanonicalFixtureManifest() ([]byte, error) {
	fixtures, err := CanonicalFixtures()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	for _, name := range names {
		fmt.Fprintf(&out, "%s  %s\n", fixtures[name].SHA256, name)
	}
	return out.Bytes(), nil
}

func fixtureDigest(label string) string {
	h := sha256.Sum256([]byte("pysolate.lab-v1.fixture\x00" + label))
	return fmt.Sprintf("sha256:%x", h[:])
}
func header(kind Kind, source string) Header {
	return Header{SchemaVersion: "pysolate." + string(kind), SourceSHA256: source, GeneratedAtPolicy: "omitted"}
}
func page(n, total int) Page {
	p := Page{Returned: uint32(n), Total: uint32(total), Truncated: total > n}
	if p.Truncated {
		p.NextCursor = "cursor-next"
	}
	return p
}

func CanonicalSets() (map[string]Set, error) {
	sets := map[string]Set{}
	for _, name := range []string{"empty", "ordinary", "branched", "incomplete", "truncated", "private"} {
		sets[name] = makeSet(name)
	}
	for name, set := range sets {
		if err := ValidateSet(set); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return sets, nil
}
func makeSet(name string) Set {
	source := fixtureDigest("source:" + name)
	runID := "run-" + name
	other := "run-" + name + "-parent"
	completeness := Complete
	status := "completed"
	oracle := "passed"
	privacy := PrivacyPortable
	availability := AvailabilityAvailable
	if name == "incomplete" {
		completeness = Incomplete
		status = "failed"
		oracle = "failed"
	}
	if name == "truncated" {
		completeness = Truncated
	}
	if name == "private" {
		privacy = PrivacyPrivate
		availability = AvailabilityUnavailable
	}
	refs := []Ref{}
	for _, kind := range []string{"artifact", "capability_plan", "execution", "execution_profile", "invocation", "result", "workspace_tree"} {
		refPrivacy, refAvailability := PrivacyPortable, AvailabilityAvailable
		if (kind == "result" || kind == "workspace_tree") && name == "private" {
			refPrivacy, refAvailability = PrivacyPrivate, AvailabilityUnavailable
		}
		refs = append(refs, Ref{Kind: kind, SHA256: fixtureDigest(name + ":" + kind), Privacy: refPrivacy, Availability: refAvailability})
	}
	run := RunDetail{Header: header(KindRunDetail, source), RunID: runID, WorkloadID: "structured-source-v1", Treatment: "live_capture", Status: status, OracleStatus: oracle, EvidenceClass: "mechanism_only", EvidenceCompleteness: completeness, Refs: refs, ProblemCodes: []string{}}
	if name == "incomplete" {
		run.ProblemCodes = []string{"evidence_incomplete"}
	}
	if name == "truncated" {
		run.ProblemCodes = []string{"projection_truncated"}
	}
	if name == "private" {
		run.ProblemCodes = []string{"object_unavailable"}
	}
	if name == "empty" {
		run.ProblemCodes = []string{"empty_projection"}
	}
	events := []TimelineEvent{}
	if name != "empty" && name != "private" {
		events = []TimelineEvent{{Sequence: 1, Type: "execution.started", Outcome: "none"}, {Sequence: 2, ParentSequence: 1, Type: "capability.call", Outcome: "ok", Capability: "sources.demo_catalog", ArgumentsSHA256: fixtureDigest(name + ":args"), ResultSHA256: fixtureDigest(name + ":call-result")}, {Sequence: 3, ParentSequence: 2, Type: "execution.completed", Outcome: "ok"}}
	}
	eventTotal := len(events)
	if name == "truncated" {
		eventTotal++
	}
	timeline := TimelinePage{Header: header(KindTimelinePage, source), RunID: runID, EvidenceCompleteness: completeness, Events: events, Page: page(len(events), eventTotal)}
	nodes := []DAGNode{{RunID: other, Status: "completed", EvidenceCompleteness: Complete}, {RunID: runID, Status: status, EvidenceCompleteness: completeness}}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].RunID < nodes[j].RunID })
	edges := []DAGEdge{}
	if name == "branched" {
		edges = []DAGEdge{{ParentRunID: other, ChildRunID: runID, ForkOperation: 0, SuffixMode: SuffixOverride, BranchSHA256: fixtureDigest(name + ":branch")}}
	}
	dagTotal := len(nodes)
	if name == "truncated" {
		dagTotal++
	}
	dag := BranchDAG{Header: header(KindBranchDAG, source), Nodes: nodes, Edges: edges, Page: page(len(nodes), dagTotal)}
	changes := []WorkspaceChange{}
	if name != "empty" && name != "private" {
		changes = []WorkspaceChange{{Path: "reports/result.json", Kind: "added", Before: FileState{}, After: FileState{Present: true, SizeBytes: 128, SHA256: fixtureDigest(name + ":file")}}}
	}
	changeTotal := len(changes)
	if name == "truncated" {
		changeTotal++
	}
	workspace := WorkspaceDiff{Header: header(KindWorkspaceDiff, source), RunID: runID, BaseRunID: other, Changes: changes, Page: page(len(changes), changeTotal)}
	callDeltas := []CallDelta{}
	workspaceDeltas := []WorkspaceDelta{}
	reasons := []string{"no_difference"}
	same := []string{"artifact", "profile", "workload"}
	different := []string{}
	if name == "branched" {
		callDeltas = []CallDelta{{OperationIndex: 0, Kind: "changed", LeftSHA256: fixtureDigest(name + ":left"), RightSHA256: fixtureDigest(name + ":right")}}
		workspaceDeltas = []WorkspaceDelta{{Path: "reports/result.json", Kind: "modified"}}
		reasons = []string{"capability_result_changed", "workspace_digest_changed"}
		different = []string{"capability_calls", "result", "workspace"}
	}
	comparisonTotal := len(callDeltas) + len(workspaceDeltas)
	if name == "truncated" {
		comparisonTotal++
	}
	comparison := RunComparison{Header: header(KindRunComparison, source), ComparisonID: "comparison-" + name, LeftRunID: other, RightRunID: runID, SameDimensions: same, DifferentDimensions: different, CallDeltas: callDeltas, WorkspaceDeltas: workspaceDeltas, ReasonCodes: reasons, Page: page(len(callDeltas)+len(workspaceDeltas), comparisonTotal)}
	obj := ObjectRef{Header: header(KindObjectRef, source), Ref: Ref{Kind: "result", SHA256: fixtureDigest(name + ":object"), Privacy: privacy, Availability: availability}}
	problemCode := "none"
	scope := "study"
	if name == "empty" {
		problemCode = "empty_projection"
		scope = "timeline"
	}
	if name == "incomplete" {
		problemCode = "evidence_incomplete"
		scope = "run"
	}
	if name == "truncated" {
		problemCode = "projection_truncated"
		scope = "run"
	}
	if name == "private" {
		problemCode = "object_unavailable"
		scope = "reference"
	}
	problem := Problem{Header: header(KindProblem, source), ProblemID: "problem-" + name, Code: problemCode, Severity: "info", Scope: scope}
	if scope == "run" || scope == "timeline" || scope == "reference" {
		problem.RunID = runID
	}
	if scope == "reference" {
		problem.RefSHA256 = obj.Ref.SHA256
	}
	statusTotals := []StatusTotal{}
	for _, candidate := range []string{"completed", "failed", "timed_out", "unsupported"} {
		count := uint32(0)
		if candidate == status {
			count = 1
		}
		statusTotals = append(statusTotals, StatusTotal{Status: candidate, Count: count})
	}
	study := StudySummary{Header: header(KindStudySummary, source), StudyID: "study-" + name, EvidenceClass: "mechanism_only", WorkloadCount: 1, TreatmentCount: 1, StatusTotals: statusTotals, ProhibitedClaims: evaluation.RequiredProhibitedClaims(), Storage: StorageSummary{LogicalBytes: 2048, StoredBytes: 1024, ObjectCount: 8, ReusedObjectCount: 2}}
	set := Set{Study: study, Run: run, Timeline: timeline, DAG: dag, Workspace: workspace, Comparison: comparison, Refs: obj, Problem: problem}
	items := []struct {
		rel   string
		kind  Kind
		value any
	}{{"study", KindStudySummary, study}, {"run", KindRunDetail, run}, {"timeline", KindTimelinePage, timeline}, {"branch", KindBranchDAG, dag}, {"workspace", KindWorkspaceDiff, workspace}, {"comparison", KindRunComparison, comparison}, {"reference", KindObjectRef, obj}, {"problem", KindProblem, problem}}
	links := make([]Link, len(items))
	for i, item := range items {
		_, id, _ := Encode(item.kind, item.value)
		links[i] = Link{Rel: item.rel, Kind: item.kind, SHA256: id}
	}
	set.Index = Index{Header: header(KindIndex, source), Links: links, Capabilities: []string{"branch_dag", "comparison", "timeline", "workspace_diff"}, Page: page(len(links), len(links))}
	return set
}
func CanonicalFixtures() (map[string]Fixture, error) {
	sets, err := CanonicalSets()
	if err != nil {
		return nil, err
	}
	out := map[string]Fixture{}
	for name, set := range sets {
		for _, item := range setDocuments(set) {
			raw, id, err := Encode(item.kind, item.value)
			if err != nil {
				return nil, err
			}
			out[name+"/"+string(item.kind)+".json"] = Fixture{Kind: item.kind, Bytes: raw, SHA256: id}
		}
	}
	return out, nil
}

type documentValue struct {
	kind  Kind
	value any
}

func setDocuments(s Set) []documentValue {
	return []documentValue{{KindIndex, s.Index}, {KindStudySummary, s.Study}, {KindRunDetail, s.Run}, {KindTimelinePage, s.Timeline}, {KindBranchDAG, s.DAG}, {KindWorkspaceDiff, s.Workspace}, {KindRunComparison, s.Comparison}, {KindObjectRef, s.Refs}, {KindProblem, s.Problem}}
}
func cloneSet(s Set) Set {
	raw, _ := json.Marshal(s)
	var out Set
	_ = json.Unmarshal(raw, &out)
	return out
}
