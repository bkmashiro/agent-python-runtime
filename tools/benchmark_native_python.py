#!/usr/bin/env python3
"""Measure the exact execute fixture on cold and persistent native CPython.

The parent mode invokes a selected Python executable. The private worker mode
imports the checked-in Guest bootstrap, initializes and prepares it with the
same source used by apyrun-benchmark, then exchanges one JSON request per line.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import sys
import time
from typing import Any

SCHEMA_VERSION = 1
FIXTURES = {
    "basic": {
        "prepare_source": "prepared = 41",
        "execute_source": 'result = {"prepared": prepared, "sum": sum(range(inputs["integer_work"]))}',
        "expected_result": {"prepared": 41, "sum": 499500},
    },
    "numpy-import": {
        "prepare_source": "import numpy as np\nprepared = 41",
        "execute_source": 'result = {"prepared": prepared, "numpy_version": np.__version__, "sum": int(np.arange(inputs["integer_work"]).sum())}',
        "expected_result": {"prepared": 41, "numpy_version": "2.5.1", "sum": 499500},
    },
}


def elapsed_ns(start: int) -> int:
    return time.perf_counter_ns() - start


def percentile(values: list[int], percent: int) -> int:
    if not values:
        raise ValueError("percentile requires samples")
    ordered = sorted(values)
    rank = (percent * len(ordered) + 99) // 100
    return ordered[max(1, rank) - 1]


def summarize(values: list[int]) -> dict[str, int]:
    return {
        "median_ns": int(statistics.median(values)),
        "p95_ns": percentile(values, 95),
        "p99_ns": percentile(values, 99),
        "min_ns": min(values),
        "max_ns": max(values),
    }


def request(sample: int, fixture: dict[str, Any]) -> str:
    return json.dumps(
        {
            "run_id": f"native-execute-{sample}",
            "code": fixture["execute_source"],
            "inputs": {"integer_work": 1000},
        },
        separators=(",", ":"),
        sort_keys=True,
    )


def worker(bootstrap: Path, fixture: dict[str, Any]) -> int:
    started = time.perf_counter_ns()
    sys.path.insert(0, str(bootstrap))
    import agent_runtime  # pylint: disable=import-outside-toplevel

    import_ns = elapsed_ns(started)
    started = time.perf_counter_ns()
    agent_runtime._initialize("{}")
    initialize_ns = elapsed_ns(started)
    started = time.perf_counter_ns()
    agent_runtime._prepare(fixture["prepare_source"])
    prepare_ns = elapsed_ns(started)
    print(
        json.dumps(
            {
                "kind": "ready",
                "import_ns": import_ns,
                "initialize_ns": initialize_ns,
                "prepare_ns": prepare_ns,
            },
            separators=(",", ":"),
        ),
        flush=True,
    )
    for line in sys.stdin:
        started = time.perf_counter_ns()
        encoded = agent_runtime._execute(line.rstrip("\n"))
        execute_ns = elapsed_ns(started)
        response = json.loads(encoded)
        print(json.dumps({"kind": "result", "execute_ns": execute_ns, "response": response}, separators=(",", ":")), flush=True)
    return 0


def parse_worker_output(text: str, expected_results: int, expected_result: dict[str, Any]) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    records = [json.loads(line) for line in text.splitlines() if line]
    if len(records) != expected_results + 1 or records[0].get("kind") != "ready":
        raise RuntimeError(f"invalid worker protocol: {records!r}")
    results = records[1:]
    for record in results:
        response = record.get("response", {})
        if record.get("kind") != "result" or response.get("status") != "ok" or response.get("result") != expected_result:
            raise RuntimeError(f"native fixture result mismatch: {record!r}")
    return records[0], results


def command(python: Path, script: Path, bootstrap: Path, fixture_name: str) -> list[str]:
    return [str(python), "-I", "-u", str(script), "--worker", "--bootstrap", str(bootstrap), "--fixture", fixture_name]


def cold_samples(python: Path, script: Path, bootstrap: Path, fixture_name: str, fixture: dict[str, Any], samples: int, timeout: float) -> tuple[list[dict[str, Any]], list[int]]:
    records: list[dict[str, Any]] = []
    totals: list[int] = []
    for sample in range(samples):
        started = time.perf_counter_ns()
        process = subprocess.run(
            command(python, script, bootstrap, fixture_name),
            input=request(sample, fixture) + "\n",
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
        total_ns = elapsed_ns(started)
        if process.returncode != 0 or process.stderr:
            raise RuntimeError(f"cold worker failed: exit={process.returncode}, stderr={process.stderr!r}")
        ready, results = parse_worker_output(process.stdout, 1, fixture["expected_result"])
        records.append({"sample": sample, "total_ns": total_ns, **ready, "execute_ns": results[0]["execute_ns"]})
        totals.append(total_ns)
    return records, totals


def warm_samples(python: Path, script: Path, bootstrap: Path, fixture_name: str, fixture: dict[str, Any], samples: int, timeout: float) -> tuple[dict[str, Any], list[dict[str, Any]], list[int]]:
    started = time.perf_counter_ns()
    process = subprocess.Popen(
        command(python, script, bootstrap, fixture_name),
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
    )
    assert process.stdin is not None and process.stdout is not None and process.stderr is not None
    ready_line = process.stdout.readline()
    ready_total_ns = elapsed_ns(started)
    if not ready_line:
        raise RuntimeError(f"warm worker readiness failed: {process.stderr.read()!r}")
    ready = json.loads(ready_line)
    if ready.get("kind") != "ready":
        raise RuntimeError(f"invalid warm readiness: {ready!r}")
    records: list[dict[str, Any]] = []
    totals: list[int] = []
    try:
        for sample in range(samples):
            started = time.perf_counter_ns()
            process.stdin.write(request(sample, fixture) + "\n")
            process.stdin.flush()
            result_line = process.stdout.readline()
            total_ns = elapsed_ns(started)
            if not result_line:
                raise RuntimeError(f"warm worker stopped: {process.stderr.read()!r}")
            _ignored, results = parse_worker_output(json.dumps(ready) + "\n" + result_line, 1, fixture["expected_result"])
            records.append({"sample": sample, "total_ns": total_ns, "execute_ns": results[0]["execute_ns"]})
            totals.append(total_ns)
    finally:
        process.stdin.close()
        returncode = process.wait(timeout=timeout)
    stderr = process.stderr.read()
    if returncode != 0 or stderr:
        raise RuntimeError(f"warm worker failed: exit={returncode}, stderr={stderr!r}")
    return {**ready, "ready_total_ns": ready_total_ns}, records, totals


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def git_identity(root: Path) -> dict[str, Any]:
    revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, text=True, capture_output=True, check=True).stdout.strip()
    status = subprocess.run(["git", "status", "--porcelain", "--untracked-files=no"], cwd=root, text=True, capture_output=True, check=True).stdout
    return {"revision": revision, "modified": bool(status)}


def python_identity(python: Path) -> dict[str, Any]:
    resolved = python.resolve(strict=True)
    probe = subprocess.run(
        [str(resolved), "-I", "-c", "import json,platform,sys; print(json.dumps({'version':platform.python_version(),'implementation':platform.python_implementation(),'cache_tag':sys.implementation.cache_tag}))"],
        text=True,
        capture_output=True,
        check=True,
    )
    return {"path": str(resolved), "sha256": file_sha256(resolved), **json.loads(probe.stdout)}


def benchmark(args: argparse.Namespace) -> dict[str, Any]:
    root = args.repository.resolve(strict=True)
    script = Path(__file__).resolve(strict=True)
    bootstrap = (root / "guest/bootstrap").resolve(strict=True)
    python = args.python.resolve(strict=True)
    fixture = FIXTURES[args.fixture]
    cold, cold_totals = cold_samples(python, script, bootstrap, args.fixture, fixture, args.samples, args.timeout)
    warm_ready, warm, warm_totals = warm_samples(python, script, bootstrap, args.fixture, fixture, args.samples, args.timeout)
    return {
        "schema_version": SCHEMA_VERSION,
        "evidence_kind": "native-cpython-cold-warm",
        "host_source": git_identity(root),
        "environment": {
            "system": platform.system().lower(),
            "machine": platform.machine(),
            "kernel_release": platform.release(),
        },
        "python": python_identity(python),
        "fixture": {
            "name": args.fixture,
            "samples": args.samples,
            "prepare_source": fixture["prepare_source"],
            "execute_source": fixture["execute_source"],
            "inputs": {"integer_work": 1000},
            "expected_result": fixture["expected_result"],
            "bootstrap_sha256": file_sha256(root / "guest/bootstrap/agent_runtime/__init__.py"),
        },
        "cold_process": {"samples": cold, "total": summarize(cold_totals)},
        "warm_process": {"readiness": warm_ready, "samples": warm, "total": summarize(warm_totals)},
        "limitations": [
            "Native cold includes process startup, bootstrap import, initialize, prepare, execute, and process teardown.",
            "Native warm includes JSON-line pipe round-trip and bootstrap execute; it is not an in-process function-call timer.",
            "This evidence does not make native and WASI memory, isolation, or effect-safety semantics equivalent.",
        ],
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--bootstrap", type=Path)
    parser.add_argument("--fixture", choices=sorted(FIXTURES), default="basic")
    parser.add_argument("--python", type=Path, default=Path(sys.executable))
    parser.add_argument("--repository", type=Path, default=Path.cwd())
    parser.add_argument("--samples", type=int, default=30)
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument("--output", type=Path)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.worker:
        if args.bootstrap is None:
            raise SystemExit("--bootstrap is required in worker mode")
        return worker(args.bootstrap.resolve(strict=True), FIXTURES[args.fixture])
    if args.samples < 3 or args.samples > 1000 or args.timeout <= 0 or args.timeout > 600:
        raise SystemExit("samples must be 3-1000 and timeout must be in (0,600]")
    evidence = benchmark(args)
    encoded = json.dumps(evidence, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")
    else:
        sys.stdout.write(encoded)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
