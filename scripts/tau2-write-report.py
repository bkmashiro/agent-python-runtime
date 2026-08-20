#!/usr/bin/env python3
"""Rebuild a body-safe airline/11 WRITE report from private raw evidence."""

import argparse
import hashlib
import json
import pathlib
import re
from typing import Any, Dict

EXPECTED_REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
REPORT_SCHEMA = "pysolate.tau2-private-write-canary.v1"
PRIVATE_SCHEMA = "pysolate.tau2-private-write-evidence.v1"
CAPABILITY = "tau2.airline.apply_task_11_reference_update"
SOURCE_CALL = "tools.apply_task_11_reference_update()"
LANES = {"accepted", "rejected", "expired", "failure"}
EXPECTED = {
    "accepted": {"calls": 1, "outcome": "ok", "approval": "approved", "approval_outcome": "ok", "executed": True, "changed": True, "disposition": "published", "guest_status": "ok"},
    "rejected": {"calls": 0, "outcome": "denied", "approval": "rejected", "approval_outcome": None, "executed": False, "changed": False, "disposition": "discarded", "guest_status": "error"},
    "expired": {"calls": 0, "outcome": "denied", "approval": "expired", "approval_outcome": None, "executed": False, "changed": False, "disposition": "discarded", "guest_status": "error"},
    "failure": {"calls": 1, "outcome": "error", "approval": "approved", "approval_outcome": "error", "executed": True, "changed": False, "disposition": "discarded", "guest_status": "error"},
}
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
RECEIPT_RE = re.compile(r"^rcpt_[0-9a-f]{64}$")
APPROVAL_RE = re.compile(r"^apr_[0-9a-f]{64}$")


def _reject_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def digest_bytes(body: bytes) -> str:
    return "sha256:" + hashlib.sha256(body).hexdigest()


def digest_json(value: Any) -> str:
    return digest_bytes(json.dumps(value, sort_keys=True, separators=(",", ":")).encode())


def same_digest(actual: Any, expected: str) -> bool:
    return isinstance(actual, str) and actual.removeprefix("sha256:") == expected.removeprefix("sha256:")


def require_keys(value: Any, keys: set[str], label: str) -> Dict[str, Any]:
    if not isinstance(value, dict) or set(value) != keys:
        raise ValueError(f"{label} fields mismatch")
    return value


def safe_read(root: pathlib.Path, name: Any, limit: int) -> bytes:
    if not isinstance(name, str) or not name or pathlib.PurePath(name).name != name:
        raise ValueError("unsafe private evidence filename")
    root = root.resolve()
    path = root / name
    if path.is_symlink() or not path.is_file() or path.resolve().parent != root:
        raise ValueError("private evidence file missing or unsafe")
    body = path.read_bytes()
    if not body or len(body) > limit:
        raise ValueError("private evidence file size outside contract")
    return body


def load_json(body: bytes, label: str) -> Dict[str, Any]:
    try:
        value = json.loads(body, object_pairs_hook=_reject_duplicates)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"invalid {label} JSON") from error
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be a JSON object")
    return value


def grant_identity(policy: Dict[str, Any]) -> str:
    canonical = json.dumps(policy, sort_keys=True, separators=(",", ":")).encode()
    document = b'{"schema_version":"pysolate.capability-grant.v1","policy":' + canonical + b"}"
    return digest_bytes(document)


def source_document_identity(source_sha: str) -> str:
    return digest_bytes(b"pysolate.source-document.v0\x00python\x00" + source_sha.encode())


def source_occurrence_identity(source_sha: str, capability: str, span: tuple[int, int, int, int]) -> str:
    coordinates = f"{span[0]}:{span[1]}:{span[2]}:{span[3]}"
    return digest_bytes(f"pysolate.semantic-call-site.v0\x00{source_sha}\x00{capability}\x00{coordinates}".encode())


def receipt_identity(receipt: Dict[str, Any]) -> str:
    source = receipt["source"]
    fields = [
        "pysolate-receipt-v3", receipt["run_id"], receipt["capability_plan_sha256"], receipt["call_id"],
        receipt["parent_call_id"], receipt["approval_request_id"], receipt["capability"], str(receipt["operation_index"]),
        receipt["request_sha256"], source["schema_version"], source["claim_level"], source["document_id"],
        source["source_sha256"], source["occurrence_id"], source["capability"], str(source["dynamic_occurrence"]),
        str(source["start_line"]), str(source["start_column"]), str(source["end_line"]), str(source["end_column"]),
    ]
    digest = hashlib.sha256()
    for field in fields:
        digest.update(field.encode())
        digest.update(b"\x00")
    return "rcpt_" + digest.hexdigest()


