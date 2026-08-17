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

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const TaskSnapshotSchema = "pysolate.lab-task.v2"

const taskCorpusSHA = "sha256:ed1bd1b525484d19ec46902801afb286261aa1deecc285bfc1d4dd8d2ab56584"
const taskReportSHA = "sha256:ea2a1e6e4b8934f502a5a4cae50377ca0d9ec4950f8502453fc2e534e5b0041a"
const taskCaptureSHA = "sha256:4e51bf4c457e093f7384e559ee6467aa10bbcc46805149df4ce3da112fbf342e"

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
	Corpus  []byte
	Report  []byte
	Capture []byte
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

type TaskContext struct {
	Files                  []string `json:"files"`
	Analyses               []string `json:"analyses"`
	RepeatedTransformation string   `json:"repeated_transformation"`
	WaitBoundary           string   `json:"wait_boundary"`
	Observation            string   `json:"observation"`
	SelectedChild          int      `json:"selected_child"`
}

type TaskOutput struct {
	AgentID       string `json:"agent_id"`
	Label         string `json:"label"`
	Path          string `json:"path,omitempty"`
	Disposition   string `json:"disposition"`
	Body          string `json:"body"`
	SHA256        string `json:"sha256"`
	EventSequence int    `json:"event_sequence"`
}

type TaskSnapshot struct {
	SchemaVersion    string       `json:"schema_version"`
	Identity         string       `json:"identity"`
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	Task             string       `json:"task"`
	Status           string       `json:"status"`
	ExpectedArtifact string       `json:"expected_artifact"`
	ProviderIO       string       `json:"provider_io"`
	Context          TaskContext  `json:"context"`
	Sources          []TaskSource `json:"sources"`
	Outputs          []TaskOutput `json:"outputs"`
	Events           []TaskEvent  `json:"events"`
	Stats            TaskStats    `json:"stats"`
}

