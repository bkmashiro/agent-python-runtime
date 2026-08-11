package runtime

import (
	"bytes"
	"crypto/sha256"
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
	// Compatibility is an untrusted profile/import declaration. The Host
	// independently binds the artifact profile and allowed import set.
	Compatibility *CompatibilityDeclaration `json:"compatibility,omitempty"`
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
	if rawCompatibility, present := envelope["compatibility"]; present && bytes.Equal(bytes.TrimSpace(rawCompatibility), []byte("null")) {
		return RunRequest{}, errors.New("compatibility must be an object")
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
	if err := ValidateCompatibilityDeclaration(request.Compatibility); err != nil {
		return RunRequest{}, err
	}
	var input any
	if err := json.Unmarshal(request.Inputs, &input); err != nil {
		return RunRequest{}, fmt.Errorf("inputs must be JSON: %w", err)
	}
	return request, nil
}

// EncodeRunRequest produces the deterministic Host projection used for Guest
// execution and workspace receipt identity.
func EncodeRunRequest(request RunRequest) ([]byte, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode run request: %w", err)
	}
	return encoded, nil
}

func RunRequestSHA256(request RunRequest) (string, error) {
	encoded, err := EncodeRunRequest(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
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
