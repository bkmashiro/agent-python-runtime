#!/usr/bin/env python3
"""Write a deterministic provenance manifest for a built guest artifact."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import subprocess
from typing import Any

IMPORT_RE = re.compile(r'\(import\s+"([^"]+)"\s+"([^"]+)"')
EXPORT_RE = re.compile(r'\(export\s+"([^"]+)"')


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_manifest(
    *,
    artifact: pathlib.Path,
    wat: pathlib.Path,
    source_lock: pathlib.Path,
    commit: str,
    source_date_epoch: str,
) -> dict[str, Any]:
    lock = json.loads(source_lock.read_text())
    wat_text = wat.read_text()
    imports = [
        {"module": module, "name": name}
        for module, name in IMPORT_RE.findall(wat_text)
    ]
    exports = EXPORT_RE.findall(wat_text)
    return {
        "schema_version": 1,
        "abi_version": "v1",
        "target": lock["target"],
        "artifact": {
            "filename": artifact.name,
            "size": artifact.stat().st_size,
            "sha256": sha256(artifact),
        },
        "build": {
            "repository_commit": commit,
            "source_date_epoch": source_date_epoch,
            "compiler_target": "wasm32-wasip1",
            "execution_model": "reactor",
        },
        "sources": lock["sources"],
        "wasm": {
            "imports": imports,
            "exports": exports,
        },
        "packages": [
            {"name": "cpython", "version": "3.14.0", "status": "core"},
        ],
        "limitations": [
            "NumPy is not included in the core artifact",
            "fetch_many is the only supported capability and requires explicit Host grants",
            "WASI execution evidence is recorded separately",
        ],
    }


def git_commit() -> str:
    return subprocess.check_output(
        ["git", "rev-parse", "HEAD"], text=True
    ).strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifact", required=True, type=pathlib.Path)
    parser.add_argument("--wat", required=True, type=pathlib.Path)
    parser.add_argument("--source-lock", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()

    manifest = build_manifest(
        artifact=args.artifact,
        wat=args.wat,
        source_lock=args.source_lock,
        commit=os.environ.get("GITHUB_SHA", git_commit()),
        source_date_epoch=os.environ.get("SOURCE_DATE_EPOCH", "unknown"),
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
