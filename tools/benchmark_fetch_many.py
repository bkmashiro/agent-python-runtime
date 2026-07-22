#!/usr/bin/env python3
"""Run and normalize the canonical sequential fetch_many benchmark."""

import argparse
import json
import pathlib
import re
import statistics
import subprocess
from typing import Dict, List

CANONICAL_OPERATIONS = (1, 5, 20)
CANONICAL_SAMPLES = 3
CANONICAL_ITERATIONS = 10
CANONICAL_PROVIDER_DELAY_NS = 2_000_000

BENCHMARK_RE = re.compile(
    r"^BenchmarkFetchManySequential/operations=(?P<operations>\d+)-\d+\s+"
    r"(?P<iterations>\d+)\s+(?P<ns>[0-9.]+)\s+ns/op\s+"
    r"(?P<metric_operations>[0-9.]+)\s+operations/batch\s+"
    r"(?P<delay>[0-9.]+)\s+provider-delay-ns/operation$"
)


def parse_benchmark_output(output: str, source_commit: str) -> dict:
    environment: Dict[str, str] = {}
    samples: Dict[int, List[int]] = {count: [] for count in CANONICAL_OPERATIONS}
    for raw_line in output.splitlines():
        line = raw_line.strip()
        for key in ("goos", "goarch", "cpu"):
            prefix = key + ":"
            if line.startswith(prefix):
                environment[key] = line[len(prefix) :].strip()
        match = BENCHMARK_RE.match(line)
        if not match:
            continue
        operations = int(match.group("operations"))
        if operations not in samples:
            raise ValueError("unexpected canonical operation count: %d" % operations)
        iterations = int(match.group("iterations"))
        metric_operations = float(match.group("metric_operations"))
        delay = int(float(match.group("delay")))
        if iterations != CANONICAL_ITERATIONS:
            raise ValueError("benchmark iteration count drifted")
        if metric_operations != operations:
            raise ValueError("operations/batch metric drifted")
        if delay != CANONICAL_PROVIDER_DELAY_NS:
            raise ValueError("provider delay fixture drifted")
        samples[operations].append(int(round(float(match.group("ns")))))

    if set(environment) != {"goos", "goarch", "cpu"}:
        raise ValueError("benchmark environment metadata is incomplete")
    for operations, values in samples.items():
        if len(values) != CANONICAL_SAMPLES:
            raise ValueError(
                "operations=%d must have exactly three samples; got %d"
                % (operations, len(values))
            )

    results = []
    for operations in CANONICAL_OPERATIONS:
        values = samples[operations]
        median_batch = int(statistics.median(values))
        results.append(
            {
                "operations": operations,
                "samples_ns_per_batch": values,
                "median_ns_per_batch": median_batch,
                "median_ns_per_operation": int(round(median_batch / operations)),
            }
        )
    return {
        "schema_version": 1,
        "benchmark": "fetch_many_provider_delay",
        "mode": "sequential",
        "evidence_class": "synthetic-provider-delay",
        "source_commit": source_commit,
        "environment": environment,
        "fixture": {
            "operation_counts": list(CANONICAL_OPERATIONS),
            "samples_per_count": CANONICAL_SAMPLES,
            "iterations_per_sample": CANONICAL_ITERATIONS,
            "provider_delay_ns_per_operation": CANONICAL_PROVIDER_DELAY_NS,
        },
        "results": results,
        "limitations": [
            "Synthetic fixed-delay Fetcher; excludes DNS, TCP, TLS, provider, and guest overhead.",
            "Host-local measurements are not Linux production latency claims.",
            "The sequential baseline exists to decide and compare bounded Host parallelism.",
        ],
    }


def git_output(root: pathlib.Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=str(root), text=True).strip()


def run(root: pathlib.Path, output_path: pathlib.Path) -> dict:
    status = git_output(root, "status", "--porcelain")
    if status:
        raise RuntimeError("benchmark evidence requires a clean exact source commit")
    source_commit = git_output(root, "rev-parse", "HEAD")
    command = [
        "go",
        "test",
        "./runtime/capability",
        "-run",
        "^$",
        "-bench",
        "^BenchmarkFetchManySequential$",
        "-benchtime=%dx" % CANONICAL_ITERATIONS,
        "-count=%d" % CANONICAL_SAMPLES,
    ]
    completed = subprocess.run(
        command,
        cwd=str(root),
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        check=True,
    )
    evidence = parse_benchmark_output(completed.stdout, source_commit)
    evidence["command"] = " ".join(command)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n")
    return evidence


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--output",
        default="docs/benchmarks/fetch-many-sequential-baseline.json",
        type=pathlib.Path,
    )
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    output_path = args.output
    if not output_path.is_absolute():
        output_path = root / output_path
    evidence = run(root, output_path)
    print(json.dumps({
        "output": str(output_path),
        "source_commit": evidence["source_commit"],
        "results": evidence["results"],
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
