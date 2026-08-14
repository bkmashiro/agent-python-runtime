package capabilityrpc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const CallPath = "/v1/calls"

func HTTPHandler(registry *Registry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(CallPath, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodPost || registry == nil {
			writeHTTPError(writer, http.StatusNotFound, "not_found")
			return
		}
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") || len(authorization) <= len("Bearer ") {
			writeHTTPError(writer, http.StatusUnauthorized, "channel_denied")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxFrameBytes))
		if err != nil {
			writeHTTPError(writer, http.StatusRequestEntityTooLarge, "invalid_request")
			return
		}
		response, err := registry.Dispatch(request.Context(), strings.TrimPrefix(authorization, "Bearer "), body)
		if err != nil {
			status := http.StatusBadRequest
			code := "invalid_request"
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

func writeHTTPError(writer http.ResponseWriter, status int, code string) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		SchemaVersion string `json:"schema_version"`
		ErrorCode     string `json:"error_code"`
	}{SchemaVersion: SchemaVersion, ErrorCode: code})
}
