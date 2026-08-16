#!/usr/bin/env python3
"""Validate private tau2 pure-task evidence and emit a body-safe closeout report."""

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REPORT_SCHEMA = "pysolate.tau2-pure-natural-qualification.v1"


def digest_bytes(body: bytes) -> str:
    return hashlib.sha256(body).hexdigest()


def read_json(path: pathlib.Path) -> Dict[str, Any]:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError("evidence must be a JSON object")
    return value


def census(source_root: pathlib.Path) -> list[dict[str, Any]]:
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=str(source_root), check=True, capture_output=True, text=True
    ).stdout.strip()
    if revision != EXPECTED_REVISION:
        raise ValueError("source revision mismatch")
    sys.path.insert(0, str(source_root / "src"))
    from tau2.registry import registry  # type: ignore[import-not-found]
    from tau2.runner import get_tasks  # type: ignore[import-not-found]

    rows = []
    for domain in registry.get_domains():
        try:
            tasks = get_tasks(domain)
        except Exception:
            continue
        for task in tasks:
            criteria = task.evaluation_criteria
            if criteria is None or criteria.actions or criteria.env_assertions:
                continue
            rows.append({
                "domain": domain,
                "task_id": str(task.id),
                "communicate_items": len(criteria.communicate_info or []),
                "nl_assertions": len(criteria.nl_assertions or []),
                "reward_basis": [
                    str(value.value if hasattr(value, "value") else value)
                    for value in (criteria.reward_basis or [])
                ],
            })
    return sorted(rows, key=lambda row: (row["domain"], row["task_id"]))


def validate(rows, qualification, direct, authored):
    natural = [row for row in rows if row["domain"] != "mock"]
    deterministic = [row for row in natural if row["communicate_items"] > 0]
    if len(rows) != 10 or len(natural) != 9 or deterministic != [{
        "domain": "retail", "task_id": "24", "communicate_items": 2,
        "nl_assertions": 1, "reward_basis": ["DB", "NL_ASSERTION"],
    }]:
        raise ValueError("pure candidate census drift")
    oracle = qualification.get("oracle", {})
    if qualification.get("schema_version") != "pysolate.tau2-pure-oracle.v1":
        raise ValueError("qualification schema mismatch")
    if any(oracle.get(key) != expected for key, expected in {
        "correct_control": 1.0, "empty_control": 0.0, "wrong_control": 0.0,
        "official_upstream_component": True, "official_task_overall": False,
    }.items()):
        raise ValueError("oracle controls incomplete")
    if direct.get("schema_version") != "pysolate.tau2-pure-paired-private.v1" or direct.get("source_revision") != EXPECTED_REVISION:
        raise ValueError("direct evidence identity mismatch")
    if direct.get("lane") != "direct" or direct.get("model") != "deepseek/deepseek-v4-pro" or direct.get("seed") != 42 or direct.get("temperature") != 0.0:
        raise ValueError("direct configuration mismatch")
    if direct.get("evaluation_type") != "COMMUNICATE" or direct.get("status") != "completed" or direct.get("tool_calls") != 2:
        raise ValueError("direct disqualification evidence mismatch")
    simulation = direct.get("simulation") or {}
    if (simulation.get("reward_info") or {}).get("reward") != 0.0:
        raise ValueError("direct component reward mismatch")
    if authored.get("schema_version") != "pysolate.tau2-pure-turn-private.v1":
        raise ValueError("authored Guest evidence identity mismatch")
    for field in ("semantic_call_sites", "broker_call_count", "receipt_count"):
        if authored.get(field) != 0:
            raise ValueError("authored Guest was not authority-free")
    for field in ("artifact_sha256", "capability_plan_sha256", "source_sha256", "request_sha256", "response_sha256"):
        if not isinstance(authored.get(field), str) or not authored[field].startswith("sha256:"):
            raise ValueError("authored Guest identity incomplete")
    return {
        "schema_version": REPORT_SCHEMA,
        "source": {"repository": "sierra-research/tau2-bench", "revision": EXPECTED_REVISION, "version": "1.0.1"},
        "selection_contract": {
            "reference_actions": 0,
            "environment_assertions": 0,
            "requires_no_op_sensitive_deterministic_official_component": True,
            "requires_natural_direct_tool_calls": 0,
        },
        "census": {
            "no_action_no_env_assertion_tasks": len(rows),
            "natural_domain_tasks": len(natural),
            "no_op_sensitive_deterministic_component_candidates": len(deterministic),
            "candidate": {"domain": "retail", "task_id": "24"},
        },
        "oracle_qualification": {
            "implementation": "tau2 CommunicateEvaluator",
            "correct_control": 1.0,
            "empty_control": 0.0,
            "wrong_control": 0.0,
            "official_upstream_component": True,
            "official_task_overall": False,
        },
        "natural_direct_probe": {
            "model": "deepseek/deepseek-v4-pro",
            "seed": 42,
            "temperature": 0.0,
            "evaluation_type": "COMMUNICATE",
            "component_reward": 0.0,
            "tool_calls": 2,
            "qualification": "disqualified_not_no_tool",
            "treatment_run": False,
        },
        "zero_authority_runtime_control": {
            "authored_source": True,
            "real_wasm_guest": True,
            "empty_capability_plan": True,
            "semantic_call_sites": 0,
            "broker_call_count": 0,
            "receipt_count": 0,
            "classification": "SUPPORTED_MECHANISM_ONLY",
        },
        "classification": "NO_ELIGIBLE_PURE_NATURAL_TASK",
        "claim_boundary": {
            "supports": [
                "one zero-capability authored Python program through the real Guest",
                "fail-closed natural-task selection against a no-op-sensitive upstream evaluator component",
            ],
            "does_not_support": [
                "a successful natural pure-task treatment",
                "official task overall reward for retail/24",
                "leaderboard comparison",
                "a claim that tau2 contains no pure tasks under other selection contracts",
            ],
        },
        "raw_task_bodies_included": False,
        "raw_model_bodies_included": False,
        "raw_guest_bodies_included": False,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--qualification", required=True)
    parser.add_argument("--direct", required=True)
    parser.add_argument("--authored-guest", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    report = validate(
        census(pathlib.Path(args.source_root).resolve()),
        read_json(pathlib.Path(args.qualification)),
        read_json(pathlib.Path(args.direct)),
        read_json(pathlib.Path(args.authored_guest)),
    )
    body = (json.dumps(report, indent=2, sort_keys=True) + "\n").encode()
    pathlib.Path(args.output).write_bytes(body)
    print(digest_bytes(body))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
