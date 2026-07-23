package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type HostCall struct {
	RunIdentity    string
	CallID         string
	ToolID         string
	CatalogDigest  string
	HandlerVersion string
	TransactionID  string
	OperationID    string
	AttemptID      string
	Arguments      json.RawMessage
}

type BoundCall struct {
	RunIdentity    string
	CallID         string
	ToolID         string
	CatalogDigest  string
	HandlerVersion string
	EffectClass    string
	Policy         string
	ArgumentDigest string
}

type BoundOperation struct {
	TransactionID  string
	OperationID    string
	AttemptID      string
	OperationIndex uint32
	ManifestDigest string
}

type BoundOutcome struct {
	Status       Status
	ResultDigest string
	ErrorCode    string
}

type CallBinder interface {
	Begin(context.Context, BoundCall) (BoundOperation, error)
	Complete(context.Context, BoundOperation, BoundOutcome) error
}

type Handler interface {
	Handle(context.Context, HostCall) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, HostCall) (json.RawMessage, error)

func (function HandlerFunc) Handle(ctx context.Context, call HostCall) (json.RawMessage, error) {
	return function(ctx, call)
}

type HandlerSpec struct {
	ToolID         string
	HandlerVersion string
	InputSchema    []byte
	OutputSchema   []byte
	Handler        Handler
}

type registeredHandler struct {
	spec         HandlerSpec
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
}

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]registeredHandler
}

const registryMaxPayloadBytes = 1024 * 1024

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]registeredHandler)}
}

func (registry *Registry) Register(spec HandlerSpec) error {
	if registry == nil || !validIdentifier(spec.ToolID) || !validIdentifier(spec.HandlerVersion) ||
		len(spec.InputSchema) == 0 || len(spec.InputSchema) > registryMaxPayloadBytes ||
		len(spec.OutputSchema) == 0 || len(spec.OutputSchema) > registryMaxPayloadBytes || spec.Handler == nil {
		return errors.New("invalid handler specification")
	}
	inputSchema, err := compileRegisteredSchema(spec.ToolID+"-input", spec.InputSchema)
	if err != nil {
		return fmt.Errorf("compile input schema: %w", err)
	}
	outputSchema, err := compileRegisteredSchema(spec.ToolID+"-output", spec.OutputSchema)
	if err != nil {
		return fmt.Errorf("compile output schema: %w", err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.handlers[spec.ToolID]; exists {
		return errors.New("handler already registered")
	}
	spec.InputSchema = append([]byte(nil), spec.InputSchema...)
	spec.OutputSchema = append([]byte(nil), spec.OutputSchema...)
	registry.handlers[spec.ToolID] = registeredHandler{spec: spec, inputSchema: inputSchema, outputSchema: outputSchema}
	return nil
}

func compileRegisteredSchema(name string, raw []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	resource := "mem:///" + url.PathEscape(name) + ".json"
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func (registry *Registry) snapshot() *Registry {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	cloned := &Registry{handlers: make(map[string]registeredHandler, len(registry.handlers))}
	for id, handler := range registry.handlers {
		handler.spec.InputSchema = append([]byte(nil), handler.spec.InputSchema...)
		handler.spec.OutputSchema = append([]byte(nil), handler.spec.OutputSchema...)
		cloned.handlers[id] = handler
	}
	return cloned
}

func (registry *Registry) lookup(toolID string) (registeredHandler, bool) {
	if registry == nil {
		return registeredHandler{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	handler, exists := registry.handlers[toolID]
	return handler, exists
}

func validateHandlerArguments(schema *jsonschema.Schema, raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple argument values")
		}
		return err
	}
	return schema.Validate(value)
}

type registeredResponse struct {
	CallID string          `json:"call_id"`
	Status Status          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (broker *Broker) callRegistered(ctx context.Context, request toolRequest) (response []byte, err error) {
	grant, granted := broker.config.ToolGrants[request.Capability]
	handler, registered := broker.config.Registry.lookup(request.Capability)
	if !granted || !registered {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "capability_denied", "capability is not granted")
	}
	if request.CatalogDigest == nil || request.HandlerVersion == nil {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "invalid_request", "typed tool request is missing catalog or handler binding")
	}
	if *request.CatalogDigest != broker.config.CatalogDigest {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "stale_catalog", "catalog digest does not match this Run")
	}
	if *request.HandlerVersion != grant.HandlerVersion || *request.HandlerVersion != handler.spec.HandlerVersion {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "handler_version_mismatch", "handler version does not match the Host grant")
	}
	if err := validateHandlerArguments(handler.inputSchema, request.Arguments); err != nil {
		return encodeRegisteredResponse(request.CallID, StatusError, nil, "invalid_arguments", "arguments do not match the registered schema")
	}
	requestDigest := request.EnvelopeDigest
	if prior, exists := broker.typedCalls[request.CallID]; exists {
		if prior.RequestDigest != requestDigest {
			return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "duplicate_call_id", "call_id was already used for a different typed request")
		}
		return append([]byte(nil), prior.Response...), nil
	}
	admitted := false
	defer func() {
		if admitted && err == nil {
			broker.typedCalls[request.CallID] = typedCallReplay{RequestDigest: requestDigest, Response: append([]byte(nil), response...)}
		}
	}()
	if broker.calls[grant.ToolID] >= grant.MaxCalls {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "call_budget_exceeded", "capability call budget exhausted")
	}
	argumentDigest := digestBytes(request.Arguments)
	bound, err := broker.config.Binder.Begin(ctx, BoundCall{
		RunIdentity: broker.config.RunIdentity, CallID: request.CallID, ToolID: request.Capability,
		CatalogDigest: broker.config.CatalogDigest, HandlerVersion: *request.HandlerVersion,
		EffectClass: grant.EffectClass, Policy: grant.Policy, ArgumentDigest: argumentDigest,
	})
	if err != nil || !validIdentifier(bound.TransactionID) || !validIdentifier(bound.OperationID) ||
		!validIdentifier(bound.AttemptID) || bound.OperationIndex == 0 || !catalogDigestPattern.MatchString(bound.ManifestDigest) {
		return encodeRegisteredResponse(request.CallID, StatusDenied, nil, "transaction_not_ready", "Host transaction binder denied dispatch")
	}
	admitted = true
	broker.calls[grant.ToolID]++
	result, handlerErr := handler.spec.Handler.Handle(ctx, HostCall{
		RunIdentity: broker.config.RunIdentity, CallID: request.CallID, ToolID: request.Capability,
		CatalogDigest: broker.config.CatalogDigest, HandlerVersion: *request.HandlerVersion,
		TransactionID: bound.TransactionID, OperationID: bound.OperationID, AttemptID: bound.AttemptID,
		Arguments: append(json.RawMessage(nil), request.Arguments...),
	})
	if handlerErr != nil {
		status := StatusError
		code := "handler_failed"
		message := "Host tool handler failed"
		if errors.Is(handlerErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code, message = StatusTimeout, "handler_timeout", "Host tool handler timed out"
		}
		return broker.finishRegistered(ctx, request, bound, status, nil, code, message)
	}
	if len(result) == 0 || len(result) > registryMaxPayloadBytes || !json.Valid(result) ||
		validateHandlerArguments(handler.outputSchema, result) != nil {
		return broker.finishRegistered(ctx, request, bound, StatusError, nil, "invalid_handler_result", "Host tool handler returned a result outside its registered schema")
	}
	return broker.finishRegistered(ctx, request, bound, StatusOK, result, "", "")
}

