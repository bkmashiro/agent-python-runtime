#!/usr/bin/env python3
"""Fail-closed verifier for a workstation Guest build evidence directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re

DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
HEX40 = re.compile(r"^[0-9a-f]{40}$")


def file_digest(path: pathlib.Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            value.update(block)
    return "sha256:" + value.hexdigest()


def verify(root: pathlib.Path, source_commit: str | None = None) -> dict[str, object]:
    if root.is_symlink():
        raise ValueError("evidence root must be a real directory")
    root = root.resolve()
    if not root.is_dir():
        raise ValueError("evidence root must be a real directory")
    ready_path = root / "RESULT.READY"
    sums_path = root / "SHA256SUMS"
    artifact = root / "dist/agent-python-runtime.wasm"
    cache_path = root / "dist/build-cache.json"
    for required in (ready_path, sums_path, artifact, cache_path, root / "build.log"):
        if not required.is_file() or required.is_symlink():
            raise ValueError(f"missing regular evidence file: {required.relative_to(root)}")
    for line in sums_path.read_text().splitlines():
        parts = line.split("  ", 1)
        if len(parts) != 2 or not re.fullmatch(r"[0-9a-f]{64}", parts[0]):
            raise ValueError("malformed SHA256SUMS")
        relative = pathlib.PurePosixPath(parts[1])
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("checksum path escapes evidence root")
        target = root.joinpath(*relative.parts)
        if not target.is_file() or target.is_symlink() or file_digest(target) != "sha256:" + parts[0]:
            raise ValueError(f"checksum mismatch: {relative}")
    ready = json.loads(ready_path.read_text())
    cache = json.loads(cache_path.read_text())
    if ready.get("schema_version") != "pysolate.workstation-guest-build.v0":
        raise ValueError("unknown workstation evidence schema")
    if not HEX40.fullmatch(str(ready.get("source_commit", ""))) or not HEX40.fullmatch(str(ready.get("source_tree", ""))):
        raise ValueError("invalid source identity")
    if source_commit is not None and ready["source_commit"] != source_commit:
        raise ValueError("source commit mismatch")
    if ready.get("builder") != "gpu31.doc.ic.ac.uk" or ready.get("target") != "wasm32-wasip1":
        raise ValueError("unexpected builder or target")
    if ready.get("requested_cache_mode") not in {"off", "auto", "refresh"}:
        raise ValueError("invalid requested cache mode")
    if ready.get("cache_disposition") not in {"off", "hit", "miss"}:
        raise ValueError("invalid cache disposition")
    if not isinstance(ready.get("build_millis"), int) or ready["build_millis"] <= 0:
        raise ValueError("invalid build duration")
    if cache.get("schema_version") != "pysolate.guest-build-cache-evidence.v0" or cache.get("cache_key") != ready.get("cache_key") or cache.get("disposition") != ready.get("cache_disposition") or cache.get("layer_sha256") != ready.get("cache_layer_sha256"):
        raise ValueError("cache evidence mismatch")
    if not DIGEST.fullmatch(str(ready.get("cache_key", ""))):
        raise ValueError("invalid cache key")
    if ready["cache_disposition"] == "off":
        if ready.get("cache_layer_sha256") is not None:
            raise ValueError("off cache has a layer digest")
    elif not DIGEST.fullmatch(str(ready.get("cache_layer_sha256", ""))):
        raise ValueError("cached build lacks layer digest")
    return {
        "source_commit": ready["source_commit"],
        "source_tree": ready["source_tree"],
        "artifact_sha256": file_digest(artifact),
        "cache_key": ready["cache_key"],
        "cache_disposition": ready["cache_disposition"],
        "cache_layer_sha256": ready.get("cache_layer_sha256"),
        "build_millis": ready["build_millis"],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    parser.add_argument("--source-commit")
    args = parser.parse_args()
    print(json.dumps(verify(args.root, args.source_commit), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
