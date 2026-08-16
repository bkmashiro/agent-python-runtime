#!/usr/bin/env python3
"""Strict aggregate for the treatment-only T2 remediation cohort."""

import argparse
import collections
import hashlib
import importlib.util
import json
import pathlib
from typing import Any

HERE = pathlib.Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("t2_base_report", HERE / "tau2-t2-report.py")
assert SPEC is not None and SPEC.loader is not None
base = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(base)

PUBLIC_SCHEMA = "pysolate.tau2.t2-remediation-preregistration.v1"
PRIVATE_SCHEMA = "pysolate.tau2.t2-remediation-preregistration-private.v1"
REPORT_SCHEMA = "pysolate.tau2.t2-remediation-report.v1"


def validate_prereg(public: dict[str, Any], private: dict[str, Any]) -> dict[str, list[dict[str, Any]]]:
    if public.get("schema_version") != PUBLIC_SCHEMA or private.get("schema_version") != PRIVATE_SCHEMA:
        raise ValueError("remediation schema mismatch")
    projected = dict(public)
    identity = projected.pop("identity", None)
    if identity != base.sha(base.canonical(projected)) or private.get("public_identity") != identity:
        raise ValueError("remediation identity mismatch")
    if private.get("protocol") != public.get("protocol") or public.get("protocol", {}).get("lanes") != ["programmatic_python"]:
        raise ValueError("remediation protocol mismatch")
    if public.get("protocol", {}).get("post_provider_reruns") != 0 or public.get("protocol", {}).get("max_total_provider_invocations_per_trial") != 64:
        raise ValueError("remediation provider contract mismatch")
    public_tasks = public.get("tasks", [])
    private_tasks = private.get("tasks", [])
    if len(public_tasks) != 16 or len(private_tasks) != 16:
        raise ValueError("remediation denominator mismatch")
    actions = {}
    for exposed, secret in zip(public_tasks, private_tasks):
        if exposed.get("task_id") != secret.get("task_id"):
            raise ValueError("remediation task order mismatch")
        body, reference = secret.get("task"), secret.get("reference_actions")
        if base.sha(base.canonical(body)) != exposed.get("task_sha256") or base.sha(base.canonical(reference)) != exposed.get("reference_actions_sha256"):
            raise ValueError("remediation private task digest mismatch")
        actions[exposed["task_id"]] = reference
    return actions


def build(public: dict[str, Any], private: dict[str, Any], cells_root: pathlib.Path) -> dict[str, Any]:
    actions = validate_prereg(public, private)
    raw_refs = collections.Counter()
    cells = {}
    for task_id in actions:
        path = cells_root / f"task-{task_id}-programmatic_python.json"
        if not path.is_file():
            raise ValueError(f"missing remediation cell {task_id}")
        cell = json.loads(path.read_text())
        cells[task_id] = cell
        for event in cell.get("pysolate_events") or []:
            if event.get("kind") == "program":
                raw = event.get("turn", {}).get("raw_bodies", {})
                raw_refs.update(value for value in (raw.get("guest_request"), raw.get("guest_response")) if isinstance(value, str))
    collisions = sorted(path for path, count in raw_refs.items() if count > 1)
    if collisions:
        raise ValueError("remediation raw evidence paths collide")
    rows = []
    for task_id, reference in actions.items():
        result = base.validate_cell(cells[task_id], task_id, "programmatic_python", reference, cells_root, public["protocol"], set())
        rows.append({"task_id": task_id, "lane": "programmatic_python", **result})
    statuses = collections.Counter(row["status"] for row in rows)
    return {
        "schema_version": REPORT_SCHEMA,
        "classification": "TREATMENT_REMEDIATION_RECORDED",
        "source": public["source"], "preregistration_identity": public["identity"], "parent": public["parent"],
        "denominator": {"tasks": 16, "planned_cells": 16, "recorded_cells": 16, "post_hoc_dropped": 0, "status_counts": dict(sorted(statuses.items()))},
        "result_summary": {
            "known_provider_calls": sum(row["provider_calls"] for row in rows if isinstance(row["provider_calls"], int)),
            "unknown_provider_cells": sum(row["provider_calls"] is None for row in rows),
            "completed_rows": statuses.get("completed", 0),
            "reconstructed_rows": sum(row["causal_evidence_status"] == "reconstructed" for row in rows),
            "source_joins": sum(row["source_joins"] for row in rows),
        },
        "evidence_integrity": {"shared_raw_path_collisions": 0, "post_provider_reruns": 0, "task_scoped_raw_paths": True},
        "rows": rows,
        "claim_boundary": {
            "supports": "remediation treatment outcomes and source-bound causal evidence only for completed reconstructed rows",
            "does_not_support": ["replacement of T2-v1", "leaderboard score", "matched-surface performance comparison", "model WRITE ability", "production external effects"],
        },
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
    body = base.canonical(report) + b"\n"
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(body)
    print(hashlib.sha256(body).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
