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
	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func main() {
	evidence := flag.String("evidence", "", "sealed workflow evidence v1")
	store := flag.String("store", "", "private Labstore root")
	output := flag.String("output", "", "sealed private trajectory export")
	sourceCommit := flag.String("source-commit", "", "exact converter source commit")
	flag.Parse()
	if err := generate(*evidence, *store, *output, *sourceCommit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(evidencePath, storePath, outputPath, sourceCommit string) error {
	if !filepath.IsAbs(evidencePath) || !filepath.IsAbs(storePath) || !filepath.IsAbs(outputPath) || len(sourceCommit) != 40 {
		return errors.New("absolute paths and exact source commit are required")
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		return err
	}
	evidence, err := workflowbench.DecodeEvidence(raw)
	if err != nil {
		return err
	}
	manifest, err := workflowbench.DecodeManifest(evidence.Manifest)
	if err != nil {
		return err
	}
	store, err := labstore.Open(storePath, labstore.Options{})
	if err != nil {
		return err
	}
	defer store.Close()
	logPath := filepath.Join(filepath.Dir(storePath), "workflow-trajectory.jsonl")
	log, err := trajectory.Create(logPath, store, trajectory.SessionHeader{SessionID: "session-workflow-experiment-0001", SourceCommit: sourceCommit})
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
		elapsed += 3
		event, err := log.Append(input)
		if err != nil {
			return trajectory.Event{}, fmt.Errorf("append %s tool_call=%s: %w", input.Type, input.ToolCallID, err)
		}
		return event, nil
	}
	actor := "agent-workflow-harness-0001"
	session, err := appendOne(trajectory.EventInput{Type: trajectory.EventSessionStart, Source: trajectory.SourceHarness, ActorID: actor, Status: "running"})
	if err != nil {
		return err
	}
	system, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceSystem, actor, session.EventID, "", "", true, "You are the deterministic workflow benchmark planner. Execute only the sealed prepared task and report measured evidence.", labstore.KindPrompt))
	if err != nil {
		return err
	}
	developer, err := appendOne(bodyEvent(trajectory.EventContext, trajectory.SourceDeveloper, actor, session.EventID, "", "", true, "Treatments use balanced seeded AB/BA order. Record wall duration, process user+system CPU, physical executions and observable equivalence.", labstore.KindPrompt))
	if err != nil {
		return err
	}
	user, err := appendOne(bodyEvent(trajectory.EventUserMessage, trajectory.SourceUser, actor, session.EventID, "", "", true, "Run the sealed 14-task workflow-boundary experiment and inspect every task trajectory.", labstore.KindPrompt))
	if err != nil {
		return err
	}
	headerBody, err := canonicalJSON(map[string]any{"reason": "turn", "system": "workflow benchmark fixture", "tools": []string{"workflowbench.execute_pair"}})
	if err != nil {
		return err
	}
	header, err := appendOne(trajectory.EventInput{Type: trajectory.EventRequestHeader, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: user.EventID, ModelVisible: true, Body: headerBody, BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"})
	if err != nil {
		return err
	}

	resultIDs := make([]string, 0, len(evidence.Tasks))
	for index, metrics := range evidence.Tasks {
		turnID := fmt.Sprintf("turn-workflow-%04d", index+1)
		stepID := fmt.Sprintf("step-workflow-%04d", index+1)
		turn, err := appendOne(trajectory.EventInput{Type: trajectory.EventTurnStart, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: session.EventID, TurnID: turnID, Status: "running"})
		if err != nil {
			return err
		}
		step, err := appendOne(trajectory.EventInput{Type: trajectory.EventStepStart, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: turn.EventID, TurnID: turnID, StepID: stepID, Status: "running"})
		if err != nil {
			return err
		}
		request, err := appendOne(trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: step.EventID, TurnID: turnID, StepID: stepID, ContextEventIDs: []string{header.EventID, system.EventID, developer.EventID, user.EventID}, Provider: "scripted", Model: "workflowbench-fixture", Status: "completed"})
		if err != nil {
			return err
		}
		reasoning := fmt.Sprintf("Prepared task %s has class %s and treatment order %s. Execute both arms without inferred scheduling.", metrics.TaskID, metrics.Class, metrics.TreatmentOrder)
		reasoningChunkBody, err := canonicalJSON(map[string]any{"text": reasoning, "type": "reasoning-delta"})
		if err != nil {
			return err
		}
		reasoningChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, actor, request.EventID, turnID, stepID, false, string(reasoningChunkBody), labstore.KindMetadataEvent))
		if err != nil {
			return err
		}
		reasoningInput := bodyEvent(trajectory.EventAssistantReasoning, trajectory.SourceModel, actor, request.EventID, turnID, stepID, false, reasoning, labstore.KindProviderBody)
		reasoningInput.SourceEventIDs = []string{reasoningChunk.EventID}
		reasoningEvent, err := appendOne(reasoningInput)
		if err != nil {
			return err
		}
		outputText := "I will invoke the sealed paired-treatment tool and inspect its measured Runtime evidence."
		outputChunkBody, err := canonicalJSON(map[string]any{"text": outputText, "type": "text-delta"})
		if err != nil {
			return err
		}
		outputChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, actor, reasoningEvent.EventID, turnID, stepID, false, string(outputChunkBody), labstore.KindMetadataEvent))
		if err != nil {
			return err
		}
		outputInput := bodyEvent(trajectory.EventAssistantOutput, trajectory.SourceModel, actor, reasoningEvent.EventID, turnID, stepID, true, outputText, labstore.KindProviderBody)
		outputInput.SourceEventIDs = []string{outputChunk.EventID}
		outputEvent, err := appendOne(outputInput)
		if err != nil {
			return err
		}
		taskBody, err := canonicalJSON(manifest.Tasks[index])
		if err != nil {
			return err
		}
		callChunkBody, err := canonicalJSON(map[string]any{"arguments": string(taskBody), "call_id": fmt.Sprintf("call-workflow-%04d", index+1), "name": "workflowbench.execute_pair", "type": "tool-call-delta"})
		if err != nil {
			return err
		}
		callChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, actor, outputEvent.EventID, turnID, stepID, false, string(callChunkBody), labstore.KindMetadataEvent))
		if err != nil {
			return err
		}
		call, err := appendOne(trajectory.EventInput{Type: trajectory.EventToolCall, Source: trajectory.SourceModel, ActorID: actor, ParentEventID: outputEvent.EventID, TurnID: turnID, StepID: stepID, ModelVisible: true, SourceEventIDs: []string{callChunk.EventID}, ToolCallID: fmt.Sprintf("call-workflow-%04d", index+1), ToolName: "workflowbench.execute_pair", Status: "started", Body: taskBody, BodyKind: labstore.KindToolPayload, ContentType: "application/json"})
		if err != nil {
			return err
		}

		verified, err := observe.DecodeOptimizationReport(evidence.Reports[index])
		if err != nil {
			return err
		}
		report, err := verified.Report()
		if err != nil {
			return err
		}
		runEvents := map[string]trajectory.Event{}
		for _, run := range report.Runs {
			cpu, wall := metrics.BaselineCPUTimeNanos, metrics.BaselineDurationNanos
			if run.Treatment == "optimized" {
				cpu, wall = metrics.OptimizedCPUTimeNanos, metrics.OptimizedDurationNanos
			}
			body, err := canonicalJSON(map[string]any{"treatment": run.Treatment, "order": run.Order, "wall_nanos": wall, "cpu_nanos": cpu, "cpu_accounting": evidence.CPUAccounting})
			if err != nil {
				return err
			}
			event, err := appendOne(trajectory.EventInput{Type: trajectory.EventRuntime, Source: trajectory.SourceRuntime, ActorID: actor, ParentEventID: call.EventID, TurnID: turnID, StepID: stepID, ToolCallID: call.ToolCallID, ToolName: call.ToolName, RunID: run.RunID, Status: "completed", DurationNanos: wall, Body: body, BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"})
			if err != nil {
				return err
			}
			runEvents[run.RunID] = event
		}
		for _, physical := range report.PhysicalExecutions {
			parent := runEvents[physical.RunID]
			body, err := canonicalJSON(physical)
			if err != nil {
				return err
			}
			if _, err = appendOne(trajectory.EventInput{Type: trajectory.EventRuntime, Source: trajectory.SourceRuntime, ActorID: actor, ParentEventID: parent.EventID, TurnID: turnID, StepID: stepID, ToolCallID: call.ToolCallID, ToolName: call.ToolName, RunID: physical.RunID, LogicalRequestID: physical.ProducerLogicalRequestID, PhysicalExecutionID: physical.PhysicalExecutionID, Status: "completed", DurationNanos: physical.EndedNanos - physical.StartedNanos, Body: body, BodyKind: labstore.KindMetadataEvent, ContentType: "application/json"}); err != nil {
				return err
			}
		}
		workspace := bodyEvent(trajectory.EventWorkspace, trajectory.SourceWorkspace, actor, call.EventID, turnID, stepID, false, "Read-only fixture: no workspace files changed.\n", labstore.KindFile)
		workspace.ToolCallID, workspace.ToolName = call.ToolCallID, call.ToolName
		if _, err = appendOne(workspace); err != nil {
			return err
		}
		metricsBody, err := canonicalJSON(metrics)
		if err != nil {
			return err
		}
		result, err := appendOne(trajectory.EventInput{Type: trajectory.EventToolResult, Source: trajectory.SourceTool, ActorID: actor, ParentEventID: call.EventID, TurnID: turnID, StepID: stepID, ModelVisible: true, SourceEventIDs: []string{call.EventID}, ToolCallID: call.ToolCallID, ToolName: call.ToolName, Status: "completed", DurationNanos: metrics.BaselineDurationNanos + metrics.OptimizedDurationNanos, Body: metricsBody, BodyKind: labstore.KindToolPayload, ContentType: "application/json"})
		if err != nil {
			return err
		}
		resultIDs = append(resultIDs, result.EventID)
		stepEnd, err := appendOne(trajectory.EventInput{Type: trajectory.EventStepEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: result.EventID, TurnID: turnID, StepID: stepID, Status: "completed"})
		if err != nil {
			return err
		}
		if _, err = appendOne(trajectory.EventInput{Type: trajectory.EventTurnEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: stepEnd.EventID, TurnID: turnID, Status: "completed"}); err != nil {
			return err
		}
	}

	finalTurnID, finalStepID := "turn-workflow-summary", "step-workflow-summary"
	finalTurn, err := appendOne(trajectory.EventInput{Type: trajectory.EventTurnStart, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: session.EventID, TurnID: finalTurnID, Status: "running"})
	if err != nil {
		return err
	}
	finalStep, err := appendOne(trajectory.EventInput{Type: trajectory.EventStepStart, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: finalTurn.EventID, TurnID: finalTurnID, StepID: finalStepID, Status: "running"})
	if err != nil {
		return err
	}
	contextIDs := append([]string{header.EventID, system.EventID, developer.EventID, user.EventID}, resultIDs...)
	finalRequest, err := appendOne(trajectory.EventInput{Type: trajectory.EventModelRequest, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: finalStep.EventID, TurnID: finalTurnID, StepID: finalStepID, ContextEventIDs: contextIDs, Provider: "scripted", Model: "workflowbench-fixture", Status: "completed"})
	if err != nil {
		return err
	}
	summary := fmt.Sprintf("Completed %d paired tasks with %d divergences. Physical executions: %d baseline, %d optimized. Process CPU: %d ns baseline, %d ns optimized.", len(evidence.Tasks), evidence.Divergences, evidence.BaselinePhysicalExecutions, evidence.OptimizedPhysicalExecutions, evidence.BaselineCPUTimeNanos, evidence.OptimizedCPUTimeNanos)
	finalReasoningText := "All task results are present in the exact final model context; summarize without inferring unmeasured effects."
	finalReasoningChunkBody, err := canonicalJSON(map[string]any{"text": finalReasoningText, "type": "reasoning-delta"})
	if err != nil {
		return err
	}
	finalReasoningChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, actor, finalRequest.EventID, finalTurnID, finalStepID, false, string(finalReasoningChunkBody), labstore.KindMetadataEvent))
	if err != nil {
		return err
	}
	finalReasoningInput := bodyEvent(trajectory.EventAssistantReasoning, trajectory.SourceModel, actor, finalRequest.EventID, finalTurnID, finalStepID, false, finalReasoningText, labstore.KindProviderBody)
	finalReasoningInput.SourceEventIDs = []string{finalReasoningChunk.EventID}
	finalReasoning, err := appendOne(finalReasoningInput)
	if err != nil {
		return err
	}
	finalChunkBody, err := canonicalJSON(map[string]any{"text": summary, "type": "text-delta"})
	if err != nil {
		return err
	}
	finalChunk, err := appendOne(bodyEvent(trajectory.EventAssistantChunk, trajectory.SourceModel, actor, finalReasoning.EventID, finalTurnID, finalStepID, false, string(finalChunkBody), labstore.KindMetadataEvent))
	if err != nil {
		return err
	}
	finalOutputInput := bodyEvent(trajectory.EventAssistantOutput, trajectory.SourceModel, actor, finalReasoning.EventID, finalTurnID, finalStepID, false, summary, labstore.KindProviderBody)
	finalOutputInput.SourceEventIDs = []string{finalChunk.EventID}
	finalOutput, err := appendOne(finalOutputInput)
	if err != nil {
		return err
	}
	finalStepEnd, err := appendOne(trajectory.EventInput{Type: trajectory.EventStepEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: finalOutput.EventID, TurnID: finalTurnID, StepID: finalStepID, Status: "completed"})
	if err != nil {
		return err
	}
	finalTurnEnd, err := appendOne(trajectory.EventInput{Type: trajectory.EventTurnEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: finalStepEnd.EventID, TurnID: finalTurnID, Status: "completed"})
	if err != nil {
		return err
	}
	if _, err = appendOne(trajectory.EventInput{Type: trajectory.EventSessionEnd, Source: trajectory.SourceHarness, ActorID: actor, ParentEventID: finalTurnEnd.EventID, Status: "completed"}); err != nil {
		return err
	}
	if err := log.Close(); err != nil {
		return err
	}
	closed = true
	exported, err := log.ExportPrivate()
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, append(encoded, '\n'), 0o600)
}

func bodyEvent(kind trajectory.EventType, source trajectory.Source, actor, parent, turn, step string, modelVisible bool, body string, bodyKind labstore.Kind) trajectory.EventInput {
	contentType := "text/plain"
	return trajectory.EventInput{Type: kind, Source: source, ActorID: actor, ParentEventID: parent, TurnID: turn, StepID: step, ModelVisible: modelVisible, Body: []byte(body), BodyKind: bodyKind, ContentType: contentType}
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}
