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
			"expected_errors": []string{"capability_denied", "stale_catalog", "handler_version_mismatch", "invalid_arguments", "call_budget_exceeded", "transaction_call_budget_exceeded", "result_schema_mismatch"},
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
	runtimeBuilder.WriteString("_UNSET = object()\n_CALL_COUNTER = 0\n\n")
	runtimeBuilder.WriteString("class HostToolError(RuntimeError):\n")
	runtimeBuilder.WriteString("    def __init__(self, code: str, message: str) -> None:\n")
	runtimeBuilder.WriteString("        self.code = str(code)[:128]\n")
	runtimeBuilder.WriteString("        super().__init__(str(message)[:4096])\n\n")
	runtimeBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any:\n")
	runtimeBuilder.WriteString("    global _CALL_COUNTER\n")
	runtimeBuilder.WriteString("    _CALL_COUNTER += 1\n")
	runtimeBuilder.WriteString("    _call_id = f\"typed:{_CALL_COUNTER}\"\n")
	runtimeBuilder.WriteString("    _envelope = {\"call_id\": _call_id, \"capability\": tool_id, \"catalog_digest\": catalog_digest, \"handler_version\": handler_version, \"arguments\": arguments}\n")
	runtimeBuilder.WriteString("    from _agent_runtime_host import call as _host_call\n")
	runtimeBuilder.WriteString("    try:\n")
	runtimeBuilder.WriteString("        _payload = _host_call(_json.dumps(_envelope, ensure_ascii=False, separators=(\",\", \":\"), allow_nan=False))\n")
	runtimeBuilder.WriteString("        if not isinstance(_payload, str):\n            raise ValueError(\"non-string response\")\n")
	runtimeBuilder.WriteString("        _response = _json.loads(_payload)\n")
	runtimeBuilder.WriteString("    except HostToolError:\n        raise\n")
	runtimeBuilder.WriteString("    except Exception as _error:\n        raise HostToolError(\"protocol_error\", \"Host tool response is invalid\") from _error\n")
	runtimeBuilder.WriteString("    if not isinstance(_response, dict) or set(_response) != {\"call_id\", \"status\", \"result\", \"error\"} or _response[\"call_id\"] != _call_id:\n")
	runtimeBuilder.WriteString("        raise HostToolError(\"protocol_error\", \"Host tool response envelope is invalid\")\n")
	runtimeBuilder.WriteString("    if _response[\"status\"] not in {\"ok\", \"denied\", \"error\", \"timeout\"}:\n")
	runtimeBuilder.WriteString("        raise HostToolError(\"protocol_error\", \"Host tool response status is invalid\")\n")
	runtimeBuilder.WriteString("    if _response[\"status\"] != \"ok\":\n")
	runtimeBuilder.WriteString("        _error = _response[\"error\"]\n")
	runtimeBuilder.WriteString("        if not isinstance(_error, dict) or set(_error) != {\"code\", \"message\"}:\n            raise HostToolError(\"protocol_error\", \"Host tool error envelope is invalid\")\n")
	runtimeBuilder.WriteString("        _code, _message = _error[\"code\"], _error[\"message\"]\n")
	runtimeBuilder.WriteString("        if not isinstance(_code, str) or not _code or len(_code) > 128 or not isinstance(_message, str) or len(_message) > 4096:\n")
	runtimeBuilder.WriteString("            raise HostToolError(\"protocol_error\", \"Host tool error fields are invalid\")\n")
	runtimeBuilder.WriteString("        raise HostToolError(_code, _message)\n")
	runtimeBuilder.WriteString("    if _response[\"error\"] is not None:\n        raise HostToolError(\"protocol_error\", \"Successful Host tool response contains an error\")\n")
	runtimeBuilder.WriteString("    return _response[\"result\"]\n\n")
	stubBuilder.WriteString("class HostToolError(RuntimeError):\n    code: str\n\n")
	stubBuilder.WriteString("def _call(tool_id: str, catalog_digest: str, handler_version: str, arguments: dict[str, Any]) -> Any: ...\n\n")
	metadataSource := "_TOOL_METADATA = _json.loads(" + metadataLiteral + ")\n\n"
	runtimeBuilder.WriteString(metadataSource)
	stubBuilder.WriteString(metadataSource)
	transactionProjection := "def current_transaction() -> dict[str, str]:\n    \"\"\"Return an opaque view of the current Host transaction; no IDs or control authority are exposed.\"\"\"\n    return {\"scope\": \"current\", \"authority\": \"host-owned\", \"lifecycle\": \"host-managed\", \"catalog_digest\": CATALOG_DIGEST}\n\n"
	runtimeBuilder.WriteString(transactionProjection)
	stubBuilder.WriteString("def current_transaction() -> dict[str, str]: ...\n\n")
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
		doc = doc + "\n\nCatalog: " + snapshot.digest + "; handler: " + tool.HandlerVersion + "; projection: " + string(tool.Projection) + "; effect: " + tool.EffectClass + "; policy: " + tool.Policy + "; max_calls=" + fmt.Sprintf("%d", tool.MaxCalls) + ".\nExpected errors: capability_denied, stale_catalog, handler_version_mismatch, invalid_arguments, call_budget_exceeded, transaction_call_budget_exceeded, result_schema_mismatch."
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
		stubBuilder.WriteString("# " + tool.ToolID + " | catalog=" + snapshot.digest + " | handler=" + tool.HandlerVersion + " | projection=" + string(tool.Projection) + " | effect=" + tool.EffectClass + " | policy=" + tool.Policy + " | max_calls=" + fmt.Sprintf("%d", tool.MaxCalls) + " | errors=capability_denied,stale_catalog,handler_version_mismatch,invalid_arguments,call_budget_exceeded,transaction_call_budget_exceeded,result_schema_mismatch\n\n")
		emitted++
	}
	if emitted == 0 && len(snapshot.tools) > 0 {
		return "", "", errors.New("snapshot has no Python-projectable granted tools")
	}
	runtimeResult, stubResult := runtimeBuilder.String(), stubBuilder.String()
	if len(runtimeResult) > maxGeneratedRuntimeBytes || len(stubResult) > maxGeneratedSummaryBytes {
		return "", "", errors.New("generated Python surface exceeds bounded size")
	}
	return runtimeResult, stubResult, nil
}

