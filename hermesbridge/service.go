package hermesbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

const (
	ResponseStatusOK    = "ok"
	ResponseStatusError = "error"
)

type BridgeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ExecuteResponse struct {
	Version        string                               `json:"version"`
	RequestID      string                               `json:"request_id"`
	Status         string                               `json:"status"`
	Result         json.RawMessage                      `json:"result,omitempty"`
	Error          *BridgeError                         `json:"error,omitempty"`
	Metrics        *runtimeconfig.RunMetrics            `json:"metrics,omitempty"`
	ExecutionRef   *runtimeconfig.ExecutionRef          `json:"execution_ref,omitempty"`
	RunPlan        *runtimeconfig.FrozenRunPlan         `json:"run_plan,omitempty"`
	ImportReceipts *runtimeconfig.ImportReceiptEvidence `json:"import_receipts,omitempty"`
	Outcome        *runtimeconfig.ExecutionOutcome      `json:"outcome,omitempty"`
}

type IDSource func() (string, error)

type InvocationTrace interface {
	RuntimeStarted(context.Context, runtimeconfig.InvocationRef, string) (string, error)
	RuntimeCompleted(context.Context, string, runtimeconfig.ExecutionRef, string, string) error
}

type Service struct {
	runner  engine.Runner
	trace   InvocationTrace
	ids     IDSource
	timeout time.Duration
	profile *runtimeconfig.ExecutionProfile
}

func NewService(runner engine.Runner, trace InvocationTrace, ids IDSource, timeout time.Duration) (*Service, error) {
	if runner == nil || trace == nil || ids == nil || timeout <= 0 || timeout > 5*time.Minute {
		return nil, errors.New("invalid Hermes bridge service")
	}
	properties := runner.Properties()
	if properties.Validate() != nil {
		return nil, errors.New("invalid Hermes bridge runner")
	}
	return &Service{runner: runner, trace: trace, ids: ids, timeout: timeout, profile: properties.ExecutionProfile()}, nil
}

func (service *Service) Observe(parent context.Context, request ObserveRequest) ObserveResponse {
	response := ObserveResponse{Version: ProtocolVersion, RequestID: request.RequestID, Status: ResponseStatusError}
	if service == nil || service.trace == nil || request.Validate() != nil {
		response.Error = &BridgeError{Code: "invalid_observation", Message: "invalid metadata observation"}
		return response
	}
	observer, ok := service.trace.(interface {
		Observe(context.Context, ObserveRequest) (agenttrace.Event, error)
	})
	if !ok {
		response.Error = &BridgeError{Code: "trace_unavailable", Message: "metadata observation is unavailable"}
		return response
	}
	traceCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	event, err := observer.Observe(traceCtx, request)
	if err != nil {
		response.Error = &BridgeError{Code: "trace_unavailable", Message: "metadata observation was not persisted"}
		return response
	}
	response.Status = ResponseStatusOK
	response.EventID = event.EventID
	response.Sequence = event.Sequence
	return response
}

