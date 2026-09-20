// Package service exposes a bounded local HTTP control plane for Pysolate.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	"github.com/tetratelabs/wazero"
)

const maxHTTPBody = 1 << 20

var errRequestTooLarge = errors.New("request body exceeds 1 MiB limit")

type Server struct {
	plain     *pysolate.Runner
	workspace *pysolate.Runner
	manager   *workspacepkg.Manager
	slots     chan struct{}
	cache     wazero.CompilationCache

	mu     sync.Mutex
	leases map[workspacepkg.Ref]*workspacepkg.Lease
	closed bool
}

type createRequest struct {
	Files []workspacepkg.InitialFile `json:"files"`
}

type runRequest struct {
	Source string          `json:"source"`
	Inputs json.RawMessage `json:"inputs"`
}

type runResponse struct {
	Value       json.RawMessage             `json:"value,omitempty"`
	Stdout      string                      `json:"stdout,omitempty"`
	Transformed string                      `json:"transformed,omitempty"`
	Before      workspacepkg.Revision       `json:"before,omitempty"`
	After       workspacepkg.Revision       `json:"after,omitempty"`
	Changes     *workspacepkg.ChangeSummary `json:"changes,omitempty"`
	RunNS       int64                       `json:"run_ns"`
	TotalNS     int64                       `json:"total_ns"`
	Error       string                      `json:"error,omitempty"`
}

func New(ctx context.Context, wasm []byte, manifest pysolate.Manifest, manager *workspacepkg.Manager, maxActive int) (*Server, error) {
	if manager == nil || maxActive < 1 || maxActive > 64 {
		return nil, errors.New("invalid service configuration")
	}
	cache := wazero.NewCompilationCache()
	ctx = pysolate.WithCompilationCache(ctx, cache)
	var plain, withWorkspace *pysolate.Runner
	var err error
	if runtime.GOOS == "linux" {
		plain, err = pysolate.NewPreparedCOW(ctx, wasm, manifest)
	} else {
		plain, err = pysolate.NewPrepared(ctx, wasm, manifest)
	}
	if err != nil {
		_ = cache.Close(context.Background())
		return nil, err
	}
	if runtime.GOOS == "linux" {
		withWorkspace, err = pysolate.NewPreparedWorkspaceCOW(ctx, wasm, manifest)
	} else {
		withWorkspace, err = pysolate.NewPreparedWorkspace(ctx, wasm, manifest)
	}
	if err != nil {
		_ = plain.Close(context.Background())
		_ = cache.Close(context.Background())
		return nil, err
	}
	return &Server{plain: plain, workspace: withWorkspace, manager: manager, slots: make(chan struct{}, maxActive), cache: cache, leases: make(map[workspacepkg.Ref]*workspacepkg.Lease)}, nil
}

func (server *Server) Close(ctx context.Context) error {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return nil
	}
	server.closed = true
	leases := server.leases
	server.leases = nil
	server.mu.Unlock()
	var result error
	for ref, lease := range leases {
		if err := lease.Release(); err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(result, server.manager.Destroy(ref))
	}
	result = errors.Join(result, server.workspace.Close(ctx), server.plain.Close(ctx), server.cache.Close(ctx), server.manager.Close())
	return result
}

func (server *Server) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]any{"ready": !server.isClosed()})
		return
	}
	if request.URL.Path == "/v1/run" {
		server.handlePlainRun(w, request)
		return
	}
	if request.URL.Path == "/v1/workspaces" {
		server.handleCreate(w, request)
		return
	}
	const prefix = "/v1/workspaces/"
	if strings.HasPrefix(request.URL.Path, prefix) {
		rest := strings.TrimPrefix(request.URL.Path, prefix)
		parts := strings.Split(rest, "/")
		if len(parts) >= 1 && parts[0] != "" {
			ref := workspacepkg.Ref(parts[0])
			switch {
			case len(parts) == 1 && request.Method == http.MethodDelete:
				server.handleDestroy(w, ref)
			case len(parts) == 2 && parts[1] == "run":
				server.handleWorkspaceRun(w, request, ref)
			case len(parts) == 2 && parts[1] == "snapshot" && request.Method == http.MethodGet:
				server.handleSnapshot(w, ref)
			case len(parts) == 2 && parts[1] == "files" && request.Method == http.MethodGet:
				server.handleFile(w, request, ref)
			default:
				writeError(w, http.StatusNotFound, "not found")
			}
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (server *Server) handleCreate(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body createRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(w, requestErrorStatus(err), err.Error())
		return
	}
	ref, err := server.manager.Create(body.Files, workspacepkg.DefaultLimits())
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	lease, err := server.manager.Acquire(ref, "pysolate-service")
	if err != nil {
		_ = server.manager.Destroy(ref)
		writeError(w, statusFor(err), err.Error())
		return
	}
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		_ = lease.Release()
		_ = server.manager.Destroy(ref)
		writeError(w, http.StatusServiceUnavailable, "service closed")
		return
	}
	server.leases[ref] = lease
	server.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"workspace": ref})
}

