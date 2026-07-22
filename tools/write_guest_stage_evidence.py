#!/usr/bin/env python3
"""Retain and describe stage identities for guest reproducibility research."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import shutil
from typing import Any


RETAINED_NAMES = {
    "raw_wasm": "agent-python-runtime.raw.wasm",
    "final_wasm": "agent-python-runtime.wasm",
    "repeat_packed_wasm": "agent-python-runtime.pack-b.wasm",
    "patched_wasi_vfs_archive": "libwasi_vfs.patched.a",
    "linked_storage_object": "linked_storage.o",
    "source_lock": "sources.lock.json",
}


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def file_identity(path: pathlib.Path, *, retained: bool, reported_path: str) -> dict[str, Any]:
    return {
        "path": reported_path,
        "size_bytes": path.stat().st_size,
        "sha256": sha256(path),
        "retained": retained,
    }


def build_vfs_manifest(vfs_root: pathlib.Path) -> dict[str, Any]:
    entries = []
    file_count = 0
    directory_count = 0
    for path in sorted(vfs_root.rglob("*"), key=lambda item: item.relative_to(vfs_root).as_posix()):
        if path.is_symlink():
            raise ValueError(f"VFS manifest refuses symlink: {path}")
        relative = path.relative_to(vfs_root).as_posix()
        if path.is_dir():
            entries.append({"path": relative, "kind": "directory"})
            directory_count += 1
            continue
        if not path.is_file():
            raise ValueError(f"VFS manifest refuses special file: {path}")
        entries.append(
            {
                "path": relative,
                "kind": "file",
                "size_bytes": path.stat().st_size,
                "sha256": sha256(path),
            }
        )
        file_count += 1
    return {
        "schema_version": 1,
        "manifest_type": "guest-vfs-path-content",
        "guest_mount": "/usr/lib/python3.14",
        "directory_count": directory_count,
        "file_count": file_count,
        "entries": entries,
    }


def write_vfs_manifest(vfs_root: pathlib.Path, output: pathlib.Path) -> dict[str, Any]:
    vfs_root = pathlib.Path(vfs_root)
    if not vfs_root.is_dir() or vfs_root.is_symlink():
        raise ValueError(f"vfs_root must be a regular directory: {vfs_root}")
    manifest = build_vfs_manifest(vfs_root)
    output = pathlib.Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    return manifest


def _validate_identity(repository_commit: str, source_date_epoch: str) -> None:
    if re.fullmatch(r"[0-9a-f]{40}", repository_commit) is None:
        raise ValueError("repository commit must be 40 lowercase hexadecimal characters")
    if not source_date_epoch.isdigit() or int(source_date_epoch) <= 0:
        raise ValueError("SOURCE_DATE_EPOCH must be a positive integer")


def write_evidence(
    *,
    evidence_dir: pathlib.Path,
    raw_wasm: pathlib.Path,
    final_wasm: pathlib.Path,
    repeat_packed_wasm: pathlib.Path,
    patched_wasi_vfs_archive: pathlib.Path,
    linked_storage_object: pathlib.Path,
    wasi_vfs_cli: pathlib.Path,
    source_lock: pathlib.Path,
    vfs_manifest: pathlib.Path,
    repository_commit: str,
    source_date_epoch: str,
    run_id: str,
    run_attempt: str,
    job: str,
    replica: str,
    runner_os: str,
    runner_arch: str,
    build_dir: str,
    dist_dir: str,
    configured_vfs_root: str,
) -> dict[str, Any]:
    _validate_identity(repository_commit, source_date_epoch)
    inputs = {
        "raw_wasm": pathlib.Path(raw_wasm),
        "final_wasm": pathlib.Path(final_wasm),
        "repeat_packed_wasm": pathlib.Path(repeat_packed_wasm),
        "patched_wasi_vfs_archive": pathlib.Path(patched_wasi_vfs_archive),
        "linked_storage_object": pathlib.Path(linked_storage_object),
        "wasi_vfs_cli": pathlib.Path(wasi_vfs_cli),
        "source_lock": pathlib.Path(source_lock),
        "vfs_manifest": pathlib.Path(vfs_manifest),
    }
    for role, path in inputs.items():
        if not path.is_file() or path.is_symlink():
            raise ValueError(f"{role} must be a regular non-symlink file: {path}")

    # Parse before creating the evidence directory so malformed inputs fail closed.
    json.loads(inputs["source_lock"].read_text())
    manifest = json.loads(inputs["vfs_manifest"].read_text())
    if (
        manifest.get("schema_version") != 1
        or manifest.get("manifest_type") != "guest-vfs-path-content"
        or manifest.get("guest_mount") != "/usr/lib/python3.14"
        or not isinstance(manifest.get("entries"), list)
    ):
        raise ValueError("pre-pack VFS manifest schema/type is invalid")

    evidence_dir = pathlib.Path(evidence_dir)
    evidence_dir.mkdir(parents=True, exist_ok=False)
    retained_paths = {}
    for role, name in RETAINED_NAMES.items():
        destination = evidence_dir / name
        shutil.copyfile(inputs[role], destination)
        retained_paths[role] = destination

    manifest_path = evidence_dir / "vfs-manifest.json"
    shutil.copyfile(inputs["vfs_manifest"], manifest_path)

    files = {
        role: file_identity(path, retained=True, reported_path=path.name)
        for role, path in retained_paths.items()
    }
    files["wasi_vfs_cli"] = file_identity(
        inputs["wasi_vfs_cli"],
        retained=False,
        reported_path=inputs["wasi_vfs_cli"].name,
    )
    files["vfs_manifest"] = file_identity(
        manifest_path,
        retained=True,
        reported_path=manifest_path.name,
    )

    raw_path = str(inputs["raw_wasm"])
    final_path = str(inputs["final_wasm"])
    repeat_path = str(inputs["repeat_packed_wasm"])
    cli_path = str(inputs["wasi_vfs_cli"])
    report = {
        "schema_version": 2,
        "evidence_type": "guest-reproducibility-stage-identities",
        "build_identity": {
            "repository_commit": repository_commit,
            "source_date_epoch": source_date_epoch,
            "github": {
                "run_id": run_id,
                "run_attempt": run_attempt,
                "job": job,
                "replica": replica,
            },
            "runner": {"os": runner_os, "arch": runner_arch},
        },
        "environment_allowlist": {
            "AGENT_RUNTIME_BUILD_DIR": build_dir,
            "AGENT_RUNTIME_DIST_DIR": dist_dir,
            "AGENT_RUNTIME_VFS_ROOT": configured_vfs_root,
            "SOURCE_DATE_EPOCH": source_date_epoch,
        },
        "pack_command": [
            cli_path,
            "pack",
            raw_path,
            "--dir",
            f"{configured_vfs_root}::/usr/lib/python3.14",
            "-o",
            final_path,
        ],
        "repeat_pack_command": [
            cli_path,
            "pack",
            raw_path,
            "--dir",
            f"{configured_vfs_root}::/usr/lib/python3.14",
            "-o",
            repeat_path,
        ],
        "files": files,
    }
    report_path = evidence_dir / "stage-evidence.json"
    report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    manifest_parser = subparsers.add_parser("manifest")
    manifest_parser.add_argument("--vfs-root", required=True, type=pathlib.Path)
    manifest_parser.add_argument("--output", required=True, type=pathlib.Path)

    evidence_parser = subparsers.add_parser("evidence")
    evidence_parser.add_argument("--evidence-dir", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--raw-wasm", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--final-wasm", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--repeat-packed-wasm", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--patched-wasi-vfs-archive", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--linked-storage-object", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--wasi-vfs-cli", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--source-lock", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--vfs-manifest", required=True, type=pathlib.Path)
    evidence_parser.add_argument("--repository-commit", required=True)
    evidence_parser.add_argument("--source-date-epoch", required=True)
    evidence_parser.add_argument("--run-id", default="local")
    evidence_parser.add_argument("--run-attempt", default="local")
    evidence_parser.add_argument("--job", default="local")
    evidence_parser.add_argument("--replica", default="local")
    evidence_parser.add_argument("--runner-os", default="unknown")
    evidence_parser.add_argument("--runner-arch", default="unknown")
    evidence_parser.add_argument("--build-dir", required=True)
    evidence_parser.add_argument("--dist-dir", required=True)
    evidence_parser.add_argument("--configured-vfs-root", required=True)
    args = parser.parse_args()

    if args.command == "manifest":
        write_vfs_manifest(args.vfs_root, args.output)
        return 0

    write_evidence(
        evidence_dir=args.evidence_dir,
        raw_wasm=args.raw_wasm,
        final_wasm=args.final_wasm,
        repeat_packed_wasm=args.repeat_packed_wasm,
        patched_wasi_vfs_archive=args.patched_wasi_vfs_archive,
        linked_storage_object=args.linked_storage_object,
        wasi_vfs_cli=args.wasi_vfs_cli,
        source_lock=args.source_lock,
        vfs_manifest=args.vfs_manifest,
        repository_commit=args.repository_commit,
        source_date_epoch=args.source_date_epoch,
        run_id=args.run_id,
        run_attempt=args.run_attempt,
        job=args.job,
        replica=args.replica,
        runner_os=args.runner_os,
        runner_arch=args.runner_arch,
        build_dir=args.build_dir,
        dist_dir=args.dist_dir,
        configured_vfs_root=args.configured_vfs_root,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
