#!/usr/bin/env python3
"""Strict body-safe aggregate builder for the frozen tau2 T2 cohort."""

import argparse
import hashlib
import json
import pathlib
import re
from collections import Counter
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
MODEL = "deepseek/deepseek-v4-pro"
PUBLIC_SCHEMA = "pysolate.tau2-t2-preregistration.v1"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
CELL_SCHEMA = "pysolate.tau2-t2-cell-private.v1"
REPORT_SCHEMA = "pysolate.tau2-t2-cohort.v1"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
TERMINAL = {"completed", "agent_error", "failed", "rejected", "unclassifiable", "unscorable", "not_recorded"}


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def sha(value: bytes, prefix: bool = True) -> str:
    digest = hashlib.sha256(value).hexdigest()
    return "sha256:" + digest if prefix else digest


def require_exact(value: Any, keys: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != keys:
        raise ValueError(f"{label} fields mismatch")
    return value


def receipt_id(receipt: dict[str, Any]) -> str:
    source = receipt["source"]
    fields = [
        "pysolate-receipt-v3", receipt["run_id"], receipt["capability_plan_sha256"], receipt["call_id"],
        receipt.get("parent_call_id", ""), receipt.get("approval_request_id", ""), receipt["capability"],
        str(receipt["operation_index"]), receipt["request_sha256"], source["schema_version"], source["claim_level"],
        source["document_id"], source["source_sha256"], source["occurrence_id"], source["capability"],
        str(source["dynamic_occurrence"]), str(source["start_line"]), str(source["start_column"]),
        str(source["end_line"]), str(source["end_column"]),
    ]
    digest = hashlib.sha256()
    for field in fields:
        digest.update(str(field).encode())
        digest.update(b"\0")
    return "rcpt_" + digest.hexdigest()


def grant_identity(policy: dict[str, Any]) -> str:
    document = {"schema_version": "pysolate.capability-grant.v1", "policy": json.loads(canonical(policy))}
    return sha(json.dumps(document, separators=(",", ":")).encode())


def validate_prereg(public: dict[str, Any], private: dict[str, Any]) -> dict[str, list[dict[str, Any]]]:
    if public.get("schema_version") != PUBLIC_SCHEMA or private.get("schema_version") != PRIVATE_SCHEMA:
        raise ValueError("preregistration schema mismatch")
    if public.get("source", {}).get("revision") != REVISION or private.get("source", {}).get("revision") != REVISION:
        raise ValueError("preregistration source mismatch")
    public_identity = public.get("identity")
    projected = dict(public)
    projected.pop("identity", None)
    if public_identity != sha(canonical(projected)):
        raise ValueError("public preregistration identity mismatch")
    public_ids = [item["task_id"] for item in public.get("tasks", [])]
    private_ids = [item["task_id"] for item in private.get("tasks", [])]
    if public_ids != private_ids or len(public_ids) != 16 or len(set(public_ids)) != 16:
        raise ValueError("frozen task denominator mismatch")
    if public.get("protocol") != private.get("protocol") or public["protocol"].get("model") != MODEL:
        raise ValueError("public/private protocol mismatch")
    if private.get("public_identity") != public_identity:
        raise ValueError("private preregistration is not bound to public identity")
    by_task = {}
    for public_task, private_task in zip(public["tasks"], private["tasks"]):
        body = private_task.get("task_body")
        actions = private_task.get("reference_actions")
        if sha(canonical(body)) != public_task.get("task_sha256") or sha(canonical(actions)) != public_task.get("reference_actions_sha256"):
            raise ValueError("private task body does not match frozen digest")
        if len(actions) != public_task.get("reference_action_count"):
            raise ValueError("reference action count mismatch")
        by_task[public_task["task_id"]] = actions
    return by_task


def validate_turn(event: dict[str, Any], root: pathlib.Path, task_id: str, allowed: list[dict[str, Any]]) -> None:
    if event.get("kind") != "program" or event.get("tool") not in {item["name"] for item in allowed}:
        raise ValueError("treatment event is not a frozen READ")
    if not any(item["name"] == event["tool"] and item["arguments"] == event.get("arguments") for item in allowed):
        raise ValueError("treatment event arguments outside frozen scope")
    source = event.get("source")
    turn = event.get("turn")
    if not isinstance(source, str) or not isinstance(turn, dict) or turn.get("task_id") != task_id:
        raise ValueError("treatment source/turn identity missing")
    raw = require_exact(turn.get("raw_bodies"), {"guest_request", "guest_response"}, "raw body references")
    request_body = (root / raw["guest_request"]).read_bytes()
    response_body = (root / raw["guest_response"]).read_bytes()
    if sha(request_body) != turn.get("request_sha256") or sha(response_body) != turn.get("response_sha256"):
        raise ValueError("Guest body digest mismatch")
    plan = turn.get("plan_document")
    if not isinstance(plan, dict) or sha(json.dumps(plan, separators=(",", ":")).encode()) != turn.get("capability_plan_sha256"):
        raise ValueError("Plan identity mismatch")
    if plan.get("schema_version") != "pysolate.capability-plan.v6" or plan.get("max_calls") != 1 or len(plan.get("capabilities", [])) != 1 or len(plan.get("grants", [])) != 1:
        raise ValueError("Plan shape mismatch")
    capability_name = "tau2.airline." + event["tool"]
    spec = plan["capabilities"][0]
    if spec.get("capability") != capability_name or spec.get("effect_class") != "external_read" or spec.get("playback") != "live_only":
        raise ValueError("Plan capability semantics mismatch")
    if spec.get("handler_identity") != "pysolate.tau2.airline.t2-read.handler." + REVISION:
        raise ValueError("Plan handler identity mismatch")
    projection = spec.get("python")
    if not isinstance(projection, dict) or projection.get("module") != "tools" or projection.get("method") != event["tool"] or projection.get("result_field") != "content":
        raise ValueError("Plan Python projection mismatch")
    schema = spec.get("input_schema")
    arguments = event["arguments"]
    if not isinstance(schema, dict) or schema.get("additionalProperties") is not False or set(schema.get("required", [])) != set(arguments):
        raise ValueError("Plan input schema mismatch")
    for key, value in arguments.items():
        if schema.get("properties", {}).get(key) != {"const": value, "type": "string"}:
            raise ValueError("Plan input const mismatch")
    policy = turn.get("grant_policy")
    expected_policy = {
        "benchmark": "tau2-t2", "domain": "airline", "effect": "external_read", "source_revision": REVISION,
        "task_id": task_id, "tool": event["tool"], "arguments_sha256": sha(canonical(arguments)),
    }
    if not isinstance(policy, dict) or policy != expected_policy or plan["grants"][0] != {"capability": capability_name, "policy_sha256": grant_identity(policy)}:
        raise ValueError("Grant identity mismatch")
    response = json.loads(response_body)
    receipt = turn.get("receipt")
    if not isinstance(response, dict) or not isinstance(receipt, dict):
        raise ValueError("Guest response/receipt type mismatch")
    if response.get("status") != "ok" or response.get("capability_plan_sha256") != turn["capability_plan_sha256"] or response.get("receipts") != [receipt]:
        raise ValueError("Guest response/receipt join mismatch")
    if receipt.get("outcome") != "ok" or receipt.get("capability") != capability_name or receipt.get("capability_plan_sha256") != turn["capability_plan_sha256"]:
        raise ValueError("receipt authority mismatch")
    if receipt.get("request_sha256") != sha(canonical(arguments), prefix=False) or receipt.get("response_sha256") != sha(canonical({"content": turn.get("content")}), prefix=False):
        raise ValueError("receipt body identity mismatch")
    source_evidence = receipt.get("source")
    if not isinstance(source_evidence, dict):
        raise ValueError("receipt source evidence missing")
    source_sha = sha(source.encode())
    start_column = source.index("tools.")
    document_id = sha(("pysolate.source-document.v0\0python\0" + source_sha).encode())
    occurrence_doc = "\x00".join(["pysolate.semantic-call-site.v0", source_sha, capability_name, f"1:{start_column}:1:{len(source)}"])
    expected_source = {
        "schema_version": "pysolate.source-binding.v0", "claim_level": "source_bound",
        "occurrence_id": sha(occurrence_doc.encode()), "document_id": document_id, "source_sha256": source_sha,
        "start_line": 1, "start_column": start_column, "end_line": 1, "end_column": len(source),
        "capability": capability_name, "dynamic_occurrence": 1,
    }
    if source_evidence != expected_source or receipt.get("receipt_id") != receipt_id(receipt):
        raise ValueError("source-bound receipt identity mismatch")
    request = json.loads(request_body)
    if request.get("run_id") != receipt.get("run_id") or request.get("code", "").find(source) < 0:
        raise ValueError("Guest request/source receipt join mismatch")


def direct_tool_calls(messages: list[Any]) -> int:
    return sum(len(message.get("tool_calls") or []) for message in messages if isinstance(message, dict))


def validate_cell(cell: dict[str, Any], task_id: str, lane: str, actions: list[dict[str, Any]], root: pathlib.Path, protocol: dict[str, Any]) -> dict[str, Any]:
    base = {"schema_version", "source_revision", "task_id", "lane", "model", "seed", "temperature", "status", "provider_calls", "simulation", "official_action_diagnostic", "pysolate_events"}
    if cell.get("status") == "unscorable":
        base.add("failure")
    require_exact(cell, base, "cell")
    if cell["schema_version"] != CELL_SCHEMA or cell["source_revision"] != REVISION or cell["task_id"] != task_id or cell["lane"] != lane or cell["model"] != MODEL or cell["seed"] != protocol["seed"] or cell["temperature"] != 0.0:
        raise ValueError("cell frozen identity mismatch")
    if cell["status"] not in TERMINAL or not isinstance(cell["provider_calls"], int) or not 0 <= cell["provider_calls"] <= protocol["max_total_provider_invocations_per_trial"]:
        raise ValueError("cell terminal status/budget mismatch")
    if cell["status"] != "completed":
        if cell["simulation"] is not None or cell["official_action_diagnostic"] is not None:
            raise ValueError("unscorable cell contains fabricated score")
        return {"status": cell["status"], "default_reward": None, "action_reward": None, "provider_calls": cell["provider_calls"], "messages": None, "termination_reason": None, "logical_calls": 0, "physical_calls": 0, "source_joins": 0}
    simulation = cell["simulation"]
    diagnostic = cell["official_action_diagnostic"]
    if not isinstance(simulation, dict) or simulation.get("task_id") != task_id or not isinstance(diagnostic, dict):
        raise ValueError("completed cell lacks official SimulationRun/diagnostic")
    reward = simulation.get("reward_info", {}).get("reward")
    action_reward = diagnostic.get("reward")
    if not isinstance(reward, (int, float)) or not isinstance(action_reward, (int, float)):
        raise ValueError("completed cell reward missing")
    events = cell["pysolate_events"]
    if lane == "direct":
        if events is not None:
            raise ValueError("direct cell contains treatment events")
        logical = direct_tool_calls(simulation.get("messages", []))
        physical = logical
        joins = 0
    else:
        if not isinstance(events, list) or len(events) > protocol["max_agent_model_invocations_per_trial"]:
            raise ValueError("treatment event budget mismatch")
        program_events = [event for event in events if event.get("kind") == "program"]
        for event in program_events:
            validate_turn(event, root, task_id, actions)
        logical = len(program_events)
        physical = len(program_events)
        joins = len(program_events)
    return {
        "status": "completed", "default_reward": float(reward), "action_reward": float(action_reward),
        "provider_calls": cell["provider_calls"], "messages": len(simulation.get("messages", [])),
        "termination_reason": simulation.get("termination_reason"), "logical_calls": logical, "physical_calls": physical, "source_joins": joins,
    }


def build(public: dict[str, Any], private: dict[str, Any], cells_root: pathlib.Path) -> dict[str, Any]:
    actions = validate_prereg(public, private)
    protocol = public["protocol"]
    rows = []
    seen = set()
    for task_id in actions:
        for lane in ("direct", "programmatic_python"):
            path = cells_root / f"task-{task_id}-{lane}.json"
            if not path.is_file():
                raise ValueError(f"missing frozen cell {task_id}/{lane}")
            cell = json.loads(path.read_text())
            key = (task_id, lane)
            if key in seen:
                raise ValueError("duplicate cell")
            seen.add(key)
            result = validate_cell(cell, task_id, lane, actions[task_id], cells_root, protocol)
            rows.append({"task_id": task_id, "lane": lane, **result})
    statuses = Counter(row["status"] for row in rows)
    return {
        "schema_version": REPORT_SCHEMA, "classification": "NATURAL_BOUNDED_COHORT_RECORDED",
        "source": public["source"], "preregistration_identity": public["identity"],
        "denominator": {"tasks": 16, "planned_cells": 32, "recorded_cells": len(rows), "status_counts": dict(sorted(statuses.items())), "post_hoc_dropped": 0},
        "protocol": {"model": MODEL, "seed": protocol["seed"], "temperature": 0.0, "tool_surface_matched": False, "performance_comparison_supported": False, "leaderboard_comparable": False, "official_action_component_is_diagnostic": True},
        "rows": rows,
        "claim_boundary": {"supports": "frozen natural READ cohort execution and source-bound treatment evidence", "does_not_support": ["leaderboard score", "performance advantage", "matched tool surfaces", "model WRITE ability", "production external effects"]},
        "private_bodies_included": False,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--public-preregistration", required=True)
    parser.add_argument("--private-preregistration", required=True)
    parser.add_argument("--cells-root", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    report = build(json.loads(pathlib.Path(args.public_preregistration).read_text()), json.loads(pathlib.Path(args.private_preregistration).read_text()), pathlib.Path(args.cells_root))
    body = canonical(report) + b"\n"
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(body)
    print(hashlib.sha256(body).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
