#!/usr/bin/env python3
"""Fetch one source declared in the immutable source lock and verify SHA-256."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import tempfile
import urllib.request
from typing import Any

DEFAULT_LOCK = pathlib.Path("guest/build/sources.lock.json")


def find_source(lock: dict[str, Any], source_id: str) -> dict[str, Any]:
    for source in lock.get("sources", []):
        if source.get("id") == source_id:
            return source
    raise KeyError(f"unknown source id: {source_id}")


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_digest(path: pathlib.Path, expected: str) -> None:
    actual = file_sha256(path)
    if actual != expected:
        raise ValueError(f"digest mismatch for {path}: expected {expected}, got {actual}")


def download_source(source: dict[str, Any], destination: pathlib.Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    request = urllib.request.Request(
        source["url"],
        headers={"User-Agent": "agent-python-runtime-source-fetcher/1"},
    )
    with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as temporary:
        temporary_path = pathlib.Path(temporary.name)
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                while chunk := response.read(1024 * 1024):
                    temporary.write(chunk)
            temporary.flush()
            verify_digest(temporary_path, source["sha256"])
            temporary_path.replace(destination)
        except BaseException:
            temporary_path.unlink(missing_ok=True)
            raise


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("source_id")
    parser.add_argument("destination", type=pathlib.Path)
    parser.add_argument("--lock", type=pathlib.Path, default=DEFAULT_LOCK)
    args = parser.parse_args()

    lock = json.loads(args.lock.read_text())
    source = find_source(lock, args.source_id)
    download_source(source, args.destination)
    print(
        json.dumps(
            {
                "id": source["id"],
                "version": source["version"],
                "sha256": source["sha256"],
                "destination": str(args.destination),
            },
            sort_keys=True,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
