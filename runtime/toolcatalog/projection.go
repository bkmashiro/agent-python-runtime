package toolcatalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const maxProjectionDepth = 8

func projectInput(schema map[string]any) ([]Parameter, Projection) {
	if schema["$ref"] != nil || schemaType(schema) != "object" {
		return nil, ProjectionUnsupported
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		if len(schema) == 1 {
			return nil, ProjectionLossy
		}
		return nil, ProjectionUnsupported
	}
	required := map[string]bool{}
	if rawRequired, exists := schema["required"]; exists {
		values, ok := rawRequired.([]any)
		if !ok {
			return nil, ProjectionUnsupported
		}
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, ProjectionUnsupported
			}
			required[name] = true
		}
	}
	projection := ProjectionExact
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		projection = ProjectionLossy
	}
	parameters := make([]Parameter, 0, len(properties))
	pythonNames := map[string]bool{}
	for name, rawProperty := range properties {
		property, ok := rawProperty.(map[string]any)
		if !ok || name == "" || len(name) > maxName {
			return nil, ProjectionUnsupported
		}
		pythonName, err := normalizePythonIdentifier(name)
		if err != nil || pythonNames[pythonName] {
			return nil, ProjectionUnsupported
		}
		pythonNames[pythonName] = true
		annotation, projected := projectType(property, 1)
		projection = mergeProjection(projection, projected)
		if projected == ProjectionUnsupported {
			return nil, ProjectionUnsupported
		}
		parameters = append(parameters, Parameter{
			Name: name, PythonName: pythonName, Annotation: annotation, Required: required[name],
		})
	}
	for name := range required {
		if _, exists := properties[name]; !exists {
			return nil, ProjectionUnsupported
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
	return parameters, projection
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
