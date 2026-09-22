#!/usr/bin/env python3
"""Paired Linux durable-execution measurements; no runtime policy changes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import time


def cells():
    # Main delay comparison, then a small residency sweep at the I/O-heavy point.
    return [(8, "0s"), (8, "50ms"), (8, "200ms"), (2, "200ms"), (4, "200ms")]


def cgroup_memory():
    """Record effective hard limits visible in this process's cgroup ancestry."""
    found = {}
    for line in Path("/proc/self/cgroup").read_text().splitlines():
        _, controllers, relative = line.split(":", 2)
        if not controllers:
            root, filename = Path("/sys/fs/cgroup"), "memory.max"
        elif "memory" in controllers.split(","):
            root, filename = Path("/sys/fs/cgroup/memory"), "memory.limit_in_bytes"
        else:
            continue
        node = root / relative.lstrip("/")
        while node == root or root in node.parents:
            limit = node / filename
            if limit.exists():
                found[str(limit)] = limit.read_text().strip()
            if node == root:
                break
            node = node.parent
    return found


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--guest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repeats", type=int, default=5)
    parser.add_argument("--smoke", action="store_true")
    args = parser.parse_args()
    if platform.system() != "Linux" or args.repeats < 1:
        parser.error("requires Linux and positive repeats")
    args.output.mkdir(parents=True, exist_ok=True)
    raw_path = args.output / "runs.jsonl"
    if raw_path.exists():
        parser.error("output already contains runs.jsonl; choose a new directory")
    limits = cgroup_memory()
    finite = [int(v) for v in limits.values() if v.isdigit()]
    effective = min(finite) if finite else None
    if effective is None or effective > 2 * 1024**3:
        parser.error(f"requires verified cgroup memory cap <= 2 GiB, observed {limits}")
    original_affinity = sorted(getattr(os, "sched_getaffinity")(0))
    if len(original_affinity) < 2:
        parser.error("requires at least two allocated logical CPUs")
    getattr(os, "sched_setaffinity")(0, original_affinity[:2])
    metadata = {
        "allocated_cpu_affinity": original_affinity,
        "platform": platform.platform(), "machine": platform.machine(),
        "cpu_affinity": sorted(getattr(os, "sched_getaffinity")(0)),
        "page_size": os.sysconf("SC_PAGE_SIZE"),
        "cgroup_memory_limits": limits, "effective_memory_limit_bytes": effective,
        "slurm_job_id": os.environ.get("SLURM_JOB_ID"),
        "gomaxprocs": 2,
        "guest_sha256": hashlib.sha256(args.guest.read_bytes()).hexdigest(),
        "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
        "repeats": args.repeats, "smoke": args.smoke,
        "lifecycle": "fresh process per arm; two warmup runs; batch excludes setup and task creation; request latency includes queue",
    }
    (args.output / "environment.json").write_text(json.dumps(metadata, indent=2) + "\n")
    plan = [(8, "50ms")] if args.smoke else cells()
    failures = 0
    with raw_path.open("x") as raw:
        for repeat in range(args.repeats):
            for resident, delay in plan:
                for external in ([False, True] if repeat % 2 == 0 else [True, False]):
                    command = [str(args.binary.resolve()), "-guest", str(args.guest.resolve()),
                               "-case", "read-finish", "-mode", "executor", "-tasks", "32",
                               "-active", "2", "-resident", str(resident), "-tool-active", "8",
                               "-heap", "8", "-hold", delay, "-cow=true", "-warmup", "2",
                               f"-external-io={str(external).lower()}"]
                    row = {"repeat": repeat, "resident": resident, "delay": delay,
                           "external_io": external, "command": command}
                    begin = time.monotonic()
                    try:
                        result = subprocess.run(command, capture_output=True, text=True, timeout=180,
                                                env={**os.environ, "GOMAXPROCS": "2"})
                        row.update(exit_code=result.returncode, stderr=result.stderr)
                        if result.returncode == 0:
                            row["measurement"] = json.loads(result.stdout)
                            if row["measurement"].get("completed") != 32:
                                raise ValueError("completed count differs from 32")
                        else:
                            row["stdout"] = result.stdout
                            failures += 1
                    except (subprocess.TimeoutExpired, ValueError) as exc:
                        row["error"] = str(exc)
                        failures += 1
                    row["process_wall_s"] = time.monotonic() - begin
                    raw.write(json.dumps(row) + "\n")
                    raw.flush()
                    print(f"repeat={repeat} resident={resident} delay={delay} external={external} exit={row.get('exit_code')} error={row.get('error')}", flush=True)
    if failures:
        raise SystemExit(f"{failures} failed cells; retained in {raw_path}")


if __name__ == "__main__":
    main()
