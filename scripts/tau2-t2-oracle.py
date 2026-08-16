#!/usr/bin/env python3
"""Independently rebuild tau2 rewards from one private cell."""

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
PRIVATE_SCHEMA = "pysolate.tau2.t2-remediation-preregistration-private.v1"
CELL_SCHEMA = "pysolate.tau2-t2-cell-private.v1"
ORACLE_SCHEMA = "pysolate.tau2.t2-oracle-rebuild.v1"


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def sha(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def require_clean_source(root: pathlib.Path) -> None:
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=root, capture_output=True, text=True, check=True
    ).stdout.strip()
    dirty = subprocess.run(
        ["git", "status", "--porcelain"], cwd=root, capture_output=True, text=True, check=True
    ).stdout
    if revision != REVISION or dirty:
        raise ValueError("tau2 source checkout mismatch")


def terminal_event_contents(events: Any) -> list[str]:
    if not isinstance(events, list):
        raise ValueError("cell events missing")
    contents = []
    for event in events:
        kind = event.get("kind")
        raw = event.get("model_response")
        if not isinstance(raw, str):
            raise ValueError("event model response missing")
        if kind == "answer":
            action = json.loads(raw)
            if set(action) != {"kind", "content"} or action.get("kind") != "answer" or not isinstance(action.get("content"), str) or not action["content"].strip():
                raise ValueError("terminal answer event is malformed")
            contents.append(action["content"])
        elif kind == "invalid_model_action":
            contents.append(raw if raw.strip() else "[empty or whitespace-only invalid model action]")
    return contents


def rebuild(source_root: pathlib.Path, private: dict[str, Any], cell: dict[str, Any]) -> dict[str, Any]:
    require_clean_source(source_root)
    if private.get("schema_version") != PRIVATE_SCHEMA:
        raise ValueError("private remediation manifest mismatch")
    if cell.get("schema_version") != CELL_SCHEMA or cell.get("status") != "completed":
        raise ValueError("oracle requires one completed cell")
    task_id = cell.get("task_id")
    matches = [item for item in private.get("tasks", []) if item.get("task_id") == task_id]
    if len(matches) != 1:
        raise ValueError("cell task outside private manifest")
    private_task = matches[0]
    sys.path.insert(0, str(source_root / "src"))
    from tau2.data_model.message import AssistantMessage, ToolCall
    from tau2.data_model.simulation import SimulationRun
    from tau2.evaluator.evaluator import EvaluationType, evaluate_simulation
    from tau2.evaluator.evaluator_action import ActionEvaluator
    from tau2.orchestrator.orchestrator import DEFAULT_FIRST_AGENT_MESSAGE
    from tau2.runner import get_tasks

    source_tasks = get_tasks("airline", task_ids=[task_id])
    if len(source_tasks) != 1:
        raise ValueError("fixed source task missing")
    task = source_tasks[0]
    task_body = task.model_dump(mode="json")
    if canonical(task_body) != canonical(private_task.get("task")):
        raise ValueError("fixed source task differs from private manifest")
    actions = private_task.get("reference_actions")
    if not isinstance(actions, list) or not actions:
        raise ValueError("private reference actions missing")
    simulation_body = cell.get("simulation")
    if not isinstance(simulation_body, dict) or not simulation_body.get("messages"):
        raise ValueError("SimulationRun messages missing")
    simulation = SimulationRun.model_validate(simulation_body)
    for message in simulation.messages:
        message.validate()
    roles = [message.role for message in simulation.messages]
    expected_roles = ["assistant" if index % 2 == 0 else "user" for index in range(len(roles))]
    if roles != expected_roles:
        raise ValueError("simulation message roles do not follow the frozen half-duplex trajectory")
    assistant_messages = [message for message in simulation.messages if message.role == "assistant"]
    first = assistant_messages[0]
    if first.content != DEFAULT_FIRST_AGENT_MESSAGE.content or first.tool_calls:
        raise ValueError("simulation initial assistant message mismatch")
    expected_terminal_contents = terminal_event_contents(cell.get("pysolate_events"))
    outward = assistant_messages[1:]
    if len(outward) != len(expected_terminal_contents):
        raise ValueError("assistant trajectory is not bound to terminal events")
    for message, expected in zip(outward, expected_terminal_contents):
        if message.content != expected or message.tool_calls:
            raise ValueError("assistant trajectory differs from terminal event evidence")
    default_info = evaluate_simulation(
        simulation=simulation,
        task=task,
        evaluation_type=EvaluationType.ALL,
        solo_mode=False,
        domain="airline",
    ).model_dump(mode="json")
    if canonical(default_info) != canonical(simulation_body.get("reward_info")):
        raise ValueError("default official reward reconstruction mismatch")

    allowed = {(item["name"], canonical(item["arguments"])) for item in actions}
    calls = []
    for index, event in enumerate(cell.get("pysolate_events") or []):
        if event.get("kind") != "program":
            continue
        key = (event.get("tool"), canonical(event.get("arguments")))
        if key not in allowed:
            raise ValueError("diagnostic event outside frozen reference actions")
        calls.append(ToolCall(id=f"treatment-{index}", name=event["tool"], arguments=event["arguments"]))
    diagnostic_messages = [AssistantMessage(role="assistant", content=None, tool_calls=calls)] if calls else []
    action_info = ActionEvaluator.calculate_reward(task, diagnostic_messages).model_dump(mode="json")
    if canonical(action_info) != canonical(cell.get("official_action_diagnostic")):
        raise ValueError("official ActionEvaluator reconstruction mismatch")
    return {
        "schema_version": ORACLE_SCHEMA,
        "source_revision": REVISION,
        "task_id": task_id,
        "task_sha256": sha(canonical(task_body)),
        "reference_actions_sha256": sha(canonical(actions)),
        "simulation_messages_sha256": sha(canonical(simulation_body["messages"])),
        "default_reward_info_sha256": sha(canonical(default_info)),
        "action_reward_info_sha256": sha(canonical(action_info)),
        "default_reward": float(default_info["reward"]),
        "action_reward": float(action_info["reward"]),
        "message_count": len(simulation_body["messages"]),
        "assistant_messages_bound": len(assistant_messages),
        "assistant_event_binding_sha256": sha(canonical(expected_terminal_contents)),
        "action_call_count": len(calls),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--private-manifest", required=True)
    parser.add_argument("--cell", required=True)
    args = parser.parse_args()
    result = rebuild(
        pathlib.Path(args.source_root).resolve(),
        json.loads(pathlib.Path(args.private_manifest).read_text()),
        json.loads(pathlib.Path(args.cell).read_text()),
    )
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
