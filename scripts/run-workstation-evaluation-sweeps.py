#!/usr/bin/env python3
"""Adaptive gpu31..gpu35 workstation launcher for evaluation sweeps."""

from __future__ import annotations

import argparse
import concurrent.futures
import importlib.util
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
from typing import Any, Iterable


TARGETS = ("gpu31", "gpu32", "gpu33", "gpu34", "gpu35")
APPROVED_ROOT = "/vol/bitbucket/ys25/pysolate"
DEFAULT_MIN_FREE_DISK = 1 * 1024**3
DEFAULT_MIN_FREE_MEMORY = 1 * 1024**3
DEFAULT_MAX_NORMALIZED_LOAD = 2.0
PROBE_SCHEMA = "pysolate.linux-evaluation-sweeps-probe.v1"

REMOTE_PROBE = r'''import hashlib, json, os, pathlib, platform, socket

approved = "/vol/bitbucket/ys25/pysolate"
root = pathlib.Path(approved)
try:
    resolved_root = str(root.resolve(strict=True))
    approved_root = resolved_root == approved and not root.is_symlink()
except OSError:
    approved_root = False
try:
    stat = os.statvfs(root)
    free_disk = stat.f_bavail * stat.f_frsize
except OSError:
    free_disk = 0
free_memory = 0
try:
    for line in pathlib.Path("/proc/meminfo").read_text().splitlines():
        if line.startswith("MemAvailable:"):
            free_memory = int(line.split()[1]) * 1024
            break
except OSError:
    pass
logical_cpus = os.cpu_count() or 0
try:
    load_1m = os.getloadavg()[0]
except OSError:
    load_1m = -1.0
normalized_load = load_1m / logical_cpus if logical_cpus > 0 else -1.0
def identity_digest(path):
    try:
        return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
    except OSError:
        return ""
print(json.dumps({
    "hostname": socket.gethostname(),
    "system": platform.system(),
    "architecture": platform.machine(),
    "approved_root": approved_root,
    "free_disk_bytes": free_disk,
    "free_memory_bytes": free_memory,
    "logical_cpus": logical_cpus,
    "load_1m": load_1m,
    "normalized_load": normalized_load,
    "machine_identity_sha256": identity_digest("/etc/machine-id"),
    "boot_identity_sha256": identity_digest("/proc/sys/kernel/random/boot_id"),
}, sort_keys=True))
'''


def ceil_div(numerator: int, denominator: int) -> int:
    if denominator <= 0:
        raise ValueError("denominator must be positive")
    return (numerator + denominator - 1) // denominator


def smallest_even_at_least(value: int) -> int:
    if value <= 0:
        raise ValueError("value must be positive")
    return value if value % 2 == 0 else value + 1


def repetitions_for_hosts(host_count: int) -> tuple[int, int]:
    if not 1 <= host_count <= len(TARGETS):
        raise ValueError("host count must be between one and five")
    plm = smallest_even_at_least(ceil_div(20, host_count))
    cow = max(3, ceil_div(12, host_count))
    return plm, cow


def parse_targets(values: Iterable[str]) -> list[str]:
    flattened: list[str] = []
    for value in values:
        flattened.extend(part.strip() for part in value.split(",") if part.strip())
    if flattened == ["auto"]:
        return list(TARGETS)
    if not flattened or any(value not in TARGETS for value in flattened) or len(set(flattened)) != len(flattened):
        raise ValueError("targets must be auto or a unique subset of gpu31..gpu35")
    return flattened


def _probe_error(host_id: str, reason: str, **extra: Any) -> dict[str, Any]:
    return {
        "host_id": host_id,
        "reachable": False,
        "eligible": False,
        "reasons": [reason],
        **extra,
    }


def evaluate_probe(
    probe: dict[str, Any],
    min_free_disk: int = DEFAULT_MIN_FREE_DISK,
    min_free_memory: int = DEFAULT_MIN_FREE_MEMORY,
    max_normalized_load: float = DEFAULT_MAX_NORMALIZED_LOAD,
) -> dict[str, Any]:
    result = dict(probe)
    reasons: list[str] = []
    if not result.get("reachable", False):
        reasons.append("unreachable")
    if result.get("system") != "Linux":
        reasons.append("not Linux")
    if result.get("architecture") != "x86_64":
        reasons.append("not x86_64")
    if result.get("approved_root") is not True:
        reasons.append("approved root check failed")
    if not isinstance(result.get("free_disk_bytes"), int) or result["free_disk_bytes"] < min_free_disk:
        reasons.append("insufficient free disk")
    if not isinstance(result.get("free_memory_bytes"), int) or result["free_memory_bytes"] < min_free_memory:
        reasons.append("insufficient free memory")
    normalized_load = result.get("normalized_load")
    if not isinstance(normalized_load, (int, float)) or normalized_load < 0 or normalized_load > max_normalized_load:
        reasons.append("normalized load too high or unavailable")
    result["reasons"] = reasons
    result["eligible"] = not reasons
    return result


