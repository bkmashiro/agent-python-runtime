#!/usr/bin/env python3
"""Summarize paired process repeats, not pooled request samples."""
import argparse
from collections import defaultdict
import json
import math
from pathlib import Path
import statistics


def summarize(rows, repeats):
    groups = defaultdict(dict)
    for row in rows:
        if row.get("exit_code") != 0 or row.get("error"):
            raise ValueError("campaign contains failed cell")
        m = row["measurement"]
        if not (m["completed"] == m["result_count"] == m["tool_dispatches"] == m["tasks"] == 32 and m["errors"] == 0):
            raise ValueError("task accounting failed")
        if len(m["request_ns"]) != m["tasks"] or m["batch_ns"] <= 0:
            raise ValueError("invalid latency accounting")
        key = (row["resident"], row["delay"])
        identity = (row["repeat"], row["external_io"])
        if identity in groups[key]:
            raise ValueError("duplicate cell")
        groups[key][identity] = m
    result = []
    for (resident, delay), pairs in groups.items():
        if set(pairs) != {(r, arm) for r in range(repeats) for arm in (False, True)}:
            raise ValueError("missing paired repetitions")
        entry = {"resident_limit": resident, "delay": delay, "repeats": repeats}
        for arm, name in [(False, "inline"), (True, "external_io")]:
            runs = [pairs[r, arm] for r in range(repeats)]
            metrics = {
                "throughput_tasks_s": [m["tasks"] * 1e9 / m["batch_ns"] for m in runs],
                "batch_ms": [m["batch_ns"] / 1e6 for m in runs],
                "request_p95_ms": [sorted(m["request_ns"])[math.ceil(.95 * m["tasks"]) - 1] / 1e6 for m in runs],
                "sampled_peak_pss_mib": [m["sampled_peak_pss_kib"] / 1024 for m in runs],
                "sampled_peak_rss_mib": [m["sampled_peak_rss_kib"] / 1024 for m in runs],
                "sampled_peak_private_dirty_mib": [m["sampled_peak_private_dirty_kib"] / 1024 for m in runs],
                "observed_resident_peak": [m["peak_executor_resident"] for m in runs],
            }
            entry[name] = {k: {"median": statistics.median(v), "min": min(v), "max": max(v)} for k, v in metrics.items()}
        ratios = [pairs[r, False]["batch_ns"] / pairs[r, True]["batch_ns"] for r in range(repeats)]
        entry["paired_throughput_ratio"] = {"median": statistics.median(ratios), "min": min(ratios), "max": max(ratios)}
        result.append(entry)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    environment = json.loads((args.directory / "environment.json").read_text())
    rows = [json.loads(line) for line in (args.directory / "runs.jsonl").read_text().splitlines()]
    expected = (1 if environment["smoke"] else 5) * 2 * environment["repeats"]
    if len(rows) != expected:
        raise ValueError(f"expected {expected} cells, got {len(rows)}")
    output = {"process_runs": len(rows), "completed_tasks": sum(r["measurement"]["completed"] for r in rows),
              "statistics": "median across independent process runs; p95 nearest-rank within each run; speedup paired by repetition",
              "cells": summarize(rows, environment["repeats"])}
    text = json.dumps(output, indent=2) + "\n"
    (args.directory / "summary.json").write_text(text)
    print(text)


if __name__ == "__main__":
    main()
