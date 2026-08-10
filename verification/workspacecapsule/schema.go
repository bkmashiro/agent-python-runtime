package workspacecapsule

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed report-v2.schema.json
var reportSchemaBytes []byte

var (
	reportSchemaOnce sync.Once
	reportSchema     *jsonschema.Schema
	reportSchemaErr  error
)

// ValidateReportJSON validates one encoded v2 report against the embedded,
// fail-closed schema used by the CLI output boundary.
func ValidateReportJSON(payload []byte) error {
	if err := rejectDuplicateReportJSON(payload); err != nil {
		return err
	}
	schema, err := compiledReportSchema()
	if err != nil {
		return err
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return fmt.Errorf("decode workspace verification report: %w", err)
	}
	if err := schema.Validate(document); err != nil {
		return fmt.Errorf("validate workspace verification report: %w", err)
	}
	return nil
}

func rejectDuplicateReportJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	nodes := 0
	if err := consumeUniqueReportJSON(decoder, 0, &nodes); err != nil {
		return fmt.Errorf("workspace verification report JSON is not unique: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("workspace verification report contains trailing JSON")
	}
	return nil
}

func consumeUniqueReportJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 32 || *nodes >= 10000 {
		return errors.New("report JSON is too complex")
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
			if err != nil || !ok {
				return errors.New("invalid object key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = true
			if err := consumeUniqueReportJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid object closing token")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueReportJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid array closing token")
		}
	default:
		return errors.New("invalid composite token")
	}
	return nil
}

func compiledReportSchema() (*jsonschema.Schema, error) {
	reportSchemaOnce.Do(func() {
		var document any
		if err := json.Unmarshal(reportSchemaBytes, &document); err != nil {
			reportSchemaErr = fmt.Errorf("decode embedded workspace report schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("workspace-report-v2.schema.json", document); err != nil {
			reportSchemaErr = fmt.Errorf("load embedded workspace report schema: %w", err)
			return
		}
		reportSchema, reportSchemaErr = compiler.Compile("workspace-report-v2.schema.json")
	})
	if reportSchemaErr != nil {
		return nil, reportSchemaErr
	}
	if reportSchema == nil {
		return nil, errors.New("embedded workspace report schema is unavailable")
	}
	return reportSchema, nil
}
