#!/usr/bin/env python3
"""Fail-closed verifier for a workstation Guest build evidence directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re

DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
HEX40 = re.compile(r"^[0-9a-f]{40}$")
SUM_LINE = re.compile(r"^([0-9a-f]{64})  ([^\n]+)$")


def file_digest(path: pathlib.Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            value.update(block)
    return "sha256:" + value.hexdigest()


def evidence_files(root: pathlib.Path) -> set[str]:
    files: set[str] = set()
    for directory, names, filenames in os.walk(root, followlinks=False):
        parent = pathlib.Path(directory)
        for name in names:
            candidate = parent / name
            if candidate.is_symlink():
                raise ValueError(f"symlinked evidence directory: {candidate.relative_to(root)}")
        for name in filenames:
            candidate = parent / name
            if candidate.is_symlink() or not candidate.is_file():
                raise ValueError(f"non-regular evidence file: {candidate.relative_to(root)}")
            relative = candidate.relative_to(root).as_posix()
            if relative != "SHA256SUMS":
                files.add(relative)
    return files


def verify(
    root: pathlib.Path,
    source_commit: str | None = None,
    source_tree: str | None = None,
) -> dict[str, object]:
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

    checksums: dict[str, str] = {}
    for line in sums_path.read_text().splitlines():
        match = SUM_LINE.fullmatch(line)
        if match is None:
            raise ValueError("malformed SHA256SUMS")
        relative = pathlib.PurePosixPath(match.group(2))
        if relative.is_absolute() or ".." in relative.parts or relative.as_posix() == "SHA256SUMS":
            raise ValueError("checksum path escapes or recurses into evidence manifest")
        name = relative.as_posix()
        if name in checksums:
            raise ValueError("duplicate SHA256SUMS path")
        checksums[name] = "sha256:" + match.group(1)
    expected_files = evidence_files(root)
    if set(checksums) != expected_files:
        raise ValueError("SHA256SUMS does not cover the exact evidence file set")
    for relative, expected in checksums.items():
        target = root.joinpath(*pathlib.PurePosixPath(relative).parts)
        if file_digest(target) != expected:
            raise ValueError(f"checksum mismatch: {relative}")

    ready = json.loads(ready_path.read_text())
    cache = json.loads(cache_path.read_text())
    if ready.get("schema_version") != "pysolate.workstation-guest-build.v0":
        raise ValueError("unknown workstation evidence schema")
    if not HEX40.fullmatch(str(ready.get("source_commit", ""))) or not HEX40.fullmatch(str(ready.get("source_tree", ""))):
        raise ValueError("invalid source identity")
    if source_commit is not None and ready["source_commit"] != source_commit:
        raise ValueError("source commit mismatch")
    if source_tree is not None and ready["source_tree"] != source_tree:
        raise ValueError("source tree mismatch")
    if ready.get("builder") != "gpu31.doc.ic.ac.uk" or ready.get("target") != "wasm32-wasip1":
        raise ValueError("unexpected builder or target")
    if ready.get("requested_cache_mode") not in {"off", "auto", "refresh"}:
        raise ValueError("invalid requested cache mode")
    if ready.get("cache_disposition") not in {"off", "hit", "miss"}:
        raise ValueError("invalid cache disposition")
    if not isinstance(ready.get("build_millis"), int) or ready["build_millis"] <= 0:
        raise ValueError("invalid build duration")
    if (
        cache.get("schema_version") != "pysolate.guest-build-cache-evidence.v1"
        or cache.get("cache_key") != ready.get("cache_key")
        or cache.get("disposition") != ready.get("cache_disposition")
        or cache.get("layer_sha256") != ready.get("cache_layer_sha256")
        or cache.get("final_cache_key") != ready.get("final_cache_key")
        or cache.get("final_cache_disposition") != ready.get("final_cache_disposition")
    ):
        raise ValueError("cache evidence mismatch")
    if not DIGEST.fullmatch(str(ready.get("cache_key", ""))):
        raise ValueError("invalid cache key")
    if ready["cache_disposition"] == "off":
        if ready.get("cache_layer_sha256") is not None:
            raise ValueError("off cache has a layer digest")
    elif not DIGEST.fullmatch(str(ready.get("cache_layer_sha256", ""))):
        raise ValueError("cached build lacks layer digest")
    if ready.get("final_cache_disposition") not in {"off", "hit", "miss"}:
        raise ValueError("invalid final cache disposition")
    if ready["final_cache_disposition"] == "off":
        if ready.get("final_cache_key") is not None:
            raise ValueError("off final cache has an identity")
    elif not DIGEST.fullmatch(str(ready.get("final_cache_key", ""))):
        raise ValueError("final cache lacks exact identity")
    artifact_digest = file_digest(artifact)
    manifest = json.loads((root / "dist/manifest.json").read_text())
    if manifest.get("artifact", {}).get("sha256") != artifact_digest.removeprefix("sha256:"):
        raise ValueError("artifact manifest digest mismatch")
    return {
        "source_commit": ready["source_commit"],
        "source_tree": ready["source_tree"],
        "artifact_sha256": artifact_digest,
        "cache_key": ready["cache_key"],
        "requested_cache_mode": ready["requested_cache_mode"],
        "cache_disposition": ready["cache_disposition"],
        "cache_layer_sha256": ready.get("cache_layer_sha256"),
        "final_cache_key": ready.get("final_cache_key"),
        "final_cache_disposition": ready["final_cache_disposition"],
        "build_millis": ready["build_millis"],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    parser.add_argument("--source-commit")
    parser.add_argument("--source-tree")
    args = parser.parse_args()
    print(json.dumps(verify(args.root, args.source_commit, args.source_tree), sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
