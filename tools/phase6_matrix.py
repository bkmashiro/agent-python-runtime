#!/usr/bin/env python3
"""Plan and run bounded Phase 6 NumPy-ready COW experiments."""

from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


@dataclasses.dataclass(frozen=True)
class Cell:
    cell_id: str
    workload: str
    arrival_mode: str
    slots: int
    consumers: int
    duration_seconds: int
    arrival_rate: int = 0
    queue_capacity: int = 0
    repetition: int = 1


_CANARY = (
    Cell("closed-numpy-v1-s4-c1", "numpy-v1", "closed-loop", 4, 1, 5),
    Cell("open-numpy-v1-s4-w1-r1", "numpy-v1", "open-loop-fixed-v1", 4, 1, 5, 1, 4),
)

_SMALL = (
    Cell("closed-numpy-v1-s64-c1", "numpy-v1", "closed-loop", 64, 1, 10),
    Cell("closed-numpy-v1-s64-c8", "numpy-v1", "closed-loop", 64, 8, 10),
    Cell("closed-numpy-v1-s64-c16", "numpy-v1", "closed-loop", 64, 16, 10),
    Cell("closed-numpy-v1-s256-c8", "numpy-v1", "closed-loop", 256, 8, 10),
    Cell("closed-numpy-v1-s256-c16", "numpy-v1", "closed-loop", 256, 16, 10),
    Cell("closed-numpy-mixed-v1-s256-c8", "numpy-mixed-v1", "closed-loop", 256, 8, 10),
    Cell("closed-numpy-mixed-v1-s256-c16", "numpy-mixed-v1", "closed-loop", 256, 16, 10),
    Cell("open-numpy-v1-s256-w8-r25", "numpy-v1", "open-loop-fixed-v1", 256, 8, 10, 25, 64),
    Cell("open-numpy-v1-s256-w8-r100", "numpy-v1", "open-loop-fixed-v1", 256, 8, 10, 100, 64),
    Cell("open-numpy-mixed-v1-s256-w16-r10", "numpy-mixed-v1", "open-loop-fixed-v1", 256, 16, 10, 10, 64),
    Cell("open-numpy-mixed-v1-s256-w16-r40", "numpy-mixed-v1", "open-loop-fixed-v1", 256, 16, 10, 40, 64),
)


def cells_for_tier(tier: str, selected_ids: list[str] | None = None) -> list[Cell]:
    if tier == "canary":
        if selected_ids:
            raise ValueError("canary does not accept a formal selection")
        return list(_CANARY)
    if tier == "small":
        if selected_ids:
            raise ValueError("small does not accept a formal selection")
        return list(_SMALL)
    if tier != "formal":
        raise ValueError(f"unknown tier: {tier}")
    if not selected_ids:
        raise ValueError("formal requires at least one explicitly selected small-matrix cell")
    by_id = {cell.cell_id: cell for cell in _SMALL}
    if len(selected_ids) != len(set(selected_ids)):
        raise ValueError("formal selection contains duplicate cell IDs")
    missing = sorted(set(selected_ids) - set(by_id))
    if missing:
        raise ValueError(f"formal selection contains unknown cells: {missing}")
    return [dataclasses.replace(by_id[cell_id], repetition=repetition)
            for cell_id in selected_ids for repetition in range(1, 4)]


