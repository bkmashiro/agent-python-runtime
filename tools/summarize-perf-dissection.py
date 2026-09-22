#!/usr/bin/env python3
"""Summarize performance dissection rows without treating nested spans as additive."""
import json
from pathlib import Path
import statistics
import sys

root = Path(sys.argv[1])
index = [json.loads(line) for line in (root / "index.jsonl").read_text().splitlines()]
assert len(index) == 36 and len({r["name"] for r in index}) == 36
summary = {"processes": len(index), "measured_requests": sum(r["completed"] for r in index),
           "cases": {}, "queue": {}}
for case in ("python", "tool", "durable"):
    variants = {}
    for variant in ("baseline", "off", "phases"):
        medians = []
        for path in sorted(root.glob(f"{case}-{variant}-*.jsonl")):
            rows = [json.loads(line) for line in path.read_text().splitlines()]
            runs = [r for r in rows if r.get("kind") == "run"]
            assert len(runs) == 200 and not any(r.get("error") for r in runs)
            assert all(r["physical_calls"] == (0 if case == "python" else 1) for r in runs)
            medians.append(statistics.median(r["ns"] / 1e6 for r in runs))
        if medians:
            assert len(medians) == 3
            variants[variant] = {"process_medians_ms": medians, "median_ms": statistics.median(medians)}
    phases = {}
    for path in sorted(root.glob(f"{case}-phases-*.phases.json")):
        for r in json.loads(path.read_text())["phases"]:
            phases.setdefault(r["phase"], []).append(r["total_ns"] / 200 / 1e6)
    alloc = json.loads((root / (case + "-alloc.alloc.json")).read_text())["measured_delta"]
    summary["cases"][case] = {"latency": variants, "phase_mean_ms_per_request":
        {k: statistics.median(v) for k, v in phases.items()},
        "allocated_bytes_per_request": alloc["total_alloc_bytes"] / 500,
        "allocations_per_request": alloc["mallocs"] / 500}
for arm in ("false", "true"):
    row = json.loads((root / f"queue-{arm}-phases.jsonl").read_text())
    assert row["completed"] == row["tool_dispatches"] == 64 and row["errors"] == 0
    phases = json.loads((root / f"queue-{arm}-phases.phases.json").read_text())["phases"]
    summary["queue"][arm] = {"batch_ms": row["batch_ns"] / 1e6,
        "phase_mean_ms_per_request": {r["phase"]: r["total_ns"] / 64 / 1e6 for r in phases}}
encoded = json.dumps(summary, indent=2) + "\n"
(root / "summary.json").write_text(encoded)
print(encoded)
