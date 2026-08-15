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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", type=pathlib.Path, required=True)
    parser.add_argument("--host-system", default=platform.system())
    parser.add_argument("--host-arch", default=platform.machine())
    parser.add_argument("--document", action="store_true")
    args = parser.parse_args()
    if args.document:
        print(json.dumps(build_document(args.repository, args.host_system, args.host_arch), sort_keys=True))
    else:
        print(build_identity(args.repository, args.host_system, args.host_arch))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
