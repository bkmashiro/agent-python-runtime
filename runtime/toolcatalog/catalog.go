package toolcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxTools       = 128
	maxSchemaBytes = 256 * 1024
	maxDescription = 2048
	maxName        = 256
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Projection string

const (
	ProjectionExact       Projection = "exact"
	ProjectionLossy       Projection = "lossy"
	ProjectionUnsupported Projection = "unsupported"
)

type DiscoveredTool struct {
	ToolID         string
	ServerID       string
	Name           string
	Description    string
	HandlerVersion string
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
}

type Grant struct {
	ToolID       string
	EffectClass  string
	Policy       string
	GrantVersion string
	MaxCalls     uint32
}

type BuildOptions struct {
	Revision     uint64
	DiscoveredAt time.Time
}

type Parameter struct {
	Name          string
	PythonName    string
	Annotation    string
	Required      bool
	HasDefault    bool
	DefaultPython string
}

type TypeField struct {
	Name       string
	Annotation string
	Required   bool
}

type TypeDefinition struct {
	Name   string
	Fields []TypeField
}

type Tool struct {
	ToolID          string
	ServerID        string
	Name            string
	PythonName      string
	Description     string
	HandlerVersion  string
	InputSchema     json.RawMessage
	OutputSchema    json.RawMessage
	Projection      Projection
	EffectClass     string
	Policy          string
	GrantVersion    string
	MaxCalls        uint32
	TypeDefinitions []TypeDefinition
	Parameters      []Parameter
	ReturnType      string
}

type Snapshot struct {
	snapshotID   string
	revision     uint64
	digest       string
	discoveredAt time.Time
	tools        []Tool
}

func (snapshot Snapshot) SnapshotID() string      { return snapshot.snapshotID }
func (snapshot Snapshot) Revision() uint64        { return snapshot.revision }
func (snapshot Snapshot) Digest() string          { return snapshot.digest }
func (snapshot Snapshot) DiscoveredAt() time.Time { return snapshot.discoveredAt }

func (snapshot Snapshot) Tools() []Tool {
	result := make([]Tool, len(snapshot.tools))
	for index, tool := range snapshot.tools {
		result[index] = cloneTool(tool)
	}
	return result
}

