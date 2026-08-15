package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func main() {
	output := flag.String("output", "", "private trajectory JSON export")
	store := flag.String("store", "", "private trajectory object-store directory")
	sourceCommit := flag.String("source-commit", "", "40-character source commit")
	flag.Parse()
	if err := generate(*output, *store, *sourceCommit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(output, storePath, sourceCommit string) error {
	if output == "" || storePath == "" || !filepath.IsAbs(output) || !filepath.IsAbs(storePath) {
		return errors.New("absolute output and store paths are required")
	}
	store, err := labstore.Open(storePath, labstore.Options{})
	if err != nil {
		return err
	}
	defer store.Close()
	logPath := filepath.Join(filepath.Dir(storePath), "trajectory.jsonl")
	log, err := trajectory.Create(logPath, store, trajectory.SessionHeader{SessionID: "session-dev-trajectory-0001", SourceCommit: sourceCommit})
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = log.Close()
		}
	}()

	var elapsed uint64
	appendOne := func(input trajectory.EventInput) (trajectory.Event, error) {
		input.OccurredMillis = elapsed
		elapsed += 7
		return log.Append(input)
	}
	turn := "turn-dev-trajectory-0001"
	step1 := "step-dev-trajectory-0001"
	step2 := "step-dev-trajectory-0002"
	actor := "agent-primary-0001"

	session, err := appendOne(trajectory.EventInput{Type: trajectory.EventSessionStart, Source: trajectory.SourceHarness, ActorID: actor, Status: "running"})
	if err != nil {
		return err
	}
	turnStart, err := appendOne(trajectory.EventInput{Type: trajectory.EventTurnStart, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: session.EventID, TurnID: turn, Status: "running"})
	if err != nil {
		return err
	}
	system, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceSystem, turn, "", turnStart.EventID, true, labstore.KindPrompt, "You are a development agent. Inspect evidence before answering and use typed tools for workspace access."))
	if err != nil {
		return err
	}
	developer, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceDeveloper, turn, "", turnStart.EventID, true, labstore.KindPrompt, "Return a concise answer and preserve the Runtime authority boundary."))
	if err != nil {
		return err
	}
	memory, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceMemory, turn, "", turnStart.EventID, true, labstore.KindPrompt, "Project convention: generated evidence stays local until explicitly exported."))
	if err != nil {
		return err
	}
	skill, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceSkill, turn, "", turnStart.EventID, true, labstore.KindPrompt, "Trajectory rule: every model-visible context item must be present in the append-only session log."))
	if err != nil {
		return err
	}
	user, err := appendOne(bodyEvent(trajectory.EventUserMessage, trajectory.SourceUser, turn, "", turnStart.EventID, true, labstore.KindPrompt, "Inspect the project status and explain whether the trajectory contract is wired."))
	if err != nil {
		return err
	}
	header, err := appendOne(trajectory.EventInput{Type: trajectory.EventRequestHeader, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: user.EventID, TurnID: turn, ModelVisible: true, Body: []byte(`{"reason":"turn","system":"development fixture","tools":["workspace.status"]}`), BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"})
	if err != nil {
		return err
	}
	request1, err := appendOne(trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: header.EventID, TurnID: turn, StepID: step1, ContextEventIDs: []string{header.EventID, system.EventID, developer.EventID, memory.EventID, skill.EventID, user.EventID}, Provider: "scripted", Model: "development-fixture", Status: "completed", DurationNanos: 4_000_000, Usage: &trajectory.TokenUsage{Input: 108, Output: 46, Reasoning: 21}})
	if err != nil {
		return err
	}
	reasoningChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, turn, step1, request1.EventID, false, labstore.KindMetadataEvent, `{"text":"I need the repository status and the trajectory files before I can answer.","type":"reasoning-delta"}`))
	if err != nil {
		return err
	}
	reasoningInput := bodyEvent(trajectory.EventAssistantReasoning, trajectory.SourceModel, turn, step1, request1.EventID, false, labstore.KindProviderBody, "I need the repository status and the trajectory files before I can answer.")
	reasoningInput.SourceEventIDs = []string{reasoningChunk.EventID}
	reasoning, err := appendOne(reasoningInput)
	if err != nil {
		return err
	}
	leadChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, turn, step1, reasoning.EventID, false, labstore.KindMetadataEvent, `{"text":"I’ll inspect the repository state first.","type":"text-delta"}`))
	if err != nil {
		return err
	}
	leadInput := bodyEvent(trajectory.EventAssistantOutput, trajectory.SourceModel, turn, step1, reasoning.EventID, true, labstore.KindProviderBody, "I’ll inspect the repository state first.")
	leadInput.SourceEventIDs = []string{leadChunk.EventID}
	lead, err := appendOne(leadInput)
	if err != nil {
		return err
	}
	callChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, turn, step1, lead.EventID, false, labstore.KindMetadataEvent, `{"arguments":"{\"include_diff\":true,\"path\":\".\"}","call_id":"call-workspace-status-0001","name":"workspace.status","type":"tool-call-delta"}`))
	if err != nil {
		return err
	}
	call, err := appendOne(trajectory.EventInput{Type: trajectory.EventToolCall, Source: trajectory.SourceModel, ActorID: actor, ParentEventID: lead.EventID, TurnID: turn, StepID: step1, ModelVisible: true, SourceEventIDs: []string{callChunk.EventID}, ToolCallID: "call-workspace-status-0001", ToolName: "workspace.status", Body: []byte(`{"include_diff":true,"path":"."}`), BodyKind: labstore.KindToolPayload, ContentType: "application/json"})
	if err != nil {
		return err
	}
	runtimeRoot, err := appendOne(trajectory.EventInput{Type: trajectory.EventRuntime, Source: trajectory.SourceRuntime, ActorID: "agent-runtime-0001", ParentEventID: call.EventID, TurnID: turn, StepID: step1, ToolCallID: call.ToolCallID, ToolName: call.ToolName, RunID: "run-dev-trajectory-0001", LogicalRequestID: "logical-workspace-status-0001", PhysicalExecutionID: "physical-workspace-status-0001", SpanID: "span-pysolate-run-0001", Status: "started", Body: []byte(`{"authority":"host","backend":"wasi","phase":"run.start"}`), BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"})
	if err != nil {
		return err
	}
	_, err = appendOne(trajectory.EventInput{Type: trajectory.EventRuntime, Source: trajectory.SourceRuntime, ActorID: "agent-runtime-0001", ParentEventID: runtimeRoot.EventID, TurnID: turn, StepID: step1, ToolCallID: call.ToolCallID, ToolName: call.ToolName, RunID: runtimeRoot.RunID, LogicalRequestID: runtimeRoot.LogicalRequestID, PhysicalExecutionID: runtimeRoot.PhysicalExecutionID, SpanID: "span-host-tool-0001", Status: "completed", DurationNanos: 3_100_000, Body: []byte(`{"effect":"read","phase":"host.tool","terminal_disposition":"closed"}`), BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"})
	if err != nil {
		return err
	}
	_, err = appendOne(trajectory.EventInput{Type: trajectory.EventWorkspace, Source: trajectory.SourceWorkspace, ActorID: actor, ParentEventID: runtimeRoot.EventID, TurnID: turn, StepID: step1, ToolCallID: call.ToolCallID, ToolName: call.ToolName, Status: "unchanged", Body: []byte("No workspace files changed.\n"), BodyKind: labstore.KindFile, ContentType: "text/plain"})
	if err != nil {
		return err
	}
	result, err := appendOne(trajectory.EventInput{Type: trajectory.EventToolResult, Source: trajectory.SourceTool, ActorID: actor, ParentEventID: call.EventID, TurnID: turn, StepID: step1, ModelVisible: true, SourceEventIDs: []string{call.EventID}, ToolCallID: call.ToolCallID, ToolName: call.ToolName, Status: "completed", DurationNanos: 3_400_000, Body: []byte(`{"branch":"feat/unified-execution-profiles","clean":false,"trajectory_files":2}`), BodyKind: labstore.KindToolPayload, ContentType: "application/json"})
	if err != nil {
		return err
	}
	dispatch, err := appendOne(trajectory.EventInput{Type: trajectory.EventSubagentDispatch, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: result.EventID, TurnID: turn, StepID: step1, ChildSessionID: "session-reviewer-trajectory-0001", Status: "completed", Body: []byte("Review the append-only trajectory contract and report missing model-visible sources."), BodyKind: labstore.KindPrompt, ContentType: "text/plain"})
	if err != nil {
		return err
	}
	subagent, err := appendOne(trajectory.EventInput{Type: trajectory.EventSubagentResult, Source: trajectory.SourceSubagent, ActorID: "agent-reviewer-0001", ParentEventID: dispatch.EventID, TurnID: turn, StepID: step1, ChildSessionID: dispatch.ChildSessionID, ModelVisible: true, Status: "completed", Body: []byte("The fixture includes system, developer, memory, skill, user, reasoning, output, tool, Runtime and workspace sources."), BodyKind: labstore.KindProviderBody, ContentType: "text/plain"})
	if err != nil {
		return err
	}
	request2, err := appendOne(trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: subagent.EventID, TurnID: turn, StepID: step2, ContextEventIDs: []string{header.EventID, system.EventID, developer.EventID, memory.EventID, skill.EventID, user.EventID, lead.EventID, call.EventID, result.EventID, subagent.EventID}, Provider: "scripted", Model: "development-fixture", Status: "completed", DurationNanos: 3_000_000, Usage: &trajectory.TokenUsage{Input: 241, Output: 38, Reasoning: 14, CacheRead: 96}})
	if err != nil {
		return err
	}
	finalReasoningChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, turn, step2, request2.EventID, false, labstore.KindMetadataEvent, `{"text":"The status result and reviewer output are sufficient to answer with the exact boundary.","type":"reasoning-delta"}`))
	if err != nil {
		return err
	}
	finalReasoningInput := bodyEvent(trajectory.EventAssistantReasoning, trajectory.SourceModel, turn, step2, request2.EventID, false, labstore.KindProviderBody, "The status result and reviewer output are sufficient to answer with the exact boundary.")
	finalReasoningInput.SourceEventIDs = []string{finalReasoningChunk.EventID}
	finalReasoning, err := appendOne(finalReasoningInput)
	if err != nil {
		return err
	}
	finalChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, turn, step2, finalReasoning.EventID, false, labstore.KindMetadataEvent, `{"text":"The append-only trajectory contract is wired, while this checked-in dataset remains a scripted development fixture rather than a live provider run.","type":"text-delta"}`))
	if err != nil {
		return err
	}
	finalInput := bodyEvent(trajectory.EventAssistantOutput, trajectory.SourceModel, turn, step2, finalReasoning.EventID, false, labstore.KindProviderBody, "The append-only trajectory contract is wired, while this checked-in dataset remains a scripted development fixture rather than a live provider run.")
	finalInput.SourceEventIDs = []string{finalChunk.EventID}
	final, err := appendOne(finalInput)
	if err != nil {
		return err
	}
	turnEnd, err := appendOne(trajectory.EventInput{Type: trajectory.EventTurnEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: final.EventID, TurnID: turn, Status: "completed"})
	if err != nil {
		return err
	}
	_, err = appendOne(trajectory.EventInput{Type: trajectory.EventSessionEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: turnEnd.EventID, Status: "completed"})
	if err != nil {
		return err
	}
	if err := log.Validate(); err != nil {
		return err
	}
	exported, err := log.ExportPrivate()
	if err != nil {
		return err
	}
	if err := log.Close(); err != nil {
		return err
	}
	closed = true
	encoded, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(output, encoded, 0o600)
}

func bodyEvent(kind trajectory.EventType, source trajectory.Source, turn, step, parent string, visible bool, bodyKind labstore.Kind, body string) trajectory.EventInput {
	return trajectory.EventInput{Type: kind, Source: source, ActorID: "agent-primary-0001", ParentEventID: parent, TurnID: turn, StepID: step, ModelVisible: visible, Body: []byte(body), BodyKind: bodyKind, ContentType: "text/plain"}
}
