#!/usr/bin/env python3
"""Plan and run bounded Phase 6 NumPy-ready COW experiments."""

from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
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


def validate_output(evidence: dict[str, Any], *, cell: Cell, revision: str, artifact_sha256: str) -> None:
    if evidence.get("schema_version") != 11 or evidence.get("evidence_kind") != "cow-pressure":
        raise RuntimeError(f"{cell.cell_id}: wrong evidence schema or kind")
    if evidence.get("evidence_class") != "profile-candidate":
        raise RuntimeError(f"{cell.cell_id}: wrong evidence class")
    artifact = evidence.get("artifact", {})
    if artifact.get("sha256") != artifact_sha256 or artifact.get("artifact_profile") != "numpy-core":
        raise RuntimeError(f"{cell.cell_id}: artifact identity drifted")
    host = evidence.get("host_source", {})
    if host.get("revision") != revision or host.get("modified") is not False:
        raise RuntimeError(f"{cell.cell_id}: Host source identity drifted")
    limits = evidence.get("limits", {})
    if (limits.get("workload") != cell.workload or limits.get("warmup_profile") != "numpy-ready-v1" or
            limits.get("consumers") != cell.consumers or limits.get("max_slots") != cell.slots):
        raise RuntimeError(f"{cell.cell_id}: workload or warmup profile drifted")
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
    if (arrival.get("mode") != cell.arrival_mode or arrival.get("rate_per_second") != cell.arrival_rate or
            arrival.get("queue_capacity") != cell.queue_capacity or offered != accepted + rejected):
        raise RuntimeError(f"{cell.cell_id}: arrival conservation failed")
    if accepted != started or started != completed + failed or failed != 0:
        raise RuntimeError(f"{cell.cell_id}: request accounting failed")
    if load.get("replenish_status") != "complete" or load.get("ready_before") != load.get("ready_after"):
        raise RuntimeError(f"{cell.cell_id}: prepared inventory did not recover")
    class_names = {entry.get("name") for entry in load.get("request_classes", [])}
    if not class_names or any(not isinstance(name, str) or not name.startswith("numpy-") for name in class_names):
        raise RuntimeError(f"{cell.cell_id}: request-class evidence is not NumPy-bound")


def load_selection(path: Path | None) -> list[str] | None:
    if path is None:
        return None
    value = json.loads(path.read_text(encoding="utf-8"))
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
    for path in (binary, artifact, artifact_manifest):
        if not path.is_file():
            raise RuntimeError(f"required input is not a file: {path}")
    revision = exact_clean_revision(repo)
    artifact_sha256 = sha256_file(artifact)
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
        evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
        validate_output(evidence, cell=cell, revision=revision, artifact_sha256=artifact_sha256)
        records.append({
            "cell": dataclasses.asdict(cell), "command": command,
            "evidence": evidence_path.name, "evidence_sha256": sha256_file(evidence_path),
            "stdout": f"{stem}.stdout.log", "stderr": f"{stem}.stderr.log", "exit_code": 0,
        })
    manifest = {
        "schema_version": 1, "manifest_kind": "phase6-numpy-density-run",
        "tier": args.tier, "host_revision": revision,
        "binary_sha256": sha256_file(binary), "artifact_sha256": artifact_sha256,
        "artifact_manifest_sha256": sha256_file(artifact_manifest),
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