func (broker *Broker) finishRegistered(ctx context.Context, request toolRequest, bound BoundOperation, status Status, result json.RawMessage, code, message string) ([]byte, error) {
	outcome := BoundOutcome{Status: status, ErrorCode: code}
	if result != nil {
		outcome.ResultDigest = digestBytes(result)
	}
	receiptStatus := status
	if err := broker.config.Binder.Complete(ctx, bound, outcome); err != nil {
		receiptStatus = Status("reconciliation_required")
		status, result, code, message = StatusError, nil, "reconciliation_required", "Host transaction completion requires reconciliation"
	}
	broker.recordRegistered(request, bound, receiptStatus, result)
	return encodeRegisteredResponse(request.CallID, status, result, code, message)
}

func encodeRegisteredResponse(callID string, status Status, result json.RawMessage, code, message string) ([]byte, error) {
	if result == nil {
		result = json.RawMessage(`{}`)
	}
	var responseError *Error
	if code != "" {
		responseError = &Error{Code: code, Message: message}
	}
	return json.Marshal(registeredResponse{CallID: callID, Status: status, Result: result, Error: responseError})
}

func (broker *Broker) recordRegistered(request toolRequest, bound BoundOperation, status Status, response []byte) {
	target := "tool:" + request.Capability + ":arguments-" + digestBytes(request.Arguments)
	grant := broker.config.ToolGrants[request.Capability]
	broker.receipts = append(broker.receipts, receipt.NewBound(
		broker.config.RunIdentity, request.CallID, request.Capability, bound.OperationIndex,
		bound.TransactionID, bound.OperationID, bound.AttemptID,
		broker.config.CatalogDigest, grant.HandlerVersion, grant.EffectClass, grant.Policy, bound.ManifestDigest,
		target, string(status), response,
	))
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validEffectClass(value string) bool {
	switch value {
	case "read_only", "reversible", "compensatable", "irreversible":
		return true
	default:
		return false
	}
}

func validPolicyOutcome(value string) bool {
	switch value {
	case "DENY", "AUTO_COMMIT", "AGENT_COMMIT_REQUIRED", "USER_APPROVAL_REQUIRED":
		return true
	default:
		return false
	}
}
