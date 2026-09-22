#!/usr/bin/env python3
"""Bounded Linux profiling campaign. Run only inside an allocated job."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
out = root / ("results-" + os.environ.get("SLURM_JOB_ID", "local"))
out.mkdir(exist_ok=False)
spec = importlib.util.spec_from_file_location("density", root / "run-linux-density.py")
assert spec is not None and spec.loader is not None
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
limits = module.cgroup_memory()
finite = [int(v) for v in limits.values() if v.isdigit() and int(v) < 2**60]
assert finite and min(finite) <= 2 * 1024**3, limits
affinity = sorted(getattr(os, "sched_getaffinity")(0))
assert len(affinity) >= 2
getattr(os, "sched_setaffinity")(0, affinity[:2])
os.environ["GOMAXPROCS"] = "2"
metadata = {"platform": platform.platform(), "cpu_affinity": sorted(getattr(os, "sched_getaffinity")(0)),
            "gomaxprocs": 2, "memory_limits": limits,
            "sha256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                       for p in [root / "bench", root / "baseline", root / "queue", root / "pysolate.wasm"]}}
(out / "environment.json").write_text(json.dumps(metadata, indent=2) + "\n")


def run(name, binary, flags, count, queue=False):
    cmd = [str(root / binary), "-guest", str(root / "pysolate.wasm"), *flags]
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=240)
    (out / (name + ".jsonl")).write_text(p.stdout)
    (out / (name + ".stderr")).write_text(p.stderr)
    rows = [json.loads(line) for line in p.stdout.splitlines()]
    assert p.returncode == 0, (name, p.returncode, p.stderr)
    if queue:
        assert len(rows) == 1 and rows[0]["completed"] == count and rows[0]["errors"] == 0
    else:
        requests = [r for r in rows if r.get("kind") == "run"]
        assert len(requests) == count and not any(r.get("error") for r in requests), (name, rows[-2:])
    with (out / "index.jsonl").open("a") as f:
        f.write(json.dumps({"name": name, "command": cmd, "completed": count}) + "\n")
    print(name, "ok", flush=True)


cases = {
    "python": ["-mode", "cow", "-work", "python"],
    "tool": ["-mode", "cow", "-work", "tools", "-calls", "1"],
    "durable": ["-mode", "durable-live", "-prepare", "cow", "-work", "tools", "-calls", "1", "-durable-case", "finish"],
}
# Unprofiled latency and phase-only perturbation, alternating order.
for rep in range(3):
    for case, flags in cases.items():
        variants = ["off", "phases"] if case == "durable" else ["baseline", "off", "phases"]
        if rep % 2:
            variants.reverse()
        for variant in variants:
            name = f"{case}-{variant}-{rep}"
            extra = ["-phases", str(out / (name + ".phases.json"))] if variant == "phases" else []
            run(name, "baseline" if variant == "baseline" else "bench", flags + ["-n", "200"] + extra, 200)
# CPU and allocation attribution collected in separate processes.
for case, flags in cases.items():
    name = case + "-cpu"
    run(name, "bench", flags + ["-n", "1000", "-measured-cpuprofile", str(out / (name + ".pprof"))], 1000)
    name = case + "-alloc"
    run(name, "bench", flags + ["-n", "500", "-allocprofile", str(out / (name + ".alloc.json"))], 500)
# Slow tools through the real bounded executor; compare inclusive phases and waits.
for external in [False, True]:
    flags = ["-case", "read-finish", "-mode", "executor", "-tasks", "64", "-active", "2",
             "-resident", "8", "-tool-active", "8", "-heap", "8", "-hold", "50ms", "-warmup", "2",
             "-cow=true", "-external-io=" + str(external).lower()]
    for kind, flag, suffix in [("phases", "-phases", ".phases.json"), ("trace", "-traceprofile", ".trace"),
                               ("cpu", "-measured-cpuprofile", ".pprof")]:
        name = f"queue-{str(external).lower()}-{kind}"
        run(name, "queue", flags + [flag, str(out / (name + suffix))], 64, queue=True)
(out / "COMPLETE").write_text("36 processes completed\n")
