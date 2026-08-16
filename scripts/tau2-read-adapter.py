#!/usr/bin/env python3
"""Exact, read-only tau2 airline/3 Host adapter for the Pysolate canary."""

import argparse
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict


EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REQUEST_SCHEMA = "pysolate.tau2-read-request.v1"
RESPONSE_SCHEMA = "pysolate.tau2-read-response.v1"
MAX_REQUEST_BYTES = 16 * 1024
REQUEST_FIELDS = {
    "schema_version",
    "source_revision",
    "domain",
    "task_id",
    "call_id",
    "tool",
    "arguments",
}
EXACT_SCOPE = {
    "get_reservation_details": {"reservation_id": "JMO1MG"},
    "get_user_details": {"user_id": "anya_garcia_5901"},
}


def _reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key")
        value[key] = item
    return value


def validate_request(value: Dict[str, Any]) -> Dict[str, Any]:
    if not isinstance(value, dict) or set(value) != REQUEST_FIELDS:
        raise ValueError("invalid request fields")
    if value["schema_version"] != REQUEST_SCHEMA:
        raise ValueError("invalid request schema")
    if value["source_revision"] != EXPECTED_REVISION:
        raise ValueError("source revision mismatch")
    if value["domain"] != "airline" or value["task_id"] != "3":
        raise ValueError("request outside canary task scope")
    if not isinstance(value["call_id"], str) or not value["call_id"] or len(value["call_id"]) > 128:
        raise ValueError("invalid call identity")
    tool = value["tool"]
    if tool not in EXACT_SCOPE or value["arguments"] != EXACT_SCOPE[tool]:
        raise ValueError("request outside exact read scope")
    return value


def response_envelope(request: Dict[str, Any], content: str) -> Dict[str, Any]:
    if not isinstance(content, str):
        raise ValueError("upstream tool content must be a string")
    return {
        "schema_version": RESPONSE_SCHEMA,
        "source_revision": EXPECTED_REVISION,
        "domain": "airline",
        "task_id": "3",
        "call_id": request["call_id"],
        "tool": request["tool"],
        "content": content,
    }


def _verify_checkout(root: pathlib.Path) -> None:
    if not root.is_dir():
        raise ValueError("source root is not a directory")
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=str(root),
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source checkout revision mismatch")


def _execute(root: pathlib.Path, request: Dict[str, Any]) -> str:
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import ToolCall  # type: ignore[import-not-found]
    from tau2.runner import build_environment, get_tasks  # type: ignore[import-not-found]

    task = get_tasks("airline", task_ids=["3"])[0]
    environment = build_environment("airline")
    initial = task.initial_state
    environment.set_state(
        initialization_data=(initial.initialization_data if initial else None),
        initialization_actions=(initial.initialization_actions if initial else None),
        message_history=(list(initial.message_history) if initial and initial.message_history else []),
    )
    response = environment.get_response(
        ToolCall(
            id=request["call_id"],
            name=request["tool"],
            arguments=request["arguments"],
        )
    )
    if response.error:
        raise RuntimeError("upstream tool rejected qualified request")
    return response.content


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
    content = _execute(root, request)
    print(json.dumps(response_envelope(request, content), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"tau2 read adapter failed: {error}", file=sys.stderr)
        raise SystemExit(2)
