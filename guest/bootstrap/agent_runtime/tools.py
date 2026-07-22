"""Narrow Python SDK for Host-owned read capabilities."""

from __future__ import annotations

import json
from typing import Any

_MAX_REQUESTS = 64
_MAX_IDENTIFIER = 128
_MAX_PATH = 4096
_call_counter = 0


class CapabilityError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        self.code = code[:128]
        super().__init__(message[:4096])


class CapabilityProtocolError(RuntimeError):
    pass


def _identifier(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value or len(value) > _MAX_IDENTIFIER:
        raise TypeError(f"{field} must be a bounded non-empty string")
    allowed = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-")
    if value[0] not in allowed or any(character not in allowed for character in value):
        raise ValueError(f"{field} contains unsupported characters")
    return value


def _validate_requests(requests: Any) -> list[dict[str, str]]:
    if not isinstance(requests, list):
        raise TypeError("requests must be a list")
    if not requests or len(requests) > _MAX_REQUESTS:
        raise ValueError("requests must contain between 1 and 64 items")
    validated: list[dict[str, str]] = []
    seen: set[str] = set()
    for request in requests:
        if not isinstance(request, dict):
            raise TypeError("each request must be an object")
        if set(request) != {"request_id", "target", "path"}:
            raise ValueError("request fields must be exactly request_id, target, and path")
        request_id = _identifier(request["request_id"], "request_id")
        target = _identifier(request["target"], "target")
        path = request["path"]
        if not isinstance(path, str) or not path or len(path) > _MAX_PATH:
            raise TypeError("path must be a bounded non-empty string")
        if not path.startswith("/") or path.startswith("//") or "://" in path or "#" in path:
            raise ValueError("path must be an origin-relative path without a fragment")
        if request_id in seen:
            raise ValueError("request_id values must be unique")
        seen.add(request_id)
        validated.append({"request_id": request_id, "target": target, "path": path})
    return validated


def _decode_host_response(payload: Any, call_id: str) -> list[dict[str, Any]]:
    if not isinstance(payload, str):
        raise CapabilityProtocolError("Host response must be a JSON string")
    try:
        response = json.loads(payload)
    except (TypeError, ValueError) as exc:
        raise CapabilityProtocolError("Host response is not valid JSON") from exc
    if not isinstance(response, dict) or set(response) != {"call_id", "status", "result", "error"}:
        raise CapabilityProtocolError("Host response has an invalid envelope")
    if response["call_id"] != call_id:
        raise CapabilityProtocolError("Host response call_id mismatch")
    status = response["status"]
    if status not in {"ok", "denied", "error", "timeout"}:
        raise CapabilityProtocolError("Host response status is invalid")
    if status != "ok":
        error = response["error"]
        if not isinstance(error, dict) or set(error) != {"code", "message"}:
            raise CapabilityProtocolError("Host error payload is invalid")
        raise CapabilityError(str(error["code"]), str(error["message"]))
    if response["error"] is not None:
        raise CapabilityProtocolError("successful Host response contains an error")
    result = response["result"]
    if not isinstance(result, dict) or set(result) != {"items"} or not isinstance(result["items"], list):
        raise CapabilityProtocolError("Host fetch_many result is invalid")
    return result["items"]


def fetch_many(requests: list[dict[str, str]]) -> list[dict[str, Any]]:
    """Perform ordered Host-mediated GET requests against granted target aliases."""
    validated = _validate_requests(requests)
    global _call_counter
    _call_counter += 1
    call_id = f"fetch_many:{_call_counter}"
    envelope = {
        "call_id": call_id,
        "capability": "fetch_many",
        "arguments": {"requests": validated},
    }
    from _agent_runtime_host import call as host_call  # type: ignore[import-not-found]

    response = host_call(json.dumps(envelope, ensure_ascii=False, separators=(",", ":"), allow_nan=False))
    return _decode_host_response(response, call_id)