func (service *Service) Execute(parent context.Context, request ExecuteRequest) ExecuteResponse {
	response := ExecuteResponse{Version: ProtocolVersion, RequestID: request.RequestID, Status: ResponseStatusError}
	if service == nil || request.Validate() != nil {
		return bridgeFailure(response, "invalid_request", "invalid runtime request")
	}
	executionID, err := service.ids()
	if err != nil || !boundedIdentifier(executionID, 128) {
		return bridgeFailure(response, "identity_unavailable", "Host execution identity unavailable")
	}
	invocationRef := runtimeconfig.InvocationRef{
		AgentRunID: request.Invocation.AgentRunID, TurnSeq: request.Invocation.TurnSeq,
		OutputItemSeq: request.Invocation.OutputItemSeq, SegmentSeq: request.Invocation.SegmentSeq,
		InvocationID: request.Invocation.InvocationID, InvocationAttempt: request.Invocation.InvocationAttempt,
		ExecutionID: executionID,
	}
	hostCtx, err := engine.WithInvocationRef(parent, invocationRef)
	if err != nil {
		return bridgeFailure(response, "invalid_invocation_ref", "invalid Host invocation reference")
	}
	traceCtx, cancelTrace := context.WithTimeout(context.WithoutCancel(hostCtx), 5*time.Second)
	defer cancelTrace()
	ctx, cancel := context.WithTimeout(hostCtx, service.timeout)
	defer cancel()

	runRequest := runtimeconfig.RunRequest{
		RunID: "hermes-" + executionID, Code: request.Code,
		Inputs:        append(json.RawMessage(nil), request.Inputs...),
		OutputSchema:  append(json.RawMessage(nil), request.OutputSchema...),
		Compatibility: cloneCompatibility(request.Compatibility),
		Requirements:  append([]runtimeconfig.RequiredFeature(nil), request.Requirements...),
	}
	runPayload, err := json.Marshal(runRequest)
	if err != nil {
		return bridgeFailure(response, "invalid_request", "encode runtime request")
	}
	if admissionErr := runtimeconfig.AdmitRunRequirements(runRequest); admissionErr != nil {
		outcome, outcomeErr := runtimeconfig.NewUnsupportedOutcome(runPayload, admissionErr)
		if outcomeErr != nil {
			return bridgeFailure(response, "runtime_error", "runtime admission failed")
		}
		response.Outcome = &outcome
		return bridgeFailure(response, "runtime_unsupported", "runtime requirements unsupported; escalation required")
	}
	if admissionErr := runtimeconfig.AdmitRunCompatibility(runRequest, service.profile); admissionErr != nil {
		return bridgeFailure(response, "profile_unsupported", "execution profile unsupported")
	}
	startedEventID, err := service.trace.RuntimeStarted(traceCtx, invocationRef, digestBytes(runPayload))
	if err != nil {
		return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
	}

	executionRef := runtimeconfig.ExecutionRef{InvocationRef: invocationRef, ExecutedCodeSHA256: digestString(request.Code)}
	payload, runErr := service.runner.Run(ctx, runPayload, "")
	if runErr != nil && !errors.Is(runErr, runtimeconfig.ErrRunResultSchemaMismatch) {
		status := "runtime_error"
		code := "runtime_error"
		message := "runtime execution failed"
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code, message = "timeout", "runtime_timeout", "runtime execution timed out"
		}
		if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, status, ""); traceErr != nil {
			return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
		}
		return bridgeFailure(response, code, message)
	}

	guestResponse, validationErr := runtimeconfig.DecodeAndValidateRunResponse(runRequest, payload)
	if validationErr != nil && !errors.Is(validationErr, runtimeconfig.ErrRunResultSchemaMismatch) {
		if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, "invalid_guest_response", ""); traceErr != nil {
			return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
		}
		return bridgeFailure(response, "invalid_guest_response", "runtime returned an invalid response")
	}
	if !executionRefMatches(guestResponse.ExecutionRef, invocationRef, request.Code) {
		if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, "execution_ref_mismatch", ""); traceErr != nil {
			return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
		}
		return bridgeFailure(response, "execution_ref_mismatch", "runtime execution reference mismatch")
	}
	executionRef = *guestResponse.ExecutionRef
	response.RunPlan = guestResponse.RunPlan
	response.ImportReceipts = guestResponse.ImportReceipts

	if errors.Is(validationErr, runtimeconfig.ErrRunResultSchemaMismatch) || errors.Is(runErr, runtimeconfig.ErrRunResultSchemaMismatch) {
		if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, "schema_mismatch", digestBytes(guestResponse.Result)); traceErr != nil {
			return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
		}
		response.ExecutionRef, response.Metrics = &executionRef, guestResponse.Metrics
		return bridgeFailure(response, "output_schema_mismatch", "runtime result does not match output schema")
	}
	if guestResponse.Status == runtimeconfig.RunResponseError {
		if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, "guest_error", ""); traceErr != nil {
			return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
		}
		response.ExecutionRef, response.Metrics = &executionRef, guestResponse.Metrics
		code, message := "guest_error", "Python execution failed"
		if guestResponse.Error != nil {
			code = guestResponse.Error.Code
			message = guestResponse.Error.Message
		}
		return bridgeFailure(response, code, message)
	}
	if traceErr := service.trace.RuntimeCompleted(traceCtx, startedEventID, executionRef, "ok", digestBytes(guestResponse.Result)); traceErr != nil {
		return bridgeFailure(response, "trace_required", "required runtime trace unavailable")
	}
	response.Status = ResponseStatusOK
	response.Result = append(json.RawMessage(nil), guestResponse.Result...)
	response.Metrics = guestResponse.Metrics
	response.ExecutionRef = &executionRef
	return response
}

func executionRefMatches(actual *runtimeconfig.ExecutionRef, expected runtimeconfig.InvocationRef, code string) bool {
	return actual != nil && actual.Validate() == nil && actual.InvocationRef == expected && actual.ExecutedCodeSHA256 == digestString(code)
}

func cloneCompatibility(value *runtimeconfig.CompatibilityDeclaration) *runtimeconfig.CompatibilityDeclaration {
	if value == nil {
		return nil
	}
	return &runtimeconfig.CompatibilityDeclaration{
		Profile: value.Profile,
		Imports: append([]string(nil), value.Imports...),
	}
}

func bridgeFailure(response ExecuteResponse, code, message string) ExecuteResponse {
	response.Status = ResponseStatusError
	response.Result = nil
	response.Error = &BridgeError{Code: code, Message: message}
	return response
}

func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func digestString(value string) string { return digestBytes([]byte(value)) }