def validate_source_binding(source: Any, source_body: bytes) -> Dict[str, Any]:
    keys = {
        "schema_version", "claim_level", "document_id", "source_sha256", "occurrence_id", "capability",
        "dynamic_occurrence", "start_line", "start_column", "end_line", "end_column",
    }
    source = require_keys(source, keys, "source binding")
    if source["schema_version"] != "pysolate.source-binding.v0" or source["claim_level"] != "source_bound":
        raise ValueError("source binding schema mismatch")
    source_sha = digest_bytes(source_body)
    if source["source_sha256"] != source_sha or source["document_id"] != source_document_identity(source_sha):
        raise ValueError("source document identity mismatch")
    if source["capability"] != CAPABILITY or source["dynamic_occurrence"] != 1:
        raise ValueError("source capability or occurrence mismatch")
    span = (source["start_line"], source["start_column"], source["end_line"], source["end_column"])
    if any(not isinstance(value, int) or value < 0 for value in span) or span[0] != 1 or span[2] != 1:
        raise ValueError("source span outside exact contract")
    line = source_body.decode().splitlines()[0]
    if line[span[1]:span[3]] != SOURCE_CALL:
        raise ValueError("source span does not identify exact call")
    if source["occurrence_id"] != source_occurrence_identity(source_sha, CAPABILITY, span):
        raise ValueError("source occurrence identity mismatch")
    return source


def validate_receipt(receipt: Any, source_body: bytes, plan_sha: str, approval: Dict[str, Any], expected_outcome: str) -> Dict[str, Any]:
    common = {
        "receipt_id", "run_id", "capability_plan_sha256", "call_id", "parent_call_id", "approval_request_id",
        "capability", "operation_index", "request_sha256", "outcome", "source",
    }
    expected_keys = common | ({"response_sha256"} if expected_outcome == "ok" else set())
    receipt = require_keys(receipt, expected_keys, "receipt")
    if receipt["capability"] != CAPABILITY or receipt["capability_plan_sha256"] != plan_sha or receipt["operation_index"] != 0:
        raise ValueError("receipt capability/Plan identity mismatch")
    if receipt["outcome"] != expected_outcome or not RECEIPT_RE.fullmatch(receipt["receipt_id"]):
        raise ValueError("receipt outcome or ID invalid")
    if not same_digest(receipt["request_sha256"], digest_json({})):
        raise ValueError("receipt request identity mismatch")
    if not APPROVAL_RE.fullmatch(receipt["approval_request_id"]):
        raise ValueError("receipt approval identity invalid")
    if any(receipt[key] != approval[key] for key in ("run_id", "call_id", "parent_call_id")) or receipt["approval_request_id"] != approval["request_id"]:
        raise ValueError("receipt-to-approval identity mismatch")
    validate_source_binding(receipt["source"], source_body)
    if receipt["receipt_id"] != receipt_identity(receipt):
        raise ValueError("receipt operation identity mismatch")
    return receipt


def validate_plan(plan_body: bytes, lane_name: str, grant_sha: str) -> str:
    plan_sha = digest_bytes(plan_body)
    plan = load_json(plan_body, "Plan")
    require_keys(plan, {"schema_version", "max_calls", "capabilities", "grants"}, "Plan")
    if plan["schema_version"] != "pysolate.capability-plan.v7" or plan["max_calls"] != 1:
        raise ValueError("Plan header mismatch")
    if not isinstance(plan["capabilities"], list) or len(plan["capabilities"]) != 1:
        raise ValueError("Plan must contain one capability")
    if plan["grants"] != [{"capability": CAPABILITY, "policy_sha256": grant_sha}]:
        raise ValueError("Plan Grant binding mismatch")
    spec = plan["capabilities"][0]
    required = {"capability", "version", "description", "effect_class", "playback", "handler_identity", "input_schema", "output_schema", "python", "approval"}
    require_keys(spec, required, "Plan capability")
    if spec["capability"] != CAPABILITY or spec["effect_class"] != "workspace_write" or spec["playback"] != "live_only":
        raise ValueError("Plan capability semantics mismatch")
    expected_handler = "pysolate.tau2.airline.private-write.handler." + EXPECTED_REVISION
    if lane_name == "failure":
        expected_handler += ".injected-failure"
    if spec["handler_identity"] != expected_handler:
        raise ValueError("Plan handler identity mismatch")
    expected_lease = 20 if lane_name == "expired" else 2000
    if spec["approval"] != {"mode": "lease", "lease_milliseconds": expected_lease}:
        raise ValueError("Plan approval policy mismatch")
    if spec["input_schema"] != {"type": "object", "properties": {}, "additionalProperties": False}:
        raise ValueError("Plan input schema mismatch")
    projection = spec["python"]
    if projection != {"module": "tools", "method": "apply_task_11_reference_update", "arguments": [], "result_field": "content"}:
        raise ValueError("Plan Python projection mismatch")
    return plan_sha


