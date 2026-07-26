package agentic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var providerToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var bfclStringPunctuation = regexp.MustCompile(`[ ,./\-_*^]`)

type FunctionCall struct {
	Name      string
	Arguments json.RawMessage
}

type CallScore struct {
	Passed    bool   `json:"passed"`
	ErrorCode string `json:"error_code,omitempty"`
}

type expectedCallOracle struct {
	Kind  string                        `json:"kind"`
	Turns []map[string]map[string][]any `json:"turns"`
}

func ProviderToolName(canonical string) (string, error) {
	providerName := strings.ReplaceAll(canonical, ".", "_")
	if !providerToolNamePattern.MatchString(providerName) {
		return "", ErrDataset
	}
	return providerName, nil
}

func ScoreStatelessCalls(task Task, calls []FunctionCall) CallScore {
	if task.Track != "stateless_function_calling" || task.Interaction.Mode != "single_turn" {
		return CallScore{ErrorCode: "unsupported_task"}
	}
	var oracle expectedCallOracle
	if decodeUseNumber(task.Oracle, &oracle) != nil || oracle.Kind != "expected_call_trace" || len(oracle.Turns) == 0 {
		return CallScore{ErrorCode: "invalid_oracle"}
	}
	if len(calls) != len(oracle.Turns) {
		return CallScore{ErrorCode: "wrong_call_count"}
	}
	tools := make(map[string]Tool, len(task.Tools))
	providerNames := make(map[string]string, len(task.Tools))
	for _, tool := range task.Tools {
		providerName, err := ProviderToolName(tool.Name)
		if err != nil {
			return CallScore{ErrorCode: "invalid_tool_surface"}
		}
		if prior := providerNames[providerName]; prior != "" && prior != tool.Name {
			return CallScore{ErrorCode: "invalid_tool_surface"}
		}
		providerNames[providerName] = tool.Name
		tools[tool.Name] = tool
	}
	matched := make([]bool, len(calls))
	for _, expected := range oracle.Turns {
		found := false
		for index, actual := range calls {
			if matched[index] {
				continue
			}
			canonical := providerNames[actual.Name]
			if canonical == "" {
				canonical = actual.Name
			}
			if matchExpectedCall(expected, canonical, actual.Arguments, tools[canonical]) {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			return CallScore{ErrorCode: "call_mismatch"}
		}
	}
	return CallScore{Passed: true}
}

func matchExpectedCall(expected map[string]map[string][]any, name string, raw json.RawMessage, tool Tool) bool {
	if len(expected) != 1 || tool.Name == "" {
		return false
	}
	allowed, exists := expected[name]
	if !exists {
		return false
	}
	var arguments map[string]any
	if decodeUseNumber(raw, &arguments) != nil || arguments == nil {
		return false
	}
	schema, err := compileSchema(tool.Parameters)
	if err != nil || schema.Validate(arguments) != nil {
		return false
	}
	for key, value := range arguments {
		options, ok := allowed[key]
		if !ok || !matchesAny(value, options) {
			return false
		}
	}
	for key, options := range allowed {
		if _, ok := arguments[key]; !ok && !containsOptionalMarker(options) {
			return false
		}
	}
	return true
}

func matchesAny(value any, options []any) bool {
	for _, option := range options {
		if equivalentBFCL(value, option) {
			return true
		}
	}
	return false
}

func equivalentBFCL(left, right any) bool {
	switch value := left.(type) {
	case string:
		candidate, ok := right.(string)
		return ok && normalizeBFCLString(value) == normalizeBFCLString(candidate)
	case json.Number:
		candidate, ok := right.(json.Number)
		if !ok {
			return false
		}
		leftRat, leftOK := new(big.Rat).SetString(value.String())
		rightRat, rightOK := new(big.Rat).SetString(candidate.String())
		return leftOK && rightOK && leftRat.Cmp(rightRat) == 0
	case []any:
		candidate, ok := right.([]any)
		if !ok || len(value) != len(candidate) {
			return false
		}
		for index := range value {
			if !equivalentBFCL(value[index], candidate[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		candidate, ok := right.(map[string]any)
		if !ok || len(value) != len(candidate) {
			return false
		}
		for key, item := range value {
			candidateItem, exists := candidate[key]
			if !exists || !equivalentBFCL(item, candidateItem) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

func normalizeBFCLString(value string) string {
	return strings.ReplaceAll(strings.ToLower(bfclStringPunctuation.ReplaceAllString(value, "")), "'", `"`)
}

func containsOptionalMarker(options []any) bool {
	for _, option := range options {
		if value, ok := option.(string); ok && value == "" {
			return true
		}
	}
	return false
}

func decodeUseNumber(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrDataset
		}
		return err
	}
	return nil
}

func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	var document any
	if json.Unmarshal(raw, &document) != nil {
		return nil, ErrDataset
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(denyExternalSchemaLoader{})
	const resource = "mem:///external-agentic-call-schema.json"
	if compiler.AddResource(resource, document) != nil {
		return nil, ErrDataset
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, ErrDataset
	}
	return schema, nil
}