def command_for_cell(
    cell: Cell,
    *,
    binary: Path,
    artifact: Path,
    artifact_manifest: Path,
    output: Path,
    memory_budget_bytes: int,
    memory_reserve_bytes: int,
    max_cpu: int,
    greed: int,
) -> list[str]:
    command = [
        str(binary),
        "-kind=cow-pressure",
        "-class=profile-candidate",
        "-strategy=cow-ready-single-use",
        f"-artifact={artifact}",
        f"-manifest={artifact_manifest}",
        f"-output={output}",
        "-cow-warmup-profile=numpy-ready-v1",
        f"-memory-budget-bytes={memory_budget_bytes}",
        f"-memory-reserve-bytes={memory_reserve_bytes}",
        f"-max-pressure-slots={cell.slots}",
        f"-consumers={cell.consumers}",
        f"-pressure-duration={cell.duration_seconds}s",
        f"-pressure-workload={cell.workload}",
        "-pressure-refill-workers=0",
        "-pressure-burst-factor=1",
        f"-pressure-arrival-mode={cell.arrival_mode}",
        f"-pressure-arrival-rate={cell.arrival_rate}",
        f"-pressure-queue-capacity={cell.queue_capacity}",
        f"-pressure-max-cpu={max_cpu}",
        f"-pressure-greed={greed}",
    ]
    return command


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_unique_json(path: Path, *, maximum_bytes: int) -> Any:
    if path.stat().st_size <= 0 or path.stat().st_size > maximum_bytes:
        raise RuntimeError(f"JSON input is outside the size bound: {path}")

    def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise RuntimeError(f"duplicate JSON key {key!r}: {path}")
            result[key] = value
        return result

    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique_object)


def artifact_source_identity(artifact: Path, manifest_path: Path, artifact_sha256: str) -> str:
    manifest = load_unique_json(manifest_path, maximum_bytes=1 << 20)
    if not isinstance(manifest, dict) or manifest.get("artifact_profile") != "numpy-core":
        raise RuntimeError("artifact manifest is not the NumPy-core profile")
    entry = manifest.get("artifact")
    build = manifest.get("build")
    if not isinstance(entry, dict) or not isinstance(build, dict):
        raise RuntimeError("artifact manifest identity is incomplete")
    if (entry.get("filename") != artifact.name or entry.get("sha256") != artifact_sha256 or
            entry.get("size") != artifact.stat().st_size):
        raise RuntimeError("artifact bytes drifted from the artifact manifest")
    source_commit = build.get("repository_commit")
    if not isinstance(source_commit, str) or re.fullmatch(r"[0-9a-f]{40}", source_commit) is None:
        raise RuntimeError("artifact build source revision is not exact")
    return source_commit


def exact_clean_revision(repo: Path) -> str:
    revision = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=repo, check=True, text=True,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout.strip()
    status = subprocess.run(
        ["git", "status", "--porcelain=v1", "--untracked-files=all"], cwd=repo,
        check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    ).stdout
    if status:
        raise RuntimeError("Phase 6 execution requires a clean source tree")
    if len(revision) != 40:
        raise RuntimeError("Phase 6 execution requires an exact Git revision")
    return revision