def validate(evidence: Dict[str, Any], oracle_request: Dict[str, Any], oracle: Dict[str, Any], evidence_root: pathlib.Path) -> Dict[str, Any]:
    top_keys = {
        "schema_version", "source", "artifact_sha256", "arguments", "grant_operation_sha256", "accepted_tool_content",
        "grant_policy_file", "grant_policy_sha256", "accepted_final_state_file", "accepted_final_state_sha256", "lanes",
    }
    require_keys(evidence, top_keys, "private evidence")
    if evidence["schema_version"] != PRIVATE_SCHEMA or not DIGEST_RE.fullmatch(evidence["artifact_sha256"]):
        raise ValueError("invalid private evidence identity")
    if evidence["source"] != {"revision": EXPECTED_REVISION, "domain": "airline", "task_id": "11"}:
        raise ValueError("private evidence source mismatch")
    operation_sha = digest_json(evidence["arguments"])
    if evidence["grant_operation_sha256"] != operation_sha or not isinstance(evidence["accepted_tool_content"], str) or not evidence["accepted_tool_content"]:
        raise ValueError("operation evidence mismatch")

    grant_body = safe_read(evidence_root, evidence["grant_policy_file"], 16 * 1024)
    if digest_bytes(grant_body) != evidence["grant_policy_sha256"]:
        raise ValueError("Grant policy raw hash mismatch")
    grant_policy = load_json(grant_body, "Grant policy")
    expected_policy = {
        "benchmark": "tau2", "domain": "airline", "effect": "workspace_write", "operation_sha256": operation_sha,
        "source_revision": EXPECTED_REVISION, "task_id": "11", "workspace_scope": "attempt_private",
    }
    if grant_policy != expected_policy:
        raise ValueError("Grant policy semantics mismatch")
    grant_sha = grant_identity(grant_policy)

    lanes = evidence["lanes"]
    if not isinstance(lanes, list) or len(lanes) != 4 or {lane.get("name") for lane in lanes if isinstance(lane, dict)} != LANES:
        raise ValueError("exact four-lane evidence required")
    by_name = {lane["name"]: lane for lane in lanes}
    for name, expected in EXPECTED.items():
        lane = by_name[name]
        lane_keys = {
            "name", "disposition", "initial_state_sha256", "final_state_sha256", "plan_sha256", "workspace_ref",
            "handler_calls", "receipt", "approval", "request_sha256", "guest_response_sha256", "workspace_event", "raw_bodies",
        }
        require_keys(lane, lane_keys, f"{name} lane")
        if not all(DIGEST_RE.fullmatch(lane[key]) for key in ("initial_state_sha256", "final_state_sha256", "plan_sha256", "request_sha256", "guest_response_sha256")):
            raise ValueError(f"{name} lane digest invalid")
        raw_names = require_keys(lane["raw_bodies"], {"request", "response", "source", "plan"}, f"{name} raw bodies")
        request_body = safe_read(evidence_root, raw_names["request"], 1024 * 1024)
        response_body = safe_read(evidence_root, raw_names["response"], 1024 * 1024)
        source_body = safe_read(evidence_root, raw_names["source"], 16 * 1024)
        plan_body = safe_read(evidence_root, raw_names["plan"], 64 * 1024)
        if digest_bytes(request_body) != lane["request_sha256"] or digest_bytes(response_body) != lane["guest_response_sha256"]:
            raise ValueError(f"{name} raw Guest body hash mismatch")
        plan_sha = validate_plan(plan_body, name, grant_sha)
        if plan_sha != lane["plan_sha256"]:
            raise ValueError(f"{name} canonical Plan identity mismatch")
        request = load_json(request_body, f"{name} Guest request")
        response = load_json(response_body, f"{name} Guest response")
        require_keys(request, {"run_id", "code", "inputs"}, f"{name} Guest request")
        if not isinstance(request["code"], str) or source_body.decode().strip() not in request["code"]:
            raise ValueError(f"{name} authored source missing from Guest request")
        if response.get("capability_plan_sha256") != plan_sha or response.get("status") != expected["guest_status"]:
            raise ValueError(f"{name} Guest response identity mismatch")
        approvals = lane["approval"]
        if not isinstance(approvals, list) or len(approvals) != 1:
            raise ValueError(f"{name} approval evidence missing")
        approval = approvals[0]
        approval_required = {"request_id", "run_id", "plan_sha256", "call_id", "parent_call_id", "capability", "arguments_sha256", "status", "executed"}
        if not isinstance(approval, dict) or not approval_required.issubset(approval):
            raise ValueError(f"{name} approval evidence incomplete")
        if approval["capability"] != CAPABILITY or approval["plan_sha256"] != plan_sha or approval["arguments_sha256"] != digest_json({}):
            raise ValueError(f"{name} approval authority mismatch")
        receipt = validate_receipt(lane["receipt"], source_body, plan_sha, approval, expected["outcome"])
        if request["run_id"] != receipt["run_id"] or response.get("receipts") != [receipt]:
            raise ValueError(f"{name} raw Guest/receipt join mismatch")
        changed = lane["initial_state_sha256"] != lane["final_state_sha256"]
        observed = {
            "calls": lane["handler_calls"], "outcome": receipt["outcome"], "approval": approval["status"],
            "approval_outcome": approval.get("dispatch_outcome"), "executed": approval["executed"],
            "changed": changed, "disposition": lane["disposition"], "guest_status": response["status"],
        }
        if observed != expected:
            raise ValueError(f"{name} lane contract mismatch")
        event = require_keys(lane["workspace_event"], {"action", "attempt_ref", "result_ref", "verified", "post_state_sha256", "post_state_absent"}, f"{name} workspace event")
        if event["attempt_ref"] != lane["workspace_ref"] or event["verified"] is not True:
            raise ValueError(f"{name} workspace identity mismatch")
        if name == "accepted":
            if event != {"action": "publish", "attempt_ref": lane["workspace_ref"], "result_ref": lane["workspace_ref"], "verified": True, "post_state_sha256": lane["final_state_sha256"], "post_state_absent": False}:
                raise ValueError("accepted publish evidence mismatch")
        elif event != {"action": "discard", "attempt_ref": lane["workspace_ref"], "result_ref": "", "verified": True, "post_state_sha256": "", "post_state_absent": True}:
            raise ValueError(f"{name} discard evidence mismatch")

    if by_name["accepted"]["plan_sha256"] != by_name["rejected"]["plan_sha256"]:
        raise ValueError("accepted and rejected controls must share one Plan")
    accepted = by_name["accepted"]
    final_state_body = safe_read(evidence_root, evidence["accepted_final_state_file"], 4 * 1024 * 1024)
    if digest_bytes(final_state_body) != evidence["accepted_final_state_sha256"] or digest_bytes(final_state_body) != accepted["final_state_sha256"]:
        raise ValueError("accepted raw final state hash mismatch")
    final_state = load_json(final_state_body, "accepted final state")
    expected_response = {"content": evidence["accepted_tool_content"], "operation_sha256": operation_sha, "state_sha256": accepted["final_state_sha256"]}
    if not same_digest(accepted["receipt"].get("response_sha256"), digest_json(expected_response)):
        raise ValueError("accepted receipt response does not join final state")

    oracle_request_keys = {"schema_version", "source_revision", "domain", "task_id", "call", "assistant_text", "observed_final_state"}
    require_keys(oracle_request, oracle_request_keys, "oracle request")
    if oracle_request["schema_version"] != "pysolate.tau2-write-oracle-request.v1" or oracle_request["source_revision"] != EXPECTED_REVISION or oracle_request["domain"] != "airline" or oracle_request["task_id"] != "11":
        raise ValueError("oracle request source mismatch")
    call = require_keys(oracle_request["call"], {"call_id", "tool", "arguments", "content"}, "oracle call")
    if call["call_id"] != "oracle-write" or call["tool"] != "update_reservation_flights" or call["arguments"] != evidence["arguments"] or call["content"] != evidence["accepted_tool_content"]:
        raise ValueError("oracle operation replay mismatch")
    if oracle_request["observed_final_state"] != final_state:
        raise ValueError("oracle observed state does not join raw workspace state")

    oracle_keys = {"schema_version", "source", "reward_basis", "db_reward", "communicate_reward", "overall_reward", "db_match", "communicate_met", "observed_final_state_match", "tool_calls", "tool_bodies_included", "assistant_text_included"}
    require_keys(oracle, oracle_keys, "oracle report")
    expected_oracle_source = {"revision": EXPECTED_REVISION, "domain": "airline", "task_id": "11", "repository": "https://github.com/sierra-research/tau2-bench"}
    if oracle["schema_version"] != "pysolate.tau2-write-oracle.v1" or oracle["source"] != expected_oracle_source:
        raise ValueError("oracle report source mismatch")
    if oracle["reward_basis"] != ["DB", "COMMUNICATE"] or oracle["tool_calls"] != 1 or oracle["tool_bodies_included"] is not False or oracle["assistant_text_included"] is not False:
        raise ValueError("oracle report contract mismatch")
    if any(oracle[key] != 1.0 for key in ("db_reward", "communicate_reward", "overall_reward")) or any(oracle[key] is not True for key in ("db_match", "communicate_met", "observed_final_state_match")):
        raise ValueError("official oracle did not pass")

    return {
        "schema_version": REPORT_SCHEMA,
        "classification": "SUPPORTED_BENCHMARK_PRIVATE_ONLY",
        "source": {"repository": "https://github.com/sierra-research/tau2-bench", "revision": EXPECTED_REVISION, "domain": "airline", "task_id": "11"},
        "scope": {"authored_reference_program": True, "model_generated": False, "benchmark_private_workspace": True, "external_write": False, "leaderboard_comparable": False, "performance_comparison_supported": False},
        "operation": {"capability": CAPABILITY, "effect_class": "workspace_write", "operation_sha256": operation_sha, "arguments_included": False, "tool_result_included": False},
        "lanes": {name: {"approval_status": EXPECTED[name]["approval"], "approval_executed": EXPECTED[name]["executed"], "approval_dispatch_outcome": EXPECTED[name]["approval_outcome"], "handler_calls": EXPECTED[name]["calls"], "receipt_outcome": EXPECTED[name]["outcome"], "state_changed": EXPECTED[name]["changed"], "workspace_disposition": EXPECTED[name]["disposition"], "source_bound": True} for name in sorted(LANES)},
        "causal_join": {"source_to_plan": True, "plan_to_approval": True, "approval_to_dispatch": True, "dispatch_to_operation_digest": True, "operation_to_final_state": True, "final_state_to_receipt_response": True, "accepted_operation_to_official_oracle": True, "accepted_workspace_state_to_official_gold": True, "raw_guest_bodies_verified": True, "workspace_disposition_event_verified": True},
        "oracle": {"implementation": "tau2 EnvironmentEvaluator+CommunicateEvaluator", "reward_basis": ["DB", "COMMUNICATE"], "db_reward": 1.0, "communicate_reward": 1.0, "overall_reward": 1.0, "db_match": True, "communicate_met": True, "observed_final_state_match": True, "observed_final_state_included": False},
        "claim_boundary": {"supports": "one exact task-private authority-gated mutation through real Guest and Broker", "does_not_support": ["model WRITE ability", "production external WRITE", "generic rollback", "leaderboard score"]},
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--evidence", required=True)
    parser.add_argument("--oracle-request", required=True)
    parser.add_argument("--oracle-report", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    evidence_path = pathlib.Path(args.evidence)
    evidence = load_json(evidence_path.read_bytes(), "private evidence manifest")
    oracle_request = load_json(pathlib.Path(args.oracle_request).read_bytes(), "oracle request")
    oracle = load_json(pathlib.Path(args.oracle_report).read_bytes(), "oracle report")
    body = json.dumps(validate(evidence, oracle_request, oracle, evidence_path.parent), sort_keys=True, separators=(",", ":")) + "\n"
    pathlib.Path(args.output).write_text(body)
    print(hashlib.sha256(body.encode()).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
