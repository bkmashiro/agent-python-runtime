#!/usr/bin/env python3
"""Validate and aggregate GitChameleon NumPy-derived PLM evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import pathlib
import random
import statistics
from collections import defaultdict
from typing import Any

EVIDENCE_SCHEMA = "pysolate.gitchameleon-numpy-derived-plm.evidence.v1"
MANIFEST_SCHEMA = "pysolate.gitchameleon-numpy-derived-plm.v1"
ANALYSIS_SCHEMA = "pysolate.gitchameleon-numpy-derived-plm.analysis.v1"
TREATMENTS = ("serial_whole_file", "pysolate_pooled_prefix")
RATES = (20, 50, 100, 200)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


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
        "pair_count": len(pairs),
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


def load_manifest(path: pathlib.Path) -> tuple[dict[str, Any], dict[tuple[str, int], dict[str, Any]], str]:
    encoded = path.read_bytes()
    manifest = json.loads(encoded)
    require(manifest.get("schema_version") == MANIFEST_SCHEMA, "manifest schema drift")
    tasks = manifest.get("tasks")
    require(isinstance(tasks, list) and len(tasks) == manifest.get("task_count") == 15, "manifest task count drift")
    require(manifest.get("provider_delay_ms") == 200, "manifest provider delay drift")
    require(manifest.get("mock_stream", {}).get("rates_tokens_per_second") == list(RATES), "manifest rate matrix drift")
    cells: dict[tuple[str, int], dict[str, Any]] = {}
    for task in tasks:
        example_id = str(task.get("example_id"))
        for cell in task.get("cells", []):
            rate = int(cell.get("tokens_per_second", -1))
            key = (example_id, rate)
            require(rate in RATES and key not in cells, "manifest cell identity drift")
            cells[key] = {"task": task, "cell": cell}
    require(len(cells) == 60, "manifest cell matrix drift")
    return manifest, cells, digest(encoded)


def analyze_directory(raw_dir: pathlib.Path, manifest_path: pathlib.Path, expected_runs: int, bootstrap_samples: int, seed: int) -> dict[str, Any]:
    manifest, expected_cells, manifest_sha = load_manifest(manifest_path)
    paths = sorted(raw_dir.glob("*.json"))
    require(len(paths) == len(expected_cells), "evidence cell matrix file count drift")
    documents: dict[tuple[str, int], dict[str, Any]] = {}
    identities: set[tuple[str, str, str, str, str]] = set()
    cell_rows: list[dict[str, Any]] = []
    rate_e2e: dict[int, list[tuple[int, int]]] = defaultdict(list)
    rate_post: dict[int, list[tuple[int, int]]] = defaultdict(list)
    total_samples = 0
    total_pairs = 0

    for path in paths:
        document = json.loads(path.read_text(encoding="utf-8"))
        require(document.get("schema_version") == EVIDENCE_SCHEMA, f"{path.name}: schema drift")
        example_id = str(document.get("example_id"))
        rate = int(document.get("tokens_per_second", -1))
        key = (example_id, rate)
        require(key in expected_cells and key not in documents, "evidence cell identity drift")
        task = expected_cells[key]["task"]
        manifest_cell = expected_cells[key]["cell"]
        calls = len(task["inputs"])
        require(document.get("cell_id") == f"example-{example_id}-rate-{rate}tps", "cell id drift")
        require(document.get("runs") == expected_runs, "run count drift")
        require(document.get("provider_delay_ms") == 200, "provider delay drift")
        require(document.get("source_tree_state") == "clean", "source tree was not clean")
        require(document.get("manifest_sha256") == manifest_sha, "manifest identity drift")
        require(document.get("dataset_name") == manifest["dataset"]["name"], "dataset name drift")
        require(document.get("dataset_commit") == manifest["dataset"]["commit"], "dataset commit drift")
        require(document.get("dataset_sha256") == manifest["dataset"]["sha256"], "dataset hash drift")
        require(document.get("dataset_row") == task["dataset_row"], "dataset row drift")
        require(document.get("target_numpy_version") == task["target_numpy_version"] and document.get("api") == task["api"], "task metadata drift")
        require(document.get("input_count") == calls and document.get("suffix_tokens") == task["suffix_tokens"], "task dimension drift")
        require(document.get("source_window_ms") == manifest_cell["source_window_ms"], "mock stream window drift")
        require(document.get("source_sha256") == task["source_sha256"], "source identity drift")
        require(document.get("mock_stream_tokenizer") == manifest["mock_stream"]["tokenizer"], "tokenizer drift")
        require(document.get("mock_stream_tokenizer_version") == manifest["mock_stream"]["tokenizer_version"], "tokenizer version drift")
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
        result_hashes: set[str] = set()
        ready_before_final: list[int] = []
        for trial in range(expected_runs):
            pair = grouped[trial]
            require(set(pair) == set(TREATMENTS), "paired treatment missing")
            serial, plm = pair[TREATMENTS[0]], pair[TREATMENTS[1]]
            result_hashes.update((serial["outcome"]["ResultSHA256"], plm["outcome"]["ResultSHA256"]))
            e2e = (int(serial["post_begin_nanos"]), int(plm["post_begin_nanos"]))
            post = (int(serial["finalize_nanos"]), int(plm["finalize_nanos"]))
            e2e_pairs.append(e2e)
            post_pairs.append(post)
            rate_e2e[rate].append(e2e)
            rate_post[rate].append(post)
            ready_before_final.append(int(plm["outcome"].get("ReadyBeforeFinalize", 0)))
        require(len(result_hashes) == 1, "result identity drift")
        cell_seed = seed + int(example_id) * 1_000 + rate
        cell_rows.append({
            "cell_id": document["cell_id"],
            "example_id": example_id,
            "dataset_row": task["dataset_row"],
            "api": task["api"],
            "target_numpy_version": task["target_numpy_version"],
            "input_count": calls,
            "suffix_tokens": task["suffix_tokens"],
            "tokens_per_second": rate,
            "source_window_ms": manifest_cell["source_window_ms"],
            "paired_repetitions": expected_runs,
            "result_sha256": next(iter(result_hashes)),
            "pysolate_ready_before_final": ready_before_final,
            "end_to_end": metric_summary(e2e_pairs, bootstrap_samples, cell_seed),
            "post_generation": metric_summary(post_pairs, bootstrap_samples, cell_seed + 1),
        })
        total_samples += len(samples)
        total_pairs += expected_runs
        documents[key] = document

    require(set(documents) == set(expected_cells), "evidence cell matrix is incomplete")
    require(len(identities) == 1, "source, artifact, or host identity drift")
    source_commit, source_tree, host_id, artifact_sha256, source_tree_state = next(iter(identities))
    cell_rows.sort(key=lambda row: (int(row["example_id"]), row["tokens_per_second"]))
    rate_rows = []
    for rate in RATES:
        require(len(rate_e2e[rate]) == 15 * expected_runs, "rate pair count drift")
        rate_rows.append({
            "tokens_per_second": rate,
            "source_window_ms_range": [min(row["source_window_ms"] for row in cell_rows if row["tokens_per_second"] == rate), max(row["source_window_ms"] for row in cell_rows if row["tokens_per_second"] == rate)],
            "end_to_end": metric_summary(rate_e2e[rate], bootstrap_samples, seed + rate),
            "post_generation": metric_summary(rate_post[rate], bootstrap_samples, seed + rate + 1),
        })
    return {
        "schema_version": ANALYSIS_SCHEMA,
        "source_commit": source_commit,
        "source_tree": source_tree,
        "source_tree_state": source_tree_state,
        "host_id": host_id,
        "artifact_sha256": artifact_sha256,
        "manifest_sha256": manifest_sha,
        "dataset": manifest["dataset"],
        "task_count": 15,
        "rates_tokens_per_second": list(RATES),
        "provider_delay_ms": 200,
        "runs_per_arm": expected_runs,
        "bootstrap_samples": bootstrap_samples,
        "bootstrap_seed": seed,
        "cell_count": len(cell_rows),
        "sample_count": total_samples,
        "paired_comparison_count": total_pairs,
        "correctness": {"all_samples_valid": True, "result_identity_per_cell": True, "call_accounting_valid": True, "materialisation_valid": True, "manifest_identity_valid": True},
        "rates": rate_rows,
        "cells": cell_rows,
    }


def markdown(summary: dict[str, Any]) -> str:
    lines = [
        "# GitChameleon NumPy-derived PLM results",
        "",
        f"Host: `{summary['host_id']}`  ",
        f"Source: `{summary['source_commit']}`  ",
        f"Artifact: `{summary['artifact_sha256']}`  ",
        f"Dataset: `{summary['dataset']['commit']}` / `{summary['dataset']['sha256']}`  ",
        f"Tasks: {summary['task_count']}; pairs per task/rate cell: {summary['runs_per_arm']}",
        "",
        "| Mock rate (tokens/s) | Window range (ms) | Pairs | E2E serial (ms) | E2E PLM (ms) | Paired saving (ms) | 95% bootstrap (ms) | Post-gen saving (ms) |",
        "|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for row in summary["rates"]:
        e2e = row["end_to_end"]
        post = row["post_generation"]
        low, high = e2e["paired_saving_bootstrap_95_nanos"]
        window_low, window_high = row["source_window_ms_range"]
        lines.append(
            f"| {row['tokens_per_second']} | {window_low}–{window_high} | {e2e['pair_count']} | "
            f"{e2e['serial_median_nanos']/1e6:.3f} | {e2e['pysolate_median_nanos']/1e6:.3f} | "
            f"{e2e['paired_saving_median_nanos']/1e6:.3f} | [{low/1e6:.3f}, {high/1e6:.3f}] | "
            f"{post['paired_saving_median_nanos']/1e6:.3f} |"
        )
    lines.extend(("", "All required outcomes, dataset/source identities, paired arms, call counts, provider concurrency and PLM materialisation counters passed validation.", ""))
    return "\n".join(lines)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--raw-dir", required=True, type=pathlib.Path)
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--markdown-output", type=pathlib.Path)
    parser.add_argument("--expected-runs", type=int, default=10)
    parser.add_argument("--bootstrap-samples", type=int, default=10_000)
    parser.add_argument("--seed", type=int, default=20260903)
    args = parser.parse_args()
    summary = analyze_directory(args.raw_dir, args.manifest, args.expected_runs, args.bootstrap_samples, args.seed)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if args.markdown_output:
        args.markdown_output.parent.mkdir(parents=True, exist_ok=True)
        args.markdown_output.write_text(markdown(summary), encoding="utf-8")
    print(json.dumps({"cell_count": summary["cell_count"], "sample_count": summary["sample_count"], "output": str(args.output)}, sort_keys=True))


if __name__ == "__main__":
    main()
