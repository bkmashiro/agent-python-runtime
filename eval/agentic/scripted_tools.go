package agentic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

var ErrScriptedToolRuntime = errors.New("scripted Host tool runtime rejected the call")

type ScriptedTool struct {
	ToolID      string
	InputSchema json.RawMessage
	EffectClass string
}

type ScriptedExpectedCall struct {
	Name      string
	Arguments json.RawMessage
	Result    json.RawMessage
}

type ScriptedObservedCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Matched   bool            `json:"matched"`
}

type ScriptedToolRuntime struct {
	snapshot   toolcatalog.Snapshot
	registry   *capability.Registry
	toolGrants map[string]capability.ToolGrant
	ids        runtimeIDs

	mu       sync.Mutex
	expected []ScriptedExpectedCall
	trace    []ScriptedObservedCall
	cursor   int
}

type scriptedToolHandler struct {
	runtime *ScriptedToolRuntime
	toolID  string
}

func NewScriptedToolRuntime(tools []ScriptedTool, expected []ScriptedExpectedCall) (*ScriptedToolRuntime, error) {
	if len(tools) == 0 || len(tools) > maxFunctionCalls || len(expected) > maxFunctionCalls {
		return nil, ErrScriptedToolRuntime
	}
	discovered := make([]toolcatalog.DiscoveredTool, 0, len(tools))
	grants := make(map[string]toolcatalog.Grant, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.ToolID == "" || seen[tool.ToolID] || len(bytes.TrimSpace(tool.InputSchema)) == 0 {
			return nil, ErrScriptedToolRuntime
		}
		seen[tool.ToolID] = true
		effectClass := tool.EffectClass
		if effectClass == "" {
			effectClass = "read_only"
		}
		discovered = append(discovered, toolcatalog.DiscoveredTool{
			ToolID: tool.ToolID, ServerID: "placement-scripted", Name: tool.ToolID,
			Description: "Frozen placement scripted capability", HandlerVersion: "placement-scripted-v1",
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...), OutputSchema: json.RawMessage(`{"type":"object"}`),
		})
		grants[tool.ToolID] = toolcatalog.Grant{
			ToolID: tool.ToolID, EffectClass: effectClass, Policy: "AUTO_COMMIT",
			GrantVersion: "placement-scripted-grant-v1", MaxCalls: maxFunctionCalls,
		}
	}
	for _, call := range expected {
		if !seen[call.Name] || !validJSONObject(call.Arguments) || len(bytes.TrimSpace(call.Result)) == 0 {
			return nil, ErrScriptedToolRuntime
		}
	}
	snapshot, err := toolcatalog.BuildSnapshot(discovered, grants, toolcatalog.BuildOptions{Revision: 1})
	if err != nil {
		return nil, err
	}
	for _, tool := range snapshot.Tools() {
		if tool.Projection == toolcatalog.ProjectionUnsupported {
			return nil, ErrScriptedToolRuntime
		}
	}
	runtime := &ScriptedToolRuntime{
		snapshot: snapshot, expected: cloneExpectedCalls(expected), trace: make([]ScriptedObservedCall, 0, len(expected)),
	}
	handlers := make(map[string]capability.Handler, len(tools))
	for _, tool := range tools {
		handlers[tool.ToolID] = &scriptedToolHandler{runtime: runtime, toolID: tool.ToolID}
	}
	registry, toolGrants, err := capability.BuildRegistryFromSnapshot(snapshot, handlers)
	if err != nil {
		return nil, err
	}
	runtime.registry, runtime.toolGrants = registry, toolGrants
	return runtime, nil
}

func (runtime *ScriptedToolRuntime) TrustedPrepare() (string, error) {
	if runtime == nil {
		return "", ErrScriptedToolRuntime
	}
	return runtime.snapshot.GenerateTrustedPrepare()
}

func (runtime *ScriptedToolRuntime) TrustedPrepareWithPreboundTools() (string, error) {
	if runtime == nil {
		return "", ErrScriptedToolRuntime
	}
	return runtime.snapshot.GenerateTrustedPrepareWithToolBindings()
}

func (runtime *ScriptedToolRuntime) NewWorkflowBroker(runID string, maxCalls uint32) (*capability.Broker, error) {
	return runtime.newBroker(runID, transaction.TransactionModeWorkflow, maxCalls)
}

