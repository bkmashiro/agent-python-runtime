package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

const (
	developmentModel      = "gpt-5.4"
	gpt41DevelopmentModel = "gpt-4.1"
	maxPythonPromptBytes  = 16 * 1024
)

func supportedDevelopmentModel(model string) bool {
	return model == developmentModel || model == gpt41DevelopmentModel
}

type PythonWorkflow interface {
	Execute(context.Context, string, string, uint32) (PythonRunResult, error)
	Close(context.Context) error
}

type PythonWorkflowFactory func(*ToolRuntime) (PythonWorkflow, error)

type ExecutionIdentity struct {
	RepositoryCommit          string `json:"repository_commit"`
	HostArtifactDigest        string `json:"host_artifact_digest"`
	DatasetManifestDigest     string `json:"dataset_manifest_digest"`
	ProviderCatalogDigest     string `json:"provider_catalog_digest"`
	ProviderCatalogObservedAt string `json:"provider_catalog_observed_at"`
	GuestArtifactDigest       string `json:"guest_artifact_digest,omitempty"`
	GuestProfile              string `json:"guest_profile,omitempty"`
}

type TrialResult struct {
	Version            string             `json:"version"`
	TrialID            string             `json:"trial_id"`
	SpecDigest         string             `json:"spec_digest"`
	TaskID             string             `json:"task_id"`
	TaskDigest         string             `json:"task_digest"`
	SourceRecordDigest string             `json:"source_record_digest"`
	Condition          Condition          `json:"condition"`
	Model              string             `json:"model"`
	Identity           ExecutionIdentity  `json:"identity"`
	Replicate          uint32             `json:"replicate"`
	Limits             TrialLimits        `json:"limits"`
	PromptDigest       string             `json:"prompt_digest"`
	SurfaceDigest      string             `json:"surface_digest"`
	Passed             bool               `json:"passed"`
	ErrorCode          string             `json:"error_code,omitempty"`
	ProviderAttempts   uint32             `json:"provider_attempts"`
	ProviderCalls      uint32             `json:"provider_calls"`
	ToolCalls          int                `json:"tool_calls"`
	PythonAttempts     uint32             `json:"python_attempts"`
	PythonRuns         uint32             `json:"python_runs"`
	Usage              provider.Usage     `json:"usage"`
	CatalogDigest      string             `json:"catalog_digest"`
	InitialStateDigest string             `json:"initial_state_digest,omitempty"`
	FinalStateDigest   string             `json:"final_state_digest,omitempty"`
	TextDigests        []string           `json:"text_digests,omitempty"`
	Exchanges          []ExchangeEvidence `json:"exchanges"`
	PythonEvidence     []PythonRunResult  `json:"python_evidence,omitempty"`
	StatelessScore     *CallScore         `json:"stateless_score,omitempty"`
	StatefulScore      *StatefulScore     `json:"stateful_score,omitempty"`
	RawDebug           *TrialRawDebug     `json:"-"`
}

type TrialRawDebug struct {
	DeveloperPrompt   string                `json:"developer_prompt"`
	ToolSurface       json.RawMessage       `json:"tool_surface"`
	ProviderExchanges []RawProviderExchange `json:"provider_exchanges"`
	PythonRuns        []RawPythonRun        `json:"python_runs,omitempty"`
	ToolCalls         [][]RawToolCall       `json:"tool_calls"`
}

type RawPythonRun struct {
	Turn          int             `json:"turn"`
	Code          string          `json:"code"`
	Observation   json.RawMessage `json:"observation,omitempty"`
	GuestRequest  json.RawMessage `json:"guest_request,omitempty"`
	GuestResponse json.RawMessage `json:"guest_response,omitempty"`
	Error         string          `json:"error,omitempty"`
}

type benchmarkMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func RunDevelopmentTrial(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	limits TrialLimits,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	return RunDevelopmentTrialReplicate(ctx, adapter, task, condition, 0, limits, pythonFactory)
}

func RunDevelopmentTrialReplicate(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	replicate uint32,
	limits TrialLimits,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	return RunDevelopmentTrialWithIdentity(ctx, adapter, task, condition, replicate, limits, ExecutionIdentity{}, pythonFactory)
}

func RunDevelopmentTrialWithIdentity(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	replicate uint32,
	limits TrialLimits,
	identity ExecutionIdentity,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	return RunDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, developmentModel, replicate, limits, identity, pythonFactory)
}

