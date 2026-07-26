package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const (
	benchmarkHandlerVersion = "bfcl-v4-handler-v1"
	benchmarkGrantVersion   = "bfcl-v4-grant-v1"
)

var reversibleFilesystemTools = map[string]bool{
	"cd": true, "cp": true, "echo": true, "mkdir": true, "mv": true, "touch": true,
}

type ToolRuntime struct {
	task       Task
	snapshot   toolcatalog.Snapshot
	registry   *capability.Registry
	toolGrants map[string]capability.ToolGrant
	filesystem *GorillaFileSystem
	handlers   map[string]*benchmarkHandler
	ids        runtimeIDs

	traceMu sync.Mutex
	turn    int
	trace   [][]StatefulCall
}

type benchmarkHandler struct {
	runtime    *ToolRuntime
	toolID     string
	reversible bool

	mu   sync.Mutex
	undo map[string]undoRecord
}

type undoRecord struct {
	transactionID string
	snapshot      FileSystemSnapshot
	postVersion   uint64
	changed       bool
	rolledBack    bool
}

type runtimeIDs struct{ next atomic.Uint64 }

func (ids *runtimeIDs) New(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty id prefix")
	}
	return fmt.Sprintf("%s_%d", prefix, ids.next.Add(1)), nil
}

func NewToolRuntime(task Task) (*ToolRuntime, error) {
	if task.Split != "dev" || len(task.Tools) == 0 || len(task.Tools) > maxFunctionCalls || len(task.Interaction.Turns) == 0 {
		return nil, ErrDataset
	}
	runtime := &ToolRuntime{task: task, turn: 0, trace: make([][]StatefulCall, len(task.Interaction.Turns))}
	if task.Track == "stateful_local_tools" {
		filesystem, err := NewGorillaFileSystem(task.Environment.InitialState)
		if err != nil {
			return nil, err
		}
		runtime.filesystem = filesystem
	}
	discovered := make([]toolcatalog.DiscoveredTool, 0, len(task.Tools))
	grants := make(map[string]toolcatalog.Grant, len(task.Tools))
	for _, tool := range task.Tools {
		inputSchema, err := closedInputSchema(tool.Parameters)
		if err != nil {
			return nil, err
		}
		effectClass := "read_only"
		if runtime.filesystem != nil && reversibleFilesystemTools[tool.Name] {
			effectClass = "reversible"
		}
		discovered = append(discovered, toolcatalog.DiscoveredTool{
			ToolID: tool.Name, ServerID: "bfcl", Name: tool.Name, Description: tool.Description,
			HandlerVersion: benchmarkHandlerVersion, InputSchema: inputSchema,
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		})
		grants[tool.Name] = toolcatalog.Grant{
			ToolID: tool.Name, EffectClass: effectClass, Policy: "AUTO_COMMIT",
			GrantVersion: benchmarkGrantVersion, MaxCalls: maxFunctionCalls,
		}
	}
	snapshot, err := toolcatalog.BuildSnapshot(discovered, grants, toolcatalog.BuildOptions{Revision: 1})
	if err != nil {
		return nil, err
	}
	for _, tool := range snapshot.Tools() {
		if tool.Projection == toolcatalog.ProjectionUnsupported {
			return nil, fmt.Errorf("tool %s cannot be projected into the Guest SDK", tool.ToolID)
		}
	}
	runtime.snapshot = snapshot
	runtime.handlers = make(map[string]*benchmarkHandler, len(task.Tools))
	handlers := make(map[string]capability.Handler, len(task.Tools))
	for _, tool := range snapshot.Tools() {
		handler := &benchmarkHandler{
			runtime: runtime, toolID: tool.ToolID, reversible: tool.EffectClass == "reversible",
			undo: map[string]undoRecord{},
		}
		runtime.handlers[tool.ToolID] = handler
		handlers[tool.ToolID] = handler
	}
	registry, toolGrants, err := capability.BuildRegistryFromSnapshot(snapshot, handlers)
	if err != nil {
		return nil, err
	}
	runtime.registry, runtime.toolGrants = registry, toolGrants
	return runtime, nil
}

func closedInputSchema(raw json.RawMessage) (json.RawMessage, error) {
	var document map[string]any
	if decodeUseNumber(raw, &document) != nil || document["type"] != "object" {
		return nil, ErrDataset
	}
	document["additionalProperties"] = false
	result, err := json.Marshal(document)
	if err != nil {
		return nil, ErrDataset
	}
	return result, nil
}

func (runtime *ToolRuntime) Snapshot() toolcatalog.Snapshot {
	if runtime == nil {
		return toolcatalog.Snapshot{}
	}
	return runtime.snapshot
}

func (runtime *ToolRuntime) FileSystem() *GorillaFileSystem {
	if runtime == nil {
		return nil
	}
	return runtime.filesystem
}

func (runtime *ToolRuntime) TrustedPrepare() (string, error) {
	if runtime == nil {
		return "", ErrDataset
	}
	return runtime.snapshot.GenerateTrustedPrepare()
}

func (runtime *ToolRuntime) SetTurn(index int) error {
	if runtime == nil || index < 0 || index >= len(runtime.trace) {
		return ErrDataset
	}
	runtime.traceMu.Lock()
	defer runtime.traceMu.Unlock()
	runtime.turn = index
	return nil
}