def validate_output(
    evidence: dict[str, Any], *, cell: Cell, revision: str,
    artifact_sha256: str, artifact_source_commit: str,
    memory_budget_bytes: int, memory_reserve_bytes: int, max_cpu: int, greed: int,
) -> None:
    if evidence.get("schema_version") != 11 or evidence.get("evidence_kind") != "cow-pressure":
        raise RuntimeError(f"{cell.cell_id}: wrong evidence schema or kind")
    if evidence.get("evidence_class") != "profile-candidate":
        raise RuntimeError(f"{cell.cell_id}: wrong evidence class")
    artifact = evidence.get("artifact", {})
    if (artifact.get("sha256") != artifact_sha256 or artifact.get("artifact_profile") != "numpy-core" or
            artifact.get("source_commit") != artifact_source_commit):
        raise RuntimeError(f"{cell.cell_id}: artifact identity drifted")
    host = evidence.get("host_source", {})
    if host.get("revision") != revision or host.get("modified") is not False:
        raise RuntimeError(f"{cell.cell_id}: Host source identity drifted")
    limits = evidence.get("limits", {})
    if (limits.get("workload") != cell.workload or limits.get("warmup_profile") != "numpy-ready-v1" or
            limits.get("consumers") != cell.consumers or limits.get("max_slots") != cell.slots or
            limits.get("runtime_budget_bytes") != memory_budget_bytes or
            limits.get("reserved_bytes") != memory_reserve_bytes or
            limits.get("allocation_bytes") != memory_budget_bytes + memory_reserve_bytes):
        raise RuntimeError(f"{cell.cell_id}: workload or warmup profile drifted")
    policy = evidence.get("policy", {})
    if (policy.get("max_memory_bytes") != memory_budget_bytes or policy.get("max_cpu") != max_cpu or
            policy.get("greed") != greed):
        raise RuntimeError(f"{cell.cell_id}: policy inputs drifted")
    load = evidence.get("load", {})
    arrival = load.get("arrival", {})
    fields = {
        "offered": arrival.get("offered_requests"), "accepted": arrival.get("accepted_requests"),
        "rejected": arrival.get("rejected_requests"), "started": load.get("started_requests"),
        "completed": load.get("completed_requests"), "failed": load.get("failed_requests"),
    }
    if any(isinstance(value, bool) or not isinstance(value, int) or value < 0 for value in fields.values()):
        raise RuntimeError(f"{cell.cell_id}: request accounting fields are not non-negative integers")
    offered, accepted, rejected = fields["offered"], fields["accepted"], fields["rejected"]
    started, completed, failed = fields["started"], fields["completed"], fields["failed"]
    expected_window_ns = cell.duration_seconds * 1_000_000_000 if cell.arrival_mode == "open-loop-fixed-v1" else 0
    if (arrival.get("mode") != cell.arrival_mode or arrival.get("window_ns") != expected_window_ns or
            arrival.get("rate_per_second") != cell.arrival_rate or
            arrival.get("queue_capacity") != cell.queue_capacity or offered != accepted + rejected):
        raise RuntimeError(f"{cell.cell_id}: arrival conservation failed")
    expected_offered = cell.duration_seconds * cell.arrival_rate if cell.arrival_mode == "open-loop-fixed-v1" else started
    if offered != expected_offered:
        raise RuntimeError(f"{cell.cell_id}: offered count drifted from the arrival tape")
    if accepted != started or started != completed + failed or failed != 0 or completed <= 0:
        raise RuntimeError(f"{cell.cell_id}: request accounting failed")
    if load.get("result_oracle") != "numpy-exact-v1" or load.get("validated_results") != completed:
        raise RuntimeError(f"{cell.cell_id}: NumPy result oracle evidence drifted")
    latency_samples = load.get("latency_samples_ns")
    if (not isinstance(latency_samples, list) or len(latency_samples) != completed or len(latency_samples) > 250_000 or
            any(isinstance(value, bool) or not isinstance(value, int) or value <= 0 for value in latency_samples) or
            latency_samples != sorted(latency_samples)):
        raise RuntimeError(f"{cell.cell_id}: latency samples are invalid")

    def percentile(percent: int) -> int:
        index = (len(latency_samples) * percent + 99) // 100
        return latency_samples[max(index, 1) - 1]

    latency_total = sum(latency_samples)
    derived_latency = {
        "latency_total_ns": latency_total,
        "latency_mean_ns": latency_total // completed,
        "latency_p50_ns": percentile(50),
        "latency_p95_ns": percentile(95),
        "latency_p99_ns": percentile(99),
        "latency_max_ns": latency_samples[-1],
    }
    if any(load.get(field) != value for field, value in derived_latency.items()):
        raise RuntimeError(f"{cell.cell_id}: derived latency evidence drifted")
    if load.get("replenish_status") != "complete" or load.get("ready_before") != load.get("ready_after"):
        raise RuntimeError(f"{cell.cell_id}: prepared inventory did not recover")
    request_classes = load.get("request_classes")
    if not isinstance(request_classes, list) or not request_classes:
        raise RuntimeError(f"{cell.cell_id}: request-class evidence is missing")
    observed_classes: dict[str, int] = {}
    class_started_total = class_completed_total = class_failed_total = 0
    for entry in request_classes:
        if not isinstance(entry, dict):
            raise RuntimeError(f"{cell.cell_id}: request-class evidence is invalid")
        name = entry.get("name")
        class_started, class_completed, class_failed = entry.get("started"), entry.get("completed"), entry.get("failed")
        if (not isinstance(name, str) or not name.startswith("numpy-") or name in observed_classes or
                any(isinstance(value, bool) or not isinstance(value, int) or value < 0
                    for value in (class_started, class_completed, class_failed)) or
                class_started <= 0 or class_completed + class_failed != class_started):
            raise RuntimeError(f"{cell.cell_id}: request-class evidence is invalid")
        observed_classes[name] = class_started
        class_started_total += class_started
        class_completed_total += class_completed
        class_failed_total += class_failed
    if cell.workload == "numpy-v1":
        expected_classes = {"numpy-tiny": started}
    else:
        full_cycles, remainder = divmod(started, 20)
        expected_classes = {
            "numpy-tiny": full_cycles * 12 + min(remainder, 12),
            "numpy-cpu": full_cycles * 5 + min(max(remainder - 12, 0), 5),
            "numpy-dirty-4m-500ms": full_cycles * 2 + min(max(remainder - 17, 0), 2),
            "numpy-dirty-16m-2s": full_cycles + min(max(remainder - 19, 0), 1),
        }
        expected_classes = {name: count for name, count in expected_classes.items() if count > 0}
    if observed_classes != expected_classes:
        raise RuntimeError(f"{cell.cell_id}: request-class distribution drifted")
    if (class_started_total != started or class_completed_total != completed or
            class_failed_total != failed):
        raise RuntimeError(f"{cell.cell_id}: request-class totals drifted")


