package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type RunResponseStatus string

const (
	RunResponseOK    RunResponseStatus = "ok"
	RunResponseError RunResponseStatus = "error"
)

var ErrRunResultSchemaMismatch = errors.New("run result does not match output_schema")

type RunMetrics struct {
	GuestTimeMS     *float64 `json:"guest_time_ms,omitempty"`
	CapabilityCalls uint32   `json:"capability_calls"`
	ResultBytes     uint32   `json:"result_bytes"`
}

type RunError struct {
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	ErrorType *string `json:"error_type,omitempty"`
	Traceback *string `json:"traceback,omitempty"`
}

type RunResponse struct {
	Status       RunResponseStatus `json:"status"`
	Result       json.RawMessage   `json:"result"`
	Receipts     json.RawMessage   `json:"receipts"`
	Metrics      *RunMetrics       `json:"metrics"`
	Error        *RunError         `json:"error"`
	ExecutionRef *ExecutionRef     `json:"execution_ref,omitempty"`
}

func DecodeAndValidateGuestRunResponse(request RunRequest, data []byte) (RunResponse, error) {
	if err := rejectDuplicateBoundedJSON(data); err != nil {
		return RunResponse{}, errors.New("Guest run response contains duplicate JSON keys")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil || !hasExactKeys(envelope, "status", "result", "receipts", "metrics", "error") {
		return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
	}
	var metrics map[string]json.RawMessage
	if json.Unmarshal(envelope["metrics"], &metrics) != nil || !hasRequiredAndOnlyExactKeys(metrics,
		[]string{"capability_calls", "result_bytes"}, "guest_time_ms") {
		return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
	}
	if rawError := bytes.TrimSpace(envelope["error"]); !bytes.Equal(rawError, []byte("null")) {
		var guestError map[string]json.RawMessage
		if json.Unmarshal(rawError, &guestError) != nil || !hasRequiredAndOnlyExactKeys(guestError,
			[]string{"code", "message"}, "error_type", "traceback") {
			return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
		}
	}
	return decodeAndValidateRunResponse(request, data, false)
}

func rejectDuplicateBoundedJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueBoundedJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("run response contains trailing JSON")
	}
	return nil
}

const maxCanonicalJSONNodes = 16384

func consumeUniqueBoundedJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxCanonicalJSONNodes {
		return errors.New("run response JSON is too complex")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return errors.New("run response contains duplicate JSON keys")
			}
			seen[key] = true
			if err := consumeUniqueBoundedJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("run response contains invalid JSON")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueBoundedJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("run response contains invalid JSON")
		}
	default:
		return errors.New("run response contains invalid JSON")
	}
	return nil
}

func hasExactKeys(values map[string]json.RawMessage, required ...string) bool {
	if len(values) != len(required) {
		return false
	}
	for _, key := range required {
		if _, exists := values[key]; !exists {
			return false
		}
	}
	return true
}

func hasRequiredAndOnlyExactKeys(values map[string]json.RawMessage, required []string, optional ...string) bool {
	allow := make(map[string]struct{}, len(required)+len(optional))
	for _, key := range required {
		allow[key] = struct{}{}
		if _, exists := values[key]; !exists {
			return false
		}
	}
	for _, key := range optional {
		allow[key] = struct{}{}
	}
	for key := range values {
		if _, exists := allow[key]; !exists {
			return false
		}
	}
	return true
}

func DecodeAndValidateRunResponse(request RunRequest, data []byte) (RunResponse, error) {
	return decodeAndValidateRunResponse(request, data, true)
}

func decodeAndValidateRunResponse(request RunRequest, data []byte, requireHostEvidence bool) (RunResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response RunResponse
	if err := decoder.Decode(&response); err != nil {
		return RunResponse{}, fmt.Errorf("decode run response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RunResponse{}, errors.New("run response contains trailing JSON")
	}
	hostEvidenceInvalid := !requireHostEvidence && response.ExecutionRef != nil
	if (response.Status != RunResponseOK && response.Status != RunResponseError) || len(response.Result) == 0 || len(response.Receipts) == 0 || response.Metrics == nil ||
		(response.Metrics.GuestTimeMS != nil && *response.Metrics.GuestTimeMS < 0) || (response.ExecutionRef != nil && response.ExecutionRef.Validate() != nil) ||
		hostEvidenceInvalid {
		return RunResponse{}, errors.New("run response has invalid required fields")
	}
	var receipts []any
	if json.Unmarshal(response.Receipts, &receipts) != nil {
		return RunResponse{}, errors.New("run response receipts are not an array")
	}
	if response.Status == RunResponseOK {
		if response.Error != nil {
			return RunResponse{}, errors.New("successful run response contains an error")
		}
		if len(request.OutputSchema) != 0 && string(request.OutputSchema) != "null" {
			if err := validateRunOutput(request.OutputSchema, response.Result); err != nil {
				return response, err
			}
		}
	} else {
		if response.Error == nil || response.Error.Code == "" || len(response.Error.Code) > 128 || response.Error.Message == "" || len(response.Error.Message) > 4096 || string(response.Result) != "null" {
			return RunResponse{}, errors.New("failed run response has invalid error fields")
		}
	}
	return response, nil
}

type denySchemaURLLoader struct{}

func (denySchemaURLLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

func validateRunOutput(schemaBytes, resultBytes []byte) error {
	var schemaDocument any
	if json.Unmarshal(schemaBytes, &schemaDocument) != nil {
		return errors.New("output_schema is not valid JSON")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(denySchemaURLLoader{})
	if err := compiler.AddResource("mem:///run-output.schema.json", schemaDocument); err != nil {
		return errors.New("output_schema is invalid")
	}
	compiled, err := compiler.Compile("mem:///run-output.schema.json")
	if err != nil {
		return errors.New("output_schema is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(resultBytes))
	decoder.UseNumber()
	var result any
	if decoder.Decode(&result) != nil {
		return errors.New("run result is invalid JSON")
	}
	if err := compiled.Validate(result); err != nil {
		return ErrRunResultSchemaMismatch
	}
	return nil
}