func (server *Server) handlePlainRun(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body runRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(w, requestErrorStatus(err), err.Error())
		return
	}
	if body.Source == "" {
		writeError(w, http.StatusBadRequest, "invalid run request")
		return
	}
	if !server.admit() {
		writeError(w, http.StatusTooManyRequests, "execution capacity exhausted")
		return
	}
	defer server.release()
	started := time.Now()
	runStarted := time.Now()
	out, err := server.plain.Run(request.Context(), body.Source, decodeInputs(body.Inputs))
	response := runResponse{Value: out.Value, Stdout: out.Stdout, Transformed: out.Transformed, RunNS: time.Since(runStarted).Nanoseconds(), TotalNS: time.Since(started).Nanoseconds()}
	if err != nil {
		response.Error = err.Error()
		writeJSON(w, http.StatusUnprocessableEntity, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (server *Server) handleWorkspaceRun(w http.ResponseWriter, request *http.Request, ref workspacepkg.Ref) {
	if request.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body runRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(w, requestErrorStatus(err), err.Error())
		return
	}
	if body.Source == "" {
		writeError(w, http.StatusBadRequest, "invalid run request")
		return
	}
	lease := server.lease(ref)
	if lease == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !server.admit() {
		writeError(w, http.StatusTooManyRequests, "execution capacity exhausted")
		return
	}
	defer server.release()
	started := time.Now()
	before, err := lease.Snapshot()
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	runStarted := time.Now()
	out, runErr := server.workspace.RunWorkspace(request.Context(), body.Source, decodeInputs(body.Inputs), lease)
	runNS := time.Since(runStarted).Nanoseconds()
	after, snapshotErr := lease.Snapshot()
	if snapshotErr != nil {
		writeError(w, statusFor(snapshotErr), snapshotErr.Error())
		return
	}
	changes := workspacepkg.Diff(before, after)
	response := runResponse{Value: out.Value, Stdout: out.Stdout, Transformed: out.Transformed, Before: before.Revision, After: after.Revision, Changes: &changes, RunNS: runNS, TotalNS: time.Since(started).Nanoseconds()}
	if runErr != nil {
		response.Error = runErr.Error()
		writeJSON(w, http.StatusUnprocessableEntity, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (server *Server) handleSnapshot(w http.ResponseWriter, ref workspacepkg.Ref) {
	lease := server.lease(ref)
	if lease == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	snapshot, err := lease.Snapshot()
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (server *Server) handleFile(w http.ResponseWriter, request *http.Request, ref workspacepkg.Ref) {
	name := request.URL.Query().Get("path")
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}
	lease := server.lease(ref)
	if lease == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	file, err := lease.ReadFile(name, maxHTTPBody)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (server *Server) handleDestroy(w http.ResponseWriter, ref workspacepkg.Ref) {
	server.mu.Lock()
	lease := server.leases[ref]
	if lease != nil {
		delete(server.leases, ref)
	}
	server.mu.Unlock()
	if lease == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if err := lease.Release(); err != nil {
		server.mu.Lock()
		server.leases[ref] = lease
		server.mu.Unlock()
		writeError(w, statusFor(err), err.Error())
		return
	}
	if err := server.manager.Destroy(ref); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"destroyed": true})
}

func (server *Server) lease(ref workspacepkg.Ref) *workspacepkg.Lease {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.leases[ref]
}
func (server *Server) isClosed() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closed
}
func (server *Server) admit() bool {
	select {
	case server.slots <- struct{}{}:
		return true
	default:
		return false
	}
}
func (server *Server) release() { <-server.slots }

func decodeBody(request *http.Request, target any) error {
	defer request.Body.Close()
	data, err := io.ReadAll(io.LimitReader(request.Body, maxHTTPBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxHTTPBody {
		return errRequestTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}
func requestErrorStatus(err error) int {
	if errors.Is(err, errRequestTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
func decodeInputs(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return map[string]any{}
	}
	return value
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func statusFor(err error) int {
	switch {
	case errors.Is(err, workspacepkg.ErrWorkspaceBusy):
		return http.StatusConflict
	case errors.Is(err, workspacepkg.ErrWorkspaceNotFound):
		return http.StatusNotFound
	case errors.Is(err, workspacepkg.ErrInvalidWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

var _ http.Handler = (*Server)(nil)
