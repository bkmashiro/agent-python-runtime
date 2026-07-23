package toolcatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const maxProjectionDepth = 8

func projectInput(schema map[string]any, toolPythonName string) ([]Parameter, []TypeDefinition, Projection) {
	if schema["$ref"] != nil || schemaType(schema) != "object" {
		return nil, nil, ProjectionUnsupported
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		if len(schema) == 1 {
			return nil, nil, ProjectionLossy
		}
		return nil, nil, ProjectionUnsupported
	}
	required := map[string]bool{}
	if rawRequired, exists := schema["required"]; exists {
		values, ok := rawRequired.([]any)
		if !ok {
			return nil, nil, ProjectionUnsupported
		}
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, nil, ProjectionUnsupported
			}
			required[name] = true
		}
	}
	projection := ProjectionExact
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		projection = ProjectionLossy
	}
	parameters := make([]Parameter, 0, len(properties))
	definitions := make([]TypeDefinition, 0)
	definitionNames := map[string]bool{}
	pythonNames := map[string]bool{}
	for name, rawProperty := range properties {
		property, ok := rawProperty.(map[string]any)
		if !ok || name == "" || len(name) > maxName {
			return nil, nil, ProjectionUnsupported
		}
		pythonName, err := normalizePythonIdentifier(name)
		if err != nil || pythonNames[pythonName] {
			return nil, nil, ProjectionUnsupported
		}
		pythonNames[pythonName] = true
		annotation, projected := projectType(property, 1)
		if schemaType(property) == "object" {
			definition, typedProjection := projectTypedDict(toolPythonName, pythonName, property)
			if typedProjection == ProjectionUnsupported || definitionNames[definition.Name] {
				return nil, nil, ProjectionUnsupported
			}
			definitionNames[definition.Name] = true
			definitions = append(definitions, definition)
			annotation, projected = definition.Name, typedProjection
		}
		defaultPython, hasDefault, defaultProjection := projectDefault(property)
		projection = mergeProjection(projection, projected, defaultProjection)
		if projected == ProjectionUnsupported || defaultProjection == ProjectionUnsupported {
			return nil, nil, ProjectionUnsupported
		}
		parameters = append(parameters, Parameter{
			Name: name, PythonName: pythonName, Annotation: annotation, Required: required[name],
			HasDefault: hasDefault && !required[name], DefaultPython: defaultPython,
		})
	}
	for name := range required {
		if _, exists := properties[name]; !exists {
			return nil, nil, ProjectionUnsupported
		}
	}
	sort.Slice(parameters, func(i, j int) bool {
		if parameters[i].Required != parameters[j].Required {
			return parameters[i].Required
		}
		return parameters[i].PythonName < parameters[j].PythonName
	})
	projection = mergeProjection(projection, constraintProjection(
		schema, "type", "properties", "required", "additionalProperties", "description", "title", "default",
	))
	return parameters, definitions, projection
}

func projectTypedDict(toolName, parameterName string, schema map[string]any) (TypeDefinition, Projection) {
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return TypeDefinition{}, ProjectionUnsupported
	}
	required := map[string]bool{}
	if rawRequired, exists := schema["required"]; exists {
		values, ok := rawRequired.([]any)
		if !ok {
			return TypeDefinition{}, ProjectionUnsupported
		}
		for _, raw := range values {
			name, ok := raw.(string)
			if !ok {
				return TypeDefinition{}, ProjectionUnsupported
			}
			required[name] = true
		}
	}
	definition := TypeDefinition{Name: pascalIdentifier(toolName) + pascalIdentifier(parameterName) + "Input"}
	projection := ProjectionExact
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		projection = ProjectionLossy
	}
	for name, raw := range properties {
		fieldSchema, ok := raw.(map[string]any)
		if !ok {
			return TypeDefinition{}, ProjectionUnsupported
		}
		annotation, projected := projectType(fieldSchema, 2)
		if projected == ProjectionUnsupported {
			return TypeDefinition{}, projected
		}
		projection = mergeProjection(projection, projected)
		if _, hasDefault := fieldSchema["default"]; hasDefault {
			projection = mergeProjection(projection, ProjectionLossy)
		}
		definition.Fields = append(definition.Fields, TypeField{Name: name, Annotation: annotation, Required: required[name]})
	}
	for name := range required {
		if _, exists := properties[name]; !exists {
			return TypeDefinition{}, ProjectionUnsupported
		}
	}
	sort.Slice(definition.Fields, func(i, j int) bool { return definition.Fields[i].Name < definition.Fields[j].Name })
	projection = mergeProjection(projection, constraintProjection(
		schema, "type", "properties", "required", "additionalProperties", "description", "title", "default",
	))
	return definition, projection
}

func pascalIdentifier(value string) string {
	parts := strings.FieldsFunc(value, func(character rune) bool { return character == '_' || character == '-' || character == '.' })
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		builder.WriteString(part[1:])
	}
	if builder.Len() == 0 {
		return "Tool"
	}
	return builder.String()
}

