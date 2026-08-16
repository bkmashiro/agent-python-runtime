#!/usr/bin/env python3
"""Qualify and score the exact tau2 retail/24 pure communication component."""

import argparse
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REQUEST_SCHEMA = "pysolate.tau2-pure-oracle-request.v1"
REPORT_SCHEMA = "pysolate.tau2-pure-oracle.v1"
MAX_REQUEST_BYTES = 64 * 1024


def _reject_duplicates(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError("duplicate JSON field")
        value[key] = item
    return value


def validate_request(value: Any) -> Dict[str, Any]:
    if not isinstance(value, dict) or set(value) != {"schema_version", "source_revision", "domain", "task_id", "assistant_text"}:
        raise ValueError("invalid oracle request fields")
    if value["schema_version"] != REQUEST_SCHEMA or value["source_revision"] != EXPECTED_REVISION:
        raise ValueError("request identity mismatch")
    if value["domain"] != "retail" or value["task_id"] != "24":
        raise ValueError("exact task mismatch")
    if not isinstance(value["assistant_text"], str) or not value["assistant_text"].strip():
        raise ValueError("assistant text required")
    return value


def _verify_checkout(root: pathlib.Path) -> None:
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(root), check=True, capture_output=True, text=True
    ).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source revision mismatch")


def _evaluate(root: pathlib.Path, request: Dict[str, Any]) -> Dict[str, Any]:
    sys.path.insert(0, str(root / "src"))
    from tau2.data_model.message import AssistantMessage  # type: ignore[import-not-found]
    from tau2.evaluator.evaluator_communicate import CommunicateEvaluator  # type: ignore[import-not-found]
    from tau2.runner import get_tasks  # type: ignore[import-not-found]

    tasks = get_tasks("retail", task_ids=["24"])
    if len(tasks) != 1:
        raise ValueError("exact task unavailable")
    task = tasks[0]
    criteria = task.evaluation_criteria
    if criteria is None or criteria.actions or criteria.env_assertions or len(criteria.communicate_info or []) != 2:
        raise ValueError("task no longer has the frozen pure communication shape")
    basis = [str(value.value if hasattr(value, "value") else value) for value in (criteria.reward_basis or [])]
    if basis != ["DB", "NL_ASSERTION"]:
        raise ValueError("task reward basis drift")

    correct_text = " ".join(criteria.communicate_info)
    correct = CommunicateEvaluator.calculate_reward(task, [AssistantMessage(role="assistant", content=correct_text)])
    empty = CommunicateEvaluator.calculate_reward(task, [])
    wrong = CommunicateEvaluator.calculate_reward(task, [AssistantMessage(role="assistant", content="I cannot provide that information.")])
    candidate = CommunicateEvaluator.calculate_reward(
        task, [AssistantMessage(role="assistant", content=request["assistant_text"])]
    )
    if (float(correct.reward), float(empty.reward), float(wrong.reward)) != (1.0, 0.0, 0.0):
        raise ValueError("official component is not no-op sensitive")
    return {
        "schema_version": REPORT_SCHEMA,
        "source": {"revision": EXPECTED_REVISION, "domain": "retail", "task_id": "24"},
        "task_shape": {
            "reference_actions": 0,
            "environment_assertions": 0,
            "communicate_items": 2,
            "default_reward_basis": basis,
        },
        "oracle": {
            "implementation": "tau2 CommunicateEvaluator",
            "official_upstream_component": True,
            "official_task_overall": False,
            "correct_control": 1.0,
            "empty_control": 0.0,
            "wrong_control": 0.0,
            "candidate_reward": float(candidate.reward),
        },
        "claim_boundary": {
            "supports": "deterministic official communication-component scoring for one no-reference-action task",
            "does_not_support": ["official task overall reward", "NL assertion judge reproducibility", "leaderboard comparison"],
        },
        "assistant_text_included": False,
        "required_information_included": False,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    args = parser.parse_args()
    raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
    if len(raw) > MAX_REQUEST_BYTES:
        raise ValueError("oracle request exceeds size limit")
    request = validate_request(json.loads(raw, object_pairs_hook=_reject_duplicates))
    root = pathlib.Path(args.source_root).resolve()
    _verify_checkout(root)
    print(json.dumps(_evaluate(root, request), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"tau2 pure oracle failed: {error}", file=sys.stderr)
        raise SystemExit(2)
