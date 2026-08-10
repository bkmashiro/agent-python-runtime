package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// RunRequest is untrusted model-produced data. It intentionally contains no
// capabilities, credentials, destinations, mounts, environment, or budgets.
type RunRequest struct {
	RunID        string          `json:"run_id"`
	Code         string          `json:"code"`
	Inputs       json.RawMessage `json:"inputs"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	// Requirements are compatibility declarations, not grants. They can only
	// narrow admission and never authorize Host resources or ambient access.
	Requirements []RequiredFeature `json:"requirements,omitempty"`
}

func DecodeRunRequest(data []byte) (RunRequest, error) {
	if err := rejectDuplicateBoundedJSON(data); err != nil {
		return RunRequest{}, errors.New("run request JSON is invalid or contains duplicate keys")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return RunRequest{}, fmt.Errorf("decode run request: %w", err)
	}
	if rawRequirements, present := envelope["requirements"]; present && bytes.Equal(bytes.TrimSpace(rawRequirements), []byte("null")) {
		return RunRequest{}, errors.New("requirements must be an array")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request RunRequest
	if err := decoder.Decode(&request); err != nil {
		return RunRequest{}, fmt.Errorf("decode run request: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RunRequest{}, err
	}
	if request.RunID == "" {
		return RunRequest{}, errors.New("run_id is required")
	}
	if request.Code == "" {
		return RunRequest{}, errors.New("code is required")
	}
	if request.Inputs == nil {
		return RunRequest{}, errors.New("inputs is required")
	}
	if err := ValidateRunRequirements(request.Requirements); err != nil {
		return RunRequest{}, err
	}
	var input any
	if err := json.Unmarshal(request.Inputs, &input); err != nil {
		return RunRequest{}, fmt.Errorf("inputs must be JSON: %w", err)
	}
	return request, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return errors.New("run request contains trailing JSON")
}
