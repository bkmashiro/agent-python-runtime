package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Tool runs on the Host. args is Go-owned; no borrowed Guest memory escapes.
// Treat args as read-only; the run also retains them for Future matching.
type Tool func(ctx context.Context, args json.RawMessage) (any, error)

// ToolSpec binds a Python-visible tool name to its Host implementation.
// Early-read tools must return a stable snapshot for the run and honor ctx.
type ToolSpec struct {
	Call           Tool
	AllowEarlyRead bool
}

// Manifest is copied by New and injected into each Guest execution.
type Manifest map[string]ToolSpec

type callRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

func readRequest(m api.Module, ptr, size uint32) (callRequest, error) {
	var request callRequest
	data, ok := m.Memory().Read(ptr, size)
	if !ok || size > maxMessage {
		return request, errors.New("tool request outside ABI bounds")
	}
	err := json.Unmarshal(data, &request)
	return request, err
}

func encodeResponse(value any, err error) []byte {
	response := struct {
		Value any     `json:"value"`
		Error *string `json:"error,omitempty"`
	}{Value: value}
	if err != nil {
		response.Value = nil
		message := err.Error()
		response.Error = &message
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		encoded, _ = json.Marshal(map[string]string{"error": "tool result is not JSON: " + err.Error()})
	}
	return encoded
}

func (r *Runner) invoke(ctx context.Context, request callRequest) []byte {
	state := ctx.Value(runKey{}).(*runState)
	if state.controlErr != nil {
		return nil
	}
	if state.calls >= maxToolCalls {
		return encodeResponse(nil, errors.New("tool call budget exhausted"))
	}
	state.calls++
	if state.recording != nil {
		response, err := state.recording.journal.Call(ctx, request.Tool, request.Args, func(callCtx context.Context) []byte { return r.dispatch(callCtx, request) })
		if err != nil {
			state.controlErr = err
			return nil
		}
		return response
	}
	return r.dispatch(ctx, request)
}

// 1024 calls per attempt is an engineering budget, independent of Wasm limits.
const maxToolCalls = 1024

func (r *Runner) dispatch(ctx context.Context, request callRequest) []byte {
	spec, ok := r.manifest[request.Tool]
	if !ok {
		return encodeResponse(nil, fmt.Errorf("unknown tool: %s", request.Tool))
	}
	value, err := spec.Call(ctx, request.Args)
	return encodeResponse(value, err)
}

func (r *Runner) hostCall(ctx context.Context, m api.Module, ptr, size, out, capacity uint32) uint32 {
	request, err := readRequest(m, ptr, size)
	var response []byte
	if err != nil {
		response = encodeResponse(nil, err)
	} else {
		response = r.invoke(ctx, request)
	}
	if stopForJournal(ctx, m) {
		return ^uint32(0)
	}
	return writeResponse(m, out, capacity, response)
}

func (r *Runner) hostPrepare(ctx context.Context, m api.Module, ptr, size uint32) uint32 {
	request, err := readRequest(m, ptr, size)
	if err != nil {
		return 0
	}
	return ctx.Value(runKey{}).(*runState).prepare(request)
}

func (r *Runner) hostResolve(ctx context.Context, m api.Module, id, ptr, size, out, capacity uint32) uint32 {
	request, err := readRequest(m, ptr, size)
	var response []byte
	if err != nil {
		response = encodeResponse(nil, err)
	} else {
		response = ctx.Value(runKey{}).(*runState).resolve(id, request)
	}
	if stopForJournal(ctx, m) {
		return ^uint32(0)
	}
	return writeResponse(m, out, capacity, response)
}
