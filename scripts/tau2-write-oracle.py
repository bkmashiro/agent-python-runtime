#!/usr/bin/env python3
"""Run official tau2 DB/COMMUNICATE oracles for the exact airline/11 WRITE canary."""

import argparse
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REQUEST_SCHEMA = "pysolate.tau2-write-oracle-request.v1"
REPORT_SCHEMA = "pysolate.tau2-write-oracle.v1"
MAX_REQUEST_BYTES = 4 * 1024 * 1024
TOP_FIELDS = {"schema_version", "source_revision", "domain", "task_id", "call", "assistant_text", "observed_final_state"}
CALL_FIELDS = {"call_id", "tool", "arguments", "content"}
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


def validate_request(value: Dict[str, Any]) -> Dict[str, Any]:
    if not isinstance(value, dict) or set(value) != TOP_FIELDS:
        raise ValueError("invalid oracle request fields")
    if value["schema_version"] != REQUEST_SCHEMA or value["source_revision"] != EXPECTED_REVISION:
        raise ValueError("oracle request identity mismatch")
    if value["domain"] != "airline" or value["task_id"] != "11":
        raise ValueError("oracle request outside exact canary task")
    call = value["call"]
    if not isinstance(call, dict) or set(call) != CALL_FIELDS:
        raise ValueError("invalid call fields")
    if call["tool"] != "update_reservation_flights" or call["arguments"] != EXPECTED_ARGUMENTS:
        raise ValueError("call outside exact WRITE scope")
    if not isinstance(call["call_id"], str) or not call["call_id"]:
        raise ValueError("call identity required")
    if not isinstance(call["content"], str) or not call["content"]:
        raise ValueError("tool content required")
    if not isinstance(value["assistant_text"], str) or "5244" not in value["assistant_text"]:
        raise ValueError("exact communication evidence is required")
    if not isinstance(value["observed_final_state"], dict) or not value["observed_final_state"]:
        raise ValueError("observed final task-world state is required")
    return value


def public_report(env_reward: float, communicate_reward: float, db_match: bool, communicate_met: bool, observed_final_state_match: bool):
    return {
        "schema_version": REPORT_SCHEMA,
        "source": {
            "repository": "https://github.com/sierra-research/tau2-bench",
            "revision": EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
        },
        "reward_basis": ["DB", "COMMUNICATE"],
        "db_reward": env_reward,
        "communicate_reward": communicate_reward,
        "overall_reward": env_reward * communicate_reward,
        "db_match": db_match,
        "communicate_met": communicate_met,
        "observed_final_state_match": observed_final_state_match,
        "tool_calls": 1,
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
    from tau2.domains.airline.data_model import FlightDB  # type: ignore[import-not-found]
    from tau2.evaluator.evaluator_communicate import CommunicateEvaluator  # type: ignore[import-not-found]
    from tau2.evaluator.evaluator_env import EnvironmentEvaluator  # type: ignore[import-not-found]
    from tau2.registry import registry  # type: ignore[import-not-found]
    from tau2.runner import get_tasks  # type: ignore[import-not-found]

    task = get_tasks("airline", task_ids=["11"])[0]
    call = request["call"]
    tool_call = ToolCall(id=call["call_id"], name=call["tool"], arguments=call["arguments"])
    trajectory = [
        AssistantMessage(role="assistant", content=None, tool_calls=[tool_call]),
        ToolMessage(role="tool", id=call["call_id"], content=call["content"], requestor="assistant", error=False),
        AssistantMessage(role="assistant", content=request["assistant_text"]),
    ]
    environment = EnvironmentEvaluator.calculate_reward(
        environment_constructor=registry.get_env_constructor("airline"), task=task, full_trajectory=trajectory
    )
    communication = CommunicateEvaluator.calculate_reward(task=task, full_trajectory=trajectory)
    communicate_met = bool(communication.communicate_checks) and all(
        check.met for check in communication.communicate_checks
    )
    gold = registry.get_env_constructor("airline")()
    initial = task.initial_state
    gold.set_state(
        initialization_data=(initial.initialization_data if initial else None),
        initialization_actions=(initial.initialization_actions if initial else None),
        message_history=(list(initial.message_history) if initial and initial.message_history else []),
    )
    for action in task.evaluation_criteria.actions or []:
        gold.make_tool_call(tool_name=action.name, requestor=action.requestor, **action.arguments)
    observed = FlightDB.model_validate(request["observed_final_state"])
    observed_final_state_match = observed.get_hash() == gold.get_db_hash()
    return public_report(
        env_reward=float(environment.reward), communicate_reward=float(communication.reward),
        db_match=bool(environment.db_check and environment.db_check.db_match), communicate_met=communicate_met,
        observed_final_state_match=observed_final_state_match,
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
        print(f"tau2 WRITE oracle failed: {error}", file=sys.stderr)
        raise SystemExit(2)
