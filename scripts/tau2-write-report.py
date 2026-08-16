#!/usr/bin/env python3
"""Validate private airline/11 WRITE evidence and emit a body-safe report."""

import argparse
import hashlib
import json
import pathlib
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REPORT_SCHEMA = "pysolate.tau2-private-write-canary.v1"
LANES = {"accepted", "rejected", "expired", "failure"}
EXPECTED = {
    "accepted": {"calls": 1, "outcome": "ok", "approval": "approved", "approval_outcome": "ok", "executed": True, "changed": True, "disposition": "published"},
    "rejected": {"calls": 0, "outcome": "denied", "approval": "rejected", "approval_outcome": None, "executed": False, "changed": False, "disposition": "discarded"},
    "expired": {"calls": 0, "outcome": "denied", "approval": "expired", "approval_outcome": None, "executed": False, "changed": False, "disposition": "discarded"},
    "failure": {"calls": 1, "outcome": "error", "approval": "approved", "approval_outcome": "error", "executed": True, "changed": False, "disposition": "discarded"},
}


def digest_json(value: Any) -> str:
    body = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return "sha256:" + hashlib.sha256(body).hexdigest()


def same_digest(actual: Any, expected: str) -> bool:
    if not isinstance(actual, str):
        return False
    return actual.removeprefix("sha256:") == expected.removeprefix("sha256:")


