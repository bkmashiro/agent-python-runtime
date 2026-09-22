#!/usr/bin/env python3
"""Paired opt-in COW data-image measurements inside a Linux allocation."""
import importlib.util
import json
import os
from pathlib import Path
import statistics
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
out = root / ("results-" + os.environ.get("SLURM_JOB_ID", "local"))
out.mkdir(exist_ok=False)
spec = importlib.util.spec_from_file_location("density", root / "run-linux-density.py")
assert spec and spec.loader
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
limits = module.cgroup_memory()
finite = [int(v) for v in limits.values() if v.isdigit() and int(v) < 2**60]
assert finite and min(finite) <= 2 * 1024**3, limits
affinity = sorted(getattr(os, "sched_getaffinity")(0))
assert len(affinity) >= 2
getattr(os, "sched_setaffinity")(0, affinity[:2])
os.environ["GOMAXPROCS"] = "2"
(out / "environment.json").write_text(json.dumps({"cpu_affinity": affinity[:2], "gomaxprocs": 2, "memory_limits": limits}, indent=2))
for repeat in range(3):
    for case in ("python", "tool", "durable"):
        for enabled in ([False, True] if repeat % 2 == 0 else [True, False]):
            name = f"{case}-{repeat}-{str(enabled).lower()}"
            flags = ["-mode", "cow", "-work", "python" if case == "python" else "tools", "-calls", "1"]
            if case == "durable":
                flags = ["-mode", "durable-live", "-prepare", "cow", "-durable-case", "finish", "-work", "tools", "-calls", "1"]
            command = [str(root / "bench"), "-guest", str(root / "pysolate.wasm"), "-n", "200", "-cow-data-image=" + str(enabled).lower(), "-phases", str(out / (name + ".phases.json"))] + flags
            with (out / (name + ".jsonl")).open("w") as stdout, (out / (name + ".stderr")).open("w") as stderr:
                subprocess.run(command, stdout=stdout, stderr=stderr, timeout=180, check=True)
            rows = [json.loads(line) for line in (out / (name + ".jsonl")).read_text().splitlines()]
            runs = [r for r in rows if r.get("kind") == "run"]
            assert len(runs) == 200 and all(r["physical_calls"] == (0 if case == "python" else 1) for r in runs)
            env = next(r for r in rows if r["kind"] == "environment")
            batches = [r for r in rows if r["kind"] == "batch"]
            storage = next(r for r in rows if r["kind"] == "cow_storage")
            record = {"case": case, "repeat": repeat, "enabled": enabled, "median_ns": statistics.median(r["ns"] for r in runs), "setup_ns": env["setup_ns"], "max_batch_pss_kib": max(r.get("pss_kib", 0) for r in batches), "memfd_allocated_bytes": storage["allocated_bytes"], "memfd_count": storage["image_files"]}
            with (out / "summary.jsonl").open("a") as f:
                f.write(json.dumps(record) + "\n")
            print(record, flush=True)
(out / "COMPLETE").write_text("18 processes; 3600 measured requests\n")
