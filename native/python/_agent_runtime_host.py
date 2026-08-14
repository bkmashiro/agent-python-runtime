"""Native CPython bridge for the Host-owned Pysolate capability plane."""
from __future__ import annotations
import http.client
import json
import os
import socket
from typing import Any

_MAX_RESPONSE_BYTES = 1_048_576

class _UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path: str, timeout: float):
        super().__init__("localhost", timeout=timeout)
        self._path = path
    def connect(self) -> None:
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.settimeout(self.timeout)
        connection.connect(self._path)
        self.sock = connection

def _required(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise RuntimeError("native Host tool channel is not configured")
    return value

def _identity() -> dict[str, Any]:
    return {
        "schema_version": "pysolate.capability-rpc.v1",
        "channel_id": _required("PYSOLATE_RPC_CHANNEL_ID"),
        "invocation_id": _required("PYSOLATE_RPC_INVOCATION_ID"),
        "execution_id": _required("PYSOLATE_RPC_EXECUTION_ID"),
        "plan_sha256": _required("PYSOLATE_RPC_PLAN_SHA256"),
    }

def _post(path: str, document: dict[str, Any]) -> dict[str, Any]:
    encoded = json.dumps(document, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")
    if len(encoded) > _MAX_RESPONSE_BYTES:
        raise RuntimeError("Host tool request exceeds bound")
    connection = _UnixHTTPConnection(_required("PYSOLATE_RPC_SOCKET"), timeout=10.0)
    try:
        connection.request("POST", path, body=encoded, headers={
            "Authorization": "Bearer " + _required("PYSOLATE_RPC_CREDENTIAL"),
            "Content-Type": "application/json", "Content-Length": str(len(encoded)),
        })
        response = connection.getresponse()
        body = response.read(_MAX_RESPONSE_BYTES + 1)
    finally:
        connection.close()
    if len(body) > _MAX_RESPONSE_BYTES:
        raise RuntimeError("Host tool response exceeds bound")
    try:
        result = json.loads(body)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("invalid Host tool response") from exc
    if response.status != 200 or not isinstance(result, dict):
        raise RuntimeError("Host tool channel denied request")
    return result

def ready() -> dict[str, Any]:
    document = _post("/v1/ready", _identity())
    if document.get("status") != "ready" or document.get("plan_sha256") != _identity()["plan_sha256"]:
        raise RuntimeError("Host tool readiness identity mismatch")
    return document

def call(raw_call: str) -> str:
    if not isinstance(raw_call, str) or not raw_call:
        raise RuntimeError("invalid Host tool call")
    try:
        call_document: Any = json.loads(raw_call)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("invalid Host tool call") from exc
    envelope = _identity()
    envelope["call"] = call_document
    document = _post("/v1/calls", envelope)
    if document.get("status") != "completed" or not isinstance(document.get("broker_response"), dict):
        raise RuntimeError("Host tool call outcome is ambiguous")
    return json.dumps(document["broker_response"], separators=(",", ":"), ensure_ascii=False, allow_nan=False)

def seal_imports(_modules: tuple[str, ...]) -> None:
    return None