type taskCorpus struct {
	SchemaVersion string `json:"schema_version"`
	Scenarios     []struct {
		ID            string `json:"id"`
		GuestSource   string `json:"guest_source"`
		ChildPrograms []struct {
			ID             string `json:"id"`
			Role           string `json:"role"`
			Source         string `json:"source"`
			ExpectedResult string `json:"expected_result"`
			OutputPath     string `json:"output_path"`
		} `json:"child_programs"`
		Task                   string   `json:"task"`
		Files                  []string `json:"files"`
		ChildAnalyses          []string `json:"child_analyses"`
		RepeatedTransformation string   `json:"repeated_transformation"`
		WaitBoundary           string   `json:"wait_boundary"`
		Observation            string   `json:"observation"`
		SelectedChild          int      `json:"selected_child"`
		ExpectedArtifact       string   `json:"expected_artifact"`
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
	if latestSHA(inputs.Corpus) != taskCorpusSHA || latestSHA(inputs.Report) != taskReportSHA || latestSHA(inputs.Capture) != taskCaptureSHA {
		return TaskSnapshot{}, errors.New("task inspector input anchor mismatch")
	}
	capture, captureIdentity, err := composableacceptance.DecodeBodyCapture(inputs.Capture)
	if err != nil || captureIdentity != taskCaptureSHA {
		return TaskSnapshot{}, errors.New("invalid task inspector body capture")
	}
	var typedReport composableacceptance.Report
	if err := composableacceptance.DecodeReport(inputs.Report, &typedReport); err != nil {
		return TaskSnapshot{}, errors.New("invalid typed task inspector report")
	}
	var corpus taskCorpus
	var report taskReport
	if err := json.Unmarshal(inputs.Corpus, &corpus); err != nil || corpus.SchemaVersion != "pysolate.spark-scenario-corpus.v3" {
		return TaskSnapshot{}, errors.New("invalid task inspector corpus")
	}
	if err := json.Unmarshal(inputs.Report, &report); err != nil || report.SchemaVersion != "pysolate.composable-acceptance-report.v3" {
		return TaskSnapshot{}, errors.New("invalid task inspector report")
	}
	const selected = "dev-release-readiness"
	var scenario *struct {
		ID            string `json:"id"`
		GuestSource   string `json:"guest_source"`
		ChildPrograms []struct {
			ID             string `json:"id"`
			Role           string `json:"role"`
			Source         string `json:"source"`
			ExpectedResult string `json:"expected_result"`
			OutputPath     string `json:"output_path"`
		} `json:"child_programs"`
		Task                   string   `json:"task"`
		Files                  []string `json:"files"`
		ChildAnalyses          []string `json:"child_analyses"`
		RepeatedTransformation string   `json:"repeated_transformation"`
		WaitBoundary           string   `json:"wait_boundary"`
		Observation            string   `json:"observation"`
		SelectedChild          int      `json:"selected_child"`
		ExpectedArtifact       string   `json:"expected_artifact"`
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
	var typedRow *composableacceptance.Row
	for index := range typedReport.Rows {
		if typedReport.Rows[index].ScenarioID == selected {
			typedRow = &typedReport.Rows[index]
			break
		}
	}
	if typedRow == nil {
		return TaskSnapshot{}, errors.New("selected typed task inspector run is unavailable")
	}
	traceIdentity, err := composableacceptance.TraceIdentity(typedRow.Trace)
	if err != nil || capture.ScenarioID != selected || capture.ScenarioSHA256 != typedRow.ScenarioSHA256 || capture.TraceSHA256 != traceIdentity || capture.ProviderIO != composableacceptance.ProviderIONotApplicable {
		return TaskSnapshot{}, errors.New("task inspector body capture is not trace-bound")
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
	capturedOutputs := map[string]composableacceptance.CapturedAgentOutput{}
	for _, captured := range capture.AgentOutputs {
		capturedOutputs[captured.AgentID] = captured
	}
	if len(capturedOutputs) != 2 || capture.WorkflowOutput != scenario.ExpectedArtifact {
		return TaskSnapshot{}, errors.New("task inspector capture does not match public fixture")
	}
	outputs := []TaskOutput{}
	workflowDigest := latestSHA([]byte(capture.WorkflowOutput))
	workflowSequence := 0
	for _, event := range row.Trace {
		if event.Type == "oracle" && event.InputSHA256 == workflowDigest && event.OutputSHA256 == workflowDigest {
			workflowSequence = event.Sequence
		}
	}
	if workflowSequence == 0 {
		return TaskSnapshot{}, errors.New("release workflow output body is not trace-bound")
	}
	outputs = append(outputs, TaskOutput{AgentID: "orchestrator", Label: "Release decision", Disposition: "workflow_result", Body: capture.WorkflowOutput, SHA256: workflowDigest, EventSequence: workflowSequence})
	for childIndex, child := range scenario.ChildPrograms {
		captured, ok := capturedOutputs[child.ID]
		expectedDisposition := "discarded_branch"
		if childIndex == scenario.SelectedChild {
			expectedDisposition = "selected_branch"
		}
		if !ok || captured.Path != child.OutputPath || captured.Disposition != expectedDisposition || captured.Body != child.ExpectedResult {
			return TaskSnapshot{}, fmt.Errorf("captured release output for %s does not match the public fixture", child.ID)
		}
		bodyDigest := latestSHA([]byte(captured.Body))
		sequence := 0
		for _, event := range row.Trace {
			if event.AgentID != child.ID || event.Action != "agent.execute" || event.OutputSHA256 != bodyDigest {
				continue
			}
			for _, change := range event.WorkspaceChanges {
				if change.Path == child.OutputPath && change.AfterSHA256 == bodyDigest && change.Size == len([]byte(child.ExpectedResult)) {
					sequence = event.Sequence
				}
			}
		}
		if sequence == 0 {
			return TaskSnapshot{}, fmt.Errorf("release output body for %s is not trace-bound", child.ID)
		}
		label := "Dependency review"
		if child.ID == "reviewer" {
			label = "Release checklist"
		}
		outputs = append(outputs, TaskOutput{AgentID: child.ID, Label: label, Path: captured.Path, Disposition: captured.Disposition, Body: captured.Body, SHA256: bodyDigest, EventSequence: sequence})
	}
	snapshot := TaskSnapshot{
		SchemaVersion:    TaskSnapshotSchema,
		ID:               selected,
		Title:            "Prepare a release readiness review",
		Task:             scenario.Task,
		Status:           row.Status,
		ExpectedArtifact: scenario.ExpectedArtifact,
		ProviderIO:       capture.ProviderIO,
		Context: TaskContext{
			Files:                  scenario.Files,
			Analyses:               scenario.ChildAnalyses,
			RepeatedTransformation: scenario.RepeatedTransformation,
			WaitBoundary:           scenario.WaitBoundary,
			Observation:            scenario.Observation,
			SelectedChild:          scenario.SelectedChild,
		},
		Sources: sources,
		Outputs: outputs,
		Events:  row.Trace,
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
	if snapshot.SchemaVersion != TaskSnapshotSchema || !latestDigest.MatchString(snapshot.Identity) || snapshot.ID != "dev-release-readiness" || snapshot.Title == "" || snapshot.Task == "" || snapshot.Status != "passed" || snapshot.ProviderIO != composableacceptance.ProviderIONotApplicable || snapshot.ExpectedArtifact == "" || !taskPublicText(snapshot.Title+"\n"+snapshot.Task+"\n"+snapshot.ExpectedArtifact) || len(snapshot.Context.Files) < 1 || len(snapshot.Context.Analyses) != 2 || snapshot.Context.RepeatedTransformation == "" || snapshot.Context.WaitBoundary == "" || snapshot.Context.Observation == "" || snapshot.Context.SelectedChild < 0 || snapshot.Context.SelectedChild > 1 || len(snapshot.Sources) != 3 || len(snapshot.Events) < 10 {
		return errors.New("invalid task inspector envelope")
	}
	identity, err := taskSnapshotIdentity(snapshot)
	if err != nil || identity != snapshot.Identity {
		return errors.New("task inspector identity mismatch")
	}
	for _, file := range snapshot.Context.Files {
		if !taskRelativePath(file) {
			return errors.New("invalid task inspector context file")
		}
	}
	if !taskPublicText(strings.Join(snapshot.Context.Analyses, "\n") + "\n" + snapshot.Context.RepeatedTransformation + "\n" + snapshot.Context.WaitBoundary + "\n" + snapshot.Context.Observation) {
		return errors.New("invalid task inspector context")
	}
	sources := map[string]TaskSource{}
	expectedSources := []struct{ id, role, file string }{{"orchestrator", "orchestrator", "orchestrator.py"}, {"researcher", "dependency-reviewer", "researcher.py"}, {"reviewer", "release-reviewer", "reviewer.py"}}
	for index, source := range snapshot.Sources {
		expected := expectedSources[index]
		if source.ID != expected.id || source.Role != expected.role || source.File != expected.file || !taskID.MatchString(source.ID) || source.Source == "" || !taskPublicText(source.Source) || sources[source.ID].ID != "" {
			return errors.New("invalid task inspector source")
		}
		sources[source.ID] = source
	}
	if len(snapshot.Outputs) != 3 {
		return errors.New("task inspector outputs are incomplete")
	}
	outputs := map[string]TaskOutput{}
	outputSequences := map[int]TaskOutput{}
	selectedAgent := snapshot.Sources[snapshot.Context.SelectedChild+1].ID
	for _, output := range snapshot.Outputs {
		expectedDisposition := "discarded_branch"
		if output.AgentID == "orchestrator" {
			expectedDisposition = "workflow_result"
		} else if output.AgentID == selectedAgent {
			expectedDisposition = "selected_branch"
		}
		if sources[output.AgentID].ID == "" || output.Label == "" || output.Disposition != expectedDisposition || output.Body == "" || !taskPublicText(output.Label+"\n"+output.Body) || output.SHA256 != latestSHA([]byte(output.Body)) || output.EventSequence < 1 || outputs[output.AgentID].AgentID != "" || outputSequences[output.EventSequence].AgentID != "" || output.Path != "" && !taskRelativePath(output.Path) {
			return errors.New("invalid task inspector output")
		}
		outputs[output.AgentID] = output
		outputSequences[output.EventSequence] = output
	}
	sequences := map[int]bool{}
	spans := map[string]bool{}
	lastSequence := 0
	lastElapsed := float64(0)
	workspaceChanges := 0
	seenOutputs := map[string]bool{}
	agents := map[string]bool{}
	for _, event := range snapshot.Events {
		if event.Sequence <= lastSequence || event.SpanID == "" || spans[event.SpanID] || (!taskID.MatchString(event.AgentID) || (sources[event.AgentID].ID == "" && event.AgentID != "runtime")) || event.AgentRole == "" || event.StartedMillis < 0 || event.EndedMillis < event.StartedMillis || event.Type == "" || event.Action == "" || event.Outcome == "" || event.RelativeElapsedMillis < event.EndedMillis || event.RelativeElapsedMillis < lastElapsed || event.Count < 1 {
			return errors.New("invalid task inspector event")
		}
		if event.ParentSequence != 0 && !sequences[event.ParentSequence] {
			return errors.New("task inspector event parent is not earlier")
		}
		if event.ParentSpanID != "" && !spans[event.ParentSpanID] {
			return errors.New("task inspector event parent span is not earlier")
		}
		if event.ParentAgentID != "" && event.ParentAgentID != "runtime" && sources[event.ParentAgentID].ID == "" {
			return errors.New("task inspector event parent agent is invalid")
		}
		if !taskPublicText(event.SpanID+"\n"+event.ParentSpanID+"\n"+event.AgentRole+"\n"+event.WorkspaceID+"\n"+event.Type+"\n"+event.Action+"\n"+event.Outcome) || event.CheckpointSHA256 != "" && !latestDigest.MatchString(event.CheckpointSHA256) || event.InputSHA256 != "" && !latestDigest.MatchString(event.InputSHA256) || event.OutputSHA256 != "" && !latestDigest.MatchString(event.OutputSHA256) {
			return errors.New("invalid task inspector event body or digest")
		}
		if event.Source != nil {
			source := sources[event.Source.SourceID]
			if source.ID == "" || event.Source.File != source.File || event.Source.StartLine < 1 || event.Source.EndLine < event.Source.StartLine || event.Source.EndLine > len(strings.Split(source.Source, "\n")) {
				return errors.New("invalid task inspector source ref")
			}
		}
		if output := outputSequences[event.Sequence]; output.AgentID != "" {
			if output.Path == "" {
				if event.Type != "oracle" || event.InputSHA256 != output.SHA256 || event.OutputSHA256 != output.SHA256 {
					return errors.New("workflow output body is not bound to its recorded event")
				}
			} else {
				if event.AgentID != output.AgentID || event.OutputSHA256 != output.SHA256 {
					return errors.New("agent output body is not bound to its recorded event")
				}
				matchedChange := false
				for _, change := range event.WorkspaceChanges {
					matchedChange = matchedChange || change.Path == output.Path && change.AfterSHA256 == output.SHA256 && change.Size == len([]byte(output.Body))
				}
				if !matchedChange {
					return errors.New("workspace output body is not bound to its recorded change")
				}
			}
			seenOutputs[output.AgentID] = true
		}
		for _, change := range event.WorkspaceChanges {
			if !taskRelativePath(change.Path) || change.Kind == "" || !taskPublicText(change.Kind) || !latestDigest.MatchString(change.AfterSHA256) || change.Size < 0 {
				return errors.New("invalid task inspector workspace change")
			}
			workspaceChanges++
		}
		sequences[event.Sequence] = true
		spans[event.SpanID] = true
		lastSequence = event.Sequence
		lastElapsed = event.RelativeElapsedMillis
		agents[event.AgentID] = true
	}
	if len(agents) < 4 || len(seenOutputs) != len(snapshot.Outputs) || workspaceChanges != 2 || snapshot.Stats.Events != len(snapshot.Events) || snapshot.Stats.Agents != len(agents) || snapshot.Stats.WorkspaceChanges != workspaceChanges || snapshot.Stats.DurationMillis != snapshot.Events[len(snapshot.Events)-1].RelativeElapsedMillis {
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
