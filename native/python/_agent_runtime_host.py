"""Native CPython bridge for the Host-owned Pysolate capability plane.

The module intentionally exposes the same ``call(str) -> str`` surface as the
WASM host import. Authority remains in the Host channel registry and Broker;
environment values only locate and authenticate one short-lived private channel.
"""

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


def call(raw_call: str) -> str:
    if not isinstance(raw_call, str) or not raw_call or len(raw_call.encode("utf-8")) > _MAX_RESPONSE_BYTES:
        raise RuntimeError("invalid Host tool call")
    try:
        call_document: Any = json.loads(raw_call)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("invalid Host tool call") from exc
    envelope = {
        "schema_version": "pysolate.capability-rpc.v1",
        "channel_id": _required("PYSOLATE_RPC_CHANNEL_ID"),
        "invocation_id": _required("PYSOLATE_RPC_INVOCATION_ID"),
        "execution_id": _required("PYSOLATE_RPC_EXECUTION_ID"),
        "plan_sha256": _required("PYSOLATE_RPC_PLAN_SHA256"),
        "call": call_document,
    }
    encoded = json.dumps(envelope, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")
    connection = _UnixHTTPConnection(_required("PYSOLATE_RPC_SOCKET"), timeout=10.0)
    try:
        connection.request(
            "POST",
            "/v1/calls",
            body=encoded,
            headers={
                "Authorization": "Bearer " + _required("PYSOLATE_RPC_CREDENTIAL"),
                "Content-Type": "application/json",
                "Content-Length": str(len(encoded)),
            },
        )
        response = connection.getresponse()
        body = response.read(_MAX_RESPONSE_BYTES + 1)
    finally:
        connection.close()
    if len(body) > _MAX_RESPONSE_BYTES:
        raise RuntimeError("Host tool response exceeds bound")
    try:
        document = json.loads(body)
    except (TypeError, ValueError) as exc:
        raise RuntimeError("invalid Host tool response") from exc
    if response.status != 200 or not isinstance(document, dict):
        raise RuntimeError("Host tool channel denied request")
    if document.get("status") != "completed" or not isinstance(document.get("broker_response"), dict):
        raise RuntimeError("Host tool call outcome is ambiguous")
    return json.dumps(document["broker_response"], separators=(",", ":"), ensure_ascii=False, allow_nan=False)


def seal_imports(_modules: tuple[str, ...]) -> None:
    """Compatibility hook for the shared bootstrap; native imports stay profile-local."""
    return None
