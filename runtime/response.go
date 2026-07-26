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

type RunResponse struct {
	Status   RunResponseStatus `json:"status"`
	Result   json.RawMessage   `json:"result"`
	Receipts json.RawMessage   `json:"receipts"`
	Metrics  *struct {
		GuestTimeMS     *float64 `json:"guest_time_ms,omitempty"`
		CapabilityCalls uint32   `json:"capability_calls"`
		ResultBytes     uint32   `json:"result_bytes"`
	} `json:"metrics"`
	Error *struct {
		Code      string  `json:"code"`
		Message   string  `json:"message"`
		ErrorType *string `json:"error_type,omitempty"`
		Traceback *string `json:"traceback,omitempty"`
	} `json:"error"`
}

func DecodeAndValidateRunResponse(request RunRequest, data []byte) (RunResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response RunResponse
	if err := decoder.Decode(&response); err != nil {
		return RunResponse{}, fmt.Errorf("decode run response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RunResponse{}, errors.New("run response contains trailing JSON")
	}
	if (response.Status != RunResponseOK && response.Status != RunResponseError) || len(response.Result) == 0 || len(response.Receipts) == 0 || response.Metrics == nil || (response.Metrics.GuestTimeMS != nil && *response.Metrics.GuestTimeMS < 0) {
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
