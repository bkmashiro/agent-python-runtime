package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const maxPythonCodeBytes = 64 * 1024

const compactPythonWrapperJSON = `{"version":"agentic-python-wrapper/v2","tool_binding":"prebound-authorized-tools","default_result":"empty-object","execution":"single-nested-compile-exec","filename":"<agent-model>"}`

var pythonRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type FailureClass string

const (
	FailureClassPythonException           FailureClass = "python_exception"
	FailureClassHostToolError             FailureClass = "host_tool_error"
	FailureClassGuestOutputSchemaMismatch FailureClass = "guest_output_schema_mismatch"
	FailureClassGuestContractError        FailureClass = "guest_contract_error"
)

func (class FailureClass) valid() bool {
	switch class {
	case FailureClassPythonException, FailureClassHostToolError, FailureClassGuestOutputSchemaMismatch, FailureClassGuestContractError:
		return true
	default:
		return false
	}
}

type PythonRunResult struct {
	Turn                int                         `json:"turn"`
	Success             bool                        `json:"success"`
	ErrorCode           string                      `json:"error_code,omitempty"`
	FailureClass        FailureClass                `json:"failure_class,omitempty"`
	CapabilityCalls     uint32                      `json:"capability_calls"`
	RequestDigest       string                      `json:"request_digest"`
	ResponseDigest      string                      `json:"response_digest,omitempty"`
	ResultDigest        string                      `json:"result_digest,omitempty"`
	ModelCodeDigest     string                      `json:"model_code_digest,omitempty"`
	EffectiveCodeDigest string                      `json:"effective_code_digest,omitempty"`
	WrapperDigest       string                      `json:"wrapper_digest,omitempty"`
	ExecutionRef        *runtimeconfig.ExecutionRef `json:"execution_ref,omitempty"`
	Backend             string                      `json:"backend"`
	ResetMode           engine.ResetMode            `json:"reset_mode"`
	Observation         json.RawMessage             `json:"-"`
	RawRequest          json.RawMessage             `json:"-"`
	RawResponse         json.RawMessage             `json:"-"`
}

type PythonExecutor struct {
	mu              sync.Mutex
	runner          engine.Runner
	tools           *ToolRuntime
	prepare         string
	controller      *workflowBrokerController
	compactPrebound bool
	anyJSONResult   bool
	wrapperDigest   string
}

type workflowBrokerController struct {
	mu       sync.Mutex
	active   bool
	runID    string
	maxCalls uint32
	tools    *ToolRuntime
}

func NewPythonExecutor(runner engine.Runner, tools *ToolRuntime) (*PythonExecutor, error) {
	return newPythonExecutor(runner, tools, BaselineTreatment())
}

func NewPythonExecutorForTreatment(runner engine.Runner, tools *ToolRuntime, treatment DevelopmentTreatment) (*PythonExecutor, error) {
	return newPythonExecutor(runner, tools, treatment)
}

func newPythonExecutor(runner engine.Runner, tools *ToolRuntime, treatment DevelopmentTreatment) (*PythonExecutor, error) {
	if runner == nil || tools == nil {
		return nil, ErrAgenticRun
	}
	if !treatment.Implemented() {
		return nil, ErrDevelopmentTreatment
	}
	properties := runner.Properties()
	if properties.Validate() != nil || properties.ResetMode != engine.ResetModeFreshInstance ||
		properties.ActiveStrategy != engine.StrategyFreshInstance || properties.Fallback {
		return nil, ErrAgenticRun
	}
	prepare, err := tools.TrustedPrepare()
	if treatment.UsesPreboundCompactPython() {
		prepare, err = tools.TrustedPrepareWithPreboundTools()
	}
	if err != nil || prepare == "" {
		return nil, ErrAgenticRun
	}
	executor := &PythonExecutor{
		runner: runner, tools: tools, prepare: prepare,
		compactPrebound: treatment.UsesPreboundCompactPython(), anyJSONResult: treatment.AllowsAnyJSONPythonResult(),
	}
	if executor.compactPrebound {
		executor.wrapperDigest = compactPythonWrapperDigest()
	}
	return executor, nil
}

func NewWASIPythonExecutor(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, tools *ToolRuntime) (*PythonExecutor, error) {
	return NewWASIPythonExecutorForTreatment(ctx, wasm, config, tools, BaselineTreatment())
}

func NewWASIPythonExecutorForTreatment(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, tools *ToolRuntime, treatment DevelopmentTreatment) (*PythonExecutor, error) {
	if len(wasm) < 8 || tools == nil {
		return nil, ErrAgenticRun
	}
	controller := &workflowBrokerController{tools: tools}
	factory := wazeroengine.Factory{BrokerFactory: controller.newBroker}
	runner, err := factory.New(ctx, wasm, config)
	if err != nil {
		return nil, err
	}
	executor, err := NewPythonExecutorForTreatment(runner, tools, treatment)
	if err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	executor.controller = controller
	return executor, nil
}

func compactPythonWrapperDigest() string {
	return digest([]byte(compactPythonWrapperJSON))
}

func compactEffectivePythonCode(modelCode string) (string, error) {
	if !utf8.ValidString(modelCode) {
		return "", ErrAgenticRun
	}
	quoted, err := json.Marshal(modelCode)
	if err != nil {
		return "", ErrAgenticRun
	}
	return "result = {}\nexec(compile(" + string(quoted) + `, "<agent-model>", "exec"), globals(), globals())`, nil
}

func (controller *workflowBrokerController) newBroker(context.Context) (*capability.Broker, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !controller.active || controller.tools == nil {
		return nil, errors.New("Python workflow broker requested outside an active Run")
	}
	return controller.tools.NewWorkflowBroker(controller.runID, controller.maxCalls)
}

