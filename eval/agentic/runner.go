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
	developmentModel              = "gpt-5.4"
	gpt41DevelopmentModel         = "gpt-4.1"
	gpt4oDevelopmentModel         = "gpt-4o"
	gemini36FlashDevelopmentModel = "gemini-3.6-flash"
	grok420DevelopmentModel       = "grok-4.20-0309-non-reasoning"
	luna56DevelopmentModel        = "gpt-5.6-luna"
	maxPythonPromptBytes          = 16 * 1024
)

func supportedDevelopmentModel(model string) bool {
	return model == developmentModel || model == gpt41DevelopmentModel || model == gpt4oDevelopmentModel ||
		model == gemini36FlashDevelopmentModel || model == grok420DevelopmentModel || model == luna56DevelopmentModel
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

type FailureDetail struct {
	Class                 FailureClass `json:"class"`
	Turn                  int          `json:"turn"`
	CapabilityCallsBefore uint32       `json:"capability_calls_before_failure"`
	RetryEligible         bool         `json:"retry_eligible"`
}

type PythonRepairEvidence struct {
	Offered               bool         `json:"offered"`
	Attempted             bool         `json:"attempted"`
	Succeeded             bool         `json:"succeeded"`
	Turn                  int          `json:"turn"`
	OriginalFailureClass  FailureClass `json:"original_failure_class"`
	OriginalFailureDigest string       `json:"original_failure_digest"`
	CapabilityCallsBefore uint32       `json:"capability_calls_before_failure"`
}

func failureDetailForPython(result PythonRunResult) *FailureDetail {
	return &FailureDetail{
		Class: result.FailureClass, Turn: result.Turn, CapabilityCallsBefore: result.CapabilityCalls,
		RetryEligible: result.CapabilityCalls == 0 && result.FailureClass.valid() && result.FailureClass != FailureClassHostToolError,
	}
}

type TrialResult struct {
	Version            string                `json:"version"`
	TrialID            string                `json:"trial_id"`
	SpecDigest         string                `json:"spec_digest"`
	TaskID             string                `json:"task_id"`
	TaskDigest         string                `json:"task_digest"`
	SourceRecordDigest string                `json:"source_record_digest"`
	Condition          Condition             `json:"condition"`
	Model              string                `json:"model"`
	Identity           ExecutionIdentity     `json:"identity"`
	Replicate          uint32                `json:"replicate"`
	Limits             TrialLimits           `json:"limits"`
	PromptDigest       string                `json:"prompt_digest"`
	SurfaceDigest      string                `json:"surface_digest"`
	TreatmentID        string                `json:"treatment_id,omitempty"`
	TreatmentDigest    string                `json:"treatment_digest,omitempty"`
	Passed             bool                  `json:"passed"`
	Metrics            *TrialMetrics         `json:"metrics,omitempty"`
	ErrorCode          string                `json:"error_code,omitempty"`
	FailureDetail      *FailureDetail        `json:"failure_detail,omitempty"`
	Repair             *PythonRepairEvidence `json:"python_repair,omitempty"`
	Route              *HybridRouteDecision  `json:"hybrid_route,omitempty"`
	ProviderAttempts   uint32                `json:"provider_attempts"`
	ProviderCalls      uint32                `json:"provider_calls"`
	ToolCalls          int                   `json:"tool_calls"`
	PythonAttempts     uint32                `json:"python_attempts"`
	PythonRuns         uint32                `json:"python_runs"`
	Usage              provider.Usage        `json:"usage"`
	CatalogDigest      string                `json:"catalog_digest"`
	InitialStateDigest string                `json:"initial_state_digest,omitempty"`
	FinalStateDigest   string                `json:"final_state_digest,omitempty"`
	TextDigests        []string              `json:"text_digests,omitempty"`
	HostContextDigests []string              `json:"host_context_digests,omitempty"`
	Exchanges          []ExchangeEvidence    `json:"exchanges"`
	PythonEvidence     []PythonRunResult     `json:"python_evidence,omitempty"`
	StatelessScore     *CallScore            `json:"stateless_score,omitempty"`
	StatefulScore      *StatefulScore        `json:"stateful_score,omitempty"`
	RawDebug           *TrialRawDebug        `json:"-"`
}

type TrialRawDebug struct {
	DeveloperPrompt   string                `json:"developer_prompt"`
	ToolSurface       json.RawMessage       `json:"tool_surface"`
	ProviderExchanges []RawProviderExchange `json:"provider_exchanges"`
	PythonRuns        []RawPythonRun        `json:"python_runs,omitempty"`
	ToolCalls         [][]RawToolCall       `json:"tool_calls"`
	HostContexts      []json.RawMessage     `json:"host_contexts,omitempty"`
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
	return runDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, model, replicate, limits, identity, BaselineTreatment(), pythonFactory, false)
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
	return RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(ctx, adapter, task, condition, model, replicate, limits, identity, BaselineTreatment(), pythonFactory)
}

func RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
	ctx context.Context,
	adapter provider.Adapter,
	task Task,
	condition Condition,
	model string,
	replicate uint32,
	limits TrialLimits,
	identity ExecutionIdentity,
	treatment DevelopmentTreatment,
	pythonFactory PythonWorkflowFactory,
) (TrialResult, error) {
	if !treatment.Implemented() {
		return TrialResult{}, ErrDevelopmentTreatment
	}
	return runDevelopmentTrialForModelWithIdentity(ctx, adapter, task, condition, model, replicate, limits, identity, treatment, pythonFactory, true)
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
	treatment DevelopmentTreatment,
	pythonFactory PythonWorkflowFactory,
	captureRaw bool,
) (TrialResult, error) {
	if task.Split != "dev" || !condition.valid() || !limits.valid() || replicate > 1000 || !treatment.Implemented() ||
		!supportedDevelopmentModel(model) ||
		limits.MaxProviderCalls < uint32(len(task.Interaction.Turns)) {
		return TrialResult{}, ErrAgenticRun
	}
	tools, err := NewToolRuntime(task)
	if err != nil {
		return TrialResult{}, err
	}
	routeAware := treatment.UsesTwoStageRouter() && condition == ConditionHybrid
	if condition == ConditionDirect {
		if pythonFactory != nil {
			return TrialResult{}, ErrAgenticRun
		}
	} else if !routeAware && pythonFactory == nil {
		return TrialResult{}, ErrAgenticRun
	}

	executionCondition := condition
	continueWithinTurn := task.Track == "stateful_local_tools" && executionCondition != ConditionPython
	var (
		surface       []map[string]any
		mapping       map[string]string
		prompt        string
		promptDigest  string
		surfaceDigest string
		surfaceBytes  []byte
		directSurface []map[string]any
		directMapping map[string]string
		directPrompt  string
		pythonSurface []map[string]any
		pythonMapping map[string]string
		pythonPrompt  string
	)

	var directPromptDigest, directSurfaceDigest, pythonPromptDigest, pythonSurfaceDigest string
	var routerPromptDigest, routerSurfaceDigest string
	canExecute := true
	routeErrCode := ""
	routeChoice := (*HybridRouteDecision)(nil)

	if routeAware {
		directSurface, directMapping, directPrompt, err = buildConditionSurfaceForTreatment(tools, ConditionDirect, task.Track == "stateful_local_tools", treatment)
		if err != nil {
			return TrialResult{}, err
		}
		pythonSurface, pythonMapping, pythonPrompt, err = buildConditionSurfaceForTreatment(tools, ConditionPython, false, treatment)
		if err != nil {
			return TrialResult{}, err
		}
		directSurfaceBytes, err := json.Marshal(directSurface)
		if err != nil {
			return TrialResult{}, ErrAgenticRun
		}
		pythonSurfaceBytes, err := json.Marshal(pythonSurface)
		if err != nil {
			return TrialResult{}, ErrAgenticRun
		}
		directPromptDigest = digest([]byte(directPrompt))
		directSurfaceDigest = digest(directSurfaceBytes)
		pythonPromptDigest = digest([]byte(pythonPrompt))
		pythonSurfaceDigest = digest(pythonSurfaceBytes)

		_, _, rpPromptDigest, rpSurfaceDigest, err := buildHybridRouterContract(task)
		if err != nil {
			return TrialResult{}, err
		}
		routerPromptDigest, routerSurfaceDigest = rpPromptDigest, rpSurfaceDigest
		promptDigest, surfaceDigest = routerPromptDigest, routerSurfaceDigest
		surface = directSurface
		mapping = directMapping
		prompt = directPrompt
		continueWithinTurn = task.Track == "stateful_local_tools"

	} else {
		surface, mapping, prompt, err = buildConditionSurfaceForTreatment(tools, executionCondition, continueWithinTurn, treatment)
		if err != nil {
			return TrialResult{}, err
		}
		surfaceBytes, err = json.Marshal(surface)
		if err != nil {
			return TrialResult{}, ErrAgenticRun
		}
		promptDigest = digest([]byte(prompt))
		surfaceDigest = digest(surfaceBytes)
	}
	if err != nil {
		return TrialResult{}, err
	}
	session, err := newResponsesSession(adapter, model, limits, captureRaw)
	if err != nil {
		return TrialResult{}, err
	}
	executionMaxProviderCalls := limits.MaxProviderCalls
	if routeAware {
		if executionMaxProviderCalls < uint32(treatment.MaxRouterCallsPerHybridTrial) {
			return TrialResult{}, ErrAgenticRun
		}
		route, err := DecideHybridRoute(ctx, session, task)
		executionMaxProviderCalls -= uint32(treatment.MaxRouterCallsPerHybridTrial)
		if err != nil {
			routeErrCode = classifyTrialError(err)
			canExecute = false
		} else {
			switch route.Route {
			case HybridRouteDirect:
				if treatment.AllowsPythonRepair() {
					if executionMaxProviderCalls < treatment.MaxPythonRepairsPerTrial {
						return TrialResult{}, ErrAgenticRun
					}
					executionMaxProviderCalls -= treatment.MaxPythonRepairsPerTrial
				}
				executionCondition = ConditionDirect
				surface = directSurface
				mapping = directMapping
				prompt = directPrompt
				continueWithinTurn = task.Track == "stateful_local_tools"
				routeChoice = &HybridRouteDecision{
					Route: HybridRouteDirect, ReasonCode: route.ReasonCode,
					RouterPromptDigest: routerPromptDigest, RouterSurfaceDigest: routerSurfaceDigest,
					ExecutionPromptDigest: directPromptDigest, ExecutionSurfaceDigest: directSurfaceDigest,
					RouterUsage: route.RouterUsage,
				}
			case HybridRoutePython:
				executionCondition = ConditionPython
				surface = pythonSurface
				mapping = pythonMapping
				prompt = pythonPrompt
				continueWithinTurn = false
				routeChoice = &HybridRouteDecision{
					Route: HybridRoutePython, ReasonCode: route.ReasonCode,
					RouterPromptDigest: routerPromptDigest, RouterSurfaceDigest: routerSurfaceDigest,
					ExecutionPromptDigest: pythonPromptDigest, ExecutionSurfaceDigest: pythonSurfaceDigest,
					RouterUsage: route.RouterUsage,
				}
			default:
				routeErrCode = "provider_or_protocol_failure"
				canExecute = false
			}
		}
	}
	if routeAware {
		surfaceBytes, err = json.Marshal(surface)
		if err != nil {
			return TrialResult{}, ErrAgenticRun
		}
	}
	if promptDigest == "" {
		surfaceBytes, err := json.Marshal(surface)
		if err != nil {
			return TrialResult{}, ErrAgenticRun
		}
		promptDigest = digest([]byte(prompt))
		surfaceDigest = digest(surfaceBytes)
	}
	taskBytes, taskErr := json.Marshal(task)
	if taskErr != nil {
		return TrialResult{}, ErrAgenticRun
	}
	taskDigest := digest(taskBytes)

	baseSpec := map[string]any{
		"version": "agentic-development-trial-spec/v2", "task_digest": taskDigest,
		"condition": condition, "model": model, "replicate": replicate, "limits": limits, "identity": identity,
		"treatment_id": treatment.ID, "treatment_digest": treatment.Digest,
		"catalog_digest": tools.Snapshot().Digest(), "prompt_digest": promptDigest, "surface_digest": surfaceDigest,
	}
	if routeAware {
		baseSpec["router_prompt_digest"] = routerPromptDigest
		baseSpec["router_surface_digest"] = routerSurfaceDigest
		baseSpec["hybrid_direct_prompt_digest"] = directPromptDigest
		baseSpec["hybrid_direct_surface_digest"] = directSurfaceDigest
		baseSpec["hybrid_python_prompt_digest"] = pythonPromptDigest
		baseSpec["hybrid_python_surface_digest"] = pythonSurfaceDigest
	}
	specBytes, specErr := json.Marshal(baseSpec)
	if specErr != nil {
		return TrialResult{}, ErrAgenticRun
	}
	specDigest := digest(specBytes)
	result := TrialResult{
		Version: "agentic-development-trial/v3", TrialID: "dev_" + strings.TrimPrefix(specDigest, "sha256:")[:32],
		SpecDigest: specDigest, TaskID: task.ID, TaskDigest: taskDigest, SourceRecordDigest: task.Source.RecordSHA256,
		Condition: condition, Model: model, Identity: identity, Replicate: replicate, Limits: limits,
		PromptDigest: promptDigest, SurfaceDigest: surfaceDigest, TreatmentID: treatment.ID, TreatmentDigest: treatment.Digest,
		CatalogDigest: tools.Snapshot().Digest(), Route: routeChoice, ErrorCode: routeErrCode,
	}
	if captureRaw {
		result.RawDebug = &TrialRawDebug{DeveloperPrompt: prompt, ToolSurface: append(json.RawMessage(nil), surfaceBytes...)}
	}
	if tools.FileSystem() != nil {
		result.InitialStateDigest = tools.FileSystem().Digest()
	}
	if canExecute {
		var workflow PythonWorkflow
		if executionCondition != ConditionDirect {
			if pythonFactory == nil {
				result.ErrorCode = "provider_or_protocol_failure"
				canExecute = false
			} else {
				workflow, err = pythonFactory(tools)
				if err != nil || workflow == nil {
					return TrialResult{}, ErrAgenticRun
				}
			}
		}
		if canExecute {
			history := []any{map[string]any{"role": "developer", "content": prompt}}
			directOrdinal := 0
			repairUsed := false
			repairEnabled := treatment.AllowsPythonRepair() && executionCondition != ConditionDirect
			providerAttemptsPerTurn := uint32(1)
			if continueWithinTurn || repairEnabled {
				turns := uint32(len(task.Interaction.Turns))
				baseProviderCalls := executionMaxProviderCalls
				if repairEnabled {
					if baseProviderCalls == 0 {
						return TrialResult{}, ErrAgenticRun
					}
					baseProviderCalls--
				}
				if turns == 0 || baseProviderCalls%turns != 0 {
					return TrialResult{}, ErrAgenticRun
				}
				providerAttemptsPerTurn = baseProviderCalls / turns
				if repairEnabled {
					providerAttemptsPerTurn++
				}
				if providerAttemptsPerTurn < 2 {
					return TrialResult{}, ErrAgenticRun
				}
			}
			for turnIndex, rawTurn := range task.Interaction.Turns {
				if err := tools.SetTurn(turnIndex); err != nil {
					return TrialResult{}, err
				}
				if treatment.UsesStructuredHostContext() && turnIndex > 0 {
					_, contextBytes, contextErr := BuildHostContext(tools, turnIndex)
					if contextErr != nil {
						return TrialResult{}, contextErr
					}
					contextMessage := "Authoritative Host execution context from successful prior model effects. It contains no read observations, tool outputs, or initial directory contents. Do not repeat completed effects: " + string(contextBytes)
					history = append(history, map[string]any{"role": "developer", "content": contextMessage})
					result.HostContextDigests = append(result.HostContextDigests, digest(contextBytes))
					if result.RawDebug != nil {
						result.RawDebug.HostContexts = append(result.RawDebug.HostContexts, append(json.RawMessage(nil), contextBytes...))
					}
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
				repairPending := false
				for attempt := uint32(0); attempt < providerAttemptsPerTurn; attempt++ {
					repairOfferedThisAttempt := false
					toolChoice := "auto"
					if attempt == 0 {
						toolChoice = "required"
					}
					parsed, exchangeErr := session.Exchange(ctx, history, surface, toolChoice, executionCondition != ConditionPython, mapping)
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
						if repairPending {
							result.ErrorCode = "python_repair_not_attempted"
							break
						}
						if !turnHadCalls {
							result.ErrorCode = "no_tool_calls"
						}
						turnComplete = result.ErrorCode == ""
						break
					}
					if repairPending && (len(parsed.Calls) != 1 || parsed.Calls[0].CanonicalName != "run_python") {
						result.ErrorCode = "python_repair_protocol_error"
						break
					}
					outputs := make([]any, 0, len(parsed.Calls))
					for _, call := range parsed.Calls {
						var observation json.RawMessage
						pythonRunSucceeded := false
						if call.CanonicalName == "run_python" {
							isRepair := repairPending
							if workflow == nil || (pythonAttemptsThisTurn >= 1 && !isRepair) || result.PythonAttempts >= limits.MaxPythonRuns {
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
							if isRepair {
								if result.Repair == nil {
									return TrialResult{}, ErrAgenticRun
								}
								result.Repair.Attempted = true
								repairPending = false
							}
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
							pythonRunSucceeded = pythonResult.Success
							pythonResult.Turn = turnIndex
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
							if isRepair {
								if !pythonResult.Success {
									result.FailureDetail = failureDetailForPython(pythonResult)
									result.ErrorCode = "python_guest_error"
								}
							} else if !pythonResult.Success {
								detail := failureDetailForPython(pythonResult)
								if repairEnabled && !repairUsed && detail.RetryEligible && len(parsed.Calls) == 1 {
									formalBytes, marshalErr := json.Marshal(pythonResult)
									if marshalErr != nil {
										return TrialResult{}, ErrAgenticRun
									}
									result.Repair = &PythonRepairEvidence{
										Offered: true, Turn: turnIndex, OriginalFailureClass: pythonResult.FailureClass,
										OriginalFailureDigest: digest(formalBytes), CapabilityCallsBefore: pythonResult.CapabilityCalls,
									}
									repairUsed, repairPending, repairOfferedThisAttempt = true, true, true
								} else {
									result.FailureDetail = detail
									result.ErrorCode = "python_guest_error"
								}
							}
						} else {
							if executionCondition == ConditionPython || countStatefulCalls(tools.Trace()) >= int(limits.MaxToolCalls) {
								result.ErrorCode = "tool_call_budget_exceeded"
								break
							}
							directOrdinal++
							observation, err = tools.InvokeDirect(
								ctx, fmt.Sprintf("agentic-direct-%d", directOrdinal), fmt.Sprintf("host:%d", directOrdinal),
								call.CanonicalName, call.Arguments,
							)
							if err != nil {
								if errors.Is(err, ErrBenchmarkToolOperation) {
									result.ErrorCode = "host_application_error"
								} else {
									result.ErrorCode = "direct_host_call_failed"
								}
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
						if call.CanonicalName == "run_python" && result.Repair != nil && result.Repair.Attempted && !repairPending && pythonRunSucceeded {
							result.Repair.Succeeded = true
						}
						if result.ErrorCode != "" {
							break
						}
						if repairOfferedThisAttempt {
							break
						}
					}
					history = append(history, outputs...)
					if repairOfferedThisAttempt {
						history = append(history, map[string]any{
							"role":    "developer",
							"content": "The previous Python run failed before any Host capability call. Issue exactly one corrected run_python call now. This is the only repair attempt; do not call direct Host tools or emit multiple calls.",
						})
						turnHadCalls = true
						continue
					}
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
		}
	}
	result.ProviderAttempts = session.ProviderCalls()
	result.Exchanges = session.Evidence()
	result.ProviderCalls = uint32(len(result.Exchanges))
	result.Usage = session.Usage()
	if result.Route != nil {
		routerUsage := result.Route.RouterUsage
		if routerUsage.InputTokens > result.Usage.InputTokens || routerUsage.OutputTokens > result.Usage.OutputTokens || routerUsage.TotalTokens > result.Usage.TotalTokens {
			return result, ErrAgenticRun
		}
		result.Route.ExecutionUsage = provider.Usage{
			InputTokens:  result.Usage.InputTokens - routerUsage.InputTokens,
			OutputTokens: result.Usage.OutputTokens - routerUsage.OutputTokens,
			TotalTokens:  result.Usage.TotalTokens - routerUsage.TotalTokens,
		}
	}
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
	metrics, metricsErr := DeriveTrialMetrics(result)
	if metricsErr != nil {
		return result, metricsErr
	}
	result.Metrics = &metrics
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
	return buildConditionSurfaceForTreatment(runtime, condition, continueWithinTurn, BaselineTreatment())
}

func buildConditionSurfaceForTreatment(runtime *ToolRuntime, condition Condition, continueWithinTurn bool, treatment DevelopmentTreatment) ([]map[string]any, map[string]string, string, error) {
	if runtime == nil || !condition.valid() {
		return nil, nil, "", ErrAgenticRun
	}
	if !treatment.Implemented() {
		return nil, nil, "", ErrDevelopmentTreatment
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
		pythonDescription := "Execute one bounded Python workflow in the CPython/WASI Guest. Import functions from host_tools; calls run in source order. A fresh Guest does not reset Host-tool state, including the current working directory; do not repeat setup already completed by earlier turns. Use returned JSON values in later calls. Only call Host tools required by the user; do not add discovery or verification calls unless required to compute a later argument. Assign a JSON object to result. Available SDK: " + sdk
		if treatment.UsesPreboundCompactPython() {
			pythonDescription = "Execute one bounded Python workflow in the CPython/WASI Guest. Authorized Host functions in Available SDK are prebound; call them directly in source order and do not import host_tools. A fresh Guest does not reset Host-tool state, including the current working directory; do not repeat setup already completed by earlier turns. Use returned JSON values only when later calls depend on them. result defaults to {}; assign it only when workflow output is required. Emit bare calls when return values are unused. Only call Host tools required by the user; do not add discovery or verification calls unless required to compute a later argument. Available SDK: " + sdk
		}
		surface = append(surface, map[string]any{
			"type": "function", "name": "run_python",
			"description": pythonDescription,
			"parameters": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"code"},
				"properties": map[string]any{"code": map[string]any{"type": "string", "minLength": 1, "maxLength": maxPythonCodeBytes}},
			},
			"strict": false,
		})
		if treatment.UsesPreboundCompactPython() {
			prompt += " Python runs in an isolated WASI Guest: the Host filesystem is not available through open, pathlib, os, subprocess, or shell commands. Authorized Available SDK functions are prebound; call them directly and do not import host_tools. result defaults to {}; assign it only when workflow output is required. Emit executable code only: no prose or comments, no unused return bindings, and bare calls when return values are unused. Use loops only when they preserve exact call order and arguments. Use returned JSON values only for dependent arguments."
		} else {
			prompt += " Python runs in an isolated WASI Guest: the Host filesystem is not available through open, pathlib, os, subprocess, or shell commands. Import every Host operation from host_tools and use only the Available SDK in the run_python description; use returned JSON values for dependent arguments. Assign a JSON object to result."
		}
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
	case errors.Is(err, ErrProviderOutputLimitExceeded):
		return "provider_output_limit_exceeded"
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