func (snapshot Snapshot) GenerateTrustedPrepare() (string, error) {
	runtimeSource, _, err := snapshot.GeneratePython()
	if err != nil {
		return "", err
	}
	quoted := strconv.Quote(runtimeSource)
	prepare := "import sys as _host_sys\n" +
		"import types as _host_types\n" +
		"_host_module = _host_types.ModuleType(\"host_tools\")\n" +
		"_host_module.__package__ = \"\"\n" +
		"exec(compile(" + quoted + ", \"<host_tools>\", \"exec\"), _host_module.__dict__)\n" +
		"_host_sys.modules[\"host_tools\"] = _host_module\n" +
		"del _host_module, _host_types, _host_sys\n"
	if len(prepare) > maxGeneratedRuntimeBytes+4096 {
		return "", errors.New("trusted Python preparation exceeds bounded size")
	}
	return prepare, nil
}

func (snapshot Snapshot) GenerateTrustedPrepareWithToolBindings() (string, error) {
	runtimeSource, _, err := snapshot.GeneratePython()
	if err != nil {
		return "", err
	}
	quoted := strconv.Quote(runtimeSource)
	var toolBindings strings.Builder
	for _, tool := range snapshot.tools {
		if tool.Projection == ProjectionUnsupported {
			continue
		}
		if trustedPrepareGlobalConflict(tool.PythonName) {
			return "", fmt.Errorf("tool %q cannot be bound into trusted prepare globals", tool.ToolID)
		}
		toolBindings.WriteString(tool.PythonName)
		toolBindings.WriteString(" = _host_module.__dict__[")
		toolBindings.WriteString(strconv.Quote(tool.PythonName))
		toolBindings.WriteString("]\n")
	}
	prepare := "import sys as _host_sys\n" +
		"import types as _host_types\n" +
		"_host_module = _host_types.ModuleType(\"host_tools\")\n" +
		"_host_module.__package__ = \"\"\n" +
		"exec(compile(" + quoted + ", \"<host_tools>\", \"exec\"), _host_module.__dict__)\n" +
		toolBindings.String() +
		"_host_sys.modules[\"host_tools\"] = _host_module\n" +
		"del _host_module, _host_types, _host_sys\n"
	if len(prepare) > maxGeneratedRuntimeBytes+4096 {
		return "", errors.New("trusted Python preparation exceeds bounded size")
	}
	return prepare, nil
}

var trustedPrepareGlobalConflictNames = map[string]bool{
	"__name__":            true,
	"__doc__":             true,
	"__package__":         true,
	"__loader__":          true,
	"__spec__":            true,
	"__builtins__":        true,
	"_host_sys":           true,
	"_host_types":         true,
	"_host_module":        true,
	"_json":               true,
	"_UNSET":              true,
	"_CALL_COUNTER":       true,
	"_call":               true,
	"_TOOL_METADATA":      true,
	"CATALOG_DIGEST":      true,
	"CATALOG_REVISION":    true,
	"Any":                 true,
	"Literal":             true,
	"TypedDict":           true,
	"NotRequired":         true,
	"HostToolError":       true,
	"current_transaction": true,
	"describe_tools":      true,
	"describe_tool":       true,
	"inputs":              true,
	"result":              true,
	"host_tools":          true,
	"host_tool":           true,
	"_agent_runtime_host": true,
	"sys":                 true,
	"types":               true,
}