def select_eligible_hosts(probes: list[dict[str, Any]]) -> list[str]:
    """Freeze one eligible endpoint per physical machine and boot."""
    selected: list[str] = []
    seen: dict[tuple[str, str], str] = {}
    for probe in probes:
        probe["selected"] = False
        if probe.get("eligible") is not True:
            continue
        identity = (
            str(probe.get("machine_identity_sha256", "")),
            str(probe.get("boot_identity_sha256", "")),
        )
        if not all(identity):
            probe.setdefault("reasons", []).append("physical identity unavailable")
            probe["eligible"] = False
            continue
        if identity in seen:
            probe["alias_of"] = seen[identity]
            continue
        seen[identity] = str(probe["host_id"])
        probe["selected"] = True
        selected.append(str(probe["host_id"]))
    return selected


def probe_host(
    host_id: str,
    gateway: str,
    timeout: float = 5.0,
    runner: Any = subprocess.run,
    min_free_disk: int = DEFAULT_MIN_FREE_DISK,
    min_free_memory: int = DEFAULT_MIN_FREE_MEMORY,
    max_normalized_load: float = DEFAULT_MAX_NORMALIZED_LOAD,
) -> dict[str, Any]:
    """Probe one host using only fixed SSH arguments and return a record even on failure."""

    if host_id not in TARGETS:
        raise ValueError(f"unsupported host: {host_id}")
    command = [
        "ssh",
        "-o",
        "BatchMode=yes",
        "-o",
        f"ConnectTimeout={max(1, int(timeout))}",
        gateway,
        "ssh",
        "-o",
        "BatchMode=yes",
        "-o",
        f"ConnectTimeout={max(1, int(timeout))}",
        host_id,
        "python3",
        "-",
    ]
    try:
        completed = runner(command, input=REMOTE_PROBE, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return _probe_error(host_id, "probe timeout", timeout_seconds=timeout)
    except OSError as exc:
        return _probe_error(host_id, f"probe launch failed: {exc}")
    record: dict[str, Any] = {"host_id": host_id, "reachable": completed.returncode == 0}
    lines = [line for line in completed.stdout.splitlines() if line.strip()]
    if completed.returncode != 0:
        return evaluate_probe(
            _probe_error(host_id, "ssh probe failed", returncode=completed.returncode, stderr=completed.stderr[-512:]),
            min_free_disk,
            min_free_memory,
            max_normalized_load,
        )
    if not lines:
        return evaluate_probe(_probe_error(host_id, "probe returned no JSON"), min_free_disk, min_free_memory, max_normalized_load)
    try:
        payload = json.loads(lines[-1])
    except json.JSONDecodeError:
        return evaluate_probe(_probe_error(host_id, "probe returned invalid JSON", stdout=completed.stdout[-512:]), min_free_disk, min_free_memory, max_normalized_load)
    if not isinstance(payload, dict):
        return evaluate_probe(_probe_error(host_id, "probe JSON was not an object"), min_free_disk, min_free_memory, max_normalized_load)
    record.update(payload)
    return evaluate_probe(record, min_free_disk, min_free_memory, max_normalized_load)


def source_identity(repo_root: pathlib.Path) -> dict[str, Any]:
    if subprocess.run(["git", "-C", str(repo_root), "status", "--porcelain"], capture_output=True, text=True, check=True).stdout:
        raise ValueError("evaluation sweeps require a clean source tree")
    return {
        "commit": subprocess.check_output(["git", "-C", str(repo_root), "rev-parse", "HEAD"], text=True).strip(),
        "tree": subprocess.check_output(["git", "-C", str(repo_root), "rev-parse", "HEAD^{tree}"], text=True).strip(),
        "epoch": int(subprocess.check_output(["git", "-C", str(repo_root), "show", "-s", "--format=%ct", "HEAD"], text=True).strip()),
    }


def write_json(path: pathlib.Path, value: dict[str, Any]) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def _load_module(path: pathlib.Path, name: str) -> Any:
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_host(
    repo_root: pathlib.Path,
    target: str,
    output: pathlib.Path,
    gateway: str,
    order_offset: int,
    plm_runs: int,
    cow_runs: int,
    build_cache_root: str | None,
) -> dict[str, Any]:
    command = [
        str(repo_root / "scripts/test-host-workstation.sh"),
        "--suite",
        "evaluation-sweeps",
        "--target",
        target,
        "--gateway",
        gateway,
        "--output",
        str(output),
        "--order-offset",
        str(order_offset),
        "--plm-crossover-runs",
        str(plm_runs),
        "--cow-fanout-runs",
        str(cow_runs),
    ]
    if build_cache_root:
        command.extend(["--build-cache-root", build_cache_root])
    completed = subprocess.run(command, cwd=repo_root, capture_output=True, text=True)
    return {
        "host_id": target,
        "returncode": completed.returncode,
        "passed": completed.returncode == 0,
        "stdout_tail": completed.stdout[-1024:],
        "stderr_tail": completed.stderr[-1024:],
        "output": str(output),
    }


def manifest_path(host_output: pathlib.Path) -> pathlib.Path:
    candidates = (
        host_output / "evaluation-sweeps/manifest.json",
        host_output / "manifest.json",
    )
    for candidate in candidates:
        if candidate.is_file() and not candidate.is_symlink():
            return candidate
    raise ValueError(f"missing per-host sweep manifest: {host_output}")


def ensure_empty_output(output: pathlib.Path) -> None:
    if output.is_symlink() or (output.exists() and not output.is_dir()):
        raise ValueError("output must be absent or an empty real directory")
    if output.exists() and any(output.iterdir()):
        raise ValueError("output must be absent or an empty real directory")
    output.mkdir(parents=True, exist_ok=True)


def run(args: argparse.Namespace) -> int:
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    output = args.output
    if not output.is_absolute():
        raise ValueError("--output must be an absolute path")
    ensure_empty_output(output)
    if args.gateway not in {"shell2", "shell3"}:
        raise ValueError("--gateway must be shell2 or shell3")
    targets = parse_targets(args.targets)
    source = source_identity(repo_root)

    # This is deliberately one barrier: every requested host is probed and the
    # complete probe record is persisted before any benchmark subprocess starts.
    probes = [
        probe_host(
            target,
            args.gateway,
            args.probe_timeout,
            min_free_disk=args.min_free_disk_bytes,
            min_free_memory=args.min_free_memory_bytes,
            max_normalized_load=args.max_normalized_load,
        )
        for target in targets
    ]
    selected = select_eligible_hosts(probes)
    probe_document = {
        "schema_version": PROBE_SCHEMA,
        "source": source,
        "requested_targets": targets,
        "probes": probes,
        "selected_hosts": selected,
        "frozen": True,
    }
    write_json(output / "probe-manifest.json", probe_document)
    if not selected:
        write_json(output / "run-status.json", {"source": source, "selected_hosts": [], "status": "no eligible hosts"})
        print("no eligible evaluation workstations", file=sys.stderr)
        return 1

    plm_runs, cow_runs = repetitions_for_hosts(len(selected))
    hosts_root = output / "hosts"
    hosts_root.mkdir()
    statuses: list[dict[str, Any]] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(selected)) as executor:
        futures = [
            executor.submit(
                run_host,
                repo_root,
                host,
                hosts_root / host,
                args.gateway,
                index,
                plm_runs,
                cow_runs,
                args.build_cache_root,
            )
            for index, host in enumerate(selected)
        ]
        for future in futures:
            statuses.append(future.result())
    statuses.sort(key=lambda row: selected.index(row["host_id"]))
    run_document = {
        "schema_version": "pysolate.linux-evaluation-sweeps-run.v1",
        "source": source,
        "selected_hosts": selected,
        "config": {"plm_crossover_runs": plm_runs, "cow_fanout_runs": cow_runs},
        "hosts": statuses,
    }
    write_json(output / "run-status.json", run_document)
    if any(not row["passed"] for row in statuses):
        print("one or more workstation sweeps failed; bundle retained", file=sys.stderr)
        return 1

    manifests = [manifest_path(hosts_root / host) for host in selected]
    merger = _load_module(repo_root / "scripts/merge-linux-evaluation-sweeps.py", "merge_linux_evaluation_sweeps")
    merged = merger.merge_manifests(manifests, selected, source["commit"], source["tree"], source["epoch"])
    write_json(output / "merged-manifest.json", merged)
    print(f"merged_manifest={output / 'merged-manifest.json'}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--targets", nargs="+", default=["auto"], help="auto or a subset of gpu31..gpu35")
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--gateway", default="shell2")
    parser.add_argument("--probe-timeout", type=float, default=5.0)
    parser.add_argument("--min-free-disk-bytes", type=int, default=DEFAULT_MIN_FREE_DISK)
    parser.add_argument("--min-free-memory-bytes", type=int, default=DEFAULT_MIN_FREE_MEMORY)
    parser.add_argument("--max-normalized-load", type=float, default=DEFAULT_MAX_NORMALIZED_LOAD)
    parser.add_argument("--build-cache-root")
    args = parser.parse_args()
    if args.probe_timeout <= 0 or args.min_free_disk_bytes < 0 or args.min_free_memory_bytes < 0 or args.max_normalized_load < 0:
        parser.error("probe timeout and resource thresholds must be non-negative, with timeout positive")
    if args.build_cache_root is not None and not pathlib.Path(args.build_cache_root).is_absolute():
        parser.error("--build-cache-root must be absolute")
    try:
        return run(args)
    except (OSError, subprocess.CalledProcessError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
