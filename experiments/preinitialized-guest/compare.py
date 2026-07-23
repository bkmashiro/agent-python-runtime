#!/usr/bin/env python3
"""Compare exact baseline and build-time-preinitialized runtime evidence."""

from __future__ import annotations

import argparse
import json
import pathlib
import statistics
import tempfile
from typing import Any

_MAX_CANDIDATE_BYTES = 512 * 1024 * 1024


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def samples(evidence: dict[str, Any]) -> list[dict[str, Any]]:
    workloads = evidence.get("workloads")
    if not isinstance(workloads, dict):
        raise ValueError("runtime evidence lacks workloads")
    rows: list[dict[str, Any]] = []
    for name in ("execute", "capability"):
        group = workloads.get(name)
        if not isinstance(group, list) or len(group) != 3:
            raise ValueError(f"{name} must contain exactly three samples")
        for sample in group:
            require(isinstance(sample, dict), f"{name} contains a malformed sample")
            for field in ("runtime_init_ns", "run_total_ns"):
                require(isinstance(sample.get(field), int) and sample[field] > 0, f"{name} sample lacks positive {field}")
            rows.append(sample)
    return rows


def median(rows: list[dict[str, Any]], field: str) -> float:
    return float(statistics.median(row[field] for row in rows))


def compare(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    receipt: dict[str, Any],
) -> dict[str, Any]:
    require(receipt.get("schema_version") == 1, "unsupported transform receipt")
    for label, evidence in (("baseline", baseline), ("candidate", candidate)):
        require(evidence.get("schema_version") == 1, f"{label} runtime evidence schema drifted")
        require(evidence.get("evidence_class") == "preinitialization-spike", f"{label} evidence class drifted")
        require(evidence.get("backend") == {"name": "wazero", "reset_mode": "fresh-instance"}, f"{label} backend drifted")
        require(evidence.get("host_source", {}).get("modified") is False, f"{label} Host source is dirty")
    require(baseline.get("host_source") == candidate.get("host_source"), "Host source differs between lanes")
    require(baseline.get("environment") == candidate.get("environment"), "environment differs between lanes")
    require(baseline.get("fixture") == candidate.get("fixture"), "fixture differs between lanes")
    require(baseline.get("host_source", {}).get("revision") == receipt.get("host_revision"), "transform receipt Host revision drifted")
    require(baseline.get("artifact", {}).get("sha256") == receipt.get("input", {}).get("sha256"), "baseline artifact identity does not match transform input")
    require(candidate.get("artifact", {}).get("sha256") == receipt.get("candidate", {}).get("sha256"), "candidate artifact identity does not match transform receipt")
    require(baseline.get("artifact", {}).get("source_commit") == candidate.get("artifact", {}).get("source_commit"), "Guest source commit differs between lanes")

    baseline_rows = samples(baseline)
    candidate_rows = samples(candidate)
    baseline_runtime_init = median(baseline_rows, "runtime_init_ns")
    candidate_runtime_init = median(candidate_rows, "runtime_init_ns")
    baseline_total = median(baseline_rows, "run_total_ns")
    candidate_total = median(candidate_rows, "run_total_ns")
    runtime_init_speedup = baseline_runtime_init / candidate_runtime_init
    run_total_speedup = baseline_total / candidate_total
    candidate_size = receipt.get("candidate", {}).get("size_bytes")
    require(isinstance(candidate_size, int) and candidate_size > 0, "candidate size is invalid")

    criteria = {
        "repeat_deterministic": receipt.get("repeat_deterministic") is True,
        "complete_exact_samples": len(baseline_rows) == len(candidate_rows) == 6,
        "runtime_init_speedup_at_least_10x": runtime_init_speedup >= 10.0,
        "run_total_speedup_at_least_2x": run_total_speedup >= 2.0,
        "candidate_within_512_mib": candidate_size <= _MAX_CANDIDATE_BYTES,
    }
    if all(criteria.values()):
        verdict = "validated"
    elif runtime_init_speedup > 1.0 and run_total_speedup > 1.0:
        verdict = "partial"
    else:
        verdict = "invalidated"
    return {
        "schema_version": 1,
        "experiment": "build-time-python-preinitialization",
        "verdict": verdict,
        "host_revision": receipt["host_revision"],
        "baseline_artifact": baseline["artifact"],
        "candidate_artifact": candidate["artifact"],
        "tool": receipt["tool"],
        "criteria": criteria,
        "metrics": {
            "baseline_runtime_init_median_ns": int(baseline_runtime_init),
            "candidate_runtime_init_median_ns": int(candidate_runtime_init),
            "runtime_init_speedup": runtime_init_speedup,
            "baseline_run_total_median_ns": int(baseline_total),
            "candidate_run_total_median_ns": int(candidate_total),
            "run_total_speedup": run_total_speedup,
            "baseline_artifact_size_bytes": baseline["artifact"]["size_bytes"],
            "candidate_artifact_size_bytes": candidate["artifact"]["size_bytes"],
        },
        "limitations": [
            "This is an exploratory build-time preinitialization candidate, not a production or default artifact.",
            "The comparison proves fresh-instance execution and timing only; it does not prove session restore or post-request reset safety.",
        ],
    }


def load_json(path: pathlib.Path) -> dict[str, Any]:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def write_json_atomic(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    encoded = json.dumps(value, indent=2, sort_keys=True) + "\n"
    with tempfile.NamedTemporaryFile("w", dir=path.parent, delete=False) as handle:
        handle.write(encoded)
        temporary = pathlib.Path(handle.name)
    temporary.replace(path)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", required=True, type=pathlib.Path)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--receipt", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    report = compare(load_json(args.baseline), load_json(args.candidate), load_json(args.receipt))
    write_json_atomic(args.output, report)
    print(json.dumps({"output": str(args.output), "verdict": report["verdict"]}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
