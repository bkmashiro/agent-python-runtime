#!/usr/bin/env python3
"""Run a source-bound PLM zero-read fixed-cost supplement on preselected workstations."""
from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import importlib.util
import json
import pathlib
import subprocess
import sys
from typing import Any

PROBE_SCHEMA = "pysolate.plm-fixed-cost-probe.v1"
RUN_SCHEMA = "pysolate.plm-fixed-cost-run.v1"
MERGED_SCHEMA = "pysolate.plm-fixed-cost-merged.v1"
DEFAULT_TARGETS = ("gpu31", "gpu33", "gpu34", "gpu35")


def load_launcher(repo_root: pathlib.Path) -> Any:
    path = repo_root / "scripts/run-workstation-evaluation-sweeps.py"
    spec = importlib.util.spec_from_file_location("evaluation_launcher", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return "sha256:" + digest.hexdigest()


def load_regular(path: pathlib.Path) -> dict[str, Any]:
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"missing regular file: {path}")
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def validate_host(root: pathlib.Path, host_id: str, order_offset: int, runs: int, source: dict[str, Any]) -> dict[str, Any]:
    suite = root / "plm-fixed-cost"
    manifest_path = suite / "manifest.json"
    evidence_path = suite / "plm-fixed-cost.json"
    platform_path = suite / "platform.json"
    artifact_path = suite / "artifacts/base.wasm"
    manifest = load_regular(manifest_path)
    evidence = load_regular(evidence_path)
    platform = load_regular(platform_path)
    if manifest.get("schema_version") != "pysolate.plm-fixed-cost-host.v1":
        raise ValueError(f"{host_id}: host manifest schema drift")
    if manifest.get("source") != source or manifest.get("host_id") != host_id or manifest.get("order_offset") != order_offset or manifest.get("zero_read_runs") != runs:
        raise ValueError(f"{host_id}: host manifest identity drift")
    if platform.get("host_id") != host_id or platform.get("source_commit") != source["commit"] or platform.get("source_tree") != source["tree"] or platform.get("source_epoch") != source["epoch"]:
        raise ValueError(f"{host_id}: platform identity drift")
    required = {
        "target_commit": source["commit"],
        "source_tree": source["tree"],
        "artifact_source_commit": source["commit"],
        "evaluation_host_id": host_id,
        "evaluation_order_offset": order_offset,
        "zero_read_runs": runs,
        "zero_read": True,
        "zero_only": True,
        "read_counts": [],
        "delays_ms": [],
    }
    for key, expected in required.items():
        if evidence.get(key) != expected:
            raise ValueError(f"{host_id}: evidence field drift: {key}")
    profiles = evidence.get("profiles")
    if not isinstance(profiles, list) or [profile.get("name") for profile in profiles] != ["cold_end_to_end", "engine_precompiled"]:
        raise ValueError(f"{host_id}: profile drift")
    source_hashes: set[str] = set()
    for profile in profiles:
        samples = profile.get("samples")
        if not isinstance(samples, list) or len(samples) != 2 * runs:
            raise ValueError(f"{host_id}: sample count drift")
        observed = {(sample.get("pair_iteration"), sample.get("mode")) for sample in samples}
        expected_pairs = {(pair, mode) for pair in range(runs) for mode in ("baseline", "plm")}
        if observed != expected_pairs:
            raise ValueError(f"{host_id}: pair identity drift")
        for sample in samples:
            if sample.get("profile") != profile["name"] or sample.get("read_count") != 0 or sample.get("delay_ms") != 0:
                raise ValueError(f"{host_id}: zero-read cell drift")
            if sample.get("provider_starts") != 0 or sample.get("provider_max_concurrency") != 0 or sample.get("call_count") != 0 or sample.get("result") != [750]:
                raise ValueError(f"{host_id}: zero-read semantic drift")
            source_hashes.add(str(sample.get("source_sha256", "")))
    if len(source_hashes) != 1 or evidence.get("source_sha256") not in source_hashes:
        raise ValueError(f"{host_id}: source digest drift")
    checks = {
        "manifest_sha256": sha256(manifest_path),
        "evidence_sha256": sha256(evidence_path),
        "platform_sha256": sha256(platform_path),
        "artifact_sha256": sha256(artifact_path),
    }
    for key in ("evidence_sha256", "platform_sha256", "artifact_sha256"):
        if manifest.get(key) != checks[key]:
            raise ValueError(f"{host_id}: {key} drift")
    if evidence.get("artifact_sha256") != checks["artifact_sha256"]:
        raise ValueError(f"{host_id}: artifact evidence drift")
    return {
        "host_id": host_id,
        "order_offset": order_offset,
        "source_sha256": evidence.get("source_sha256"),
        **checks,
    }


