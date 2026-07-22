#!/usr/bin/env python3
"""Verify the provenance binding and reviewed WASM ABI of a guest artifact."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
from typing import Any

REQUIRED_EXPORTS = {
    "_initialize",
    "memory",
    "runtime_init",
    "runtime_prepare",
    "alloc",
    "dealloc",
    "execute",
}
ALLOWED_SUPPORT_EXPORTS = {
    "__data_end",
    "__heap_base",
    "__wasi_vfs_rt_init",
    "wasi_vfs_pack_fs",
}
ALLOWED_IMPORT_MODULES = {"wasi_snapshot_preview1"}


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify(artifact: pathlib.Path, manifest: dict[str, Any]) -> None:
    if artifact.read_bytes()[:4] != b"\x00asm":
        raise ValueError("artifact does not have the WASM magic")
    if manifest.get("schema_version") != 1 or manifest.get("abi_version") != "v1":
        raise ValueError("unsupported manifest or ABI version")
    if manifest.get("target") != "wasm32-wasip1":
        raise ValueError("unexpected artifact target")

    artifact_record = manifest.get("artifact", {})
    if artifact_record.get("size") != artifact.stat().st_size:
        raise ValueError("artifact size does not match manifest")
    actual_sha256 = sha256(artifact)
    if artifact_record.get("sha256") != actual_sha256:
        raise ValueError("artifact sha256 does not match manifest")

    wasm = manifest.get("wasm", {})
    exports = set(wasm.get("exports", []))
    if "_start" in exports:
        raise ValueError("command export _start is forbidden for the reactor")
    missing = sorted(REQUIRED_EXPORTS - exports)
    if missing:
        raise ValueError(f"missing exports: {', '.join(missing)}")
    unexpected = sorted(exports - REQUIRED_EXPORTS - ALLOWED_SUPPORT_EXPORTS)
    if unexpected:
        raise ValueError(f"unexpected exports: {', '.join(unexpected)}")

    import_modules = {item.get("module") for item in wasm.get("imports", [])}
    forbidden_modules = sorted(import_modules - ALLOWED_IMPORT_MODULES)
    if forbidden_modules:
        raise ValueError(f"forbidden import module: {', '.join(forbidden_modules)}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("artifact", type=pathlib.Path)
    parser.add_argument("manifest", type=pathlib.Path)
    args = parser.parse_args()
    verify(args.artifact, json.loads(args.manifest.read_text()))
    print(json.dumps({"artifact": str(args.artifact), "sha256": sha256(args.artifact)}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