def validate(evidence: Dict[str, Any], oracle_request: Dict[str, Any], oracle: Dict[str, Any]) -> Dict[str, Any]:
    if evidence.get("schema_version") != "pysolate.tau2-private-write-evidence.v1":
        raise ValueError("invalid private evidence schema")
    source = evidence.get("source", {})
    if source.get("revision") != EXPECTED_REVISION or source.get("domain") != "airline" or source.get("task_id") != "11":
        raise ValueError("private evidence source mismatch")
    lanes = evidence.get("lanes")
    if not isinstance(lanes, list) or {lane.get("name") for lane in lanes} != LANES:
        raise ValueError("exact four-lane evidence required")
    operation_sha = digest_json(evidence.get("arguments"))
    if evidence.get("grant_operation_sha256") != operation_sha:
        raise ValueError("Grant operation identity mismatch")
    by_name = {lane["name"]: lane for lane in lanes}
    for name, expected in EXPECTED.items():
        lane = by_name[name]
        receipt = lane.get("receipt", {})
        approvals = lane.get("approval")
        if not isinstance(approvals, list) or len(approvals) != 1:
            raise ValueError(f"{name} approval evidence missing")
        approval = approvals[0]
        changed = lane.get("initial_state_sha256") != lane.get("final_state_sha256")
        observed = {
            "calls": lane.get("handler_calls"), "outcome": receipt.get("outcome"),
            "approval": approval.get("status"), "approval_outcome": approval.get("dispatch_outcome"), "executed": approval.get("executed"),
            "changed": changed, "disposition": lane.get("disposition"),
        }
        if observed != expected:
            raise ValueError(f"{name} lane contract mismatch")
        source_binding = receipt.get("source", {})
        required_source = {
            "schema_version", "claim_level", "source_sha256", "occurrence_id", "dynamic_occurrence",
            "start_line", "start_column", "end_line", "end_column",
        }
        if not required_source.issubset(source_binding) or source_binding.get("schema_version") != "pysolate.source-binding.v0" or source_binding.get("claim_level") != "source_bound":
            raise ValueError(f"{name} source binding incomplete")
        if not same_digest(receipt.get("request_sha256"), digest_json({})):
            raise ValueError(f"{name} receipt request identity mismatch")
        if not isinstance(receipt.get("capability_plan_sha256"), str):
            raise ValueError(f"{name} plan identity missing")
        if approval.get("plan_sha256") != receipt.get("capability_plan_sha256") or approval.get("call_id") != receipt.get("call_id") or approval.get("parent_call_id") != receipt.get("parent_call_id"):
            raise ValueError(f"{name} approval-to-receipt identity mismatch")
    if by_name["accepted"]["plan_sha256"] != by_name["rejected"]["plan_sha256"]:
        raise ValueError("accepted and rejected controls must share one Plan")
    accepted = by_name["accepted"]
    expected_response = {
        "content": evidence.get("accepted_tool_content"),
        "operation_sha256": operation_sha,
        "state_sha256": accepted.get("final_state_sha256"),
    }
    if not same_digest(accepted["receipt"].get("response_sha256"), digest_json(expected_response)):
        raise ValueError("accepted receipt response does not join final state")
    if oracle_request.get("call", {}).get("arguments") != evidence.get("arguments") or oracle_request.get("call", {}).get("content") != evidence.get("accepted_tool_content"):
        raise ValueError("oracle request does not join accepted operation")
    if digest_json(oracle_request.get("observed_final_state")) != accepted.get("final_state_sha256"):
        raise ValueError("oracle observed state does not join accepted workspace state")
    if oracle.get("schema_version") != "pysolate.tau2-write-oracle.v1" or any(oracle.get(key) != 1.0 for key in ("db_reward", "communicate_reward", "overall_reward")):
        raise ValueError("official oracle did not pass")
    if not oracle.get("db_match") or not oracle.get("communicate_met") or not oracle.get("observed_final_state_match"):
        raise ValueError("official oracle checks incomplete")
    return {
        "schema_version": REPORT_SCHEMA,
        "classification": "SUPPORTED_BENCHMARK_PRIVATE_ONLY",
        "source": {
            "repository": "https://github.com/sierra-research/tau2-bench",
            "revision": EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
        },
        "scope": {
            "authored_reference_program": True,
            "model_generated": False,
            "benchmark_private_workspace": True,
            "external_write": False,
            "leaderboard_comparable": False,
            "performance_comparison_supported": False,
        },
        "operation": {
            "capability": accepted["receipt"]["capability"],
            "effect_class": "workspace_write",
            "operation_sha256": operation_sha,
            "arguments_included": False,
            "tool_result_included": False,
        },
        "lanes": {
            name: {
                "approval_status": EXPECTED[name]["approval"],
                "approval_executed": EXPECTED[name]["executed"],
                "approval_dispatch_outcome": EXPECTED[name]["approval_outcome"],
                "handler_calls": EXPECTED[name]["calls"],
                "receipt_outcome": EXPECTED[name]["outcome"],
                "state_changed": EXPECTED[name]["changed"],
                "workspace_disposition": EXPECTED[name]["disposition"],
                "source_bound": True,
            }
            for name in sorted(LANES)
        },
        "causal_join": {
            "source_to_plan": True,
            "plan_to_approval": True,
            "approval_to_dispatch": True,
            "dispatch_to_operation_digest": True,
            "operation_to_final_state": True,
            "final_state_to_receipt_response": True,
            "accepted_call_to_official_oracle": True,
            "accepted_workspace_state_to_official_gold": True,
        },
        "oracle": {
            "implementation": "tau2 EnvironmentEvaluator+CommunicateEvaluator",
            "reward_basis": ["DB", "COMMUNICATE"],
            "db_reward": 1.0,
            "communicate_reward": 1.0,
            "overall_reward": 1.0,
            "db_match": True,
            "communicate_met": True,
            "observed_final_state_match": True,
            "observed_final_state_included": False,
        },
        "claim_boundary": {
            "supports": "one exact task-private authority-gated mutation through real Guest and Broker",
            "does_not_support": ["model WRITE ability", "production external WRITE", "generic rollback", "leaderboard score"],
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence", required=True)
    parser.add_argument("--oracle-request", required=True)
    parser.add_argument("--oracle-report", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    evidence = json.loads(pathlib.Path(args.evidence).read_text())
    oracle_request = json.loads(pathlib.Path(args.oracle_request).read_text())
    oracle = json.loads(pathlib.Path(args.oracle_report).read_text())
    body = json.dumps(validate(evidence, oracle_request, oracle), sort_keys=True, separators=(",", ":")) + "\n"
    pathlib.Path(args.output).write_text(body)
    print(hashlib.sha256(body.encode()).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
