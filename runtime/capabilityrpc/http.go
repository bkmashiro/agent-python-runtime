package capabilityrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	CallPath  = "/v1/calls"
	ReadyPath = "/v1/ready"
)

func HTTPHandler(registry *Registry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ReadyPath, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		credential, ok := authorize(writer, request, registry)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxFrameBytes))
		if err != nil {
			writeHTTPError(writer, http.StatusRequestEntityTooLarge, "invalid_request")
			return
		}
		identity, err := decodeReadyRequest(body)
		if err != nil || registry.Check(credential, identity) != nil {
			writeHTTPError(writer, http.StatusForbidden, "channel_denied")
			return
		}
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(struct {
			SchemaVersion string `json:"schema_version"`
			Status        string `json:"status"`
			ChannelID     string `json:"channel_id"`
			PlanSHA256    string `json:"plan_sha256"`
		}{SchemaVersion: SchemaVersion, Status: "ready", ChannelID: identity.ChannelID, PlanSHA256: identity.PlanSHA256})
	})
	mux.HandleFunc(CallPath, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		credential, ok := authorize(writer, request, registry)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxFrameBytes))
		if err != nil {
			writeHTTPError(writer, http.StatusRequestEntityTooLarge, "invalid_request")
			return
		}
		response, err := registry.Dispatch(request.Context(), credential, body)
		if err != nil {
			status, code := http.StatusBadRequest, "invalid_request"
			switch {
			case errors.Is(err, ErrChannelDenied):
				status, code = http.StatusForbidden, "channel_denied"
			case errors.Is(err, ErrCallIdentityMismatch):
				status, code = http.StatusConflict, "call_identity_mismatch"
			}
			writeHTTPError(writer, status, code)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(response)
	})
	return mux
}

func authorize(writer http.ResponseWriter, request *http.Request, registry *Registry) (string, bool) {
	if request.Method != http.MethodPost || registry == nil {
		writeHTTPError(writer, http.StatusNotFound, "not_found")
		return "", false
	}
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || len(authorization) <= len("Bearer ") {
		writeHTTPError(writer, http.StatusUnauthorized, "channel_denied")
		return "", false
	}
	return strings.TrimPrefix(authorization, "Bearer "), true
}

func decodeReadyRequest(body []byte) (Request, error) {
	if len(body) == 0 || len(body) > maxFrameBytes || rejectDuplicateJSON(body) != nil {
		return Request{}, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, ErrInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Request{}, ErrInvalidRequest
	}
	if len(request.Call) != 0 || request.ValidateIdentity() != nil {
		return Request{}, ErrInvalidRequest
	}
	return request, nil
}

func writeHTTPError(writer http.ResponseWriter, status int, code string) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		SchemaVersion string `json:"schema_version"`
		ErrorCode     string `json:"error_code"`
	}{SchemaVersion: SchemaVersion, ErrorCode: code})
}
