package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxCOWPressureEvidenceBytes = 8 << 20
	maxCOWPressureSchemaBytes   = 1 << 20
	maxCOWPressureJSONNodes     = 1 << 20
	maxCOWPressureJSONDepth     = 64
)

func runCOWPressureValidationMain(options benchmarkOptions) error {
	if options.InputPath == "" || options.SchemaPath == "" {
		return errors.New("validate-cow-pressure requires -input and -schema")
	}
	evidenceBytes, err := readRegularFileBounded(options.InputPath, maxCOWPressureEvidenceBytes)
	if err != nil {
		return fmt.Errorf("read cow-pressure evidence: %w", err)
	}
	schemaBytes, err := readRegularFileBounded(options.SchemaPath, maxCOWPressureSchemaBytes)
	if err != nil {
		return fmt.Errorf("read cow-pressure schema: %w", err)
	}
	if err := validateCOWPressureDocument(evidenceBytes, schemaBytes); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"valid\":true,\"schema_version\":11,\"evidence_kind\":\"cow-pressure\"}\n")
	return nil
}

func readRegularFileBounded(path string, maximum int64) ([]byte, error) {
	if path == "" || maximum <= 0 {
		return nil, errors.New("invalid bounded file request")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximum {
		return nil, errors.New("input must be a non-empty bounded regular file")
	}
	handle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	after, err := handle.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, errors.New("input identity changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(handle, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) != after.Size() || int64(len(payload)) > maximum {
		return nil, errors.New("input size changed while reading")
	}
	return payload, nil
}

func validateCOWPressureDocument(evidenceBytes, schemaBytes []byte) error {
	if len(evidenceBytes) == 0 || len(evidenceBytes) > maxCOWPressureEvidenceBytes || len(schemaBytes) == 0 || len(schemaBytes) > maxCOWPressureSchemaBytes {
		return errors.New("cow-pressure validator input is outside bounds")
	}
	if err := rejectDuplicateJSONDocument(evidenceBytes); err != nil {
		return fmt.Errorf("cow-pressure evidence JSON is invalid: %w", err)
	}
	if err := rejectDuplicateJSONDocument(schemaBytes); err != nil {
		return fmt.Errorf("cow-pressure schema JSON is invalid: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://agent-python-runtime.invalid/benchmark/v1/cow-pressure.schema.json"
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		return fmt.Errorf("decode cow-pressure schema: %w", err)
	}
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		return fmt.Errorf("load cow-pressure schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("compile cow-pressure schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(evidenceBytes, &document); err != nil {
		return fmt.Errorf("decode cow-pressure schema document: %w", err)
	}
	if err := compiled.Validate(document); err != nil {
		return fmt.Errorf("validate cow-pressure JSON Schema: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(evidenceBytes))
	decoder.DisallowUnknownFields()
	var evidence cowPressureEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return fmt.Errorf("decode cow-pressure semantic evidence: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("cow-pressure evidence has trailing JSON: token=%v err=%v", token, err)
	}
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate cow-pressure semantic evidence: %w", err)
	}
	return nil
}

func rejectDuplicateJSONDocument(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	nodes := 0
	if err := walkUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON token=%v err=%v", token, err)
	}
	return nil
}

func walkUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > maxCOWPressureJSONDepth {
		return errors.New("JSON nesting exceeds bound")
	}
	*nodes = *nodes + 1
	if *nodes > maxCOWPressureJSONNodes {
		return errors.New("JSON node count exceeds bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("unterminated JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}
