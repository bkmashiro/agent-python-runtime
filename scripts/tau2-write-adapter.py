#!/usr/bin/env python3
"""Exact private-workspace tau2 airline/11 WRITE adapter.

The adapter never owns persistent state. It validates one frozen task action,
applies it to a caller-supplied candidate DB in this disposable process, and
returns the candidate only after the upstream tool succeeds. The Host decides
whether to atomically install that candidate in its private workspace attempt.
"""

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REQUEST_SCHEMA = "pysolate.tau2-write-request.v1"
RESPONSE_SCHEMA = "pysolate.tau2-write-response.v1"
MAX_REQUEST_BYTES = 16 * 1024 * 1024
INIT_FIELDS = {"schema_version", "source_revision", "domain", "task_id", "operation"}
APPLY_FIELDS = INIT_FIELDS | {"call_id", "tool", "arguments", "state", "inject_failure"}
EXPECTED_ARGUMENTS = {
    "reservation_id": "GV1N64",
    "cabin": "basic_economy",
    "flights": [
        {"flight_number": "HAT003", "date": "2024-05-19"},
        {"flight_number": "HAT290", "date": "2024-05-20"},
    ],
    "payment_id": "gift_card_1642017",
}


def _reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key")
        value[key] = item
    return value


def canonical_state(value: Dict[str, Any]) -> bytes:
    if not isinstance(value, dict):
        raise ValueError("state must be an object")
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def state_identity(value: Dict[str, Any]) -> str:
    return "sha256:" + hashlib.sha256(canonical_state(value)).hexdigest()


def validate_request(value: Dict[str, Any]) -> Dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError("request must be an object")
    if value.get("schema_version") != REQUEST_SCHEMA or value.get("source_revision") != EXPECTED_REVISION:
        raise ValueError("request identity mismatch")
    if value.get("domain") != "airline" or value.get("task_id") != "11":
        raise ValueError("request outside exact canary task")
    operation = value.get("operation")
    if operation == "init":
        if set(value) != INIT_FIELDS:
            raise ValueError("invalid init fields")
        return value
    if operation != "apply" or set(value) != APPLY_FIELDS:
        raise ValueError("invalid apply fields")
    if value.get("tool") != "update_reservation_flights" or value.get("arguments") != EXPECTED_ARGUMENTS:
        raise ValueError("request outside exact WRITE scope")
    if not isinstance(value.get("call_id"), str) or not value["call_id"] or len(value["call_id"]) > 128:
        raise ValueError("invalid call identity")
    if not isinstance(value.get("state"), dict):
        raise ValueError("state must be an object")
    if type(value.get("inject_failure")) is not bool:
        raise ValueError("inject_failure must be boolean")
    return value


def _verify_checkout(root: pathlib.Path) -> None:
    if not root.is_dir():
        raise ValueError("source root is not a directory")
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(root), check=True, capture_output=True, text=True
    ).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source checkout revision mismatch")


def _initial_state(root: pathlib.Path) -> Dict[str, Any]:
    sys.path.insert(0, str(root / "src"))
    from tau2.runner import build_environment  # type: ignore[import-not-found]

    environment = build_environment("airline")
    return environment.tools.db.model_dump(mode="json")


def _apply(root: pathlib.Path, request: Dict[str, Any]):
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import ToolCall  # type: ignore[import-not-found]
    from tau2.domains.airline.data_model import FlightDB  # type: ignore[import-not-found]
    from tau2.domains.airline.environment import get_environment  # type: ignore[import-not-found]

    database = FlightDB.model_validate(request["state"])
    environment = get_environment(db=database)
    response = environment.get_response(
        ToolCall(
            id=request["call_id"],
            name=request["tool"],
            arguments=request["arguments"],
        )
    )
    if response.error:
        raise RuntimeError("upstream WRITE rejected qualified request")
    candidate = environment.tools.db.model_dump(mode="json")
    if request["inject_failure"]:
        raise RuntimeError("injected failure before candidate emission")
    return response.content, candidate


def execute(root: pathlib.Path, request: Dict[str, Any]) -> Dict[str, Any]:
    if request["operation"] == "init":
        state = _initial_state(root)
        return {
            "schema_version": RESPONSE_SCHEMA,
            "source_revision": EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
            "operation": "init",
            "state_sha256": state_identity(state),
            "state": state,
        }
    content, candidate = _apply(root, request)
    return {
        "schema_version": RESPONSE_SCHEMA,
        "source_revision": EXPECTED_REVISION,
        "domain": "airline",
        "task_id": "11",
        "operation": "apply",
        "call_id": request["call_id"],
        "tool": request["tool"],
        "content": content,
        "candidate_state_sha256": state_identity(candidate),
        "candidate_state": candidate,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    args = parser.parse_args()
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if len(raw) > MAX_REQUEST_BYTES:
        raise ValueError("request exceeds size limit")
    request = json.loads(raw, object_pairs_hook=_reject_duplicates)
    validate_request(request)
    root = pathlib.Path(args.source_root).resolve()
    _verify_checkout(root)
    print(json.dumps(execute(root, request), sort_keys=True, separators=(",", ":"), ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"tau2 write adapter failed: {error}", file=sys.stderr)
        raise SystemExit(2)
