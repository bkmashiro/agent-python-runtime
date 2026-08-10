"""Trusted bootstrap for the neutral Agent Python Runtime guest."""

from __future__ import annotations

import ast
import dis
import json
import sys
import time
import traceback
import types
from typing import Any

_ALLOWED_REQUEST_FIELDS = {"run_id", "code", "inputs", "output_schema", "compatibility", "requirements"}
_TRACEBACK_MAX = 16_384
_prepared_globals: dict[str, Any] = {}
_runtime_config: dict[str, Any] = {}
_WARMUP_REQUEST_SHELL_V1 = "request-shell-v1"
_WARMUP_NUMPY_READY_V1 = "numpy-ready-v1"
_warmup_profiles: dict[str, Any] = {}
_validated_request_json: str | None = None
_validated_code: types.CodeType | None = None
_validated_import_globals: dict[str, Any] = {}
_SOURCE_CONTRACT_OK = 0
_SOURCE_CONTRACT_UNSUPPORTED = 1
_SOURCE_CONTRACT_INVALID = 2
_FORBIDDEN_DYNAMIC_NAMES = {"__import__", "eval", "exec", "import_module"}
_FORBIDDEN_IMPORT_ROOTS = {"builtins", "importlib"}


def _error(code: str, message: str, *, error_type: str | None = None, trace: str | None = None) -> dict[str, Any]:
    bounded_message = message or error_type or code
    detail: dict[str, Any] = {"code": code, "message": bounded_message[:4096]}
    if error_type is not None:
        detail["error_type"] = error_type[:256]
    if trace is not None:
        detail["traceback"] = trace[-_TRACEBACK_MAX:]
    return {
        "status": "error",
        "result": None,
        "receipts": [],
        "metrics": {"capability_calls": 0, "result_bytes": 0},
        "error": detail,
    }


def _initialize(config_json: str) -> None:
    if not isinstance(config_json, str):
        raise TypeError("config_json must be a string")
    value = json.loads(config_json)
    if not isinstance(value, dict):
        raise ValueError("runtime config must be an object")
    global _runtime_config, _prepared_globals, _validated_request_json, _validated_code, _validated_import_globals
    _runtime_config = dict(value)
    _prepared_globals = {}
    _validated_request_json = None
    _validated_code = None
    _validated_import_globals = {}


def _prepare(source: str) -> None:
    if not isinstance(source, str):
        raise TypeError("source must be a string")
    namespace: dict[str, Any] = {"__builtins__": __builtins__}
    exec(compile(source, "<trusted-prepare>", "exec"), namespace, namespace)
    global _prepared_globals
    _prepared_globals = namespace


def register_warmup_profile(name: str, handler: Any) -> None:
    if not isinstance(name, str) or not name or len(name) > 64:
        raise ValueError("warmup profile name must contain 1-64 characters")
    if not name[0].isalnum() or not name.isascii() or any(
        not (character.islower() or character.isdigit() or character in "-_.")
        for character in name
    ):
        raise ValueError("warmup profile name must use lowercase ASCII identifiers")
    if not callable(handler):
        raise TypeError("warmup profile handler must be callable")
    if name in _warmup_profiles:
        raise ValueError(f"warmup profile already registered: {name}")
    _warmup_profiles[name] = handler


def _warmup_request_shell_v1() -> None:
    # Prime deterministic request-shell paths without retaining request data,
    # wall-clock values, random state, or Host effects in the snapshot.
    request = json.loads('{"run_id":"warmup","code":"result = None","inputs":{}}')
    code = compile(request["code"], "<warmup>", "exec")
    namespace: dict[str, Any] = {"__builtins__": __builtins__, "inputs": request["inputs"]}
    exec(code, namespace, namespace)
    json.dumps({"status": "ok", "result": namespace.get("result")}, separators=(",", ":"), allow_nan=False)


def _warmup_numpy_ready_v1() -> None:
    # Import inside the canonical Guest before COW sealing and retain the
    # audited scientific namespace for request execution. No request data or
    # Host capabilities are available during this initialization boundary.
    _prepare("import numpy as np\nprepared = 41")
    numpy = _prepared_globals["np"]
    if not isinstance(numpy.__version__, str) or int(numpy.arange(4).sum()) != 6:
        raise RuntimeError("NumPy warmup self-check failed")


register_warmup_profile(_WARMUP_REQUEST_SHELL_V1, _warmup_request_shell_v1)
register_warmup_profile(_WARMUP_NUMPY_READY_V1, _warmup_numpy_ready_v1)


