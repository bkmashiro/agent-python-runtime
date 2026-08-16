#!/usr/bin/env python3
"""Append-only driver for the frozen paid tau2 T2 cohort."""

import argparse
import hashlib
import json
import os
import pathlib
import subprocess
import sys
from typing import Any

REVISION = "c3398666e6559e3a063da3fc04b5acf7f941464e"
PUBLIC_SCHEMA = "pysolate.tau2-t2-preregistration.v1"
PRIVATE_SCHEMA = "pysolate.tau2-t2-private-preregistration.v1"
REMEDIATION_PUBLIC_SCHEMA = "pysolate.tau2.t2-remediation-preregistration.v1"
REMEDIATION_PRIVATE_SCHEMA = "pysolate.tau2.t2-remediation-preregistration-private.v1"
PREFLIGHT_SCHEMA = "pysolate.tau2-t2-preflight.v1"
CELL_SCHEMA = "pysolate.tau2-t2-cell-private.v1"
MODEL = "deepseek/deepseek-v4-pro"
LANES = (("direct", "direct"), ("programmatic_python", "programmatic_python"))


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def digest(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def load_protocol(public_path: pathlib.Path, private_path: pathlib.Path, preflight_path: pathlib.Path):
    public = json.loads(public_path.read_text())
    private = json.loads(private_path.read_text())
    preflight = json.loads(preflight_path.read_text())
    public_copy = dict(public)
    identity = public_copy.pop("identity", None)
    schema = public.get("schema_version")
    expected_private_schema = PRIVATE_SCHEMA if schema == PUBLIC_SCHEMA else REMEDIATION_PRIVATE_SCHEMA
    if schema not in {PUBLIC_SCHEMA, REMEDIATION_PUBLIC_SCHEMA} or identity != digest(canonical(public_copy)):
        raise ValueError("public preregistration identity mismatch")
    if private.get("schema_version") != expected_private_schema or private.get("public_identity") != identity or private.get("protocol") != public.get("protocol"):
        raise ValueError("private preregistration mismatch")
    if preflight.get("schema_version") != PREFLIGHT_SCHEMA or preflight.get("classification") != "PREFLIGHT_SUPPORTED" or preflight.get("preregistration_identity") != identity or preflight.get("provider_calls") != 0:
        raise ValueError("provider-free preflight mismatch")
    protocol = public["protocol"]
    expected_limit = 20 if schema == PUBLIC_SCHEMA else 64
    if protocol.get("model") != MODEL or protocol.get("post_provider_reruns") != 0 or protocol.get("max_total_provider_invocations_per_trial") != expected_limit:
        raise ValueError("paid protocol mismatch")
    return public, private, preflight


def planned_cells(public: dict[str, Any]) -> list[tuple[str, str, str]]:
    task_ids = [row["task_id"] for row in public["tasks"]]
    if len(task_ids) != 16 or len(set(task_ids)) != 16:
        raise ValueError("task denominator mismatch")
    schema = public.get("schema_version")
    if schema not in {PUBLIC_SCHEMA, REMEDIATION_PUBLIC_SCHEMA}:
        raise ValueError("unsupported cohort schema")
    lanes = LANES if schema == PUBLIC_SCHEMA else (("programmatic_python", "programmatic_python"),)
    return [(task_id, public_lane, harness_lane) for task_id in task_ids for public_lane, harness_lane in lanes]


def write_not_recorded(path: pathlib.Path, task_id: str, lane: str, protocol: dict[str, Any], stderr: bytes, returncode: int) -> None:
    stderr_path = path.with_suffix(".stderr")
    stderr_path.write_bytes(stderr)
    stderr_path.chmod(0o600)
    body = {
        "schema_version": CELL_SCHEMA, "source_revision": REVISION, "task_id": task_id, "lane": lane,
        "model": protocol["model"], "seed": protocol["seed"], "temperature": protocol["temperature"],
        "status": "not_recorded", "provider_calls": None, "simulation": None,
        "official_action_diagnostic": None, "pysolate_events": None,
        "failure": {"class": "harness_process", "returncode": returncode, "stderr_sha256": digest(stderr), "stderr_file": stderr_path.name},
    }
    path.write_bytes(canonical(body) + b"\n")
    path.chmod(0o600)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--public-preregistration", required=True)
    parser.add_argument("--private-preregistration", required=True)
    parser.add_argument("--preflight", required=True)
    parser.add_argument("--source-root", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--tau2-python", required=True)
    parser.add_argument("--artifact", required=True)
    parser.add_argument("--evidence-root", required=True)
    parser.add_argument("--aggregate-output", required=True)
    parser.add_argument("--report-script")
    parser.add_argument("--execute", action="store_true")
    parser.add_argument("--max-new-cells", type=int)
    args = parser.parse_args()

    public_path = pathlib.Path(args.public_preregistration).resolve()
    private_path = pathlib.Path(args.private_preregistration).resolve()
    preflight_path = pathlib.Path(args.preflight).resolve()
    source_root = pathlib.Path(args.source_root).resolve()
    repo_root = pathlib.Path(args.repo_root).resolve()
    tau2_python = os.path.abspath(args.tau2_python)
    artifact = pathlib.Path(args.artifact).resolve()
    evidence_root = pathlib.Path(args.evidence_root).resolve()
    aggregate_output = pathlib.Path(args.aggregate_output).resolve()
    public, private, _ = load_protocol(public_path, private_path, preflight_path)
    cells = planned_cells(public)
    evidence_root.mkdir(parents=True, exist_ok=True)
    evidence_root.chmod(0o700)

    existing = []
    pending = []
    for task_id, public_lane, harness_lane in cells:
        path = evidence_root / f"task-{task_id}-{public_lane}.json"
        if path.exists():
            existing.append(path.name)
        else:
            pending.append((task_id, public_lane, harness_lane, path))
    summary = {
        "schema_version": "pysolate.tau2-t2-run-plan.v1", "preregistration_identity": public["identity"],
        "planned_cells": len(cells), "existing_cells": len(existing), "pending_cells": len(pending),
        "execute": bool(args.execute), "max_total_provider_invocations": public["protocol"]["max_total_provider_invocations"],
    }
    if not args.execute:
        print(json.dumps(summary, sort_keys=True))
        return 0
    if not os.environ.get("DEEPSEEK_API_KEY"):
        raise ValueError("DEEPSEEK_API_KEY is required for the paid cohort")

    cohort_script = repo_root / "scripts/tau2-t2-cohort.py"
    batch = pending
    if args.max_new_cells is not None:
        if args.max_new_cells < 1:
            raise ValueError("max-new-cells must be positive")
        batch = pending[:args.max_new_cells]
    for task_id, public_lane, harness_lane, path in batch:
        command = [
            tau2_python, str(cohort_script), "--lane", harness_lane, "--task-id", task_id,
            "--source-root", str(source_root),
            "--repo-root", str(repo_root), "--tau2-python", tau2_python, "--artifact", str(artifact),
            "--private-manifest", str(private_path), "--evidence-root", str(evidence_root),
        ]
        completed = subprocess.run(command, cwd=str(source_root), capture_output=True)
        produced = evidence_root / f"task-{task_id}-{public_lane}.json"
        if completed.returncode == 0 and produced.exists():
            if produced != path:
                produced.replace(path)
            continue
        write_not_recorded(path, task_id, public_lane, private["protocol"], completed.stderr + completed.stdout, completed.returncode)

    remaining = [
        (task_id, public_lane) for task_id, public_lane, _ in cells
        if not (evidence_root / f"task-{task_id}-{public_lane}.json").exists()
    ]
    if remaining:
        print(json.dumps({**summary, "execute": True, "completed_this_batch": len(batch), "remaining_cells": len(remaining)}, sort_keys=True))
        return 0

    report_script = pathlib.Path(args.report_script).resolve() if args.report_script else repo_root / "scripts/tau2-t2-report.py"
    subprocess.run([
        tau2_python, str(report_script), "--public-preregistration", str(public_path),
        "--private-preregistration", str(private_path), "--cells-root", str(evidence_root),
        "--output", str(aggregate_output),
    ], cwd=str(repo_root), check=True)
    print(json.dumps({**summary, "execute": True, "pending_cells": 0, "aggregate_output": str(aggregate_output)}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
