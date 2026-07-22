#!/usr/bin/env python3
"""Compare fresh and single-use prepared evidence without thresholds."""

import argparse
import json
import pathlib
import statistics
from typing import Any, Dict, List


def _median(rows: List[Dict[str, Any]], metric: str) -> Any:
    return statistics.median(row[metric] for row in rows)


def _identity(evidence: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "artifact_sha256": evidence["artifact"]["sha256"],
        "artifact_source_commit": evidence["artifact"]["source_commit"],
        "host_revision": evidence["host_source"]["revision"],
        "environment": evidence["environment"],
        "backend": evidence["backend"],
    }


def _validate(fresh: Dict[str, Any], prepared: Dict[str, Any]) -> None:
    if fresh.get("schema_version") != 1 or prepared.get("schema_version") != 1:
        raise ValueError("unsupported evidence schema version")
    if prepared.get("evidence_kind") != "single-use-preinitialized":
        raise ValueError("prepared evidence kind is invalid")
    if fresh.get("evidence_class") != prepared.get("evidence_class"):
        raise ValueError("evidence class mismatch")
    if fresh.get("artifact") != prepared.get("artifact") or fresh.get("host_source") != prepared.get("host_source"):
        raise ValueError("artifact or Host identity mismatch")
    if fresh.get("backend") != prepared.get("backend") or fresh.get("environment") != prepared.get("environment"):
        raise ValueError("backend or environment identity mismatch")
    fresh_fixture = fresh.get("fixture", {})
    prepared_fixture = prepared.get("fixture", {})
    for key in ("samples", "capability_operations", "provider_delay_ns_per_operation"):
        if fresh_fixture.get(key) != prepared_fixture.get(key):
            raise ValueError("fixture mismatch")
    if prepared_fixture.get("prepared_capacity") != 1:
        raise ValueError("fixture prepared capacity mismatch")
    if prepared.get("state_copy", {}).get("applicable") is not False:
        raise ValueError("state copy claim is invalid")
    sample_count = fresh_fixture.get("samples")
    fresh_execute = fresh.get("workloads", {}).get("execute", [])
    fresh_capability = fresh.get("workloads", {}).get("capability", [])
    prepared_execute = prepared.get("workloads", {}).get("steady_execute", [])
    prepared_capability = prepared.get("workloads", {}).get("steady_capability", [])
    if not isinstance(sample_count, int) or sample_count < 3 or any(
        len(rows) != sample_count
        for rows in (fresh_execute, fresh_capability, prepared_execute, prepared_capability)
    ):
        raise ValueError("sample count mismatch")
    for rows, metrics in (
        (fresh_execute, ("run_total_ns", "runtime_init_ns")),
        (fresh_capability, ("run_total_ns", "runtime_init_ns", "capability_ns")),
        (prepared_execute, ("run_total_ns", "refill_runtime_init_ns", "retained_guest_memory_bytes")),
        (prepared_capability, ("run_total_ns", "refill_runtime_init_ns", "capability_ns", "retained_guest_memory_bytes")),
    ):
        if any(any(metric not in row for metric in metrics) for row in rows):
            raise ValueError("sample metric missing")
    if "run_total_ns" not in prepared.get("workloads", {}).get("first_execute", {}):
        raise ValueError("first prepared sample missing")


def compare(fresh: Dict[str, Any], prepared: Dict[str, Any]) -> Dict[str, Any]:
    _validate(fresh, prepared)
    fresh_execute = fresh["workloads"]["execute"]
    fresh_capability = fresh["workloads"]["capability"]
    prepared_execute = prepared["workloads"]["steady_execute"]
    prepared_capability = prepared["workloads"]["steady_capability"]
    medians = {
        "fresh_execute_run_total": _median(fresh_execute, "run_total_ns"),
        "fresh_execute_runtime_init": _median(fresh_execute, "runtime_init_ns"),
        "fresh_capability_run_total": _median(fresh_capability, "run_total_ns"),
        "prepared_first_execute_run_total": prepared["workloads"]["first_execute"]["run_total_ns"],
        "prepared_steady_execute_run_total": _median(prepared_execute, "run_total_ns"),
        "prepared_steady_capability_run_total": _median(prepared_capability, "run_total_ns"),
        "prepared_refill_runtime_init": _median(prepared_execute, "refill_runtime_init_ns"),
    }
    return {
        "schema_version": 1,
        "comparison_type": "fresh-vs-single-use-preinitialized-descriptive-no-threshold",
        "evidence_class": fresh["evidence_class"],
        "sample_count": fresh["fixture"]["samples"],
        "identity": _identity(fresh),
        "readiness": prepared["readiness"],
        "state_copy": prepared["state_copy"],
        "retained_guest_memory_bytes": prepared["readiness"]["retained_guest_memory_bytes"],
        "medians_ns": medians,
        "ratios": {
            "first_execute_fresh_over_prepared": round(
                medians["fresh_execute_run_total"] / medians["prepared_first_execute_run_total"], 6
            ),
            "execute_fresh_over_prepared": round(
                medians["fresh_execute_run_total"] / medians["prepared_steady_execute_run_total"], 6
            ),
            "capability_fresh_over_prepared": round(
                medians["fresh_capability_run_total"] / medians["prepared_steady_capability_run_total"], 6
            ),
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("fresh", type=pathlib.Path)
    parser.add_argument("prepared", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    fresh = json.loads(args.fresh.read_text())
    prepared = json.loads(args.prepared.read_text())
    encoded = json.dumps(compare(fresh, prepared), indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded)
    else:
        print(encoded, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
