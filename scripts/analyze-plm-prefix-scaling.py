#!/usr/bin/env python3
"""Validate and aggregate PLM prefix source-scaling evidence."""

from __future__ import annotations

import argparse
import json
import math
import pathlib
import random
import statistics
from collections import defaultdict
from typing import Any

SCHEMA = "pysolate.plm-prefix-source-scaling.v1"
ANALYSIS_SCHEMA = "pysolate.plm-prefix-source-scaling.analysis.v1"
CALL_COUNTS = (1, 2, 4, 8)
WINDOWS_MS = (0, 100, 200, 400)
TREATMENTS = ("serial_whole_file", "pysolate_pooled_prefix")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def quantile(values: list[int] | list[float], probability: float) -> float:
    require(bool(values), "quantile requires values")
    ordered = sorted(float(value) for value in values)
    if len(ordered) == 1:
        return ordered[0]
    position = (len(ordered) - 1) * probability
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    fraction = position - lower
    return ordered[lower] * (1.0 - fraction) + ordered[upper] * fraction


def iqr(values: list[int] | list[float]) -> float:
    return quantile(values, 0.75) - quantile(values, 0.25)


def bootstrap_median_interval(values: list[int] | list[float], samples: int, seed: int) -> list[float]:
    require(bool(values), "bootstrap requires values")
    require(samples > 0, "bootstrap sample count must be positive")
    generator = random.Random(seed)
    medians = []
    for _ in range(samples):
        draw = [values[generator.randrange(len(values))] for _ in values]
        medians.append(float(statistics.median(draw)))
    return [quantile(medians, 0.025), quantile(medians, 0.975)]


def metric_summary(pairs: list[tuple[int, int]], bootstrap_samples: int, seed: int) -> dict[str, Any]:
    serial = [left for left, _ in pairs]
    plm = [right for _, right in pairs]
    savings = [left - right for left, right in pairs]
    return {
        "serial_median_nanos": statistics.median(serial),
        "serial_iqr_nanos": iqr(serial),
        "pysolate_median_nanos": statistics.median(plm),
        "pysolate_iqr_nanos": iqr(plm),
        "paired_saving_median_nanos": statistics.median(savings),
        "paired_saving_iqr_nanos": iqr(savings),
        "paired_saving_bootstrap_95_nanos": bootstrap_median_interval(savings, bootstrap_samples, seed),
        "improved_pairs": sum(value > 0 for value in savings),
        "tied_pairs": sum(value == 0 for value in savings),
        "regressed_pairs": sum(value < 0 for value in savings),
    }


def validate_sample(sample: dict[str, Any], calls: int, treatment: str) -> None:
    outcome = sample.get("outcome")
    require(isinstance(outcome, dict), "sample outcome missing")
    assert isinstance(outcome, dict)
    require(outcome.get("FinalProgramOutcome") == "success", "sample outcome failed")
    require(outcome.get("LogicalCalls") == calls and outcome.get("PhysicalAttempts") == calls, "call accounting drift")
    require(isinstance(outcome.get("ResultSHA256"), str) and outcome["ResultSHA256"].startswith("sha256:"), "result identity missing")
    require(int(sample.get("post_begin_nanos", 0)) > 0 and int(sample.get("finalize_nanos", 0)) > 0, "timing missing")
    expected_concurrency = calls if treatment == "pysolate_pooled_prefix" else 1
    require(sample.get("provider_max_concurrent") == expected_concurrency, "provider concurrency drift")
    if treatment == "pysolate_pooled_prefix":
        require(sample.get("prefix_analyzer_invocations") == 1, "prefix analysis count drift")
        split = sample.get("split_phase")
        require(isinstance(split, dict), "split-phase evidence missing")
        assert isinstance(split, dict)
        for key in ("Reused", "MaximumConcurrent", "JobsLinearized", "JobsMaterialized", "LogicalClaims", "PhysicalStarts", "PhysicalFinishes", "Consumed"):
            require(split.get(key) == calls, f"split-phase {key} drift")
        for key in ("Failed", "Cancelled", "Discarded"):
            require(split.get(key) == 0, f"split-phase {key} was nonzero")


