#!/usr/bin/env python3
"""Compute the exact reusable CPython/WASI build-layer identity."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import platform
from typing import Any

SCHEMA_VERSION = "pysolate.guest-build-cache-key.v0"
FINAL_SCHEMA_VERSION = "pysolate.guest-final-artifact-cache-key.v0"
BEGIN_MARKER = b"# BEGIN CPYTHON CACHE RECIPE\n"
END_MARKER = b"# END CPYTHON CACHE RECIPE\n"
IDENTITY_INPUTS = (
    "guest/build/cache_identity.py",
    "guest/build/cache_maintenance.py",
    "guest/build/validate_cache_layer.py",
    "guest/build/sources.lock.json",
    "tools/patch_cpython_wasi_timer_config.py",
    "tools/patch_cpython_import_gate.py",
)


def digest_bytes(value: bytes) -> str:
    return "sha256:" + hashlib.sha256(value).hexdigest()


def valid_digest(value: str) -> bool:
    return (
        len(value) == 71
        and value.startswith("sha256:")
        and all(character in "0123456789abcdef" for character in value[7:])
    )


def recipe_bytes(script: bytes) -> bytes:
    if script.count(BEGIN_MARKER) != 1 or script.count(END_MARKER) != 1:
        raise ValueError("build script must contain exactly one CPython cache recipe")
    start = script.index(BEGIN_MARKER) + len(BEGIN_MARKER)
    end = script.index(END_MARKER, start)
    if end <= start:
        raise ValueError("CPython cache recipe is empty")
    return script[start:end]


def build_document(repository: pathlib.Path, host_system: str, host_arch: str) -> dict[str, Any]:
    repository = repository.resolve()
    script = (repository / "guest/build/build-guest.sh").read_bytes()
    inputs = []
    for relative in IDENTITY_INPUTS:
        raw = (repository / relative).read_bytes()
        if relative.endswith(".json"):
            parsed = json.loads(raw)
            raw = json.dumps(parsed, sort_keys=True, separators=(",", ":")).encode()
        inputs.append({"path": relative, "sha256": digest_bytes(raw)})
    return {
        "schema_version": SCHEMA_VERSION,
        "target": "wasm32-wasip1",
        "host_system": host_system,
        "host_arch": host_arch,
        "recipe_sha256": digest_bytes(recipe_bytes(script)),
        "inputs": inputs,
    }


def build_identity(repository: pathlib.Path, host_system: str, host_arch: str) -> str:
    encoded = json.dumps(
        build_document(repository, host_system, host_arch),
        sort_keys=True,
        separators=(",", ":"),
    ).encode()
    return digest_bytes(encoded)


def final_document(
    layer_key: str,
    source_tree: str,
    source_epoch: int,
    artifact_profile: str,
    artifact_filename: str,
    extensions_lock_sha256: str,
    initial_memory_bytes: int,
    max_memory_bytes: int,
    host_system: str,
    host_arch: str,
) -> dict[str, Any]:
    if not valid_digest(layer_key):
        raise ValueError("invalid layer key")
    if len(source_tree) != 40 or any(value not in "0123456789abcdef" for value in source_tree):
        raise ValueError("invalid source tree")
    if source_epoch <= 0 or initial_memory_bytes <= 0 or max_memory_bytes < initial_memory_bytes:
        raise ValueError("invalid final build parameters")
    if extensions_lock_sha256 and not valid_digest(extensions_lock_sha256):
        raise ValueError("invalid extension lock digest")
    return {
        "schema_version": FINAL_SCHEMA_VERSION,
        "target": "wasm32-wasip1",
        "host_system": host_system,
        "host_arch": host_arch,
        "layer_key": layer_key,
        "source_tree": source_tree,
        "source_epoch": source_epoch,
        "artifact_profile": artifact_profile,
        "artifact_filename": artifact_filename,
        "extensions_lock_sha256": extensions_lock_sha256,
        "initial_memory_bytes": initial_memory_bytes,
        "max_memory_bytes": max_memory_bytes,
    }


def final_identity(**values: Any) -> str:
    encoded = json.dumps(final_document(**values), sort_keys=True, separators=(",", ":")).encode()
    return digest_bytes(encoded)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", type=pathlib.Path, required=True)
    parser.add_argument("--host-system", default=platform.system())
    parser.add_argument("--host-arch", default=platform.machine())
    parser.add_argument("--document", action="store_true")
    parser.add_argument("--final", action="store_true")
    parser.add_argument("--layer-key")
    parser.add_argument("--source-tree")
    parser.add_argument("--source-epoch", type=int)
    parser.add_argument("--artifact-profile")
    parser.add_argument("--artifact-filename")
    parser.add_argument("--extensions-lock-sha256", default="")
    parser.add_argument("--initial-memory-bytes", type=int)
    parser.add_argument("--max-memory-bytes", type=int)
    args = parser.parse_args()
    if args.final:
        required = (args.layer_key, args.source_tree, args.source_epoch, args.artifact_profile, args.artifact_filename, args.initial_memory_bytes, args.max_memory_bytes)
        if any(value is None for value in required):
            parser.error("--final requires exact layer/source/artifact/memory identity")
        values = {
            "layer_key": args.layer_key,
            "source_tree": args.source_tree,
            "source_epoch": args.source_epoch,
            "artifact_profile": args.artifact_profile,
            "artifact_filename": args.artifact_filename,
            "extensions_lock_sha256": args.extensions_lock_sha256,
            "initial_memory_bytes": args.initial_memory_bytes,
            "max_memory_bytes": args.max_memory_bytes,
            "host_system": args.host_system,
            "host_arch": args.host_arch,
        }
        if args.document:
            print(json.dumps(final_document(**values), sort_keys=True))
        else:
            print(final_identity(**values))
    elif args.document:
        print(json.dumps(build_document(args.repository, args.host_system, args.host_arch), sort_keys=True))
    else:
        print(build_identity(args.repository, args.host_system, args.host_arch))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
