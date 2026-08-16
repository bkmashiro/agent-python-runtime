#!/usr/bin/env python3
"""Run the official tau2 DB/communication oracle for the exact airline/3 canary."""

import argparse
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict


EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REQUEST_SCHEMA = "pysolate.tau2-canary-oracle-request.v1"
REPORT_SCHEMA = "pysolate.tau2-canary-oracle.v1"
MAX_REQUEST_BYTES = 64 * 1024
TOP_FIELDS = {"schema_version", "source_revision", "domain", "task_id", "calls", "assistant_text"}
CALL_FIELDS = {"call_id", "tool", "arguments", "content"}
EXPECTED_CALLS = [
    ("get_reservation_details", {"reservation_id": "JMO1MG"}),
    ("get_user_details", {"user_id": "anya_garcia_5901"}),
]


def _reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON key")
        value[key] = item
    return value


def validate_request(value: Dict[str, Any]) -> Dict[str, Any]:
    if not isinstance(value, dict) or set(value) != TOP_FIELDS:
        raise ValueError("invalid oracle request fields")
    if value["schema_version"] != REQUEST_SCHEMA or value["source_revision"] != EXPECTED_REVISION:
        raise ValueError("oracle request identity mismatch")
    if value["domain"] != "airline" or value["task_id"] != "3":
        raise ValueError("oracle request outside canary task")
    if not isinstance(value["assistant_text"], str) or not value["assistant_text"].strip():
        raise ValueError("assistant text is required")
    calls = value["calls"]
    if not isinstance(calls, list) or len(calls) != len(EXPECTED_CALLS):
        raise ValueError("exact canary call count required")
    for call, (tool, arguments) in zip(calls, EXPECTED_CALLS):
        if not isinstance(call, dict) or set(call) != CALL_FIELDS:
            raise ValueError("invalid call fields")
        if call["tool"] != tool or call["arguments"] != arguments:
            raise ValueError("call order or resource scope mismatch")
        if not isinstance(call["call_id"], str) or not call["call_id"]:
            raise ValueError("call identity required")
        if not isinstance(call["content"], str) or not call["content"]:
            raise ValueError("tool content required")
    return value


def public_report(env_reward: float, communicate_reward: float, db_match: bool, communicate_met: bool):
    overall = env_reward * communicate_reward
    return {
        "schema_version": REPORT_SCHEMA,
        "source": {
            "repository": "https://github.com/sierra-research/tau2-bench",
            "revision": EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "3",
        },
        "reward_basis": ["DB", "COMMUNICATE"],
        "db_reward": env_reward,
        "communicate_reward": communicate_reward,
        "overall_reward": overall,
        "db_match": db_match,
        "communicate_met": communicate_met,
        "tool_calls": 2,
        "tool_bodies_included": False,
        "assistant_text_included": False,
    }


def _verify_checkout(root: pathlib.Path) -> None:
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(root), check=True, capture_output=True, text=True
    ).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source checkout revision mismatch")


def _evaluate(root: pathlib.Path, request: Dict[str, Any]):
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import AssistantMessage, ToolCall, ToolMessage  # type: ignore[import-not-found]
    from tau2.evaluator.evaluator_communicate import CommunicateEvaluator  # type: ignore[import-not-found]
    from tau2.evaluator.evaluator_env import EnvironmentEvaluator  # type: ignore[import-not-found]
    from tau2.registry import registry  # type: ignore[import-not-found]
    from tau2.runner import get_tasks  # type: ignore[import-not-found]

    task = get_tasks("airline", task_ids=["3"])[0]
    initial = task.initial_state
    trajectory = list(initial.message_history) if initial and initial.message_history else []
    for call in request["calls"]:
        tool_call = ToolCall(id=call["call_id"], name=call["tool"], arguments=call["arguments"])
        trajectory.append(AssistantMessage(role="assistant", content=None, tool_calls=[tool_call]))
        trajectory.append(ToolMessage(role="tool", id=call["call_id"], content=call["content"], requestor="assistant", error=False))
    trajectory.append(AssistantMessage(role="assistant", content=request["assistant_text"]))
    environment = EnvironmentEvaluator.calculate_reward(
        environment_constructor=registry.get_env_constructor("airline"),
        task=task,
        full_trajectory=trajectory,
    )
    communication = CommunicateEvaluator.calculate_reward(task=task, full_trajectory=trajectory)
    communicate_met = bool(communication.communicate_checks) and all(
        check.met for check in communication.communicate_checks
    )
    return public_report(
        env_reward=float(environment.reward),
        communicate_reward=float(communication.reward),
        db_match=bool(environment.db_check and environment.db_check.db_match),
        communicate_met=communicate_met,
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    args = parser.parse_args()
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if len(raw) > MAX_REQUEST_BYTES:
        raise ValueError("oracle request exceeds size limit")
    request = json.loads(raw, object_pairs_hook=_reject_duplicates)
    validate_request(request)
    root = pathlib.Path(args.source_root).resolve()
    _verify_checkout(root)
    print(json.dumps(_evaluate(root, request), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"tau2 canary oracle failed: {error}", file=sys.stderr)
        raise SystemExit(2)
