#!/usr/bin/env python3
"""Write a deterministic manifest for one packed Guest artifact."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def tree_sha256(root: Path) -> str:
    digest = hashlib.sha256(b"pysolate-package-tree-v1\0")
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"package tree contains symlink: {path}")
        if not path.is_file():
            continue
        relative = path.relative_to(root).as_posix().encode()
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        digest.update(path.stat().st_size.to_bytes(8, "big"))
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
    return digest.hexdigest()


def build_manifest(profile_path: Path, inputs_path: Path, native_path: Path, raw_core: Path, package_tree: Path, artifact: Path) -> dict:
    profile = json.loads(profile_path.read_text(encoding="utf-8"))
    native = json.loads(native_path.read_text(encoding="utf-8"))
    return {
        "schema_version": 1,
        "profile": profile["profile"],
        "target": profile["target"],
        "runtime": profile["runtime"],
        "artifact": {
            "filename": artifact.name,
            "bytes": artifact.stat().st_size,
            "sha256": file_sha256(artifact),
        },
        "raw_core": {
            "bytes": raw_core.stat().st_size,
            "sha256": file_sha256(raw_core),
        },
        "package_tree": {
            "sha256": tree_sha256(package_tree),
        },
        "inputs": {
            "build_lock_sha256": file_sha256(inputs_path),
            "native_package_lock_sha256": file_sha256(native_path),
            "profile_sha256": file_sha256(profile_path),
        },
        "native_modules": [item["name"] for item in native["native_modules"]],
        "policy": profile["policy"],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--build-inputs", type=Path, required=True)
    parser.add_argument("--native-lock", type=Path, required=True)
    parser.add_argument("--raw-core", type=Path, required=True)
    parser.add_argument("--package-tree", type=Path, required=True)
    parser.add_argument("--artifact", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = build_manifest(
        args.profile,
        args.build_inputs,
        args.native_lock,
        args.raw_core,
        args.package_tree,
        args.artifact,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
