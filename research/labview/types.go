// Package labview defines the bounded, privacy-safe Lab v1 read projection.
// It is independent from LabStore disk format and carries no authority.
package labview

import "errors"

var ErrInvalid = errors.New("invalid lab v1 projection")

type Kind string

const (
	KindIndex         Kind = "lab-index.v1"
	KindStudySummary  Kind = "study-summary.v1"
	KindRunDetail     Kind = "run-detail.v1"
	KindTimelinePage  Kind = "timeline-page.v1"
	KindBranchDAG     Kind = "branch-dag.v1"
	KindWorkspaceDiff Kind = "workspace-diff.v1"
	KindRunComparison Kind = "run-comparison.v1"
	KindObjectRef     Kind = "object-ref.v1"
	KindProblem       Kind = "problem.v1"
)

func AllKinds() []Kind {
	return []Kind{KindIndex, KindStudySummary, KindRunDetail, KindTimelinePage, KindBranchDAG, KindWorkspaceDiff, KindRunComparison, KindObjectRef, KindProblem}
}

type Header struct {
	SchemaVersion     string `json:"schema_version"`
	SourceSHA256      string `json:"source_sha256"`
	GeneratedAtPolicy string `json:"generated_at_policy"`
}
type Privacy string

const (
	PrivacyPortable Privacy = "portable"
	PrivacyPrivate  Privacy = "private"
)

type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
)

type Completeness string

const (
	Complete   Completeness = "complete"
	Incomplete Completeness = "incomplete"
	Truncated  Completeness = "truncated"
)

type Page struct {
	Cursor     string `json:"cursor"`
	NextCursor string `json:"next_cursor"`
	Returned   uint32 `json:"returned"`
	Total      uint32 `json:"total"`
	Truncated  bool   `json:"truncated"`
}
type Ref struct {
	Kind         string       `json:"kind"`
	SHA256       string       `json:"sha256"`
	Privacy      Privacy      `json:"privacy"`
	Availability Availability `json:"availability"`
}
type Link struct {
	Rel    string `json:"rel"`
	Kind   Kind   `json:"kind"`
	SHA256 string `json:"sha256"`
}

type Index struct {
	Header
	Links        []Link   `json:"links"`
	Capabilities []string `json:"capabilities"`
	Page         Page     `json:"page"`
}
type StatusTotal struct {
	Status string `json:"status"`
	Count  uint32 `json:"count"`
}
type StorageSummary struct {
	LogicalBytes      uint64 `json:"logical_bytes"`
	StoredBytes       uint64 `json:"stored_bytes"`
	ObjectCount       uint32 `json:"object_count"`
	ReusedObjectCount uint32 `json:"reused_object_count"`
}
type StudySummary struct {
	Header
	StudyID          string         `json:"study_id"`
	EvidenceClass    string         `json:"evidence_class"`
	WorkloadCount    uint32         `json:"workload_count"`
	TreatmentCount   uint32         `json:"treatment_count"`
	StatusTotals     []StatusTotal  `json:"status_totals"`
	ProhibitedClaims []string       `json:"prohibited_claims"`
	Storage          StorageSummary `json:"storage"`
}
type RunDetail struct {
	Header
	RunID                string       `json:"run_id"`
	WorkloadID           string       `json:"workload_id"`
	Treatment            string       `json:"treatment"`
	Status               string       `json:"status"`
	OracleStatus         string       `json:"oracle_status"`
	EvidenceClass        string       `json:"evidence_class"`
	EvidenceCompleteness Completeness `json:"evidence_completeness"`
	Refs                 []Ref        `json:"refs"`
	ProblemCodes         []string     `json:"problem_codes"`
}
type TimelineEvent struct {
	Sequence        uint32 `json:"sequence"`
	ParentSequence  uint32 `json:"parent_sequence"`
	Type            string `json:"type"`
	Outcome         string `json:"outcome"`
	Capability      string `json:"capability"`
	OperationIndex  uint32 `json:"operation_index"`
	ArgumentsSHA256 string `json:"arguments_sha256"`
	ResultSHA256    string `json:"result_sha256"`
}
type TimelinePage struct {
	Header
	RunID                string          `json:"run_id"`
	EvidenceCompleteness Completeness    `json:"evidence_completeness"`
	Events               []TimelineEvent `json:"events"`
	Page                 Page            `json:"page"`
}
type DAGNode struct {
	RunID                string       `json:"run_id"`
	Status               string       `json:"status"`
	EvidenceCompleteness Completeness `json:"evidence_completeness"`
}
type SuffixMode string

const (
	SuffixOverride SuffixMode = "override"
	SuffixRecorded SuffixMode = "recorded_suffix"
	SuffixLive     SuffixMode = "live_suffix"
)

type DAGEdge struct {
	ParentRunID   string     `json:"parent_run_id"`
	ChildRunID    string     `json:"child_run_id"`
	ForkOperation uint32     `json:"fork_operation"`
	SuffixMode    SuffixMode `json:"suffix_mode"`
	BranchSHA256  string     `json:"branch_sha256"`
}
type BranchDAG struct {
	Header
	Nodes []DAGNode `json:"nodes"`
	Edges []DAGEdge `json:"edges"`
	Page  Page      `json:"page"`
}
type FileState struct {
	Present    bool   `json:"present"`
	SizeBytes  uint64 `json:"size_bytes"`
	Executable bool   `json:"executable"`
	SHA256     string `json:"sha256"`
}
type WorkspaceChange struct {
	Path   string    `json:"path"`
	Kind   string    `json:"kind"`
	Before FileState `json:"before"`
	After  FileState `json:"after"`
}
type WorkspaceDiff struct {
	Header
	RunID     string            `json:"run_id"`
	BaseRunID string            `json:"base_run_id"`
	Changes   []WorkspaceChange `json:"changes"`
	Page      Page              `json:"page"`
}
type CallDelta struct {
	OperationIndex uint32 `json:"operation_index"`
	Kind           string `json:"kind"`
	LeftSHA256     string `json:"left_sha256"`
	RightSHA256    string `json:"right_sha256"`
}
type WorkspaceDelta struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type RunComparison struct {
	Header
	ComparisonID        string           `json:"comparison_id"`
	LeftRunID           string           `json:"left_run_id"`
	RightRunID          string           `json:"right_run_id"`
	SameDimensions      []string         `json:"same_dimensions"`
	DifferentDimensions []string         `json:"different_dimensions"`
	CallDeltas          []CallDelta      `json:"call_deltas"`
	WorkspaceDeltas     []WorkspaceDelta `json:"workspace_deltas"`
	ReasonCodes         []string         `json:"reason_codes"`
	Page                Page             `json:"page"`
}
type ObjectRef struct {
	Header
	Ref Ref `json:"ref"`
}
type Problem struct {
	Header
	ProblemID string `json:"problem_id"`
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Scope     string `json:"scope"`
	RunID     string `json:"run_id,omitempty"`
	RefSHA256 string `json:"ref_sha256,omitempty"`
}

type Set struct {
	Index      Index
	Study      StudySummary
	Run        RunDetail
	Timeline   TimelinePage
	DAG        BranchDAG
	Workspace  WorkspaceDiff
	Comparison RunComparison
	Refs       ObjectRef
	Problem    Problem
}

func (s Set) Clone() Set { return cloneSet(s) }

type Fixture struct {
	Kind   Kind
	Bytes  []byte
	SHA256 string
}
