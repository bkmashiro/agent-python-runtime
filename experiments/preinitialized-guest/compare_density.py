#!/usr/bin/env python3
"""Compare exact canonical lifecycle-density evidence for a Wizer candidate."""

from __future__ import annotations

import argparse
import json
import pathlib
import statistics


CANONICAL_SLOTS = [1, 2, 4, 8, 16]


def _measured(sample: dict, section: str, metric: str) -> int:
    value = sample.get(section, {}).get(metric, {})
    if value.get("status") != "measured" or not isinstance(value.get("value"), int):
        raise ValueError(f"{section}.{metric} must be a measured integer")
    return value["value"]


def _validate_document(document: dict, label: str) -> None:
    if document.get("schema_version") != 1 or document.get("evidence_class") != "lifecycle-density":
        raise ValueError(f"{label} is not lifecycle-density v1 evidence")
    artifact = document.get("artifact", {})
    host_source = document.get("host_source", {})
    if artifact.get("artifact_profile") != "base" or artifact.get("target") != "wasm32-wasip1":
        raise ValueError(f"{label} artifact profile or target drifted")
    if artifact.get("source_commit") != host_source.get("revision") or host_source.get("modified") is not False:
        raise ValueError(f"{label} artifact and clean Host revision are not exact")
    strategy = document.get("strategy", {})
    if strategy != {
        "requested": "single-use-preinitialized",
        "active": "single-use-preinitialized",
        "fallback": False,
    }:
        raise ValueError(f"{label} strategy drifted or fell back")
    plan = document.get("plan", {})
    if plan.get("slot_counts") != CANONICAL_SLOTS or plan.get("repeats_per_slot") != 3:
        raise ValueError(f"{label} plan is not the canonical three-repeat sweep")
    samples = document.get("samples")
    if not isinstance(samples, list) or len(samples) != len(CANONICAL_SLOTS) * 3:
        raise ValueError(f"{label} sample count is incomplete")
    expected = [slot for slot in CANONICAL_SLOTS for _ in range(3)]
    if [sample.get("requested_slots") for sample in samples] != expected:
        raise ValueError(f"{label} sample order drifted")


def _medians(document: dict, slots: int) -> dict[str, int]:
    samples = [sample for sample in document["samples"] if sample["requested_slots"] == slots]
    selectors = {
        "ready_wall_median_ns": ("phases", "total_ns"),
        "instantiate_median_ns": ("phases", "instantiate_ns"),
        "runtime_init_median_ns": ("phases", "runtime_init_ns"),
        "ready_rss_median_bytes": ("process", "rss_bytes"),
    }
    return {
        name: int(statistics.median(_measured(sample, section, metric) for sample in samples))
        for name, (section, metric) in selectors.items()
    }


def _ratio(numerator: int, denominator: int, label: str) -> float:
    if denominator <= 0:
        raise ValueError(f"{label} denominator must be positive")
    return numerator / denominator


def compare(baseline: dict, candidate: dict) -> dict:
    _validate_document(baseline, "baseline")
    _validate_document(candidate, "candidate")
    for key in ("host_source", "backend", "environment", "strategy", "plan"):
        if baseline.get(key) != candidate.get(key):
            raise ValueError(f"baseline/candidate {key} drifted")
    if baseline["artifact"]["sha256"] == candidate["artifact"]["sha256"]:
        raise ValueError("baseline and candidate artifacts must be distinct")
    if not any("Preinitialization-spike" in item for item in candidate.get("limitations", [])):
        raise ValueError("candidate lacks the preinitialization-spike limitation")

    rows = []
    for slots in CANONICAL_SLOTS:
        base = _medians(baseline, slots)
        transformed = _medians(candidate, slots)
        rows.append(
            {
                "requested_slots": slots,
                "baseline": base,
                "candidate": transformed,
                "ready_wall_speedup": _ratio(
                    base["ready_wall_median_ns"], transformed["ready_wall_median_ns"], "ready wall"
                ),
                "runtime_init_speedup": _ratio(
                    base["runtime_init_median_ns"], transformed["runtime_init_median_ns"], "runtime init"
                ),
                "instantiate_ratio_candidate_over_baseline": _ratio(
                    transformed["instantiate_median_ns"], base["instantiate_median_ns"], "instantiate"
                ),
                "ready_rss_ratio_candidate_over_baseline": _ratio(
                    transformed["ready_rss_median_bytes"], base["ready_rss_median_bytes"], "ready RSS"
                ),
                "ready_rss_delta_bytes": transformed["ready_rss_median_bytes"] - base["ready_rss_median_bytes"],
            }
        )

    return {
        "schema_version": 1,
        "experiment": "build-time-python-preinitialization-lifecycle-density",
        "comparison": "descriptive",
        "baseline_artifact": baseline["artifact"],
        "candidate_artifact": candidate["artifact"],
        "host_source": baseline["host_source"],
        "backend": baseline["backend"],
        "environment": baseline["environment"],
        "plan": baseline["plan"],
        "per_slot": rows,
        "headline_n16": rows[-1],
        "limitations": [
            "This exact comparison is exploratory and has no production approval threshold.",
            "Ready wall includes concurrent shard compilation and slot initialization; phase sums are diagnostic work totals, not sequential wall components.",
            "Ready RSS is process-level at the idle-ready observation and does not prove cross-process page sharing or multi-tenant hash-seed safety.",
        ],
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", required=True, type=pathlib.Path)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    report = compare(
        json.loads(args.baseline.read_text()),
        json.loads(args.candidate.read_text()),
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    print(json.dumps({"comparison": report["comparison"], "output": str(args.output)}, sort_keys=True))


if __name__ == "__main__":
    main()
