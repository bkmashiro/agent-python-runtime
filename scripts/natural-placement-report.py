#!/usr/bin/env python3
"""Rebuild a body-safe natural placement report from frozen source and private evidence."""

import argparse
import hashlib
import importlib.util
import json
import pathlib
import re
from typing import Any

REPORT_SCHEMA = "pysolate.natural-placement-report.v1"
EVIDENCE_SCHEMA = "pysolate.natural-placement-evidence.v1"
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def exact(value: Any, keys: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != keys:
        raise ValueError(f"{label} keys mismatch")
    return value


def load_control(script: pathlib.Path):
    spec = importlib.util.spec_from_file_location("natural_placement_control_for_report", script)
    if spec is None or spec.loader is None:
        raise ValueError("placement control loader unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_request_bytes(request: dict[str, Any]) -> bytes:
    payload = {
        "run_id": "natural-placement-open-swe-v1",
        "code": "result = inputs['task_record_sha256']",
        "inputs": {"task_record_sha256": request["task"]["record_sha256"], "trajectory_sha256": request["task"]["trajectory_sha256"]},
        "requirements": ["shell", "subprocess"],
    }
    return json.dumps(payload, separators=(",", ":"), ensure_ascii=False).encode()


def decision_identity(decision: dict[str, Any]) -> str:
    document: dict[str, Any] = {
        "schema_version": decision["schema_version"], "status": decision["status"]
    }
    if decision.get("backend"):
        document["backend"] = decision["backend"]
    document.update({
        "reason": decision["reason"], "analyzer_version": decision["analyzer_version"],
        "request_sha256": decision["request_sha256"], "state_class": decision["state_class"],
    })
    if decision.get("parent_decision_id"):
        document["parent_decision_id"] = decision["parent_decision_id"]
    encoded = json.dumps(document, separators=(",", ":"), ensure_ascii=False).encode()
    return digest(encoded)


def validate(source_root: pathlib.Path, request_path: pathlib.Path, evidence_path: pathlib.Path, control_script: pathlib.Path) -> dict[str, Any]:
    request_bytes = request_path.read_bytes()
    request = json.loads(request_bytes)
    rebuilt = load_control(control_script).build(source_root)
    if request != rebuilt or request_bytes != canonical(rebuilt) + b"\n":
        raise ValueError("placement request does not match frozen raw source")
    evidence = json.loads(evidence_path.read_text())
    exact(evidence, {"schema_version", "source_request_sha256", "run_request_sha256", "source", "task", "requirements", "lanes", "model_calls", "private_bodies_included"}, "evidence")
    if evidence["schema_version"] != EVIDENCE_SCHEMA or evidence["source_request_sha256"] != digest(request_bytes):
        raise ValueError("evidence/request identity mismatch")
    if evidence["source"] != request["source"] or evidence["task"] != request["task"] or evidence["requirements"] != ["shell", "subprocess"]:
        raise ValueError("evidence source semantics mismatch")
    if evidence["run_request_sha256"] != digest(run_request_bytes(request)):
        raise ValueError("run request identity mismatch")
    if evidence["model_calls"] != 0 or evidence["private_bodies_included"] is not False:
        raise ValueError("evidence privacy/model contract mismatch")
    lanes = exact(evidence["lanes"], {"selected_native", "native_unavailable"}, "lanes")
    selected = exact(lanes["selected_native"], {"decision", "pysolate_backend_calls", "native_backend_calls", "promotion", "workspace_started", "effects_started"}, "selected lane")
    unavailable = exact(lanes["native_unavailable"], {"decision", "pysolate_backend_calls", "native_backend_calls", "error", "workspace_started", "effects_started"}, "unavailable lane")
    decision_keys = {"schema_version", "status", "backend", "reason", "analyzer_version", "request_sha256", "state_class", "identity"}
    selected_decision = exact(selected["decision"], decision_keys, "selected decision")
    unavailable_decision = exact(unavailable["decision"], decision_keys - {"backend"}, "unavailable decision")
    if selected_decision != {
        **{key: selected_decision[key] for key in selected_decision},
    }:
        raise AssertionError("unreachable")
    if selected_decision["schema_version"] != "pysolate.placement-decision.v1" or selected_decision["status"] != "selected" or selected_decision["backend"] != "native_sandbox" or selected_decision["reason"] != "required_native_feature" or selected_decision["analyzer_version"] != "static-v1" or selected_decision["state_class"] != "portable_value" or selected_decision["request_sha256"] != evidence["run_request_sha256"] or selected_decision["identity"] != decision_identity(selected_decision):
        raise ValueError("selected placement decision mismatch")
    if selected != {"decision": selected_decision, "pysolate_backend_calls": 0, "native_backend_calls": 1, "promotion": False, "workspace_started": False, "effects_started": False}:
        raise ValueError("selected lane execution mismatch")
    if unavailable_decision["schema_version"] != "pysolate.placement-decision.v1" or unavailable_decision["status"] != "unavailable" or unavailable_decision["reason"] != "native_unavailable" or unavailable_decision["analyzer_version"] != "static-v1" or unavailable_decision["state_class"] != "portable_value" or unavailable_decision["request_sha256"] != evidence["run_request_sha256"] or unavailable_decision["identity"] != decision_identity(unavailable_decision):
        raise ValueError("unavailable placement decision mismatch")
    if unavailable != {"decision": unavailable_decision, "pysolate_backend_calls": 0, "native_backend_calls": 0, "error": "backend_unavailable", "workspace_started": False, "effects_started": False}:
        raise ValueError("unavailable lane execution mismatch")
    for value in (request["task"]["record_sha256"], request["task"]["record_body_sha256"], request["task"]["trajectory_sha256"], evidence["run_request_sha256"], selected_decision["identity"], unavailable_decision["identity"]):
        if not DIGEST.fullmatch(value):
            raise ValueError("invalid evidence digest")
    return {
        "schema_version": REPORT_SCHEMA,
        "classification": "SUPPORTED_PLACEMENT_CONTROL",
        "source": request["source"],
        "task": {
            "record_sha256": request["task"]["record_sha256"], "language": "python", "upstream_resolved": 1,
            "trajectory_messages": request["task"]["trajectory_messages"], "tool_name_counts": request["task"]["tool_name_counts"],
            "private_task_body_included": False, "private_trajectory_bodies_included": False,
        },
        "placement": {
            "required_features": ["shell", "subprocess"], "mutable_workspace_observed": True,
            "selected_backend": "native_sandbox", "reason": "required_native_feature",
            "pysolate_guest_calls": 0, "native_backend_calls": 1, "promotion_used": False,
            "workspace_started_before_placement": False, "effects_started_before_placement": False,
        },
        "negative_control": {
            "native_unavailable_status": "unavailable", "pysolate_guest_calls": 0, "native_backend_calls": 0,
            "workspace_started": False, "effects_started": False,
        },
        "model_calls": 0,
        "claim_boundary": {
            "supports": "one frozen natural coding trajectory is conservatively placed before Guest start",
            "does_not_support": ["coding task execution", "native backend correctness", "model task success", "general placement optimality"],
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", type=pathlib.Path, required=True)
    parser.add_argument("--request", type=pathlib.Path, required=True)
    parser.add_argument("--evidence", type=pathlib.Path, required=True)
    parser.add_argument("--control-script", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    body = canonical(validate(args.source_root, args.request, args.evidence, args.control_script)) + b"\n"
    args.output.write_bytes(body)
    print(hashlib.sha256(body).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
