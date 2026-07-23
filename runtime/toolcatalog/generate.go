package toolcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	maxGeneratedRuntimeBytes = 1024 * 1024
	maxGeneratedSummaryBytes = 256 * 1024
)

func (snapshot Snapshot) GeneratePython() (runtimeSource string, stub string, err error) {
	if !identifierPattern.MatchString(snapshot.snapshotID) || snapshot.revision == 0 || snapshot.digest == "" {
		return "", "", errors.New("cannot generate Python from an empty snapshot")
	}
	metadata := make([]map[string]any, len(snapshot.tools))
	for index, tool := range snapshot.tools {
		metadata[index] = map[string]any{
			"tool_id": tool.ToolID, "python_name": tool.PythonName, "description": tool.Description,
			"handler_version": tool.HandlerVersion, "projection": tool.Projection,
			"effect_class": tool.EffectClass, "policy": tool.Policy, "max_calls": tool.MaxCalls,
			"catalog_digest":  snapshot.digest,
			"expected_errors": []string{"capability_denied", "stale_catalog", "handler_version_mismatch", "invalid_arguments", "call_budget_exceeded", "result_schema_mismatch"},
		}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", "", err
	}
	metadataLiteral := strconv.Quote(string(metadataJSON))
	var runtimeBuilder strings.Builder
	var stubBuilder strings.Builder
	constants := "CATALOG_DIGEST = " + strconv.Quote(snapshot.digest) + "\n" +
		"CATALOG_REVISION = " + fmt.Sprintf("%d", snapshot.revision) + "\n\n"
	runtimeHeader := "from __future__ import annotations\n\nimport json as _json\nfrom typing import Any, Literal, TYPE_CHECKING, TypedDict\n\n" + constants
	stubHeader := "from __future__ import annotations\n\nimport json as _json\nfrom typing import Any, Literal, NotRequired, TypedDict\n\n" + constants
	runtimeBuilder.WriteString(runtimeHeader)
	stubBuilder.WriteString(stubHeader)
	runtimeBuilder.WriteString("_UNSET = object()\n\n")
	runtimeBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any:\n")
	runtimeBuilder.WriteString("    raise RuntimeError(\"Host tool binding is not installed\")\n\n")
	stubBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any: ...\n\n")
	metadataSource := "_TOOL_METADATA = _json.loads(" + metadataLiteral + ")\n\n"
	runtimeBuilder.WriteString(metadataSource)
	stubBuilder.WriteString(metadataSource)
	runtimeBuilder.WriteString("if TYPE_CHECKING:\n    from typing import NotRequired\n")
	for _, tool := range snapshot.tools {
		for _, definition := range tool.TypeDefinitions {
			fields := make([]string, len(definition.Fields))
			for index, field := range definition.Fields {
				annotation := field.Annotation
				if !field.Required {
					annotation = "NotRequired[" + annotation + "]"
				}
				fields[index] = strconv.Quote(field.Name) + ": " + annotation
			}
			line := definition.Name + " = TypedDict(" + strconv.Quote(definition.Name) + ", {" + strings.Join(fields, ", ") + "})\n"
			runtimeBuilder.WriteString("    " + line)
			stubBuilder.WriteString(line)
		}
	}
	runtimeBuilder.WriteString("\n")
	stubBuilder.WriteString("\n")
	runtimeBuilder.WriteString("def describe_tools() -> list[dict[str, Any]]:\n")
	runtimeBuilder.WriteString("    return _json.loads(_json.dumps(_TOOL_METADATA))\n\n")
	runtimeBuilder.WriteString("def describe_tool(name: str) -> dict[str, Any]:\n")
	runtimeBuilder.WriteString("    for _item in _TOOL_METADATA:\n")
	runtimeBuilder.WriteString("        if name in (_item[\"tool_id\"], _item[\"python_name\"]):\n")
	runtimeBuilder.WriteString("            return _json.loads(_json.dumps(_item))\n")
	runtimeBuilder.WriteString("    raise KeyError(name)\n\n")
	stubBuilder.WriteString("def describe_tools() -> list[dict[str, Any]]: ...\n")
	stubBuilder.WriteString("def describe_tool(name: str) -> dict[str, Any]: ...\n\n")

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
		doc = doc + "\n\nCatalog: " + snapshot.digest + "; handler: " + tool.HandlerVersion + "; projection: " + string(tool.Projection) + "; effect: " + tool.EffectClass + "; policy: " + tool.Policy + "; max_calls=" + fmt.Sprintf("%d", tool.MaxCalls) + ".\nExpected errors: capability_denied, stale_catalog, handler_version_mismatch, invalid_arguments, call_budget_exceeded, result_schema_mismatch."
		runtimeBuilder.WriteString(runtimeSignature + ":\n")
		runtimeBuilder.WriteString("    " + strconv.Quote(doc) + "\n")
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
		stubBuilder.WriteString("# " + tool.ToolID + " | catalog=" + snapshot.digest + " | handler=" + tool.HandlerVersion + " | projection=" + string(tool.Projection) + " | effect=" + tool.EffectClass + " | policy=" + tool.Policy + " | max_calls=" + fmt.Sprintf("%d", tool.MaxCalls) + " | errors=capability_denied,stale_catalog,handler_version_mismatch,invalid_arguments,call_budget_exceeded,result_schema_mismatch\n\n")
		emitted++
	}
	if emitted == 0 {
		return "", "", errors.New("snapshot has no Python-projectable granted tools")
	}
	runtimeResult, stubResult := runtimeBuilder.String(), stubBuilder.String()
	if len(runtimeResult) > maxGeneratedRuntimeBytes || len(stubResult) > maxGeneratedSummaryBytes {
		return "", "", errors.New("generated Python surface exceeds bounded size")
	}
	return runtimeResult, stubResult, nil
}

func pythonSignature(tool Tool, optionalDefault string) string {
	parameters := make([]string, len(tool.Parameters))
	for index, parameter := range tool.Parameters {
		if !parameter.Required {
			defaultValue := optionalDefault
			if parameter.HasDefault {
				defaultValue = parameter.DefaultPython
			}
			parameters[index] = parameter.PythonName + ": " + parameter.Annotation + " = " + defaultValue
		} else {
			parameters[index] = parameter.PythonName + ": " + parameter.Annotation
		}
	}
	return "def " + tool.PythonName + "(" + strings.Join(parameters, ", ") + ") -> " + tool.ReturnType
}
