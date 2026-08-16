#!/usr/bin/env python3
"""Exact task-scoped READ adapter for the frozen tau2 T2 cohort."""

import argparse
import json
import pathlib
import subprocess
import sys
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
REQUEST_SCHEMA = "pysolate.tau2-t2-read-request.v1"
RESPONSE_SCHEMA = "pysolate.tau2-t2-read-response.v1"
READ_TOOLS = {"get_reservation_details", "get_user_details", "search_direct_flight"}
REQUEST_FIELDS = {"schema_version", "source_revision", "domain", "task_id", "call_id", "tool", "arguments"}
MAX_REQUEST_BYTES = 32 * 1024


def reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key")
        value[key] = item
    return value


def verify_checkout(root: pathlib.Path) -> None:
    if not root.is_dir():
        raise ValueError("source root is not a directory")
    commands = [
        ["git", "rev-parse", "HEAD"],
        ["git", "status", "--porcelain", "--untracked-files=no"],
        ["git", "ls-files", "--others", "--exclude-standard", "--", "src/tau2", "data/tau2"],
    ]
    outputs = [subprocess.run(command, cwd=root, check=True, capture_output=True, text=True).stdout.strip() for command in commands]
    if outputs[0] != REVISION or outputs[1] or outputs[2]:
        raise ValueError("tau2 source checkout is not the exact clean revision")


def load_scope(path: pathlib.Path, task_id: str) -> list[dict[str, Any]]:
    value = json.loads(path.read_text(), object_pairs_hook=reject_duplicates)
    if not isinstance(value, dict) or value.get("schema_version") != PRIVATE_SCHEMA:
        raise ValueError("invalid private cohort manifest")
    source = value.get("source")
    if not isinstance(source, dict) or source.get("revision") != REVISION or source.get("domain") != "airline":
        raise ValueError("private cohort source mismatch")
    matches = [item for item in value.get("tasks", []) if isinstance(item, dict) and item.get("task_id") == task_id]
    if len(matches) != 1:
        raise ValueError("task is not in frozen cohort")
    actions = matches[0].get("reference_actions")
    if not isinstance(actions, list) or not actions:
        raise ValueError("frozen task has no reference actions")
    return actions


def validate_request(value: Any, actions: list[dict[str, Any]], expected_task_id: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != REQUEST_FIELDS:
        raise ValueError("invalid request fields")
    if value["schema_version"] != REQUEST_SCHEMA or value["source_revision"] != REVISION or value["domain"] != "airline":
        raise ValueError("request source mismatch")
    if value["task_id"] != expected_task_id or not isinstance(value["call_id"], str) or not value["call_id"] or len(value["call_id"]) > 128:
        raise ValueError("invalid request identity")
    if value["tool"] not in READ_TOOLS or not isinstance(value["arguments"], dict):
        raise ValueError("request is not an allowed READ")
    if not any(action.get("name") == value["tool"] and action.get("arguments") == value["arguments"] for action in actions):
        raise ValueError("request is outside exact task reference scope")
    return value


def execute(root: pathlib.Path, request: dict[str, Any]) -> str:
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import ToolCall  # type: ignore[import-not-found]
    from tau2.runner import build_environment, get_tasks  # type: ignore[import-not-found]
    tasks = get_tasks("airline", task_ids=[request["task_id"]])
    if len(tasks) != 1:
        raise ValueError("frozen task missing")
    task = tasks[0]
    environment = build_environment("airline")
    initial = task.initial_state
    environment.set_state(
        initialization_data=(initial.initialization_data if initial else None),
        initialization_actions=(initial.initialization_actions if initial else None),
        message_history=(list(initial.message_history) if initial and initial.message_history else []),
    )
    response = environment.get_response(ToolCall(id=request["call_id"], name=request["tool"], arguments=request["arguments"]))
    if response.error or not isinstance(response.content, str):
        raise RuntimeError("upstream tool rejected qualified READ")
    return response.content


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--private-manifest", required=True)
    parser.add_argument("--task-id", required=True)
    args = parser.parse_args()
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if len(raw) > MAX_REQUEST_BYTES:
        raise ValueError("request exceeds size limit")
    request = json.loads(raw, object_pairs_hook=reject_duplicates)
    if request.get("task_id") != args.task_id:
        raise ValueError("task argument mismatch")
    root = pathlib.Path(args.source_root).resolve()
    verify_checkout(root)
    actions = load_scope(pathlib.Path(args.private_manifest), args.task_id)
    validate_request(request, actions, args.task_id)
    content = execute(root, request)
    response = {
        "schema_version": RESPONSE_SCHEMA, "source_revision": REVISION, "domain": "airline",
        "task_id": args.task_id, "call_id": request["call_id"], "tool": request["tool"], "content": content,
    }
    print(json.dumps(response, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"tau2 T2 read adapter failed: {error}", file=sys.stderr)
        raise SystemExit(2)