func (runtime *ToolRuntime) Trace() [][]StatefulCall {
	if runtime == nil {
		return nil
	}
	runtime.traceMu.Lock()
	defer runtime.traceMu.Unlock()
	trace := make([][]StatefulCall, len(runtime.trace))
	for turn := range runtime.trace {
		trace[turn] = make([]StatefulCall, len(runtime.trace[turn]))
		for index, call := range runtime.trace[turn] {
			trace[turn][index] = StatefulCall{Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...)}
		}
	}
	return trace
}

func (runtime *ToolRuntime) record(toolID string, arguments json.RawMessage) error {
	runtime.traceMu.Lock()
	defer runtime.traceMu.Unlock()
	if runtime.turn < 0 || runtime.turn >= len(runtime.trace) || len(runtime.trace[runtime.turn]) >= maxFunctionCalls {
		return ErrFileSystem
	}
	runtime.trace[runtime.turn] = append(runtime.trace[runtime.turn], StatefulCall{
		Name: toolID, Arguments: append(json.RawMessage(nil), arguments...),
	})
	return nil
}

func (handler *benchmarkHandler) Handle(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.ToolID != handler.toolID || handler.runtime == nil {
		return nil, ErrFileSystem
	}
	if err := handler.runtime.record(call.ToolID, call.Arguments); err != nil {
		return nil, err
	}
	if handler.runtime.filesystem == nil {
		return json.RawMessage(`{}`), nil
	}
	var snapshot FileSystemSnapshot
	var beforeVersion uint64
	if handler.reversible {
		snapshot = handler.runtime.filesystem.Snapshot()
		beforeVersion = handler.runtime.filesystem.Version()
	}
	output, err := handler.runtime.filesystem.Call(call.ToolID, call.Arguments)
	if err != nil {
		return nil, err
	}
	if string(output) == "null" {
		output = json.RawMessage(`{}`)
	}
	if handler.reversible {
		postVersion := handler.runtime.filesystem.Version()
		handler.mu.Lock()
		handler.undo[call.OperationID] = undoRecord{
			transactionID: call.TransactionID, snapshot: snapshot, postVersion: postVersion, changed: postVersion != beforeVersion,
		}
		handler.mu.Unlock()
	}
	return output, nil
}

func (handler *benchmarkHandler) Rollback(_ context.Context, call capability.AbortCall) error {
	if !handler.reversible || handler.runtime == nil || handler.runtime.filesystem == nil {
		return errors.New("tool is not reversible")
	}
	handler.mu.Lock()
	record, exists := handler.undo[call.Operation.ID]
	if !exists || record.transactionID != call.TransactionID {
		handler.mu.Unlock()
		return errors.New("rollback record is missing or transaction-boundary mismatched")
	}
	if record.rolledBack {
		handler.mu.Unlock()
		return nil
	}
	if !record.changed {
		record.rolledBack = true
		handler.undo[call.Operation.ID] = record
		handler.mu.Unlock()
		return nil
	}
	err := handler.runtime.filesystem.RestoreAtVersion(record.snapshot, record.postVersion)
	if err == nil {
		record.rolledBack = true
		handler.undo[call.Operation.ID] = record
	}
	handler.mu.Unlock()
	return err
}

func (*benchmarkHandler) Compensate(context.Context, capability.AbortCall) error {
	return errors.New("benchmark filesystem tools are reversible, not compensatable")
}

func (runtime *ToolRuntime) NewWorkflowBroker(runID string, maxCalls uint32) (*capability.Broker, error) {
	return runtime.newBroker(runID, transaction.TransactionModeWorkflow, maxCalls)
}

func (runtime *ToolRuntime) newBroker(runID string, mode transaction.TransactionMode, maxCalls uint32) (*capability.Broker, error) {
	if runtime == nil || runID == "" || maxCalls == 0 || maxCalls > maxFunctionCalls {
		return nil, ErrDataset
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

type deniedFetcher struct{}

func (deniedFetcher) Fetch(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
	return capability.FetchOutput{}, errors.New("network fetch is disabled for benchmark tools")
}

func (runtime *ToolRuntime) InvokeDirect(ctx context.Context, runID, callID, toolID string, arguments json.RawMessage) (json.RawMessage, error) {
	broker, err := runtime.newBroker(runID, transaction.TransactionModeDirect, 1)
	if err != nil {
		return nil, err
	}
	tool, ok := runtime.tool(toolID)
	if !ok {
		return nil, ErrDataset
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
		code := "malformed_response"
		if envelope.Error != nil && envelope.Error.Code != "" {
			code = envelope.Error.Code
		}
		return nil, fmt.Errorf("Host transaction rejected direct tool call: %s", code)
	}
	if err := broker.FinalizeRun(ctx, true, "success"); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), envelope.Result...), nil
}

func (runtime *ToolRuntime) tool(toolID string) (toolcatalog.Tool, bool) {
	for _, tool := range runtime.snapshot.Tools() {
		if tool.ToolID == toolID {
			return tool, true
		}
	}
	return toolcatalog.Tool{}, false
}