func RunDevelopmentTrialForModelWithIdentity(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	model string,
	replicate uint32,
	limits TrialLimits,
	identity ExecutionIdentity,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	return runDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, model, replicate, limits, identity, pythonFactory, false)
}

func RunDevelopmentDiagnosticTrialForModelWithIdentity(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	model string,
	replicate uint32,
	limits TrialLimits,
	identity ExecutionIdentity,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	return runDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, model, replicate, limits, identity, pythonFactory, true)
}

func runDevelopmentTrialForModelWithIdentity(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	model string,
	replicate uint32,
	limits TrialLimits,
	identity ExecutionIdentity,
	pythonFactory PythonWorkflowFactory,
	captureRaw bool,
) (TrialResult, error) {
	if task.Split != "dev" || !condition.valid() || !limits.valid() || replicate > 1000 ||
		!supportedDevelopmentModel(model) ||
		limits.MaxProviderCalls < uint32(len(task.Interaction.Turns)) ||
		(condition == ConditionDirect && pythonFactory != nil) ||
		(condition != ConditionDirect && pythonFactory == nil) {
		return TrialResult{}, ErrAgenticRun
	}
	tools, err := NewToolRuntime(task)
	if err != nil {
		return TrialResult{}, err
	}
	continueWithinTurn := task.Track == "stateful_local_tools" && condition != ConditionPython
	surface, mapping, prompt, err := buildConditionSurface(tools, condition, continueWithinTurn)
	if err != nil {
		return TrialResult{}, err
	}
	session, err := newResponsesSession(adapter, model, limits, captureRaw)
	if err != nil {
		return TrialResult{}, err
	}
	taskBytes, taskErr := json.Marshal(task)
	surfaceBytes, surfaceErr := json.Marshal(surface)
	if taskErr != nil || surfaceErr != nil {
		return TrialResult{}, ErrAgenticRun
	}
	taskDigest := digest(taskBytes)
	promptDigest, surfaceDigest := digest([]byte(prompt)), digest(surfaceBytes)
	specBytes, specErr := json.Marshal(map[string]any{
		"version": "agentic-development-trial-spec/v1", "task_digest": taskDigest,
		"condition": condition, "model": model, "replicate": replicate, "limits": limits, "identity": identity,
		"catalog_digest": tools.Snapshot().Digest(), "prompt_digest": promptDigest, "surface_digest": surfaceDigest,
	})
	if specErr != nil {
		return TrialResult{}, ErrAgenticRun
	}
	specDigest := digest(specBytes)
	result := TrialResult{
		Version: "agentic-development-trial/v1", TrialID: "dev_" + strings.TrimPrefix(specDigest, "sha256:")[:32],
		SpecDigest: specDigest, TaskID: task.ID, TaskDigest: taskDigest, SourceRecordDigest: task.Source.RecordSHA256,
		Condition: condition, Model: model, Identity: identity, Replicate: replicate, Limits: limits,
		PromptDigest: promptDigest, SurfaceDigest: surfaceDigest, CatalogDigest: tools.Snapshot().Digest(),
	}
	if captureRaw {
		result.RawDebug = &TrialRawDebug{DeveloperPrompt: prompt, ToolSurface: append(json.RawMessage(nil), surfaceBytes...)}
	}
	if tools.FileSystem() != nil {
		result.InitialStateDigest = tools.FileSystem().Digest()
	}
	var workflow PythonWorkflow
	if condition != ConditionDirect {
		workflow, err = pythonFactory(tools)
		if err != nil || workflow == nil {
			return TrialResult{}, ErrAgenticRun
		}
	}
	history := []any{map[string]any{"role": "developer", "content": prompt}}
	directOrdinal := 0
	providerAttemptsPerTurn := uint32(1)
	if continueWithinTurn {
		turns := uint32(len(task.Interaction.Turns))
		if turns == 0 || limits.MaxProviderCalls%turns != 0 {
			return TrialResult{}, ErrAgenticRun
		}
		providerAttemptsPerTurn = limits.MaxProviderCalls / turns
		if providerAttemptsPerTurn < 2 {
			return TrialResult{}, ErrAgenticRun
		}
	}
	for turnIndex, rawTurn := range task.Interaction.Turns {
		if err := tools.SetTurn(turnIndex); err != nil {
			return TrialResult{}, err
		}
		messages, err := decodeBenchmarkTurn(rawTurn)
		if err != nil {
			return TrialResult{}, err
		}
		for _, message := range messages {
			history = append(history, map[string]any{"role": message.Role, "content": message.Content})
		}
		turnHadCalls := false
		turnComplete := false
		pythonAttemptsThisTurn := uint32(0)
		for attempt := uint32(0); attempt < providerAttemptsPerTurn; attempt++ {
			toolChoice := "auto"
			if attempt == 0 {
				toolChoice = "required"
			}
			parsed, exchangeErr := session.Exchange(ctx, history, surface, toolChoice, condition != ConditionPython, mapping)
			if exchangeErr != nil {
				result.ErrorCode = classifyTrialError(exchangeErr)
				break
			}
			if parsed.TextDigest != "" {
				result.TextDigests = append(result.TextDigests, parsed.TextDigest)
			}
			if parsed.Refused {
				result.ErrorCode = "model_refusal"
				break
			}
			history = append(history, parsed.replayItems...)
			if len(parsed.Calls) == 0 {
				if !turnHadCalls {
					result.ErrorCode = "no_tool_calls"
				}
				turnComplete = result.ErrorCode == ""
				break
			}
			outputs := make([]any, 0, len(parsed.Calls))
			for _, call := range parsed.Calls {
				var observation json.RawMessage
				if call.CanonicalName == "run_python" {
					if workflow == nil || pythonAttemptsThisTurn >= 1 || result.PythonAttempts >= limits.MaxPythonRuns {
						result.ErrorCode = "python_run_budget_exceeded"
						break
					}
					var arguments struct {
						Code string `json:"code"`
					}
					if decodeStrict(call.Arguments, &arguments) != nil {
						result.ErrorCode = "invalid_python_arguments"
						break
					}
					usedCalls := countStatefulCalls(tools.Trace())
					if usedCalls >= int(limits.MaxToolCalls) {
						result.ErrorCode = "tool_call_budget_exceeded"
						break
					}
					pythonAttemptsThisTurn++
					result.PythonAttempts++
					rawPython := RawPythonRun{Turn: turnIndex, Code: arguments.Code}
					pythonResult, runErr := workflow.Execute(
						ctx, fmt.Sprintf("agentic-python-%d", result.PythonAttempts), arguments.Code,
						limits.MaxToolCalls-uint32(usedCalls),
					)
					if runErr != nil {
						if result.RawDebug != nil {
							rawPython.Error = runErr.Error()
							rawPython.GuestRequest = append(json.RawMessage(nil), pythonResult.RawRequest...)
							rawPython.GuestResponse = append(json.RawMessage(nil), pythonResult.RawResponse...)
							result.RawDebug.PythonRuns = append(result.RawDebug.PythonRuns, rawPython)
						}
						result.ErrorCode = "python_engine_failure"
						break
					}
					observation = append(json.RawMessage(nil), pythonResult.Observation...)
					if result.RawDebug != nil {
						rawPython.Observation = append(json.RawMessage(nil), pythonResult.Observation...)
						rawPython.GuestRequest = append(json.RawMessage(nil), pythonResult.RawRequest...)
						rawPython.GuestResponse = append(json.RawMessage(nil), pythonResult.RawResponse...)
						result.RawDebug.PythonRuns = append(result.RawDebug.PythonRuns, rawPython)
					}
					pythonResult.Observation = nil
					pythonResult.RawRequest = nil
					pythonResult.RawResponse = nil
					result.PythonEvidence = append(result.PythonEvidence, pythonResult)
					result.PythonRuns++
					afterCalls := countStatefulCalls(tools.Trace())
					if afterCalls < usedCalls || afterCalls-usedCalls != int(pythonResult.CapabilityCalls) || afterCalls > int(limits.MaxToolCalls) {
						result.ErrorCode = "python_trace_mismatch"
						break
					}
					if !pythonResult.Success {
						result.ErrorCode = "python_guest_error"
					}
				} else {
					if condition == ConditionPython || countStatefulCalls(tools.Trace()) >= int(limits.MaxToolCalls) {
						result.ErrorCode = "tool_call_budget_exceeded"
						break
					}
					directOrdinal++
					observation, err = tools.InvokeDirect(
						ctx, fmt.Sprintf("agentic-direct-%d", directOrdinal), fmt.Sprintf("host:%d", directOrdinal),
						call.CanonicalName, call.Arguments,
					)
					if err != nil {
						result.ErrorCode = "direct_host_call_failed"
						break
					}
				}
				if len(observation) == 0 || !json.Valid(observation) {
					result.ErrorCode = "invalid_tool_observation"
					break
				}
				outputs = append(outputs, map[string]any{
					"type": "function_call_output", "call_id": call.CallID, "output": string(observation),
				})
				if result.ErrorCode != "" {
					break
				}
			}
			history = append(history, outputs...)
			if result.ErrorCode != "" {
				break
			}
			turnHadCalls = true
			if !continueWithinTurn {
				turnComplete = true
				break
			}
		}
		if result.ErrorCode != "" {
			break
		}
		if !turnComplete {
			result.ErrorCode = "provider_turn_budget_exceeded"
			break
		}
	}
	if workflow != nil {
		if closeErr := workflow.Close(context.Background()); closeErr != nil {
			return result, closeErr
		}
	}
	result.ProviderAttempts = session.ProviderCalls()
	result.Exchanges = session.Evidence()
	result.ProviderCalls = uint32(len(result.Exchanges))
	result.Usage = session.Usage()
	trace := tools.Trace()
	result.ToolCalls = countStatefulCalls(trace)
	if result.RawDebug != nil {
		result.RawDebug.ProviderExchanges = session.RawExchanges()
		result.RawDebug.ToolCalls = tools.RawTrace()
	}
	if tools.FileSystem() != nil {
		result.FinalStateDigest = tools.FileSystem().Digest()
	}
	if task.Track == "stateless_function_calling" {
		calls := make([]FunctionCall, 0, result.ToolCalls)
		for _, turn := range trace {
			for _, call := range turn {
				calls = append(calls, FunctionCall{Name: call.Name, Arguments: call.Arguments})
			}
		}
		score := ScoreStatelessCalls(task, calls)
		result.StatelessScore = &score
		result.Passed = result.ErrorCode == "" && score.Passed
	} else {
		score, scoreErr := ScoreStateful(task, trace, tools.FileSystem())
		if scoreErr != nil {
			return result, scoreErr
		}
		result.StatefulScore = &score
		result.Passed = result.ErrorCode == "" && score.Passed
	}
	return result, nil
}

