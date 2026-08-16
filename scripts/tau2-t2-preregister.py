#!/usr/bin/env python3
"""Preregister the exact tau2 T2 natural READ cohort without provider calls."""

import argparse
import hashlib
import json
import pathlib
import re
import subprocess
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REPOSITORY = "https://github.com/sierra-research/tau2-bench"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
PUBLIC_SCHEMA = "pysolate.tau2-t2-preregistration.v1"
READ_TOOLS = {"get_reservation_details", "get_user_details", "search_direct_flight"}
EXPECTED_IDS = ["1", "2", "3", "4", "5", "6", "9", "27", "36", "38", "41", "43", "45", "47", "48", "49"]
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def digest(value: Any) -> str:
    return "sha256:" + hashlib.sha256(canonical(value)).hexdigest()


def verify_checkout(root: pathlib.Path) -> None:
    if not root.is_dir():
        raise ValueError("source root is not a directory")
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, check=True, capture_output=True, text=True).stdout.strip()
    tracked = subprocess.run(["git", "status", "--porcelain", "--untracked-files=no"], cwd=root, check=True, capture_output=True, text=True).stdout.strip()
    untracked = subprocess.run(["git", "ls-files", "--others", "--exclude-standard", "--", "src/tau2", "data/tau2"], cwd=root, check=True, capture_output=True, text=True).stdout.strip()
    if head != REVISION or tracked or untracked:
        raise ValueError("tau2 source checkout is not the exact clean revision")


def build(root: pathlib.Path) -> tuple[dict[str, Any], dict[str, Any]]:
    verify_checkout(root)
    import sys
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import AssistantMessage, ToolCall
    from tau2.evaluator.evaluator_action import ActionEvaluator
    from tau2.evaluator.evaluator_communicate import CommunicateEvaluator
    from tau2.evaluator.evaluator_env import EnvironmentEvaluator
    from tau2.runner import get_tasks
    from tau2.domains.airline.environment import get_environment

    selected = []
    for task in get_tasks("airline"):
        criteria = task.evaluation_criteria
        actions = list(criteria.actions or [])
        names = [action.name for action in actions]
        basis = sorted(str(value.value if hasattr(value, "value") else value) for value in (criteria.reward_basis or []))
        if actions and set(names).issubset(READ_TOOLS) and basis == ["COMMUNICATE", "DB"] and not (criteria.env_assertions or []):
            selected.append(task)
    selected.sort(key=lambda item: int(item.id))
    ids = [str(task.id) for task in selected]
    if ids != EXPECTED_IDS:
        raise ValueError(f"deterministic cohort drift: {ids}")

    private_tasks = []
    public_tasks = []
    for task in selected:
        actions = list(task.evaluation_criteria.actions or [])
        calls = [ToolCall(id=f"gold-{index}", name=action.name, arguments=action.arguments) for index, action in enumerate(actions)]
        gold_action = ActionEvaluator.calculate_reward(task, [AssistantMessage(role="assistant", content=None, tool_calls=calls)]).reward
        empty_action = ActionEvaluator.calculate_reward(task, []).reward
        empty_db = EnvironmentEvaluator.calculate_reward(get_environment, task, []).reward
        empty_communicate = CommunicateEvaluator.calculate_reward(task, []).reward
        if gold_action != 1.0 or empty_action != 0.0:
            raise ValueError(f"ActionEvaluator control failed for task {task.id}")
        task_body = task.model_dump(mode="json")
        action_body = [{"name": action.name, "requestor": action.requestor, "arguments": action.arguments} for action in actions]
        task_digest = digest(task_body)
        action_digest = digest(action_body)
        private_tasks.append({
            "task_id": str(task.id), "task_sha256": task_digest, "task": task_body,
            "reference_actions_sha256": action_digest, "reference_actions": action_body,
            "controls": {"gold_action_reward": gold_action, "empty_action_reward": empty_action, "empty_db_reward": empty_db, "empty_communicate_reward": empty_communicate},
        })
        public_tasks.append({
            "task_id": str(task.id), "task_sha256": task_digest,
            "reference_action_count": len(actions), "reference_action_names": [action.name for action in actions],
            "reference_actions_sha256": action_digest, "arguments_included": False,
            "official_action_component": {"gold": gold_action, "empty": empty_action, "no_op_sensitive": True},
            "default_no_op": {"db_reward": empty_db, "communicate_reward": empty_communicate, "sensitive": empty_db * empty_communicate == 0.0},
        })

    source = {"repository": REPOSITORY, "revision": REVISION, "version": "1.0.1", "license": "MIT", "domain": "airline"}
    protocol = {
        "model": "deepseek/deepseek-v4-pro", "seed": 42, "temperature": 0.0,
        "lanes": ["direct", "programmatic_python"], "trials_per_cell": 1,
        "max_steps": 16, "max_errors": 2, "max_agent_model_invocations_per_trial": 16,
        "max_total_provider_invocations_per_trial": 20, "max_total_provider_invocations": 640,
        "post_provider_reruns": 0, "pre_provider_class_wide_repair_budget": 1,
        "default_evaluation": "tau2 EvaluationType.ALL", "diagnostic_evaluation": "official upstream ActionEvaluator",
        "treatment_runtime": "ordinary Python -> source admission -> real WASM Guest -> Broker -> exact task-scoped READ adapter",
        "tool_surface_matched": False, "performance_comparison_supported": False, "leaderboard_comparable": False,
    }
    denominator = {
        "natural_read_tasks": 16, "paired_trials": 32,
        "retain_statuses": ["completed", "agent_error", "failed", "rejected", "unclassifiable", "unscorable", "not_recorded"],
        "post_hoc_task_dropping": False,
        "separate_rows": {"write_mechanism": "airline/11 authored canary", "pure": "NO_ELIGIBLE_PURE_NATURAL_TASK", "placement": "Open-SWE control", "attrs_770": "case study"},
    }
    public = {"schema_version": PUBLIC_SCHEMA, "status": "frozen_pending_preflight", "source": source, "protocol": protocol, "denominator": denominator, "tasks": public_tasks}
    public["identity"] = digest(public)
    private = {"schema_version": PRIVATE_SCHEMA, "public_identity": public["identity"], "source": source, "protocol": protocol, "tasks": private_tasks}
    return private, public


def write_private(path: pathlib.Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)
    path.write_bytes(canonical(value) + b"\n")
    path.chmod(0o600)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--private-output", required=True)
    parser.add_argument("--public-output", required=True)
    args = parser.parse_args()
    private, public = build(pathlib.Path(args.source_root).resolve())
    write_private(pathlib.Path(args.private_output), private)
    public_path = pathlib.Path(args.public_output)
    public_path.parent.mkdir(parents=True, exist_ok=True)
    public_path.write_bytes(canonical(public) + b"\n")
    print(hashlib.sha256(public_path.read_bytes()).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
