#!/usr/bin/env python3
"""Validate private tau2 canary evidence and emit a body-safe public report."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict


REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REPOSITORY = "https://github.com/sierra-research/tau2-bench"
REPORT_SCHEMA = "pysolate.tau2-canary.v1"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
RAW_DIGEST = re.compile(r"^[0-9a-f]{64}$")


def response_digest(content: str) -> str:
    raw = json.dumps({"content": content}, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def _require_digest(value: Any) -> str:
    if not isinstance(value, str) or not DIGEST.fullmatch(value):
        raise ValueError("invalid digest")
    return value


def build_report(evidence: Dict[str, Any], oracle: Dict[str, Any], oracle_request: Dict[str, Any]):
    if evidence.get("schema_version") != "pysolate.tau2-canary-private-evidence.v1":
        raise ValueError("invalid evidence schema")
    source = evidence.get("source")
    if source != {"revision": REVISION, "domain": "airline", "task_id": "3"}:
        raise ValueError("evidence source mismatch")
    for key in ("artifact_sha256", "request_sha256", "guest_response_sha256", "capability_plan_sha256"):
        _require_digest(evidence.get(key))
    receipts = evidence.get("receipts")
    if evidence.get("broker_call_count") != 2 or not isinstance(receipts, list) or len(receipts) != 2:
        raise ValueError("invalid Broker evidence")
    if any(receipt.get("outcome") != "ok" or not RAW_DIGEST.fullmatch(str(receipt.get("response_sha256", ""))) for receipt in receipts):
        raise ValueError("invalid receipt outcome")
    if evidence.get("result") != {"answer": "4", "cabin": "economy", "membership": "silver", "passenger_count": 2}:
        raise ValueError("unexpected Guest result")
    source_occurrence = evidence.get("source_occurrence_claim")
    if source_occurrence not in {"recorded", "not_recorded"}:
        raise ValueError("invalid source occurrence state")
    fresh_runs = evidence.get("fresh_runs", 1)
    tool_runs = evidence.get("tool_runs", 1)
    if source_occurrence == "recorded":
        if fresh_runs != 3 or tool_runs != 2:
            raise ValueError("invalid fresh-turn evidence")
        bindings = [receipt.get("source") for receipt in receipts]
        if any(
            not isinstance(binding, dict)
            or binding.get("schema_version") != "pysolate.source-binding.v0"
            or binding.get("claim_level") != "source_bound"
            or not DIGEST.fullmatch(str(binding.get("source_sha256", "")))
            or not DIGEST.fullmatch(str(binding.get("occurrence_id", "")))
            or binding.get("dynamic_occurrence") != 1
            or not isinstance(binding.get("start_line"), int)
            or binding.get("start_line", 0) < 1
            or not isinstance(binding.get("end_line"), int)
            or binding.get("end_line", 0) < binding.get("start_line", 0)
            for binding in bindings
        ) or len({binding["occurrence_id"] for binding in bindings}) != 2:
            raise ValueError("invalid source occurrence evidence")

    expected_oracle = {
        "schema_version": "pysolate.tau2-canary-oracle.v1",
        "source": {"repository": REPOSITORY, "revision": REVISION, "domain": "airline", "task_id": "3"},
        "reward_basis": ["DB", "COMMUNICATE"],
        "db_reward": 1.0,
        "communicate_reward": 1.0,
        "overall_reward": 1.0,
        "db_match": True,
        "communicate_met": True,
        "tool_calls": 2,
        "tool_bodies_included": False,
        "assistant_text_included": False,
    }
    if oracle != expected_oracle:
        raise ValueError("official oracle did not pass exactly")
    calls = oracle_request.get("calls") if isinstance(oracle_request, dict) else None
    if not isinstance(calls, list) or len(calls) != 2 or any(not isinstance(call.get("content"), str) for call in calls):
        raise ValueError("invalid private oracle request")
    computed = [response_digest(call["content"]) for call in calls]
    observed = [receipt["response_sha256"] for receipt in receipts]
    if computed != observed:
        raise ValueError("oracle tool results do not match Guest Broker responses")

    report = {
        "schema_version": REPORT_SCHEMA,
        "source": {"repository": REPOSITORY, "revision": REVISION, "domain": "airline", "task_id": "3"},
        "lane": "authored_reference_fresh_turns" if source_occurrence == "recorded" else "authored_reference_program",
        "conclusion": "SUPPORTED_WITH_RECORDED_GAP" if source_occurrence == "not_recorded" else "SUPPORTED",
        "runtime": {
            "real_wasm_guest": True,
            "program_surface": "programmatic",
            "effect_class": "external_read",
            "playback": "live_only",
            "artifact_sha256": evidence["artifact_sha256"],
            "capability_plan_sha256": evidence["capability_plan_sha256"],
            "broker_calls": 2,
            "fresh_runs": fresh_runs,
            "tool_runs": tool_runs,
            "receipt_outcomes": ["ok", "ok"],
        },
        "oracle": {
            "implementation": "tau2 EnvironmentEvaluator+CommunicateEvaluator",
            "reward_basis": ["DB", "COMMUNICATE"],
            "db_reward": 1.0,
            "communicate_reward": 1.0,
            "overall_reward": 1.0,
            "tool_result_identity_match": True,
        },
        "result": {"answer": "4", "passenger_count": 2, "membership": "silver", "cabin": "economy"},
        "causal_join": {
            "agent_source": "private_digest_only",
            "capability_plan": "recorded",
            "logical_operations": 2,
            "physical_operations": 2,
            "terminal_receipts": 2,
            "source_occurrence": source_occurrence,
            "workspace": "not_applicable_read_only",
        },
        "boundaries": {
            "natural_model_run": False,
            "write_effect_tested": False,
            "task_or_tool_bodies_included": False,
            "private_paths_included": False,
        },
    }
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":")).encode()
    report["identity"] = "sha256:" + hashlib.sha256(canonical).hexdigest()
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence-manifest", required=True)
    parser.add_argument("--oracle-report", required=True)
    parser.add_argument("--oracle-request", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    evidence = json.loads(pathlib.Path(args.evidence_manifest).read_text())
    oracle = json.loads(pathlib.Path(args.oracle_report).read_text())
    oracle_request = json.loads(pathlib.Path(args.oracle_request).read_text())
    report = build_report(evidence, oracle, oracle_request)
    raw = (json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n").encode()
    pathlib.Path(args.output).write_bytes(raw)
    print(hashlib.sha256(raw).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