def _warmup(profile: str) -> None:
    try:
        handler = _warmup_profiles[profile]
    except (KeyError, TypeError):
        raise ValueError(f"unsupported warmup profile: {profile}") from None
    handler()


def _decode_request(request_json: str) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
    try:
        request = json.loads(request_json)
    except (TypeError, ValueError) as exc:
        return None, _error("invalid_request", f"invalid JSON: {exc}")
    if not isinstance(request, dict):
        return None, _error("invalid_request", "request must be an object")
    unknown = sorted(set(request) - _ALLOWED_REQUEST_FIELDS)
    missing = sorted({"run_id", "code", "inputs"} - set(request))
    if unknown:
        return None, _error("invalid_request", f"unknown fields: {', '.join(unknown)}")
    if missing:
        return None, _error("invalid_request", f"missing fields: {', '.join(missing)}")
    if not isinstance(request["run_id"], str) or not request["run_id"]:
        return None, _error("invalid_request", "run_id must be a non-empty string")
    if not isinstance(request["code"], str):
        return None, _error("invalid_request", "code must be a string")
    requirements = request.get("requirements", [])
    if not isinstance(requirements, list) or requirements:
        return None, _error("invalid_request", "non-empty requirements must be rejected by Host admission")
    compatibility = request.get("compatibility")
    if compatibility is not None:
        if (
            not isinstance(compatibility, dict)
            or set(compatibility) != {"profile", "imports"}
            or not isinstance(compatibility.get("profile"), str)
            or not isinstance(compatibility.get("imports"), list)
            or any(not isinstance(name, str) for name in compatibility.get("imports", []))
        ):
            return None, _error("invalid_request", "compatibility declaration must be admitted by Host")
    return request, None


def _module_docstring(node: ast.stmt) -> bool:
    return isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str)


def _code_objects(code: types.CodeType):
    yield code
    for value in code.co_consts:
        if isinstance(value, types.CodeType):
            yield from _code_objects(value)


def _validate_agent_source(source: str, compatibility: dict[str, Any] | None) -> tuple[int, types.CodeType | None, list[ast.stmt]]:
    try:
        tree = ast.parse(source, filename="<agent-run>", mode="exec")
    except (SyntaxError, ValueError, TypeError, MemoryError):
        return _SOURCE_CONTRACT_INVALID, None, []

    import_nodes: list[ast.stmt] = []
    imported_roots: set[str] = set()
    preamble_open = True
    for index, node in enumerate(tree.body):
        if index == 0 and _module_docstring(node):
            continue
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            if not preamble_open:
                return _SOURCE_CONTRACT_UNSUPPORTED, None, []
            import_nodes.append(node)
            if isinstance(node, ast.Import):
                for alias in node.names:
                    root = alias.name.partition(".")[0]
                    if root in _FORBIDDEN_IMPORT_ROOTS:
                        return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                    imported_roots.add(root)
            else:
                if node.level != 0 or node.module is None or any(alias.name == "*" for alias in node.names):
                    return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                root = node.module.partition(".")[0]
                if root in _FORBIDDEN_IMPORT_ROOTS or root == "__future__":
                    return _SOURCE_CONTRACT_UNSUPPORTED, None, []
                imported_roots.add(root)
            continue
        preamble_open = False

    top_level_import_ids = {id(node) for node in import_nodes}
    for node in ast.walk(tree):
        if isinstance(node, (ast.Import, ast.ImportFrom)) and id(node) not in top_level_import_ids:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Name) and node.id in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Attribute) and node.attr in {"__import__", "import_module"}:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
        if isinstance(node, ast.Constant) and isinstance(node.value, str) and node.value in _FORBIDDEN_DYNAMIC_NAMES:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []

    if compatibility is not None:
        declarations = compatibility["imports"]
        if any("." in name for name in declarations) or set(declarations) != imported_roots:
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []

    body = [node for node in tree.body if id(node) not in top_level_import_ids]
    body_tree = ast.fix_missing_locations(ast.Module(body=body, type_ignores=[]))
    try:
        code = compile(body_tree, "<agent-run>", "exec")
    except (SyntaxError, ValueError, TypeError, MemoryError):
        return _SOURCE_CONTRACT_INVALID, None, []
    for candidate in _code_objects(code):
        if any(instruction.opname in {"IMPORT_NAME", "IMPORT_FROM", "IMPORT_STAR"} for instruction in dis.get_instructions(candidate)):
            return _SOURCE_CONTRACT_UNSUPPORTED, None, []
    return _SOURCE_CONTRACT_OK, code, import_nodes