def merge_hosts(output: pathlib.Path, hosts: list[str], runs: int, source: dict[str, Any]) -> dict[str, Any]:
    blocks = [validate_host(output / "hosts" / host, host, index, runs, source) for index, host in enumerate(hosts)]
    if len({block["artifact_sha256"] for block in blocks}) != 1 or len({block["source_sha256"] for block in blocks}) != 1:
        raise ValueError("fixed-cost artifact or source drift across hosts")
    return {
        "schema_version": MERGED_SCHEMA,
        "source": source,
        "selected_hosts": hosts,
        "zero_read_runs_per_host": runs,
        "pairs_per_profile": runs * len(hosts),
        "profiles": ["cold_end_to_end", "engine_precompiled"],
        "host_blocks": blocks,
    }


def run_host(repo_root: pathlib.Path, host: str, output: pathlib.Path, gateway: str, order_offset: int, runs: int, build_cache_root: str | None) -> dict[str, Any]:
    command = [
        str(repo_root / "scripts/test-host-workstation.sh"),
        "--suite", "plm-fixed-cost",
        "--target", host,
        "--gateway", gateway,
        "--output", str(output),
        "--order-offset", str(order_offset),
        "--plm-crossover-runs", str(runs),
        "--cow-fanout-runs", "3",
    ]
    if build_cache_root:
        command.extend(["--build-cache-root", build_cache_root])
    completed = subprocess.run(command, cwd=repo_root, capture_output=True, text=True)
    return {
        "host_id": host,
        "returncode": completed.returncode,
        "passed": completed.returncode == 0,
        "stdout_tail": completed.stdout[-1024:],
        "stderr_tail": completed.stderr[-1024:],
        "output": str(output),
    }


def run(args: argparse.Namespace) -> int:
    repo_root = pathlib.Path(__file__).resolve().parents[1]
    launcher = load_launcher(repo_root)
    output = args.output
    if not output.is_absolute():
        raise ValueError("--output must be absolute")
    launcher.ensure_empty_output(output)
    if args.gateway not in {"shell2", "shell3"}:
        raise ValueError("--gateway must be shell2 or shell3")
    targets = launcher.parse_targets(args.targets)
    source = launcher.source_identity(repo_root)
    probes = [
        launcher.probe_host(
            target,
            args.gateway,
            args.probe_timeout,
            min_free_disk=args.min_free_disk_bytes,
            min_free_memory=args.min_free_memory_bytes,
            max_normalized_load=args.max_normalized_load,
        )
        for target in targets
    ]
    selected = launcher.select_eligible_hosts(probes)
    launcher.write_json(output / "probe-manifest.json", {
        "schema_version": PROBE_SCHEMA,
        "source": source,
        "requested_targets": targets,
        "probes": probes,
        "selected_hosts": selected,
        "frozen": True,
    })
    if selected != targets:
        launcher.write_json(output / "run-status.json", {
            "schema_version": RUN_SCHEMA,
            "source": source,
            "requested_targets": targets,
            "selected_hosts": selected,
            "status": "preselected host set unavailable; no measurement started",
        })
        print("preselected fixed-cost host set unavailable", file=sys.stderr)
        return 1
    hosts_root = output / "hosts"
    hosts_root.mkdir()
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(selected)) as executor:
        futures = [
            executor.submit(run_host, repo_root, host, hosts_root / host, args.gateway, index, args.runs, args.build_cache_root)
            for index, host in enumerate(selected)
        ]
        statuses = [future.result() for future in futures]
    statuses.sort(key=lambda row: selected.index(row["host_id"]))
    launcher.write_json(output / "run-status.json", {
        "schema_version": RUN_SCHEMA,
        "source": source,
        "selected_hosts": selected,
        "config": {"zero_read_runs_per_host": args.runs},
        "hosts": statuses,
    })
    if any(not status["passed"] for status in statuses):
        print("one or more fixed-cost host runs failed; bundle retained", file=sys.stderr)
        return 1
    merged = merge_hosts(output, selected, args.runs, source)
    launcher.write_json(output / "merged-manifest.json", merged)
    print(f"merged_manifest={output / 'merged-manifest.json'}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--targets", nargs="+", default=list(DEFAULT_TARGETS))
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--runs", type=int, default=20)
    parser.add_argument("--gateway", default="shell2")
    parser.add_argument("--probe-timeout", type=float, default=5.0)
    parser.add_argument("--min-free-disk-bytes", type=int, default=1 * 1024**3)
    parser.add_argument("--min-free-memory-bytes", type=int, default=1 * 1024**3)
    parser.add_argument("--max-normalized-load", type=float, default=2.0)
    parser.add_argument("--build-cache-root")
    args = parser.parse_args()
    if not 3 <= args.runs <= 20:
        parser.error("--runs must be in [3,20]")
    if args.probe_timeout <= 0 or args.min_free_disk_bytes < 0 or args.min_free_memory_bytes < 0 or args.max_normalized_load < 0:
        parser.error("invalid probe/resource threshold")
    if args.build_cache_root is not None and not pathlib.Path(args.build_cache_root).is_absolute():
        parser.error("--build-cache-root must be absolute")
    try:
        return run(args)
    except (OSError, subprocess.CalledProcessError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
