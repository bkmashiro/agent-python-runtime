#!/usr/bin/env python3
"""Validate private DeepSeek paired-canary evidence and emit a body-safe report."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
MODEL = "deepseek/deepseek-chat"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def build_report(direct: Dict[str, Any], treatment: Dict[str, Any]) -> Dict[str, Any]:
    for row, lane in ((direct, "direct"), (treatment, "treatment")):
        if row.get("schema_version") != "pysolate.tau2-paired-private.v1" or row.get("source_revision") != REVISION:
            raise ValueError("invalid private row source contract")
        if row.get("lane") != lane or row.get("model") != MODEL or row.get("seed") != 42 or row.get("temperature") != 0.0:
            raise ValueError("paired configuration mismatch")

    simulation = direct.get("simulation")
    if not isinstance(simulation, dict):
        raise ValueError("direct official simulation missing")
    reward = simulation.get("reward_info") or {}
    messages = simulation.get("messages") or []
    if simulation.get("termination_reason") != "agent_error" or reward.get("reward") != 0.0 or not messages:
        raise ValueError("unexpected direct outcome")
    last = messages[-1]
    direct_calls = last.get("tool_calls") or []
    if last.get("role") != "assistant" or not last.get("content") or not direct_calls:
        raise ValueError("direct protocol violation not evidenced")
    call_names = [call.get("name") for call in direct_calls]
    if any(not isinstance(name, str) or not name for name in call_names):
        raise ValueError("invalid direct tool identity")

    if treatment.get("status") != "unscorable" or treatment.get("failure_stage") != "orchestrator_protocol_validation":
        raise ValueError("unexpected treatment status")
    if treatment.get("official_simulation_written") is not False or treatment.get("official_reward") is not None:
        raise ValueError("treatment must not imply an official score")
    if treatment.get("pysolate_model_turns_started") != 0 or treatment.get("pysolate_physical_calls") != 0:
        raise ValueError("treatment runtime must not be claimed as started")
    logs = treatment.get("logs")
    if not isinstance(logs, list) or len(logs) != 3 or any(not DIGEST.fullmatch(str(item.get("sha256", ""))) for item in logs):
        raise ValueError("invalid treatment attempt evidence")

    report = {
        "schema_version": "pysolate.tau2-paired-canary.v1",
        "source": {"repository": "https://github.com/sierra-research/tau2-bench", "revision": REVISION, "domain": "airline", "task_id": "3"},
        "configuration": {"agent_model": MODEL, "user_model": MODEL, "seed": 42, "temperature": 0.0, "trial_index": 1},
        "conclusion": "PAIR_NOT_COMPARABLE",
        "direct": {
            "official_simulation": True,
            "official_reward": 0.0,
            "termination_reason": "agent_error",
            "failure_class": "assistant_content_and_tool_call",
            "last_tool_call_names": call_names,
        },
        "treatment": {
            "official_simulation": False,
            "official_reward": "not_recorded",
            "status": "unscorable",
            "failure_class": "model_action_contract_then_orchestrator_protocol_validation",
            "setup_attempts": 2,
            "scoring_attempts": 1,
            "pysolate_model_turns_started": 0,
            "pysolate_physical_calls": 0,
        },
        "interpretation": {
            "model_runtime_comparison_supported": False,
            "reason": "Neither lane produced a successful comparable task completion; the treatment model output never reached Pysolate admission.",
            "direct_failure_attributed_to_pysolate": False,
            "treatment_failure_attributed_to_pysolate": False,
        },
        "boundaries": {"task_or_prompt_bodies_included": False, "raw_model_responses_included": False, "private_paths_included": False, "leaderboard_comparable": False},
    }
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":")).encode()
    report["identity"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--direct", required=True)
    parser.add_argument("--treatment", required=True)
    parser.add_argument("--evidence-root", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    root = pathlib.Path(args.evidence_root).resolve()
    direct = json.loads(pathlib.Path(args.direct).read_text())
    treatment = json.loads(pathlib.Path(args.treatment).read_text())
    for item in treatment.get("logs", []):
        path = (root / item["name"]).resolve()
        if path.parent != root or "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest() != item["sha256"]:
            raise ValueError("private log identity mismatch")
    report = build_report(direct, treatment)
    raw = (json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n").encode()
    pathlib.Path(args.output).write_bytes(raw)
    print(hashlib.sha256(raw).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