def validate_with_exact_binary(binary: Path, repo: Path, schema: Path, evidence: Path) -> subprocess.CompletedProcess[str]:
    completed = subprocess.run(
        [str(binary), "-kind=validate-cow-pressure", f"-input={evidence}", f"-schema={schema}"],
        cwd=repo, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"{evidence.name}: independent schema/semantic validation failed: {completed.stderr.strip()}")
    try:
        verdict = json.loads(completed.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"{evidence.name}: validator returned invalid JSON") from error
    if verdict != {"valid": True, "schema_version": 11, "evidence_kind": "cow-pressure"}:
        raise RuntimeError(f"{evidence.name}: validator returned a non-canonical verdict")
    return completed


def load_selection(path: Path | None) -> list[str] | None:
    if path is None:
        return None
    value = load_unique_json(path, maximum_bytes=64 << 10)
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise ValueError("formal selection must be a JSON array of cell IDs")
    return value


def plan_document(tier: str, cells: list[Cell]) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "plan_kind": "phase6-numpy-density-matrix",
        "tier": tier,
        "cells": [dataclasses.asdict(cell) for cell in cells],
    }


def run(args: argparse.Namespace, cells: list[Cell]) -> None:
    repo = args.repo.resolve()
    output_dir = args.output_dir.resolve()
    if output_dir.exists() and any(output_dir.iterdir()):
        raise RuntimeError("output directory must be absent or empty")
    output_dir.mkdir(parents=True, exist_ok=True)
    binary = args.binary.resolve()
    artifact = args.artifact.resolve()
    artifact_manifest = args.artifact_manifest.resolve()
    schema = repo / "benchmark/v1/cow-pressure.schema.json"
    for path in (binary, artifact, artifact_manifest, schema):
        if not path.is_file() or path.is_symlink():
            raise RuntimeError(f"required input is not a file: {path}")
    revision = exact_clean_revision(repo)
    artifact_sha256 = sha256_file(artifact)
    artifact_source_commit = artifact_source_identity(artifact, artifact_manifest, artifact_sha256)
    records: list[dict[str, Any]] = []
    for cell in cells:
        suffix = f"-r{cell.repetition}" if args.tier == "formal" else ""
        stem = f"{cell.cell_id}{suffix}"
        evidence_path = output_dir / f"{stem}.json"
        command = command_for_cell(
            cell, binary=binary, artifact=artifact, artifact_manifest=artifact_manifest,
            output=evidence_path, memory_budget_bytes=args.memory_budget_bytes,
            memory_reserve_bytes=args.memory_reserve_bytes, max_cpu=args.max_cpu, greed=args.greed,
        )
        completed = subprocess.run(command, cwd=repo, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        (output_dir / f"{stem}.stdout.log").write_text(completed.stdout, encoding="utf-8")
        (output_dir / f"{stem}.stderr.log").write_text(completed.stderr, encoding="utf-8")
        if completed.returncode != 0:
            raise RuntimeError(f"{stem}: benchmark exited {completed.returncode}")
        validator = validate_with_exact_binary(binary, repo, schema, evidence_path)
        (output_dir / f"{stem}.validator.stdout.log").write_text(validator.stdout, encoding="utf-8")
        (output_dir / f"{stem}.validator.stderr.log").write_text(validator.stderr, encoding="utf-8")
        evidence = load_unique_json(evidence_path, maximum_bytes=8 << 20)
        if not isinstance(evidence, dict):
            raise RuntimeError(f"{stem}: evidence root is not an object")
        validate_output(
            evidence, cell=cell, revision=revision, artifact_sha256=artifact_sha256,
            artifact_source_commit=artifact_source_commit,
            memory_budget_bytes=args.memory_budget_bytes, memory_reserve_bytes=args.memory_reserve_bytes,
            max_cpu=args.max_cpu, greed=args.greed,
        )
        if exact_clean_revision(repo) != revision:
            raise RuntimeError(f"{stem}: Host source revision changed during execution")
        records.append({
            "cell": dataclasses.asdict(cell), "command": command,
            "evidence": evidence_path.name, "evidence_sha256": sha256_file(evidence_path),
            "stdout": f"{stem}.stdout.log", "stderr": f"{stem}.stderr.log", "exit_code": 0,
            "validator_stdout": f"{stem}.validator.stdout.log",
            "validator_stderr": f"{stem}.validator.stderr.log",
        })
    if exact_clean_revision(repo) != revision:
        raise RuntimeError("Host source revision changed after the matrix")
    manifest = {
        "schema_version": 1, "manifest_kind": "phase6-numpy-density-run",
        "tier": args.tier, "host_revision": revision,
        "binary_sha256": sha256_file(binary), "artifact_sha256": artifact_sha256,
        "artifact_manifest_sha256": sha256_file(artifact_manifest),
        "artifact_source_revision": artifact_source_commit,
        "memory_budget_bytes": args.memory_budget_bytes,
        "memory_reserve_bytes": args.memory_reserve_bytes,
        "max_cpu": args.max_cpu, "greed": args.greed, "records": records,
    }
    (output_dir / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="action", required=True)
    for action in ("plan", "run"):
        sub = subparsers.add_parser(action)
        sub.add_argument("--tier", choices=("canary", "small", "formal"), required=True)
        sub.add_argument("--formal-selection", type=Path)
        if action == "run":
            sub.add_argument("--repo", type=Path, required=True)
            sub.add_argument("--binary", type=Path, required=True)
            sub.add_argument("--artifact", type=Path, required=True)
            sub.add_argument("--artifact-manifest", type=Path, required=True)
            sub.add_argument("--output-dir", type=Path, required=True)
            sub.add_argument("--memory-budget-bytes", type=int, required=True)
            sub.add_argument("--memory-reserve-bytes", type=int, required=True)
            sub.add_argument("--max-cpu", type=int, required=True)
            sub.add_argument("--greed", type=int, choices=range(0, 101), required=True)
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    cells = cells_for_tier(args.tier, load_selection(args.formal_selection))
    if args.action == "plan":
        print(json.dumps(plan_document(args.tier, cells), indent=2, sort_keys=True))
    else:
        run(args, cells)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1)
