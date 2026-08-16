#!/usr/bin/env python3
"""Freeze a treatment-only remediation cohort without mutating T2-v1."""

import argparse
import hashlib
import json
import pathlib
from typing import Any

PUBLIC_SCHEMA = "pysolate.tau2.t2-remediation-preregistration.v1"
PRIVATE_SCHEMA = "pysolate.tau2.t2-remediation-preregistration-private.v1"
MODEL = "deepseek/deepseek-v4-pro"


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def sha(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--parent-public", required=True)
    parser.add_argument("--parent-private", required=True)
    parser.add_argument("--parent-report", required=True)
    parser.add_argument("--public-output", required=True)
    parser.add_argument("--private-output", required=True)
    args = parser.parse_args()
    parent_public = json.loads(pathlib.Path(args.parent_public).read_text())
    parent_private = json.loads(pathlib.Path(args.parent_private).read_text())
    report_body = pathlib.Path(args.parent_report).read_bytes()
    if parent_private.get("public_identity") != parent_public.get("identity") or len(parent_public.get("tasks", [])) != 16:
        raise ValueError("parent preregistration mismatch")
    protocol = dict(parent_public["protocol"])
    protocol.update({
        "lanes": ["programmatic_python"], "max_total_provider_invocations_per_trial": 64,
        "max_total_provider_invocations": 1024, "post_provider_reruns": 0,
        "remediation_attempts_per_cell": 1, "direct_lane_rerun": False,
        "denominator_membership": "all_16_parent_tasks",
    })
    public = {
        "schema_version": PUBLIC_SCHEMA,
        "classification": "PREREGISTERED_TREATMENT_REMEDIATION",
        "source": parent_public["source"],
        "parent": {"preregistration_identity": parent_public["identity"], "report_sha256": sha(report_body)},
        "repair_scope": ["task_scoped_raw_body_paths", "whitespace_only_invalid_action_fallback", "higher_provider_guard"],
        "protocol": protocol,
        "tasks": parent_public["tasks"],
        "task_bodies_included": False,
    }
    public["identity"] = sha(canonical(public))
    private = {
        "schema_version": PRIVATE_SCHEMA, "public_identity": public["identity"],
        "source": parent_private["source"], "protocol": protocol, "tasks": parent_private["tasks"],
    }
    public_path = pathlib.Path(args.public_output)
    private_path = pathlib.Path(args.private_output)
    public_path.parent.mkdir(parents=True, exist_ok=True)
    private_path.parent.mkdir(parents=True, exist_ok=True)
    public_path.write_bytes(canonical(public) + b"\n")
    private_path.write_bytes(canonical(private) + b"\n")
    private_path.chmod(0o600)
    print(hashlib.sha256(public_path.read_bytes()).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