func decodeBenchmarkTurn(raw json.RawMessage) ([]benchmarkMessage, error) {
	var messages []benchmarkMessage
	if decodeStrict(raw, &messages) != nil || len(messages) == 0 || len(messages) > 32 {
		return nil, ErrDataset
	}
	for _, message := range messages {
		if message.Role != "user" || message.Content == "" || len([]byte(message.Content)) > maxResponseTextBytes {
			return nil, ErrDataset
		}
	}
	return messages, nil
}

func buildConditionSurface(runtime *ToolRuntime, condition Condition, continueWithinTurn bool) ([]map[string]any, map[string]string, string, error) {
	if runtime == nil || !condition.valid() {
		return nil, nil, "", ErrAgenticRun
	}
	surface := []map[string]any{}
	mapping := map[string]string{}
	if condition != ConditionPython {
		for _, tool := range runtime.Snapshot().Tools() {
			providerName, err := ProviderToolName(tool.ToolID)
			if err != nil || mapping[providerName] != "" {
				return nil, nil, "", ErrDataset
			}
			var parameters any
			if decodeUseNumber(tool.InputSchema, &parameters) != nil {
				return nil, nil, "", ErrDataset
			}
			description := tool.Description
			if tool.ToolID == "touch" {
				description += " Host semantics: returns an error if the file already exists; do not pre-check existence."
			} else if tool.ToolID == "echo" {
				description += " Host semantics: when file_name is provided, the target file must already exist; echo does not create missing files."
			}
			mapping[providerName] = tool.ToolID
			surface = append(surface, map[string]any{
				"type": "function", "name": providerName, "description": description,
				"parameters": parameters, "strict": false,
			})
		}
	}
	sdk := compactPythonSDK(runtime)
	prompt := "Use only the exposed tools and do not fabricate results. Do not add exploratory, precondition, or verification calls unless the user requests them or they are required to compute a later argument. Treat successful Host-tool observations as authoritative."
	if condition != ConditionDirect {
		prompt += " Each Python Guest run starts fresh, but Host-tool state persists across user turns. Do not replay state-changing setup already completed in earlier turns; use prior successful Host observations as current state unless the user asks to change it."
	}
	if condition == ConditionDirect {
		if continueWithinTurn {
			prompt += " Complete each user turn before moving to the next. Emit all calls whose arguments are already known together in dependency-safe order; the Host executes them in output order. Continue after tool output only when a later call requires returned data."
		} else {
			prompt += " Emit the complete tool plan for each user turn in one response. Return every required direct tool call in dependency order; the Host executes returned calls in output order."
		}
	}
	if condition != ConditionDirect {
		if mapping["run_python"] != "" {
			return nil, nil, "", ErrDataset
		}
		mapping["run_python"] = "run_python"
		surface = append(surface, map[string]any{
			"type": "function", "name": "run_python",
			"description": "Execute one bounded Python workflow in the CPython/WASI Guest. Import functions from host_tools; calls run in source order. A fresh Guest does not reset Host-tool state, including the current working directory; do not repeat setup already completed by earlier turns. Use returned JSON values in later calls. Only call Host tools required by the user; do not add discovery or verification calls unless required to compute a later argument. Assign a JSON object to result. Available SDK: " + sdk,
			"parameters": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"code"},
				"properties": map[string]any{"code": map[string]any{"type": "string", "minLength": 1, "maxLength": maxPythonCodeBytes}},
			},
			"strict": false,
		})
		prompt += " Python workflows import from host_tools and must assign a JSON object to result. Use the Available SDK in the run_python tool description."
		if condition == ConditionPython {
			prompt += " Emit exactly one run_python call per user turn. Put every required Host-tool operation, including dependencies, in that single Python workflow; never split the work across multiple run_python calls."
		}
	}
	if condition == ConditionHybrid {
		prompt += " Choose direct calls when every required argument is known before any tool runs; emit a single call, independent fan-out, or fixed ordered calls directly. Choose run_python when a later argument or control-flow decision depends on a Host-tool result, or the task requires iteration, branching, filtering, aggregation, or transformation inside the workflow. Do not choose run_python merely because there are multiple calls. Use at most one run_python call per user turn and put the complete Python workflow in that call."
		if continueWithinTurn {
			prompt += " For direct workflows, complete each user turn before moving to the next. Emit all calls whose arguments are already known together in dependency-safe order; the Host executes them in output order. Continue after tool output only when a later call requires returned data."
		} else {
			prompt += " If using direct calls, return every required call in dependency order in one response; the Host executes returned calls in output order."
		}
	}
	if len(surface) == 0 || len(surface) > maxFunctionCalls ||
		(condition != ConditionDirect && len(sdk) > maxPythonPromptBytes) || len(prompt) > maxPythonPromptBytes {
		return nil, nil, "", ErrAgenticRun
	}
	return surface, mapping, prompt, nil
}

