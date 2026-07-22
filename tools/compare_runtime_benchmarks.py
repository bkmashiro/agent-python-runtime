#!/usr/bin/env python3
"""Compare canonical runtime benchmark evidence without inventing thresholds."""

import argparse
import json
import pathlib
import statistics
from typing import Any, Dict, List

METRICS = (
    "instantiate_guest_ns",
    "_initialize_ns",
    "runtime_init_ns",
    "prepare_ns",
    "execute_ns",
    "capability_ns",
    "run_total_ns",
    "request_bytes",
    "result_bytes",
)
WORKLOADS = ("execute", "capability")


def _identity(evidence: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "artifact_sha256": evidence["artifact"]["sha256"],
        "artifact_source_commit": evidence["artifact"]["source_commit"],
        "host_revision": evidence["host_source"]["revision"],
        "environment": evidence["environment"],
    }


def _validate_pair(baseline: Dict[str, Any], candidate: Dict[str, Any]) -> None:
    if baseline.get("schema_version") != 1 or candidate.get("schema_version") != 1:
        raise ValueError("unsupported evidence schema version")
    if baseline.get("evidence_class") != candidate.get("evidence_class"):
        raise ValueError("evidence class mismatch")
    for key in ("backend",):
        if baseline.get(key) != candidate.get(key):
            raise ValueError("backend/reset mismatch")
    for key in ("capability_operations", "provider_delay_ns_per_operation"):
        if baseline.get("fixture", {}).get(key) != candidate.get("fixture", {}).get(key):
            raise ValueError("fixture mismatch")
    for workload in WORKLOADS:
        baseline_rows = baseline.get("workloads", {}).get(workload, [])
        candidate_rows = candidate.get("workloads", {}).get(workload, [])
        if len(baseline_rows) < 3 or len(baseline_rows) != len(candidate_rows):
            raise ValueError("sample count mismatch")
        for row in baseline_rows + candidate_rows:
            if any(metric not in row for metric in METRICS):
                raise ValueError("sample metric missing")


def _median(rows: List[Dict[str, Any]], metric: str) -> Any:
    return statistics.median(row[metric] for row in rows)


def compare(baseline: Dict[str, Any], candidate: Dict[str, Any]) -> Dict[str, Any]:
    _validate_pair(baseline, candidate)
    workloads: Dict[str, Any] = {}
    for workload in WORKLOADS:
        baseline_rows = baseline["workloads"][workload]
        candidate_rows = candidate["workloads"][workload]
        metrics: Dict[str, Any] = {}
        for metric in METRICS:
            baseline_median = _median(baseline_rows, metric)
            candidate_median = _median(candidate_rows, metric)
            ratio = None
            if baseline_median != 0:
                ratio = round(candidate_median / baseline_median, 6)
            metrics[metric] = {
                "baseline_median": baseline_median,
                "candidate_median": candidate_median,
                "candidate_over_baseline": ratio,
            }
        workloads[workload] = metrics
    return {
        "schema_version": 1,
        "comparison_type": "descriptive-no-threshold",
        "evidence_class": baseline["evidence_class"],
        "sample_count": len(baseline["workloads"]["execute"]),
        "baseline": _identity(baseline),
        "candidate": _identity(candidate),
        "compile_once": {
            metric: {
                "baseline": baseline["compile_once"][metric],
                "candidate": candidate["compile_once"][metric],
                "candidate_over_baseline": round(
                    candidate["compile_once"][metric] / baseline["compile_once"][metric], 6
                ),
            }
            for metric in ("instantiate_host_ns", "compile_ns")
        },
        "workloads": workloads,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("baseline", type=pathlib.Path)
    parser.add_argument("candidate", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    baseline = json.loads(args.baseline.read_text())
    candidate = json.loads(args.candidate.read_text())
    encoded = json.dumps(compare(baseline, candidate), indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded)
    else:
        print(encoded, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