func projectType(schema map[string]any, depth int) (string, Projection) {
	if depth > maxProjectionDepth || schema["$ref"] != nil || schema["$dynamicRef"] != nil {
		return "Any", ProjectionUnsupported
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		return projectUnion(alternatives, depth)
	}
	if alternatives, ok := schema["oneOf"].([]any); ok {
		annotation, projection := projectUnion(alternatives, depth)
		return annotation, mergeProjection(projection, ProjectionLossy)
	}
	kind := schemaType(schema)
	switch kind {
	case "string":
		if rawEnum, ok := schema["enum"].([]any); ok {
			if len(rawEnum) == 0 || len(rawEnum) > 64 {
				return "Any", ProjectionUnsupported
			}
			values := make([]string, len(rawEnum))
			for index, raw := range rawEnum {
				value, ok := raw.(string)
				if !ok {
					return "Any", ProjectionUnsupported
				}
				values[index] = strconv.Quote(value)
			}
			return "Literal[" + strings.Join(values, ", ") + "]", constraintProjection(schema, "type", "enum", "description", "title", "default")
		}
		return "str", constraintProjection(schema, "type", "description", "title", "default")
	case "integer":
		return "int", constraintProjection(schema, "type", "description", "title", "default")
	case "number":
		return "float", constraintProjection(schema, "type", "description", "title", "default")
	case "boolean":
		return "bool", constraintProjection(schema, "type", "description", "title", "default")
	case "null":
		return "None", ProjectionExact
	case "array":
		item, ok := schema["items"].(map[string]any)
		if !ok {
			return "list[Any]", ProjectionUnsupported
		}
		annotation, projection := projectType(item, depth+1)
		if projection == ProjectionUnsupported {
			return "list[Any]", projection
		}
		projection = mergeProjection(projection, constraintProjection(schema, "type", "items", "description", "title", "default"))
		return "list[" + annotation + "]", projection
	case "object":
		// Nested structural types remain runtime-schema validated. Emitting anonymous
		// TypedDicts would require a named shared-definition IR, so this projection is
		// intentionally honest and lossy rather than pretending dict is exact.
		return "dict[str, Any]", ProjectionLossy
	case "":
		return "Any", ProjectionUnsupported
	default:
		return "Any", ProjectionUnsupported
	}
}

func projectUnion(alternatives []any, depth int) (string, Projection) {
	if len(alternatives) < 2 || len(alternatives) > 8 {
		return "Any", ProjectionUnsupported
	}
	annotations := make([]string, 0, len(alternatives))
	projection := ProjectionExact
	seen := map[string]bool{}
	for _, raw := range alternatives {
		alternative, ok := raw.(map[string]any)
		if !ok {
			return "Any", ProjectionUnsupported
		}
		annotation, projected := projectType(alternative, depth+1)
		if projected == ProjectionUnsupported {
			return "Any", projected
		}
		projection = mergeProjection(projection, projected)
		if !seen[annotation] {
			annotations = append(annotations, annotation)
			seen[annotation] = true
		}
	}
	if len(annotations) < 2 {
		return "Any", ProjectionUnsupported
	}
	sort.Strings(annotations)
	return strings.Join(annotations, " | "), projection
}

func projectDefault(schema map[string]any) (string, bool, Projection) {
	raw, exists := schema["default"]
	if !exists {
		return "", false, ProjectionExact
	}
	switch schemaType(schema) {
	case "string":
		value, ok := raw.(string)
		if !ok {
			return "", false, ProjectionUnsupported
		}
		if values, ok := schema["enum"].([]any); ok {
			matched := false
			for _, candidate := range values {
				if candidate == value {
					matched = true
					break
				}
			}
			if !matched {
				return "", false, ProjectionUnsupported
			}
		}
		return strconv.Quote(value), true, ProjectionExact
	case "integer":
		value, ok := raw.(json.Number)
		if !ok || strings.ContainsAny(value.String(), ".eE") {
			return "", false, ProjectionUnsupported
		}
		if _, err := value.Int64(); err != nil {
			return "", false, ProjectionUnsupported
		}
		return value.String(), true, ProjectionExact
	case "number":
		value, ok := raw.(json.Number)
		if !ok {
			return "", false, ProjectionUnsupported
		}
		if _, err := value.Float64(); err != nil {
			return "", false, ProjectionUnsupported
		}
		return value.String(), true, ProjectionExact
	case "boolean":
		value, ok := raw.(bool)
		if !ok {
			return "", false, ProjectionUnsupported
		}
		if value {
			return "True", true, ProjectionExact
		}
		return "False", true, ProjectionExact
	case "null":
		if raw != nil {
			return "", false, ProjectionUnsupported
		}
		return "None", true, ProjectionExact
	default:
		return "", false, ProjectionLossy
	}
}

func schemaType(schema map[string]any) string {
	value, _ := schema["type"].(string)
	return value
}

func constraintProjection(schema map[string]any, allowed ...string) Projection {
	allow := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allow[key] = true
	}
	for key := range schema {
		if !allow[key] {
			return ProjectionLossy
		}
	}
	return ProjectionExact
}

func mergeProjection(values ...Projection) Projection {
	result := ProjectionExact
	for _, value := range values {
		switch value {
		case ProjectionUnsupported:
			return ProjectionUnsupported
		case ProjectionLossy:
			result = ProjectionLossy
		case ProjectionExact:
		default:
			panic(fmt.Sprintf("unknown projection %q", value))
		}
	}
	return result
}
