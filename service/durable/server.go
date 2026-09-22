// Package durableservice exposes the durable Runner lifecycle to a trusted local harness.
package durableservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

const maxHTTPBody = 1 << 20

var errRequestTooLarge = errors.New("request body exceeds 1 MiB limit")

type Runtime interface {
	durable.AttemptController
	ArtifactID() string
	Create(context.Context, durable.Definition) (durable.Run, error)
	Get(context.Context, string) (durable.Run, error)
	Decide(context.Context, string, durable.Decision) error
}

type Server struct {
	runtime            Runtime
	executor           *durable.Executor
	environmentVersion string

	mu     sync.Mutex
	closed bool
}

type createRequest struct {
	ID     string          `json:"id"`
	Source string          `json:"source"`
	Inputs json.RawMessage `json:"inputs"`
	Seed   string          `json:"seed"`
}

type resolveRequest struct {
	WaitID string          `json:"wait_id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type runResponse struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Outcome json.RawMessage `json:"outcome,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

type attemptResponse struct {
	State       durable.State    `json:"state"`
	Value       json.RawMessage  `json:"value,omitempty"`
	Stdout      string           `json:"stdout,omitempty"`
	Transformed string           `json:"transformed,omitempty"`
	Park        *durable.Park    `json:"park,omitempty"`
	Failure     *durable.Failure `json:"failure,omitempty"`
}

func New(runtime Runtime, environmentVersion string, limits durable.Limits) (*Server, error) {
	if runtime == nil || runtime.ArtifactID() == "" || environmentVersion == "" {
		return nil, errors.New("invalid durable service configuration")
	}
	executor, err := durable.NewExecutor(runtime, limits)
	if err != nil {
		return nil, err
	}
	return &Server{runtime: runtime, executor: executor, environmentVersion: environmentVersion}, nil
}

func (server *Server) Close(ctx context.Context) error {
	if server == nil {
		return nil
	}
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return nil
	}
	server.closed = true
	server.mu.Unlock()
	return server.executor.Close(ctx)
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
		stats := server.executor.Stats()
		writeJSON(writer, http.StatusOK, map[string]any{
			"ready": !server.isClosed(), "running": stats.Running, "resident": stats.Resident,
			"inflight_tools": stats.InflightTools, "queued": stats.Queued,
		})
		return
	}
	if request.URL.Path == "/v1/durable/runs" {
		server.handleCreate(writer, request)
		return
	}
	if request.URL.Path == "/v1/durable/waits/resolve" {
		server.handleResolve(writer, request)
		return
	}
	const prefix = "/v1/durable/runs/"
	if strings.HasPrefix(request.URL.Path, prefix) {
		rest := strings.TrimPrefix(request.URL.Path, prefix)
		parts := strings.Split(rest, "/")
		if len(parts) >= 1 && parts[0] != "" {
			switch {
			case len(parts) == 1 && request.Method == http.MethodGet:
				server.handleGet(writer, request, parts[0])
			case len(parts) == 2 && parts[1] == "attempts":
				server.handleAttempt(writer, request, parts[0])
			case len(parts) == 2 && parts[1] == "cancel":
				server.handleCancel(writer, request, parts[0])
			default:
				writeError(writer, http.StatusNotFound, "not found")
			}
			return
		}
	}
	writeError(writer, http.StatusNotFound, "not found")
}

func (server *Server) handleCreate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body createRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(writer, requestErrorStatus(err), err.Error())
		return
	}
	if body.ID == "" || strings.Contains(body.ID, "/") || body.Source == "" || body.Seed == "" {
		writeError(writer, http.StatusBadRequest, "invalid durable run request")
		return
	}
	if len(body.Inputs) == 0 {
		body.Inputs = json.RawMessage(`{}`)
	}
	if !json.Valid(body.Inputs) {
		writeError(writer, http.StatusBadRequest, "inputs must be valid JSON")
		return
	}
	run, err := server.runtime.Create(request.Context(), durable.Definition{
		ID: body.ID, Code: body.Source, Seed: body.Seed, Inputs: append(json.RawMessage(nil), body.Inputs...),
		ArtifactSHA256: server.runtime.ArtifactID(), EnvironmentVersion: server.environmentVersion,
	})
	if err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, responseForRun(run))
}

func (server *Server) handleGet(writer http.ResponseWriter, request *http.Request, id string) {
	run, err := server.runtime.Get(request.Context(), id)
	if err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, responseForRun(run))
}

func (server *Server) handleAttempt(writer http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	attempt, err := server.executor.Admit(request.Context(), id)
	if err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	result, err := attempt.Result(request.Context())
	if err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, attemptResponse{
		State: result.State, Value: result.Output.Value, Stdout: result.Output.Stdout,
		Transformed: result.Output.Transformed, Park: result.Park, Failure: result.Failure,
	})
}

func (server *Server) handleCancel(writer http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := server.executor.Cancel(request.Context(), id); err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"cancelled": true})
}

func (server *Server) handleResolve(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body resolveRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(writer, requestErrorStatus(err), err.Error())
		return
	}
	hasResult := len(body.Result) != 0
	hasError := body.Error != ""
	if body.WaitID == "" || hasResult == hasError || (hasResult && !json.Valid(body.Result)) {
		writeError(writer, http.StatusBadRequest, "invalid wait decision")
		return
	}
	if err := server.runtime.Decide(request.Context(), body.WaitID, durable.Decision{Result: body.Result, Error: body.Error}); err != nil {
		writeError(writer, statusFor(err), err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"resolved": true})
}

func responseForRun(run durable.Run) runResponse {
	return runResponse{ID: run.Definition.ID, Status: run.Status, Outcome: run.Outcome, Reason: run.Reason}
}

func (server *Server) isClosed() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closed
}

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

func statusFor(err error) int {
	switch {
	case errors.Is(err, durable.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, durable.ErrConflict), errors.Is(err, durable.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, durable.ErrQueueFull):
		return http.StatusTooManyRequests
	case errors.Is(err, durable.ErrClosed):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

var _ http.Handler = (*Server)(nil)
