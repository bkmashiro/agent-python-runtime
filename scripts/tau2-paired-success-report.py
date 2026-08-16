#!/usr/bin/env python3
"""Emit a body-safe report for the successful DeepSeek V4 Pro paired canary."""

import argparse
import hashlib
import json
import pathlib
import re
from collections import Counter
from typing import Any, Dict

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
MODEL = "deepseek/deepseek-v4-pro"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def response_digest(content: str) -> str:
    raw = json.dumps({"content": content}, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def _validate_row(row: Dict[str, Any], lane: str) -> dict:
    if row.get("schema_version") != "pysolate.tau2-paired-private.v1" or row.get("source_revision") != REVISION:
        raise ValueError("invalid private source contract")
    if row.get("lane") != lane or row.get("model") != MODEL or row.get("seed") != 42 or row.get("temperature") != 0.0 or row.get("status") != "completed":
        raise ValueError("paired configuration mismatch")
    simulation = row.get("simulation")
    reward = (simulation or {}).get("reward_info") or {}
    if not isinstance(simulation, dict) or simulation.get("termination_reason") != "user_stop":
        raise ValueError("simulation did not terminate successfully")
    if reward.get("reward") != 1.0 or reward.get("reward_breakdown") != {"COMMUNICATE": 1.0, "DB": 1.0}:
        raise ValueError("official oracle did not pass exactly")
    return simulation


def build_report(direct: Dict[str, Any], treatment: Dict[str, Any]) -> Dict[str, Any]:
    direct_sim = _validate_row(direct, "direct")
    treatment_sim = _validate_row(treatment, "treatment")
    direct_tools = [call.get("name") for message in direct_sim.get("messages", []) for call in (message.get("tool_calls") or [])]
    if direct_tools != ["get_reservation_details", "get_user_details", "transfer_to_human_agents"]:
        raise ValueError("unexpected direct action sequence")
    if any(message.get("content") and message.get("tool_calls") for message in direct_sim.get("messages", [])):
        raise ValueError("direct protocol violation")

    events = treatment.get("pysolate_events")
    if not isinstance(events, list) or [event.get("kind") for event in events] != ["answer", "program", "program", "answer", "answer", "invalid_model_action"]:
        raise ValueError("unexpected treatment action sequence")
    turns = [event["turn"] for event in events if isinstance(event.get("turn"), dict)]
    capabilities = [event.get("capability") for event in events if event.get("capability")]
    if capabilities != ["tau2.airline.get_reservation_details", "tau2.airline.get_user_details"] or len(turns) != 2:
        raise ValueError("unexpected treatment tool sequence")
    occurrences = []
    for turn in turns:
        receipt = turn.get("receipt") or {}
        source = receipt.get("source") or {}
        if receipt.get("outcome") != "ok" or source.get("claim_level") != "source_bound":
            raise ValueError("incomplete receipt join")
        if not DIGEST.fullmatch(str(source.get("occurrence_id", ""))) or response_digest(turn.get("content", "")) != receipt.get("response_sha256"):
            raise ValueError("receipt identity mismatch")
        occurrences.append(source["occurrence_id"])
    if len(set(occurrences)) != 2:
        raise ValueError("source occurrences are not distinct")
    if any(message.get("tool_calls") for message in treatment_sim.get("messages", [])):
        raise ValueError("treatment leaked direct tool actions")

    kinds = Counter(event["kind"] for event in events)
    report = {
        "schema_version": "pysolate.tau2-paired-success.v1",
        "source": {"repository": "https://github.com/sierra-research/tau2-bench", "revision": REVISION, "domain": "airline", "task_id": "3"},
        "configuration": {"agent_model": MODEL, "user_model": MODEL, "seed": 42, "temperature": 0.0, "trial_index": 1},
        "conclusion": "PAIRED_CANARY_SUPPORTED",
        "direct": {
            "official_reward": 1.0, "reward_breakdown": {"DB": 1.0, "COMMUNICATE": 1.0},
            "termination_reason": "user_stop", "messages": len(direct_sim["messages"]),
            "tool_actions": direct_tools, "content_plus_tool_violations": 0,
        },
        "treatment": {
            "official_reward": 1.0, "reward_breakdown": {"DB": 1.0, "COMMUNICATE": 1.0},
            "termination_reason": "user_stop", "messages": len(treatment_sim["messages"]),
            "model_actions": len(events), "action_kinds": dict(sorted(kinds.items())),
            "pysolate_logical_calls": 2, "pysolate_physical_calls": 2,
            "capabilities": capabilities, "receipt_outcomes": ["ok", "ok"],
            "distinct_source_occurrences": 2, "response_identity_matches": 2,
            "official_environment_tool_actions": 0,
        },
        "causal_join": {
            "agent_source": "private_digest_only", "capability_plan": "recorded",
            "source_occurrences": "recorded_distinct", "logical_operations": 2,
            "physical_operations": 2, "terminal_receipts": 2, "workspace": "not_applicable_read_only",
        },
        "interpretation": {
            "matched_task_success": True,
            "tool_surface_matched": False,
            "runtime_correctness_canary_supported": True,
            "performance_comparison_supported": False,
            "leaderboard_comparable": False,
            "notes": "N=1 read-only adapter canary; direct retained the full upstream tool surface while treatment exposed only the two preregistered READ adapters, so no fairness or performance claim is supported.",
        },
        "boundaries": {"write_effect_tested": False, "direct_tool_surface": "full_upstream", "treatment_tool_surface": "two_preregistered_read_adapters", "task_or_prompt_bodies_included": False, "raw_model_responses_included": False, "private_paths_included": False},
    }
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":")).encode()
    report["identity"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--direct", required=True)
    parser.add_argument("--treatment", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    report = build_report(json.loads(pathlib.Path(args.direct).read_text()), json.loads(pathlib.Path(args.treatment).read_text()))
    raw = (json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n").encode()
    pathlib.Path(args.output).write_bytes(raw)
    print(hashlib.sha256(raw).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
