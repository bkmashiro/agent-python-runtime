#!/usr/bin/env python3
"""Stage the pure-Python NumPy runtime tree for the isolated WASI probe."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
from pathlib import Path
from typing import Dict, List


EXCLUDED_DIRECTORIES = {"__pycache__", "tests"}
EXCLUDED_SUFFIXES = {".a", ".o", ".pyc", ".pyo", ".so"}
REQUIRED_FILES = {"__init__.py", "version.py", "_core/__init__.py"}


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _package_roots(install_root: Path) -> List[Path]:
    return sorted(
        path
        for path in install_root.rglob("numpy")
        if path.is_dir() and path.parent.name == "site-packages"
    )


def stage_package(install_root: Path, destination: Path, epoch: int) -> Dict[str, object]:
    install_root = install_root.resolve()
    if epoch <= 0:
        raise ValueError("epoch must be positive")
    roots = _package_roots(install_root)
    if len(roots) != 1:
        raise ValueError(
            "expected exactly one installed NumPy package, found {}".format(len(roots))
        )
    source = roots[0]

    entries = sorted(source.rglob("*"), key=lambda path: path.relative_to(source).as_posix())
    for entry in entries:
        if entry.is_symlink():
            raise ValueError("NumPy runtime tree contains symlink: {}".format(entry))

    if destination.is_symlink():
        raise ValueError("destination must not be a symlink")
    if destination.exists():
        shutil.rmtree(str(destination))
    destination.mkdir(parents=True, mode=0o755)
    os.utime(str(destination), (epoch, epoch))

    files = []
    for source_path in entries:
        relative = source_path.relative_to(source)
        if any(part in EXCLUDED_DIRECTORIES for part in relative.parts):
            continue
        if source_path.is_dir():
            target_dir = destination / relative
            target_dir.mkdir(parents=True, exist_ok=True, mode=0o755)
            target_dir.chmod(0o755)
            os.utime(str(target_dir), (epoch, epoch))
            continue
        if not source_path.is_file():
            raise ValueError("unsupported NumPy runtime entry: {}".format(source_path))
        if source_path.suffix in EXCLUDED_SUFFIXES:
            continue
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
        shutil.copyfile(str(source_path), str(target))
        target.chmod(0o644)
        os.utime(str(target), (epoch, epoch))
        files.append(
            {
                "path": relative.as_posix(),
                "sha256": _sha256(target),
                "size": target.stat().st_size,
            }
        )

    staged_paths = {item["path"] for item in files}
    missing = sorted(REQUIRED_FILES - staged_paths)
    if missing:
        raise ValueError("staged NumPy package is missing: {}".format(", ".join(missing)))

    return {
        "schema_version": 1,
        "source": source.relative_to(install_root).as_posix(),
        "destination": "numpy",
        "epoch": epoch,
        "file_count": len(files),
        "files": files,
        "excluded_directories": sorted(EXCLUDED_DIRECTORIES),
        "excluded_suffixes": sorted(EXCLUDED_SUFFIXES),
        "claim": "pure-Python diagnostic staging; native extensions are linked builtins",
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("install_root", type=Path)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--epoch", type=int, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    args = parser.parse_args()

    payload = stage_package(args.install_root, args.destination, args.epoch)
    args.manifest.parent.mkdir(parents=True, exist_ok=True)
    args.manifest.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    print(json.dumps(payload, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