func (runtime *ScriptedToolRuntime) newBroker(runID string, mode transaction.TransactionMode, maxCalls uint32) (*capability.Broker, error) {
	if runtime == nil || runID == "" || maxCalls == 0 || maxCalls > maxFunctionCalls {
		return nil, ErrScriptedToolRuntime
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &runtime.ids, time.Now, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: runID, CatalogDigest: runtime.snapshot.Digest(), Mode: mode})
	if err != nil {
		return nil, err
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		return nil, err
	}
	return capability.NewBroker(capability.Config{
		RunIdentity: runID, CatalogDigest: runtime.snapshot.Digest(), Registry: runtime.registry, Binder: binder,
		ToolGrants: runtime.toolGrants, MaxTransactionCalls: maxCalls,
	}, deniedFetcher{})
}

func (runtime *ScriptedToolRuntime) InvokeDirect(ctx context.Context, runID, callID, toolID string, arguments json.RawMessage) (json.RawMessage, error) {
	broker, err := runtime.newBroker(runID, transaction.TransactionModeDirect, 1)
	if err != nil {
		return nil, err
	}
	var tool toolcatalog.Tool
	found := false
	for _, candidate := range runtime.snapshot.Tools() {
		if candidate.ToolID == toolID {
			tool, found = candidate, true
			break
		}
	}
	if !found {
		return nil, ErrScriptedToolRuntime
	}
	payload, err := json.Marshal(map[string]any{
		"call_id": callID, "capability": toolID, "catalog_digest": runtime.snapshot.Digest(),
		"handler_version": tool.HandlerVersion, "arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	response, callErr := broker.Call(ctx, payload)
	var envelope struct {
		CallID string            `json:"call_id"`
		Status capability.Status `json:"status"`
		Result json.RawMessage   `json:"result"`
		Error  *capability.Error `json:"error"`
	}
	if callErr != nil || decodeStrict(response, &envelope) != nil || envelope.CallID != callID || envelope.Status != capability.StatusOK || envelope.Error != nil {
		_ = broker.FinalizeRun(ctx, false, "direct_call_failed")
		if callErr != nil {
			return nil, callErr
		}
		return nil, ErrScriptedToolRuntime
	}
	if err := broker.FinalizeRun(ctx, true, "success"); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), envelope.Result...), nil
}

func (runtime *ScriptedToolRuntime) Trace() []ScriptedObservedCall {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	trace := make([]ScriptedObservedCall, len(runtime.trace))
	for index, call := range runtime.trace {
		trace[index] = ScriptedObservedCall{Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...), Matched: call.Matched}
	}
	return trace
}

func (runtime *ScriptedToolRuntime) Complete() bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.cursor == len(runtime.expected) && len(runtime.trace) == len(runtime.expected)
}

func (handler *scriptedToolHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if handler == nil || handler.runtime == nil || call.ToolID != handler.toolID {
		return nil, ErrScriptedToolRuntime
	}
	runtime := handler.runtime
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.cursor >= len(runtime.expected) {
		return nil, ErrScriptedToolRuntime
	}
	expected := runtime.expected[runtime.cursor]
	matched := expected.Name == call.ToolID && canonicalJSONEqual(expected.Arguments, call.Arguments)
	runtime.trace = append(runtime.trace, ScriptedObservedCall{Name: call.ToolID, Arguments: canonicalJSON(call.Arguments), Matched: matched})
	if !matched {
		return nil, ErrScriptedToolRuntime
	}
	runtime.cursor++
	return append(json.RawMessage(nil), expected.Result...), nil
}

func (*scriptedToolHandler) Rollback(context.Context, capability.AbortCall) error { return nil }
func (*scriptedToolHandler) Compensate(context.Context, capability.AbortCall) error {
	return errors.New("scripted placement tools are not compensatable")
}

func validJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return decodeUseNumber(raw, &value) == nil && value != nil
}

func canonicalJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if decodeUseNumber(raw, &value) != nil {
		return nil
	}
	result, _ := json.Marshal(value)
	return result
}

func canonicalJSONEqual(left, right json.RawMessage) bool {
	return bytes.Equal(canonicalJSON(left), canonicalJSON(right))
}

func cloneExpectedCalls(calls []ScriptedExpectedCall) []ScriptedExpectedCall {
	result := make([]ScriptedExpectedCall, len(calls))
	for index, call := range calls {
		result[index] = ScriptedExpectedCall{
			Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...), Result: append(json.RawMessage(nil), call.Result...),
		}
	}
	return result
}
