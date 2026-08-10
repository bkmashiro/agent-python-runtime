"""Trusted bootstrap for the neutral Agent Python Runtime guest."""

from __future__ import annotations

import json
import time
import traceback
from typing import Any

_ALLOWED_REQUEST_FIELDS = {"run_id", "code", "inputs", "output_schema", "requirements"}
_TRACEBACK_MAX = 16_384
_prepared_globals: dict[str, Any] = {}
_runtime_config: dict[str, Any] = {}
_WARMUP_REQUEST_SHELL_V1 = "request-shell-v1"
_WARMUP_NUMPY_READY_V1 = "numpy-ready-v1"
_warmup_profiles: dict[str, Any] = {}


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
    global _runtime_config, _prepared_globals
    _runtime_config = dict(value)
    _prepared_globals = {}


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
    return request, None


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

    started = time.monotonic()
    namespace = dict(_prepared_globals)
    namespace["inputs"] = request["inputs"]
    namespace.pop("result", None)
    try:
        exec(compile(request["code"], "<agent-run>", "exec"), namespace, namespace)
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
