package toolcatalog

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (snapshot Snapshot) GeneratePython() (runtimeSource string, stub string, err error) {
	if !identifierPattern.MatchString(snapshot.snapshotID) || snapshot.revision == 0 || snapshot.digest == "" {
		return "", "", errors.New("cannot generate Python from an empty snapshot")
	}
	var runtimeBuilder strings.Builder
	var stubBuilder strings.Builder
	header := "from __future__ import annotations\n\nfrom typing import Any, Literal\n\n" +
		"CATALOG_DIGEST = " + strconv.Quote(snapshot.digest) + "\n" +
		"CATALOG_REVISION = " + fmt.Sprintf("%d", snapshot.revision) + "\n\n"
	runtimeBuilder.WriteString(header)
	stubBuilder.WriteString(header)
	runtimeBuilder.WriteString("_UNSET = object()\n\n")
	runtimeBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any:\n")
	runtimeBuilder.WriteString("    raise RuntimeError(\"Host tool binding is not installed\")\n\n")
	stubBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any: ...\n\n")

	emitted := 0
	for _, tool := range snapshot.tools {
		if tool.Projection == ProjectionUnsupported {
			continue
		}
		runtimeSignature := pythonSignature(tool, "_UNSET")
		stubSignature := pythonSignature(tool, "...")
		doc := tool.Description
		if doc == "" {
			doc = "Host-mediated tool " + tool.ToolID + "."
		}
		runtimeBuilder.WriteString(runtimeSignature + ":\n")
		runtimeBuilder.WriteString("    \"\"\"" + escapeDocstring(doc) + "\n\n")
		runtimeBuilder.WriteString("    Catalog: " + snapshot.digest + "; handler: " + tool.HandlerVersion + "; projection: " + string(tool.Projection) + ".\"\"\"\n")
		runtimeBuilder.WriteString("    _arguments: dict[str, Any] = {}\n")
		for _, parameter := range tool.Parameters {
			if parameter.Required {
				runtimeBuilder.WriteString("    _arguments[" + strconv.Quote(parameter.Name) + "] = " + parameter.PythonName + "\n")
			} else {
				runtimeBuilder.WriteString("    if " + parameter.PythonName + " is not _UNSET:\n")
				runtimeBuilder.WriteString("        _arguments[" + strconv.Quote(parameter.Name) + "] = " + parameter.PythonName + "\n")
			}
		}
		runtimeBuilder.WriteString("    return _call(" + strconv.Quote(tool.ToolID) + ", CATALOG_DIGEST, " + strconv.Quote(tool.HandlerVersion) + ", _arguments)\n\n")

		stubBuilder.WriteString(stubSignature + ": ...\n")
		stubBuilder.WriteString("# " + tool.ToolID + " | catalog=" + snapshot.digest + " | handler=" + tool.HandlerVersion + " | projection=" + string(tool.Projection) + "\n\n")
		emitted++
	}
	if emitted == 0 {
		return "", "", errors.New("snapshot has no Python-projectable granted tools")
	}
	return runtimeBuilder.String(), stubBuilder.String(), nil
}

func pythonSignature(tool Tool, optionalDefault string) string {
	parameters := make([]string, len(tool.Parameters))
	for index, parameter := range tool.Parameters {
		if !parameter.Required {
			parameters[index] = parameter.PythonName + ": " + parameter.Annotation + " = " + optionalDefault
		} else {
			parameters[index] = parameter.PythonName + ": " + parameter.Annotation
		}
	}
	return "def " + tool.PythonName + "(" + strings.Join(parameters, ", ") + ") -> " + tool.ReturnType
}

func escapeDocstring(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"\"\"", "\\\"\\\"\\\"")
	return value
}
