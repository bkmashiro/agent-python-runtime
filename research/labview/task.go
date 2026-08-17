package labview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const TaskSnapshotSchema = "pysolate.lab-task.v1"

const taskCorpusSHA = "sha256:f88e94b462dd39d094512f71f9b8a397e0627b745c217442ccee98dbaed4904a"
const taskReportSHA = "sha256:269560ea66feee6f3015658be1c3fafe8308d973dc465625580185950f70a104"

var taskID = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func taskPublicText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"/users/", "/home/", `\\users\\`, ".hermes", "file://", "private://", "prompt_body", "provider_request", "provider_response", "trace_body", "workspace_body"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func taskRelativePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) {
		return false
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return taskPublicText(value)
}

type TaskInputs struct {
	Corpus []byte
	Report []byte
}

type TaskSource struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	File   string `json:"file"`
	Source string `json:"source"`
}

type TaskSourceRef struct {
	SourceID  string `json:"source_id"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type TaskWorkspaceChange struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	AfterSHA256 string `json:"after_sha256"`
	Size        int    `json:"size"`
}

type TaskEvent struct {
	Sequence              int                   `json:"sequence"`
	ParentSequence        int                   `json:"parent_sequence,omitempty"`
	SpanID                string                `json:"span_id"`
	ParentSpanID          string                `json:"parent_span_id,omitempty"`
	AgentID               string                `json:"agent_id"`
	ParentAgentID         string                `json:"parent_agent_id,omitempty"`
	AgentRole             string                `json:"agent_role"`
	StartedMillis         float64               `json:"started_millis"`
	EndedMillis           float64               `json:"ended_millis"`
	Source                *TaskSourceRef        `json:"source,omitempty"`
	WorkspaceID           string                `json:"workspace_id,omitempty"`
	WorkspaceChanges      []TaskWorkspaceChange `json:"workspace_changes,omitempty"`
	Type                  string                `json:"type"`
	Action                string                `json:"action"`
	Outcome               string                `json:"outcome"`
	CheckpointSHA256      string                `json:"checkpoint_sha256,omitempty"`
	CheckpointStatus      string                `json:"checkpoint_status,omitempty"`
	InputSHA256           string                `json:"input_sha256,omitempty"`
	OutputSHA256          string                `json:"output_sha256,omitempty"`
	Count                 int                   `json:"count"`
	RelativeElapsedMillis float64               `json:"relative_elapsed_millis"`
}

type TaskStats struct {
	DurationMillis   float64 `json:"duration_millis"`
	Events           int     `json:"events"`
	Agents           int     `json:"agents"`
	WorkspaceChanges int     `json:"workspace_changes"`
}

type TaskSnapshot struct {
	SchemaVersion    string       `json:"schema_version"`
	Identity         string       `json:"identity"`
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	Task             string       `json:"task"`
	Status           string       `json:"status"`
	ExpectedArtifact string       `json:"expected_artifact"`
	Sources          []TaskSource `json:"sources"`
	Events           []TaskEvent  `json:"events"`
	Stats            TaskStats    `json:"stats"`
}

type taskCorpus struct {
	SchemaVersion string `json:"schema_version"`
	Scenarios     []struct {
		ID            string `json:"id"`
		GuestSource   string `json:"guest_source"`
		ChildPrograms []struct {
			ID     string `json:"id"`
			Role   string `json:"role"`
			Source string `json:"source"`
		} `json:"child_programs"`
		Task             string `json:"task"`
		ExpectedArtifact string `json:"expected_artifact"`
	} `json:"scenarios"`
}

type taskReport struct {
	SchemaVersion string `json:"schema_version"`
	Rows          []struct {
		ScenarioID string      `json:"scenario_id"`
		Status     string      `json:"status"`
		Trace      []TaskEvent `json:"trace"`
	} `json:"rows"`
}

func taskSnapshotIdentity(snapshot TaskSnapshot) (string, error) {
	snapshot.Identity = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return latestSHA(encoded), nil
}

func BuildTaskSnapshot(inputs TaskInputs) (TaskSnapshot, error) {
	if latestSHA(inputs.Corpus) != taskCorpusSHA || latestSHA(inputs.Report) != taskReportSHA {
		return TaskSnapshot{}, errors.New("task inspector input anchor mismatch")
	}
	var corpus taskCorpus
	var report taskReport
	if err := json.Unmarshal(inputs.Corpus, &corpus); err != nil || corpus.SchemaVersion != "pysolate.spark-scenario-corpus.v3" {
		return TaskSnapshot{}, errors.New("invalid task inspector corpus")
	}
	if err := json.Unmarshal(inputs.Report, &report); err != nil || report.SchemaVersion != "pysolate.composable-acceptance-report.v3" {
		return TaskSnapshot{}, errors.New("invalid task inspector report")
	}
	const selected = "dev-workspace-summary"
	var scenario *struct {
		ID            string `json:"id"`
		GuestSource   string `json:"guest_source"`
		ChildPrograms []struct {
			ID     string `json:"id"`
			Role   string `json:"role"`
			Source string `json:"source"`
		} `json:"child_programs"`
		Task             string `json:"task"`
		ExpectedArtifact string `json:"expected_artifact"`
	}
	for index := range corpus.Scenarios {
		if corpus.Scenarios[index].ID == selected {
			scenario = &corpus.Scenarios[index]
			break
		}
	}
	var row *struct {
		ScenarioID string      `json:"scenario_id"`
		Status     string      `json:"status"`
		Trace      []TaskEvent `json:"trace"`
	}
	for index := range report.Rows {
		if report.Rows[index].ScenarioID == selected {
			row = &report.Rows[index]
			break
		}
	}
	if scenario == nil || row == nil || row.Status != "passed" || len(row.Trace) == 0 {
		return TaskSnapshot{}, errors.New("selected task inspector run is unavailable")
	}
	sources := []TaskSource{{ID: "orchestrator", Role: "orchestrator", File: "orchestrator.py", Source: scenario.GuestSource}}
	for _, child := range scenario.ChildPrograms {
		sources = append(sources, TaskSource{ID: child.ID, Role: child.Role, File: child.ID + ".py", Source: child.Source})
	}
	taskAgents := map[string]bool{}
	taskChanges := 0
	for _, event := range row.Trace {
		taskAgents[event.AgentID] = true
		taskChanges += len(event.WorkspaceChanges)
	}
	snapshot := TaskSnapshot{
		SchemaVersion:    TaskSnapshotSchema,
		ID:               selected,
		Title:            "Summarize a development workspace",
		Task:             scenario.Task,
		Status:           row.Status,
		ExpectedArtifact: scenario.ExpectedArtifact,
		Sources:          sources,
		Events:           row.Trace,
		Stats: TaskStats{
			DurationMillis:   row.Trace[len(row.Trace)-1].RelativeElapsedMillis,
			Events:           len(row.Trace),
			Agents:           len(taskAgents),
			WorkspaceChanges: taskChanges,
		},
	}
	identity, err := taskSnapshotIdentity(snapshot)
	if err != nil {
		return TaskSnapshot{}, err
	}
	snapshot.Identity = identity
	return snapshot, ValidateTaskSnapshot(snapshot)
}

func ValidateTaskSnapshot(snapshot TaskSnapshot) error {
	if snapshot.SchemaVersion != TaskSnapshotSchema || !latestDigest.MatchString(snapshot.Identity) || !taskID.MatchString(snapshot.ID) || snapshot.Title == "" || snapshot.Task == "" || snapshot.Status != "passed" || snapshot.ExpectedArtifact == "" || !taskPublicText(snapshot.Title+"\n"+snapshot.Task+"\n"+snapshot.ExpectedArtifact) || len(snapshot.Sources) != 3 || len(snapshot.Events) < 10 {
		return errors.New("invalid task inspector envelope")
	}
	identity, err := taskSnapshotIdentity(snapshot)
	if err != nil || identity != snapshot.Identity {
		return errors.New("task inspector identity mismatch")
	}
	sources := map[string]TaskSource{}
	for _, source := range snapshot.Sources {
		if !taskID.MatchString(source.ID) || source.Role == "" || !taskRelativePath(source.File) || source.Source == "" || !taskPublicText(source.Role+"\n"+source.Source) || sources[source.ID].ID != "" {
			return errors.New("invalid task inspector source")
		}
		sources[source.ID] = source
	}
	sequences := map[int]bool{}
	lastSequence := 0
	workspaceChanges := 0
	agents := map[string]bool{}
	for _, event := range snapshot.Events {
		if event.Sequence <= lastSequence || event.SpanID == "" || (!taskID.MatchString(event.AgentID) || (sources[event.AgentID].ID == "" && event.AgentID != "runtime")) || event.AgentRole == "" || event.EndedMillis < event.StartedMillis || event.Type == "" || event.Action == "" || event.Outcome == "" || event.RelativeElapsedMillis < event.EndedMillis || event.Count < 1 {
			return errors.New("invalid task inspector event")
		}
		if event.ParentSequence != 0 && !sequences[event.ParentSequence] {
			return errors.New("task inspector event parent is not earlier")
		}
		if !taskPublicText(event.SpanID+"\n"+event.ParentSpanID+"\n"+event.AgentRole+"\n"+event.WorkspaceID+"\n"+event.Type+"\n"+event.Action+"\n"+event.Outcome) || event.CheckpointSHA256 != "" && !latestDigest.MatchString(event.CheckpointSHA256) || event.InputSHA256 != "" && !latestDigest.MatchString(event.InputSHA256) || event.OutputSHA256 != "" && !latestDigest.MatchString(event.OutputSHA256) {
			return errors.New("invalid task inspector event body or digest")
		}
		if event.Source != nil {
			source := sources[event.Source.SourceID]
			if source.ID == "" || event.Source.File != source.File || event.Source.StartLine < 1 || event.Source.EndLine < event.Source.StartLine || event.Source.EndLine > strings.Count(source.Source, "\n")+1 {
				return errors.New("invalid task inspector source reference")
			}
		}
		for _, change := range event.WorkspaceChanges {
			if !taskRelativePath(change.Path) || change.Kind == "" || !taskPublicText(change.Kind) || !latestDigest.MatchString(change.AfterSHA256) || change.Size < 0 {
				return errors.New("invalid task inspector workspace change")
			}
			workspaceChanges++
		}
		sequences[event.Sequence] = true
		lastSequence = event.Sequence
		agents[event.AgentID] = true
	}
	if len(agents) < 4 || workspaceChanges != 2 || snapshot.Stats.Events != len(snapshot.Events) || snapshot.Stats.Agents != len(agents) || snapshot.Stats.WorkspaceChanges != workspaceChanges || snapshot.Stats.DurationMillis != snapshot.Events[len(snapshot.Events)-1].RelativeElapsedMillis {
		return errors.New("task inspector lost agents, timing, or workspace changes")
	}
	return nil
}

func DecodeTaskSnapshot(raw []byte) (TaskSnapshot, error) {
	if workflowbench.ValidateUniqueJSONKeys(raw) != nil {
		return TaskSnapshot{}, errors.New("task inspector input contains duplicate JSON keys")
	}
	var snapshot TaskSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return TaskSnapshot{}, errors.New("invalid task inspector JSON")
	}
	return snapshot, ValidateTaskSnapshot(snapshot)
}

func TaskSnapshotJSON(snapshot TaskSnapshot) ([]byte, error) {
	if err := ValidateTaskSnapshot(snapshot); err != nil {
		return nil, err
	}
	return json.MarshalIndent(snapshot, "", "  ")
}

func TaskIdentitySource(snapshot TaskSnapshot) ([]byte, error) {
	if err := ValidateTaskSnapshot(snapshot); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("// Generated by project-task. Do not edit.\nexport const expectedTaskSnapshotIdentity = %q;\n", snapshot.Identity)), nil
}

func TaskAgents(snapshot TaskSnapshot) []string {
	seen := map[string]bool{}
	for _, event := range snapshot.Events {
		seen[event.AgentID] = true
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
