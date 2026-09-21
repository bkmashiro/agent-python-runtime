// Package corpus defines frozen programs and Host-owned tool replay fixtures.
package corpus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const SchemaVersion = 1

// Case is one immutable program, replay fixture, and result oracle.
type Case struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Source        string          `json:"source"`
	Inputs        json.RawMessage `json:"inputs"`
	Tools         []ToolSpec      `json:"tools,omitempty"`
	Replay        []Replay        `json:"replay,omitempty"`
	Expected      json.RawMessage `json:"expected"`
	Origin        Origin          `json:"origin"`
}

// Origin binds an imported case to a named upstream revision and identifier.
type Origin struct {
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
	CaseID   string `json:"case_id"`
}

// ToolSpec is the Host-owned catalog metadata needed by the existing ABI.
type ToolSpec struct {
	Name           string          `json:"name"`
	PythonPath     string          `json:"python_path"`
	Description    string          `json:"description,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	AllowEarlyRead bool            `json:"allow_early_read,omitempty"`
}

// Replay is a deterministic response for one exact tool/argument pair.
// Count defaults to one and permits repeated stable reads with the same result.
type Replay struct {
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
	Count int             `json:"count,omitempty"`
}

func (c *Case) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", c.SchemaVersion)
	}
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("case id is required")
	}
	if strings.TrimSpace(c.Source) == "" {
		return errors.New("case source is required")
	}
	if len(c.Source) > 1<<20 {
		return errors.New("case source exceeds 1 MiB")
	}
	if !validJSON(c.Inputs) {
		return errors.New("case inputs must be JSON")
	}
	if !validJSON(c.Expected) {
		return errors.New("case expected result must be JSON")
	}
	if c.Origin.Dataset == "" || c.Origin.Revision == "" || c.Origin.CaseID == "" {
		return errors.New("case origin dataset, revision, and case_id are required")
	}
	tools := make(map[string]struct{}, len(c.Tools))
	for _, tool := range c.Tools {
		if tool.Name == "" || tool.PythonPath == "" {
			return errors.New("tool name and python_path are required")
		}
		if _, exists := tools[tool.Name]; exists {
			return fmt.Errorf("duplicate tool %q", tool.Name)
		}
		tools[tool.Name] = struct{}{}
		if len(tool.InputSchema) > 0 && !validJSONObject(tool.InputSchema) {
			return fmt.Errorf("tool %q input_schema must be a JSON object", tool.Name)
		}
	}
	keys := make(map[string]struct{}, len(c.Replay))
	for index := range c.Replay {
		entry := &c.Replay[index]
		if _, exists := tools[entry.Tool]; !exists {
			return fmt.Errorf("replay references unknown tool %q", entry.Tool)
		}
		if !validJSON(entry.Args) {
			return fmt.Errorf("replay for %q has invalid args", entry.Tool)
		}
		hasValue := len(entry.Value) > 0
		hasError := entry.Error != ""
		if hasValue == hasError {
			return fmt.Errorf("replay for %q must contain exactly one of value or error", entry.Tool)
		}
		if hasValue && !validJSON(entry.Value) {
			return fmt.Errorf("replay for %q has invalid value", entry.Tool)
		}
		if entry.Count == 0 {
			entry.Count = 1
		}
		if entry.Count < 1 {
			return fmt.Errorf("replay for %q has invalid count", entry.Tool)
		}
		canonical, err := canonicalJSON(entry.Args)
		if err != nil {
			return err
		}
		key := replayKey(entry.Tool, canonical)
		if _, exists := keys[key]; exists {
			return fmt.Errorf("duplicate replay for tool %q and args %s", entry.Tool, canonical)
		}
		keys[key] = struct{}{}
	}
	return nil
}

func DecodeJSONL(reader io.Reader) ([]Case, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var cases []Case
	line := 0
	ids := make(map[string]struct{})
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var item Case
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("corpus line %d: %w", line, err)
		}
		if err := item.Validate(); err != nil {
			return nil, fmt.Errorf("corpus line %d (%s): %w", line, item.ID, err)
		}
		if _, exists := ids[item.ID]; exists {
			return nil, fmt.Errorf("corpus line %d: duplicate case id %q", line, item.ID)
		}
		ids[item.ID] = struct{}{}
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, errors.New("corpus contains no cases")
	}
	return cases, nil
}

func validJSON(raw json.RawMessage) bool { return len(raw) > 0 && json.Valid(raw) }

func validJSONObject(raw json.RawMessage) bool {
	if !validJSON(raw) {
		return false
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func canonicalJSON(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func replayKey(tool, args string) string { return tool + "\x00" + args }

// EqualJSON compares two JSON values independent of object key order and whitespace.
func EqualJSON(left, right json.RawMessage) bool {
	a, err := canonicalJSON(left)
	if err != nil {
		return false
	}
	b, err := canonicalJSON(right)
	return err == nil && a == b
}
