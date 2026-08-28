#!/usr/bin/env python3
"""Project and validate one source-bound Linux evaluation sweep bundle."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
from typing import Any, Iterable


HOST_IDS = tuple(f"gpu{number}" for number in range(31, 36))
MANIFEST_SCHEMA = "pysolate.linux-evaluation-sweeps.v1"
PLM_SCHEMA = "pysolate.plm-crossover-economics.v1"
COW_SCHEMA = "pysolate.cow-fanout-economics.v1"
PLATFORM_SCHEMA = "pysolate.platform.v1"
HEX40 = re.compile(r"^[0-9a-f]{40}$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def canonical(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def sha(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return "sha256:" + digest.hexdigest()


def load(path: pathlib.Path) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing regular sweep evidence: {path}")
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid JSON evidence: {path}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object: {path}")
    return value


def _walk_values(value: Any, keys: set[str]) -> Iterable[Any]:
    if isinstance(value, dict):
        for key, child in value.items():
            if key in keys:
                yield child
            yield from _walk_values(child, keys)
    elif isinstance(value, list):
        for child in value:
            yield from _walk_values(child, keys)


def _require_source(document: dict[str, Any], source_commit: str, source_tree: str, source_epoch: int, label: str) -> None:
    expected = {
        "source_commit": source_commit,
        "source_tree": source_tree,
        "source_epoch": source_epoch,
    }
    for key, wanted in expected.items():
        values = list(_walk_values(document, {key}))
        if not values:
            raise ValueError(f"{label} missing {key}")
        if any(value != wanted for value in values):
            raise ValueError(f"{label} {key.replace('_', ' ')} drift")


def _check_optional(document: dict[str, Any], keys: set[str], wanted: Any, label: str) -> None:
    values = list(_walk_values(document, keys))
    if values and any(value != wanted for value in values):
        raise ValueError(f"{label} drift")


def _require_any_equal(document: dict[str, Any], keys: set[str], wanted: Any, label: str) -> None:
    values = list(_walk_values(document, keys))
    if not values or any(value != wanted for value in values):
        raise ValueError(f"{label} drift")


def _require_digest(value: Any, label: str) -> str:
    if not isinstance(value, str) or DIGEST.fullmatch(value) is None:
        raise ValueError(f"invalid {label} digest")
    return value


def _regular_file(root: pathlib.Path, relative: str) -> pathlib.Path:
    path = root / relative
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"missing regular sweep evidence: {relative}")
    return path


def _validate_identity(source_commit: str, source_tree: str, source_epoch: int) -> None:
    if HEX40.fullmatch(source_commit) is None or HEX40.fullmatch(source_tree) is None:
        raise ValueError("source commit and tree must be full Git object IDs")
    if not isinstance(source_epoch, int) or isinstance(source_epoch, bool) or source_epoch <= 0:
        raise ValueError("source epoch must be a positive integer")


def project(
    root: pathlib.Path,
    source_commit: str,
    source_tree: str,
    source_epoch: int,
    host_id: str,
    order_offset: int,
    plm_crossover_runs: int,
    cow_fanout_runs: int,
) -> dict[str, Any]:
    """Validate the two gate outputs and return a deterministic per-host manifest."""

    _validate_identity(source_commit, source_tree, source_epoch)
    if host_id not in HOST_IDS:
        raise ValueError(f"unsupported evaluation host: {host_id}")
    if not isinstance(order_offset, int) or isinstance(order_offset, bool) or order_offset < 0:
        raise ValueError("order offset must be a non-negative integer")
    if not isinstance(plm_crossover_runs, int) or not 3 <= plm_crossover_runs <= 20:
        raise ValueError("PLM crossover repetitions must be in [3,20]")
    if not isinstance(cow_fanout_runs, int) or not 3 <= cow_fanout_runs <= 20:
        raise ValueError("COW fanout repetitions must be in [3,20]")
    if root.is_symlink() or not root.is_dir():
        raise ValueError("sweep root must be a real directory")

    platform_path = _regular_file(root, "platform.json")
    plm_path = _regular_file(root, "plm-crossover.json")
    cow_path = _regular_file(root, "cow-fanout.json")
    base_path = _regular_file(root, "artifacts/base.wasm")
    numpy_path = _regular_file(root, "artifacts/numpy-core.wasm")
    platform = load(platform_path)
    plm = load(plm_path)
    cow = load(cow_path)

    if platform.get("schema_version") != PLATFORM_SCHEMA:
        raise ValueError("platform schema drift")
    if plm.get("schema_version") != PLM_SCHEMA:
        raise ValueError("PLM crossover schema drift")
    if cow.get("schema_version") != COW_SCHEMA:
        raise ValueError("COW fanout schema drift")
    _require_any_equal(plm, {"source_commit", "target_commit"}, source_commit, "PLM crossover source commit")
    _require_any_equal(plm, {"artifact_source_commit"}, source_commit, "PLM crossover artifact source commit")
    _require_any_equal(plm, {"source_tree"}, source_tree, "PLM crossover source tree")
    _require_any_equal(cow, {"source_commit"}, source_commit, "COW fanout source commit")
    _require_any_equal(cow, {"source_tree"}, source_tree, "COW fanout source tree")
    _require_any_equal(plm, {"host_id", "evaluation_host_id"}, host_id, "PLM crossover host")
    _require_any_equal(cow, {"host_id", "evaluation_host_id"}, host_id, "COW fanout host")
    _require_any_equal(plm, {"order_offset", "evaluation_order_offset"}, order_offset, "PLM crossover order offset")
    _require_any_equal(cow, {"order_offset", "evaluation_order_offset"}, order_offset % 2, "COW fanout order offset")
    _require_any_equal(plm, {"runs_per_arm", "plm_crossover_runs"}, plm_crossover_runs, "PLM crossover repetitions")
    _require_any_equal(cow, {"runs", "cow_fanout_runs"}, cow_fanout_runs, "COW fanout repetitions")

    _require_source(platform, source_commit, source_tree, source_epoch, "platform")
    _check_optional(platform, {"host_id", "evaluation_host_id"}, host_id, "platform host")
    hostname = platform.get("hostname")
    if not isinstance(hostname, str) or not hostname:
        hostname = f"{host_id}.doc.ic.ac.uk"

    base_sha = sha(base_path)
    numpy_sha = sha(numpy_path)
    _require_any_equal(plm, {"artifact_sha256", "base_artifact", "base_artifact_sha256"}, base_sha, "PLM crossover artifact")
    _require_any_equal(cow, {"artifact_sha256", "numpy_artifact", "numpy_artifact_sha256"}, numpy_sha, "COW fanout artifact")

    config = {
        "plm_crossover_runs": plm_crossover_runs,
        "cow_fanout_runs": cow_fanout_runs,
        "order_offset": order_offset,
    }
    evidence = {
        "platform": {"path": "platform.json", "sha256": sha(platform_path), "schema_version": platform["schema_version"]},
        "plm_crossover": {"path": "plm-crossover.json", "sha256": sha(plm_path), "schema_version": plm["schema_version"]},
        "cow_fanout": {"path": "cow-fanout.json", "sha256": sha(cow_path), "schema_version": cow["schema_version"]},
    }
    return {
        "schema_version": MANIFEST_SCHEMA,
        "complete": True,
        "source": {"commit": source_commit, "tree": source_tree, "epoch": source_epoch},
        "host": {"id": host_id, "hostname": hostname},
        "config": config,
        "schemas": {"platform": PLATFORM_SCHEMA, "plm_crossover": PLM_SCHEMA, "cow_fanout": COW_SCHEMA},
        "artifacts": {
            "base": {"path": "artifacts/base.wasm", "sha256": base_sha},
            "numpy_core": {"path": "artifacts/numpy-core.wasm", "sha256": numpy_sha},
        },
        "platform": platform,
        "evidence": evidence,
        "checksums_path": "SHA256SUMS",
    }


def evidence_files(root: pathlib.Path) -> list[str]:
    files: list[str] = []
    for directory, _, filenames in os.walk(root, followlinks=False):
        parent = pathlib.Path(directory)
        if parent.is_symlink():
            raise ValueError("symlinked sweep evidence directory")
        for name in filenames:
            path = parent / name
            relative = path.relative_to(root).as_posix()
            if relative == "SHA256SUMS":
                continue
            if path.is_symlink() or not path.is_file():
                raise ValueError("non-regular sweep evidence file")
            files.append(relative)
    return sorted(files)


def write_checksums(root: pathlib.Path) -> None:
    lines = [f"{sha(root / relative)[len('sha256:'):]}  {relative}" for relative in evidence_files(root)]
    (root / "SHA256SUMS").write_text("\n".join(lines) + "\n")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=pathlib.Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--source-tree", required=True)
    parser.add_argument("--source-epoch", type=int, required=True)
    parser.add_argument("--host-id", required=True)
    parser.add_argument("--order-offset", type=int, required=True)
    parser.add_argument("--plm-crossover-runs", "--plm-runs", dest="plm_crossover_runs", type=int, required=True)
    parser.add_argument("--cow-fanout-runs", "--cow-runs", dest="cow_fanout_runs", type=int, required=True)
    parser.add_argument("--output", type=pathlib.Path)
    args = parser.parse_args()
    output = args.output or args.root / "manifest.json"
    manifest = project(
        args.root,
        args.source_commit,
        args.source_tree,
        args.source_epoch,
        args.host_id,
        args.order_offset,
        args.plm_crossover_runs,
        args.cow_fanout_runs,
    )
    if output.is_symlink() or (output.exists() and not output.is_file()):
        raise ValueError("manifest output must be a regular file")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(json.dumps(manifest, indent=2, sort_keys=True) .encode() + b"\n")
    write_checksums(args.root)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