func (controller *workflowBrokerController) begin(runID string, maxCalls uint32) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.active {
		return errors.New("Python workflow controller is already active")
	}
	controller.active, controller.runID, controller.maxCalls = true, runID, maxCalls
	return nil
}

func (controller *workflowBrokerController) end() {
	controller.mu.Lock()
	controller.active, controller.runID, controller.maxCalls = false, "", 0
	controller.mu.Unlock()
}

func (executor *PythonExecutor) Execute(ctx context.Context, runID, code string, maxCalls uint32) (PythonRunResult, error) {
	if executor == nil || executor.runner == nil || !pythonRunIDPattern.MatchString(runID) ||
		!utf8.ValidString(code) || strings.TrimSpace(code) == "" || strings.ContainsRune(code, 0) || len([]byte(code)) > maxPythonCodeBytes ||
		maxCalls == 0 || maxCalls > maxFunctionCalls {
		return PythonRunResult{}, ErrAgenticRun
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	hostRunID := runID
	invocationRef, hasInvocationRef := engine.InvocationRefFromContext(ctx)
	if hasInvocationRef {
		hostRunID = invocationRef.ExecutionID
	}
	if executor.controller != nil {
		if err := executor.controller.begin(hostRunID, maxCalls); err != nil {
			return PythonRunResult{}, err
		}
		defer executor.controller.end()
	}
	effectiveCode := code
	if executor.compactPrebound {
		var err error
		effectiveCode, err = compactEffectivePythonCode(code)
		if err != nil {
			return PythonRunResult{}, err
		}
	}
	outputSchema := json.RawMessage(`{"type":"object"}`)
	if executor.anyJSONResult {
		outputSchema = json.RawMessage(`{}`)
	}
	request := runtimeconfig.RunRequest{
		RunID: runID, Code: effectiveCode, Inputs: json.RawMessage(`{}`),
		OutputSchema: outputSchema,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return PythonRunResult{}, ErrAgenticRun
	}
	properties := executor.runner.Properties()
	result := PythonRunResult{
		RequestDigest: digest(requestBytes), Backend: properties.Backend, ResetMode: properties.ResetMode,
		RawRequest: append(json.RawMessage(nil), requestBytes...),
	}
	if executor.compactPrebound {
		result.ModelCodeDigest = digest([]byte(code))
		result.EffectiveCodeDigest = digest([]byte(effectiveCode))
		result.WrapperDigest = executor.wrapperDigest
	}
	payload, runErr := executor.runner.Run(ctx, requestBytes, executor.prepare)
	result.RawResponse = append(json.RawMessage(nil), payload...)
	if runErr != nil {
		if errors.Is(runErr, runtimeconfig.ErrRunResultSchemaMismatch) {
			response, validationErr := runtimeconfig.DecodeAndValidateRunResponse(request, payload)
			if !errors.Is(validationErr, runtimeconfig.ErrRunResultSchemaMismatch) || response.Metrics == nil || response.Metrics.CapabilityCalls > maxCalls ||
				!executionRefMatches(response.ExecutionRef, invocationRef, hasInvocationRef) {
				return result, ErrAgenticRun
			}
			result.ExecutionRef = response.ExecutionRef
			result.ResponseDigest = digest(payload)
			result.CapabilityCalls = response.Metrics.CapabilityCalls
			result.ErrorCode = "guest_output_schema_mismatch"
			result.FailureClass = FailureClassGuestOutputSchemaMismatch
			result.ResultDigest = digest(response.Result)
			result.Observation, _ = json.Marshal(map[string]any{"error_code": result.ErrorCode, "status": "error"})
			return result, nil
		}
		return result, runErr
	}
	result.ResponseDigest = digest(payload)
	response, err := runtimeconfig.DecodeAndValidateRunResponse(request, payload)
	if err != nil || response.Metrics == nil || response.Metrics.CapabilityCalls > maxCalls ||
		!executionRefMatches(response.ExecutionRef, invocationRef, hasInvocationRef) {
		return result, ErrAgenticRun
	}
	result.ExecutionRef = response.ExecutionRef
	result.CapabilityCalls = response.Metrics.CapabilityCalls
	if response.Status == runtimeconfig.RunResponseOK {
		result.Success = true
		result.Observation = append(json.RawMessage(nil), response.Result...)
		result.ResultDigest = digest(response.Result)
		return result, nil
	}
	result.ErrorCode = safeGuestErrorCode(response.Error.Code)
	result.FailureClass = classifyGuestFailure(response.Error.Code, response.Error.ErrorType)
	result.ResultDigest = digest(response.Result)
	observation, _ := json.Marshal(map[string]any{"error_code": result.ErrorCode, "status": "error"})
	result.Observation = observation
	return result, nil
}

func executionRefMatches(executionRef *runtimeconfig.ExecutionRef, invocationRef runtimeconfig.InvocationRef, required bool) bool {
	if !required {
		return executionRef == nil || executionRef.Validate() == nil
	}
	return executionRef != nil && executionRef.InvocationRef == invocationRef && executionRef.Validate() == nil
}

func safeGuestErrorCode(value string) string {
	switch value {
	case "invalid_request", "python_exception", "result_not_json":
		return value
	default:
		return "guest_error"
	}
}

func classifyGuestFailure(code string, errorType *string) FailureClass {
	if code == "python_exception" {
		if errorType != nil && *errorType == "HostToolError" {
			return FailureClassHostToolError
		}
		return FailureClassPythonException
	}
	return FailureClassGuestContractError
}

func (executor *PythonExecutor) Close(ctx context.Context) error {
	if executor == nil || executor.runner == nil {
		return nil
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.runner.Close(ctx)
}