func BuildSnapshot(discovered []DiscoveredTool, grants map[string]Grant, options BuildOptions) (Snapshot, error) {
	if options.Revision == 0 || len(discovered) == 0 || len(discovered) > maxTools {
		return Snapshot{}, errors.New("catalog revision and discovered tool count are outside bounds")
	}
	seenIDs := make(map[string]struct{}, len(discovered))
	seenPython := make(map[string]string, len(discovered))
	tools := make([]Tool, 0, len(discovered))
	for _, source := range discovered {
		if source.ToolID == "fetch_many" || !identifierPattern.MatchString(source.ToolID) || !identifierPattern.MatchString(source.ServerID) ||
			!identifierPattern.MatchString(source.HandlerVersion) || source.Name == "" || len(source.Name) > maxName ||
			len(source.Description) > maxDescription {
			return Snapshot{}, fmt.Errorf("invalid discovered tool metadata for %q", source.ToolID)
		}
		if _, duplicate := seenIDs[source.ToolID]; duplicate {
			return Snapshot{}, fmt.Errorf("duplicate tool id %q", source.ToolID)
		}
		seenIDs[source.ToolID] = struct{}{}
		grant, granted := grants[source.ToolID]
		if !granted || grant.ToolID != source.ToolID || !validGrant(grant) {
			return Snapshot{}, fmt.Errorf("tool %q has no matching Host grant", source.ToolID)
		}
		pythonName, err := normalizePythonIdentifier(source.Name)
		if err != nil {
			return Snapshot{}, fmt.Errorf("tool %q: %w", source.ToolID, err)
		}
		if prior, collision := seenPython[pythonName]; collision {
			return Snapshot{}, fmt.Errorf("Python name collision %q between %q and %q", pythonName, prior, source.ToolID)
		}
		seenPython[pythonName] = source.ToolID

		inputCanonical, inputDocument, err := canonicalSchema(source.InputSchema)
		if err != nil {
			return Snapshot{}, fmt.Errorf("tool %q input schema: %w", source.ToolID, err)
		}
		outputCanonical, outputDocument, err := canonicalSchema(source.OutputSchema)
		if err != nil {
			return Snapshot{}, fmt.Errorf("tool %q output schema: %w", source.ToolID, err)
		}
		parameters, typeDefinitions, inputProjection := projectInput(inputDocument, pythonName)
		returnType, outputProjection := projectType(outputDocument, 0)
		projection := mergeProjection(inputProjection, outputProjection)
		tool := Tool{
			ToolID: source.ToolID, ServerID: source.ServerID, Name: source.Name, PythonName: pythonName,
			Description: source.Description, HandlerVersion: source.HandlerVersion,
			InputSchema: inputCanonical, OutputSchema: outputCanonical, Projection: projection,
			EffectClass: grant.EffectClass, Policy: grant.Policy, GrantVersion: grant.GrantVersion, MaxCalls: grant.MaxCalls,
			TypeDefinitions: typeDefinitions, Parameters: parameters, ReturnType: returnType,
		}
		tools = append(tools, tool)
	}
	if len(grants) != len(seenIDs) {
		return Snapshot{}, errors.New("Host grants contain stale or undiscovered tools")
	}
	for _, tool := range tools {
		for _, other := range tools {
			if tool.ToolID != other.ToolID && tool.PythonName == other.ToolID {
				return Snapshot{}, fmt.Errorf("cross-namespace catalog collision %q", tool.PythonName)
			}
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].ToolID < tools[j].ToolID })
	digest, err := catalogDigest(options.Revision, tools)
	if err != nil {
		return Snapshot{}, err
	}
	discoveredAt := options.DiscoveredAt.UTC()
	if discoveredAt.IsZero() {
		discoveredAt = time.Unix(0, 0).UTC()
	}
	return Snapshot{
		snapshotID: "catalog_" + strings.TrimPrefix(digest, "sha256:")[:32],
		revision:   options.Revision, digest: digest, discoveredAt: discoveredAt, tools: cloneTools(tools),
	}, nil
}

func validGrant(grant Grant) bool {
	if !identifierPattern.MatchString(grant.GrantVersion) || grant.MaxCalls == 0 || grant.MaxCalls > 1024 {
		return false
	}
	switch grant.EffectClass {
	case "read_only", "reversible", "compensatable", "irreversible":
	default:
		return false
	}
	switch grant.Policy {
	case "DENY", "AUTO_COMMIT", "AGENT_COMMIT_REQUIRED", "USER_APPROVAL_REQUIRED":
		return true
	default:
		return false
	}
}