def _preload_and_seal_imports(import_nodes: list[ast.stmt]) -> dict[str, Any] | None:
    try:
        namespace: dict[str, Any] = {"__builtins__": __builtins__}
        if import_nodes:
            preamble = ast.fix_missing_locations(ast.Module(body=import_nodes, type_ignores=[]))
            exec(compile(preamble, "<agent-import-preamble>", "exec"), namespace, namespace)
        import _agent_runtime_host  # type: ignore[import-not-found]
        seal = getattr(_agent_runtime_host, "seal_imports")
        seal(tuple(sorted(sys.modules)))
        namespace.pop("__builtins__", None)
        return namespace
    except BaseException:
        return None


def _validate_request_source(request_json: str) -> int:
    global _validated_request_json, _validated_code, _validated_import_globals
    if not isinstance(request_json, str):
        return _SOURCE_CONTRACT_INVALID
    request, error = _decode_request(request_json)
    if error is not None or request is None:
        return _SOURCE_CONTRACT_INVALID
    if request.get("compatibility") is None:
        try:
            code = compile(request["code"], "<agent-run>", "exec")
        except (SyntaxError, ValueError, TypeError, MemoryError):
            return _SOURCE_CONTRACT_INVALID
        _validated_request_json = request_json
        _validated_code = code
        _validated_import_globals = {}
        return _SOURCE_CONTRACT_OK
    status, code, import_nodes = _validate_agent_source(request["code"], request.get("compatibility"))
    if status != _SOURCE_CONTRACT_OK or code is None:
        return status
    import_globals = _preload_and_seal_imports(import_nodes)
    if import_globals is None:
        return _SOURCE_CONTRACT_UNSUPPORTED
    _validated_request_json = request_json
    _validated_code = code
    _validated_import_globals = import_globals
    return _SOURCE_CONTRACT_OK


def _encode(response: dict[str, Any]) -> str:
    try:
        return json.dumps(response, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    except (TypeError, ValueError) as exc:
        return json.dumps(
            _error("result_not_json", f"result is not JSON serializable: {exc}"),
            ensure_ascii=False,
            separators=(",", ":"),
            allow_nan=False,
        )


def _execute(request_json: str) -> str:
    if not isinstance(request_json, str):
        return _encode(_error("invalid_request", "request_json must be a string"))
    request, error = _decode_request(request_json)
    if error is not None:
        return _encode(error)
    assert request is not None

    global _validated_request_json, _validated_code, _validated_import_globals
    if _validated_request_json != request_json or _validated_code is None:
        contract_status = _validate_request_source(request_json)
        if contract_status == _SOURCE_CONTRACT_INVALID:
            return _encode(_error("source_invalid", "agent source is not valid for the exact Guest parser"))
        if contract_status != _SOURCE_CONTRACT_OK:
            return _encode(_error("source_contract_unsupported", "agent source must use one static absolute import preamble"))
    code = _validated_code
    if code is None:
        return _encode(_error("guest_internal", "validated code is unavailable"))

    started = time.monotonic()
    namespace = dict(_prepared_globals)
    namespace["inputs"] = request["inputs"]
    namespace.pop("result", None)
    namespace.update(_validated_import_globals)
    if request.get("compatibility") is not None:
        builtins_source = vars(__builtins__) if isinstance(__builtins__, types.ModuleType) else __builtins__
        restricted_builtins = dict(builtins_source)
        for forbidden in ("__import__", "eval", "exec"):
            restricted_builtins.pop(forbidden, None)
        namespace["__builtins__"] = restricted_builtins
    try:
        exec(code, namespace, namespace)
        result = namespace.get("result")
        response = {
            "status": "ok",
            "result": result,
            "receipts": [],
            "metrics": {
                "guest_time_ms": max(0.0, (time.monotonic() - started) * 1000.0),
                "capability_calls": 0,
                "result_bytes": 0,
            },
            "error": None,
        }
        encoded = _encode(response)
        if json.loads(encoded)["status"] == "ok":
            response["metrics"]["result_bytes"] = len(
                json.dumps(result, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")
            )
            encoded = _encode(response)
        return encoded
    except BaseException as exc:
        return _encode(
            _error(
                "python_exception",
                str(exc),
                error_type=type(exc).__name__,
                trace=traceback.format_exc(),
            )
        )