var pythonBuiltinsForTrustedPrepare = map[string]bool{
	"__build_class__":           true,
	"__debug__":                 true,
	"__doc__":                   true,
	"__import__":                true,
	"__loader__":                true,
	"__name__":                  true,
	"__package__":               true,
	"__spec__":                  true,
	"abs":                       true,
	"all":                       true,
	"any":                       true,
	"ascii":                     true,
	"bin":                       true,
	"bool":                      true,
	"bytearray":                 true,
	"bytes":                     true,
	"callable":                  true,
	"chr":                       true,
	"classmethod":               true,
	"compile":                   true,
	"complex":                   true,
	"copyright":                 true,
	"credits":                   true,
	"delattr":                   true,
	"dict":                      true,
	"dir":                       true,
	"divmod":                    true,
	"enumerate":                 true,
	"eval":                      true,
	"exec":                      true,
	"filter":                    true,
	"float":                     true,
	"format":                    true,
	"frozenset":                 true,
	"getattr":                   true,
	"globals":                   true,
	"hasattr":                   true,
	"hash":                      true,
	"help":                      true,
	"hex":                       true,
	"id":                        true,
	"input":                     true,
	"int":                       true,
	"isinstance":                true,
	"issubclass":                true,
	"iter":                      true,
	"len":                       true,
	"list":                      true,
	"locals":                    true,
	"map":                       true,
	"max":                       true,
	"memoryview":                true,
	"min":                       true,
	"next":                      true,
	"object":                    true,
	"oct":                       true,
	"open":                      true,
	"ord":                       true,
	"pow":                       true,
	"print":                     true,
	"property":                  true,
	"range":                     true,
	"repr":                      true,
	"reversed":                  true,
	"round":                     true,
	"set":                       true,
	"setattr":                   true,
	"slice":                     true,
	"sorted":                    true,
	"staticmethod":              true,
	"str":                       true,
	"sum":                       true,
	"super":                     true,
	"tuple":                     true,
	"type":                      true,
	"vars":                      true,
	"zip":                       true,
	"ArithmeticError":           true,
	"AssertionError":            true,
	"AttributeError":            true,
	"BaseException":             true,
	"EOFError":                  true,
	"Exception":                 true,
	"False":                     true,
	"FloatingPointError":        true,
	"GeneratorExit":             true,
	"ImportError":               true,
	"IndentationError":          true,
	"IndexError":                true,
	"KeyError":                  true,
	"KeyboardInterrupt":         true,
	"LookupError":               true,
	"MemoryError":               true,
	"NameError":                 true,
	"None":                      true,
	"NotImplemented":            true,
	"NotImplementedError":       true,
	"OSError":                   true,
	"OverflowError":             true,
	"RuntimeError":              true,
	"StopIteration":             true,
	"SyntaxError":               true,
	"SystemError":               true,
	"SystemExit":                true,
	"True":                      true,
	"TypeError":                 true,
	"ValueError":                true,
	"ZeroDivisionError":         true,
	"FileNotFoundError":         true,
	"PermissionError":           true,
	"BlockingIOError":           true,
	"BrokenPipeError":           true,
	"BufferError":               true,
	"BytesWarning":              true,
	"ChildProcessError":         true,
	"ConnectionAbortedError":    true,
	"ConnectionError":           true,
	"ConnectionRefusedError":    true,
	"ConnectionResetError":      true,
	"DeprecationWarning":        true,
	"Ellipsis":                  true,
	"EnvironmentError":          true,
	"FileExistsError":           true,
	"FutureWarning":             true,
	"IOError":                   true,
	"ImportWarning":             true,
	"InterruptedError":          true,
	"IsADirectoryError":         true,
	"ModuleNotFoundError":       true,
	"NotADirectoryError":        true,
	"PendingDeprecationWarning": true,
	"ProcessLookupError":        true,
	"RecursionError":            true,
	"ReferenceError":            true,
	"ResourceWarning":           true,
	"RuntimeWarning":            true,
	"StopAsyncIteration":        true,
	"SyntaxWarning":             true,
	"TabError":                  true,
	"TimeoutError":              true,
	"UnboundLocalError":         true,
	"UnicodeDecodeError":        true,
	"UnicodeEncodeError":        true,
	"UnicodeError":              true,
	"UnicodeTranslateError":     true,
	"UnicodeWarning":            true,
	"UserWarning":               true,
	"Warning":                   true,
	"breakpoint":                true,
	"exit":                      true,
	"license":                   true,
	"quit":                      true,
	"aiter":                     true,
	"anext":                     true,
	"BaseExceptionGroup":        true,
	"EncodingWarning":           true,
	"ExceptionGroup":            true,
	"PythonFinalizationError":   true,
	"_IncompleteInputError":     true,
}

func trustedPrepareGlobalConflict(identifier string) bool {
	if trustedPrepareGlobalConflictNames[identifier] || pythonBuiltinsForTrustedPrepare[identifier] || pythonKeywords[identifier] {
		return true
	}
	return false
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