func normalizePythonIdentifier(value string) (string, error) {
	var builder strings.Builder
	for index, character := range value {
		valid := character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || index > 0 && character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	if result == "" {
		return "", errors.New("tool name has no representable Python identifier")
	}
	if result[0] >= '0' && result[0] <= '9' {
		result = "tool_" + result
	}
	if pythonKeywords[result] {
		result += "_tool"
	}
	if generatedPythonReserved[result] {
		return "", fmt.Errorf("Python identifier %q is reserved by the generated SDK", result)
	}
	return result, nil
}

var forbiddenAuthorityProperty = map[string]bool{
	"credential": true, "credentials": true, "password": true, "secret": true,
	"api_key": true, "api-key": true, "authorization": true, "headers": true,
}

var generatedPythonReserved = map[string]bool{
	"fetch_many": true, "_call": true, "_UNSET": true, "_arguments": true,
	"_json": true, "_TOOL_METADATA": true,
	"CATALOG_DIGEST": true, "CATALOG_REVISION": true,
	"Any": true, "Literal": true, "describe_tools": true, "describe_tool": true,
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true,
	"async": true, "await": true, "break": true, "class": true, "continue": true, "def": true,
	"del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

func canonicalSchema(raw json.RawMessage) (json.RawMessage, map[string]any, error) {
	if len(raw) == 0 || len(raw) > maxSchemaBytes {
		return nil, nil, errors.New("schema size is outside bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("schema contains multiple JSON values")
	}
	if len(document) == 0 {
		return nil, nil, errors.New("schema is empty")
	}
	if err := validateSchemaBounds(document, 0, new(int)); err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(canonical)
	resource := "mem:///schema-" + hex.EncodeToString(sum[:]) + ".json"
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, nil, err
	}
	if _, err := compiler.Compile(resource); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON Schema: %w", err)
	}
	return canonical, document, nil
}

func validateSchemaBounds(value any, depth int, nodes *int) error {
	if depth > maxProjectionDepth {
		return errors.New("schema depth exceeds bound")
	}
	*nodes++
	if *nodes > 4096 {
		return errors.New("schema node count exceeds bound")
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 256 {
			return errors.New("schema object keyword count exceeds bound")
		}
		for key, child := range typed {
			switch key {
			case "properties":
				if properties, ok := child.(map[string]any); ok {
					if len(properties) > 128 {
						return errors.New("schema property count exceeds bound")
					}
					for name := range properties {
						if forbiddenAuthorityProperty[strings.ToLower(name)] {
							return fmt.Errorf("schema property %q would expose ambient authority", name)
						}
					}
				}
			case "required":
				if required, ok := child.([]any); ok && len(required) > 128 {
					return errors.New("schema required count exceeds bound")
				}
			case "enum":
				if values, ok := child.([]any); ok && len(values) > 64 {
					return errors.New("schema enum count exceeds bound")
				}
			case "oneOf", "anyOf", "allOf":
				if alternatives, ok := child.([]any); ok && len(alternatives) > 8 {
					return errors.New("schema composition count exceeds bound")
				}
			}
			if err := validateSchemaBounds(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > 512 {
			return errors.New("schema array exceeds bound")
		}
		for _, child := range typed {
			if err := validateSchemaBounds(child, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

func catalogDigest(revision uint64, tools []Tool) (string, error) {
	type digestTool struct {
		ToolID, ServerID, Name, PythonName, Description, HandlerVersion string
		InputSchema, OutputSchema                                       json.RawMessage
		Projection, EffectClass, Policy, GrantVersion                   string
		MaxCalls                                                        uint32
	}
	document := struct {
		Revision uint64
		Tools    []digestTool
	}{Revision: revision, Tools: make([]digestTool, len(tools))}
	for index, tool := range tools {
		document.Tools[index] = digestTool{
			ToolID: tool.ToolID, ServerID: tool.ServerID, Name: tool.Name, PythonName: tool.PythonName,
			Description: tool.Description, HandlerVersion: tool.HandlerVersion,
			InputSchema: tool.InputSchema, OutputSchema: tool.OutputSchema,
			Projection: string(tool.Projection), EffectClass: tool.EffectClass, Policy: tool.Policy, GrantVersion: tool.GrantVersion, MaxCalls: tool.MaxCalls,
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func cloneTool(tool Tool) Tool {
	tool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
	tool.OutputSchema = append(json.RawMessage(nil), tool.OutputSchema...)
	definitions := tool.TypeDefinitions
	tool.TypeDefinitions = make([]TypeDefinition, len(definitions))
	for index, definition := range definitions {
		tool.TypeDefinitions[index] = definition
		tool.TypeDefinitions[index].Fields = append([]TypeField(nil), definition.Fields...)
	}
	tool.Parameters = append([]Parameter(nil), tool.Parameters...)
	return tool
}

func cloneTools(tools []Tool) []Tool {
	result := make([]Tool, len(tools))
	for index, tool := range tools {
		result[index] = cloneTool(tool)
	}
	return result
}