func compactPythonSDK(runtime *ToolRuntime) string {
	parts := make([]string, 0, len(runtime.Snapshot().Tools()))
	for _, tool := range runtime.Snapshot().Tools() {
		parameters := make([]string, 0, len(tool.Parameters))
		hints := make([]string, 0, len(tool.Parameters))
		for _, parameter := range tool.Parameters {
			name := parameter.PythonName + ": " + parameter.Annotation
			if !parameter.Required {
				name += "=..."
			}
			parameters = append(parameters, name)
			if hint := compactParameterValueHint(runtime, tool.ToolID, parameter.Name); hint != "" {
				hints = append(hints, parameter.PythonName+": "+hint)
			}
		}
		entry := tool.PythonName + "(" + strings.Join(parameters, ", ") + ")"
		if len(hints) > 0 {
			entry += " [" + strings.Join(hints, "; ") + "]"
		}
		if tool.ToolID == "touch" {
			entry += " [returns an error if the file already exists; do not pre-check existence]"
		} else if tool.ToolID == "echo" {
			entry += " [when file_name is provided, the target file must already exist; echo does not create missing files]"
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, "; ")
}

func compactParameterValueHint(runtime *ToolRuntime, toolID, parameterName string) string {
	if runtime == nil || toolID == "" || parameterName == "" {
		return ""
	}
	for _, tool := range runtime.task.Tools {
		if tool.Name != toolID {
			continue
		}
		var schema map[string]any
		if decodeUseNumber(tool.Parameters, &schema) != nil {
			return ""
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return ""
		}
		property, ok := properties[parameterName].(map[string]any)
		if !ok {
			return ""
		}
		description, _ := property["description"].(string)
		description = strings.Join(strings.Fields(description), " ")
		if description == "" || len([]byte(description)) > 256 ||
			(strings.Count(description, "'") < 2 && !strings.Contains(description, "[Enum]")) {
			return ""
		}
		return description
	}
	return ""
}

func classifyTrialError(err error) string {
	switch {
	case errors.Is(err, ErrProviderIdentityMismatch):
		return "provider_identity_mismatch"
	case errors.Is(err, ErrUsageMissing):
		return "usage_missing"
	case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrBudgetClosed):
		return "provider_budget_exceeded"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "provider_or_protocol_failure"
	}
}