def analyze_directory(raw_dir: pathlib.Path, expected_runs: int, bootstrap_samples: int, seed: int) -> dict[str, Any]:
    paths = sorted(raw_dir.glob("*.json"))
    expected_cells = {(calls, window) for calls in CALL_COUNTS for window in WINDOWS_MS}
    require(len(paths) == len(expected_cells), "cell matrix file count drift")
    documents: dict[tuple[int, int], dict[str, Any]] = {}
    identities: set[tuple[str, str, str, str, str]] = set()
    all_samples_valid = True
    cell_rows = []
    total_samples = 0
    total_pairs = 0

    for path in paths:
        document = json.loads(path.read_text(encoding="utf-8"))
        require(document.get("schema_version") == SCHEMA, f"{path.name}: schema drift")
        calls = int(document.get("call_count", -1))
        window = int(document.get("source_window_ms", -1))
        key = (calls, window)
        require(key in expected_cells and key not in documents, "cell matrix identity drift")
        require(document.get("cell_id") == f"calls-{calls}-window-{window}ms", "cell id drift")
        require(document.get("runs") == expected_runs, "run count drift")
        require(document.get("provider_delay_ms") == 200, "provider delay drift")
        require(document.get("source_tree_state") == "clean", "source tree was not clean")
        identities.add((str(document.get("source_commit")), str(document.get("source_tree")), str(document.get("host_id")), str(document.get("artifact_sha256")), str(document.get("source_tree_state"))))
        samples = document.get("samples")
        require(isinstance(samples, list) and len(samples) == expected_runs * len(TREATMENTS), "sample count drift")
        grouped: dict[int, dict[str, dict[str, Any]]] = defaultdict(dict)
        for sample in samples:
            trial = int(sample.get("trial", -1))
            treatment = str(sample.get("treatment"))
            require(0 <= trial < expected_runs and treatment in TREATMENTS, "sample identity drift")
            require(treatment not in grouped[trial], "duplicate treatment in pair")
            validate_sample(sample, calls, treatment)
            grouped[trial][treatment] = sample
        require(set(grouped) == set(range(expected_runs)), "pair trial set drift")
        e2e_pairs: list[tuple[int, int]] = []
        post_pairs: list[tuple[int, int]] = []
        result_hashes = set()
        ready_before_final = []
        for trial in range(expected_runs):
            pair = grouped[trial]
            require(set(pair) == set(TREATMENTS), "paired treatment missing")
            serial, plm = pair[TREATMENTS[0]], pair[TREATMENTS[1]]
            result_hashes.update((serial["outcome"]["ResultSHA256"], plm["outcome"]["ResultSHA256"]))
            e2e_pairs.append((int(serial["post_begin_nanos"]), int(plm["post_begin_nanos"])))
            post_pairs.append((int(serial["finalize_nanos"]), int(plm["finalize_nanos"])))
            ready_before_final.append(int(plm["outcome"].get("ReadyBeforeFinalize", 0)))
        require(len(result_hashes) == 1, "result identity drift")
        cell_seed = seed + calls * 10_000 + window
        cell_rows.append({
            "cell_id": document["cell_id"],
            "call_count": calls,
            "source_window_ms": window,
            "provider_delay_ms": 200,
            "paired_repetitions": expected_runs,
            "result_sha256": next(iter(result_hashes)),
            "pysolate_ready_before_final": ready_before_final,
            "end_to_end": metric_summary(e2e_pairs, bootstrap_samples, cell_seed),
            "post_generation": metric_summary(post_pairs, bootstrap_samples, cell_seed + 1),
        })
        total_samples += len(samples)
        total_pairs += expected_runs
        documents[key] = document

    require(set(documents) == expected_cells, "cell matrix is incomplete")
    require(len(identities) == 1, "source, artifact, or host identity drift")
    source_commit, source_tree, host_id, artifact_sha256, source_tree_state = next(iter(identities))
    cell_rows.sort(key=lambda row: (row["call_count"], row["source_window_ms"]))
    return {
        "schema_version": ANALYSIS_SCHEMA,
        "source_commit": source_commit,
        "source_tree": source_tree,
        "source_tree_state": source_tree_state,
        "host_id": host_id,
        "artifact_sha256": artifact_sha256,
        "call_counts": list(CALL_COUNTS),
        "source_windows_ms": list(WINDOWS_MS),
        "provider_delay_ms": 200,
        "runs_per_arm": expected_runs,
        "bootstrap_samples": bootstrap_samples,
        "bootstrap_seed": seed,
        "cell_count": len(cell_rows),
        "sample_count": total_samples,
        "paired_comparison_count": total_pairs,
        "correctness": {"all_samples_valid": all_samples_valid, "result_identity_per_cell": True, "call_accounting_valid": True, "materialisation_valid": True},
        "cells": cell_rows,
    }


def markdown(summary: dict[str, Any]) -> str:
    lines = [
        "# PLM prefix source-scaling results",
        "",
        f"Host: `{summary['host_id']}`  ",
        f"Source: `{summary['source_commit']}`  ",
        f"Artifact: `{summary['artifact_sha256']}`  ",
        f"Pairs per cell: {summary['runs_per_arm']}",
        "",
        "| Calls | Window (ms) | E2E serial (ms) | E2E PLM (ms) | Paired saving (ms) | 95% bootstrap (ms) | Post-gen saving (ms) |",
        "|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for cell in summary["cells"]:
        e2e = cell["end_to_end"]
        post = cell["post_generation"]
        low, high = e2e["paired_saving_bootstrap_95_nanos"]
        lines.append(
            f"| {cell['call_count']} | {cell['source_window_ms']} | {e2e['serial_median_nanos']/1e6:.3f} | "
            f"{e2e['pysolate_median_nanos']/1e6:.3f} | {e2e['paired_saving_median_nanos']/1e6:.3f} | "
            f"[{low/1e6:.3f}, {high/1e6:.3f}] | {post['paired_saving_median_nanos']/1e6:.3f} |"
        )
    lines.extend(("", "All required sample outcomes, call counts, result identities, provider concurrency and PLM materialisation counters passed validation.", ""))
    return "\n".join(lines)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--raw-dir", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--markdown-output", type=pathlib.Path)
    parser.add_argument("--expected-runs", type=int, default=10)
    parser.add_argument("--bootstrap-samples", type=int, default=10_000)
    parser.add_argument("--seed", type=int, default=20260902)
    args = parser.parse_args()
    summary = analyze_directory(args.raw_dir, args.expected_runs, args.bootstrap_samples, args.seed)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if args.markdown_output:
        args.markdown_output.parent.mkdir(parents=True, exist_ok=True)
        args.markdown_output.write_text(markdown(summary), encoding="utf-8")
    print(json.dumps({"cell_count": summary["cell_count"], "sample_count": summary["sample_count"], "output": str(args.output)}, sort_keys=True))


if __name__ == "__main__":
    main()
