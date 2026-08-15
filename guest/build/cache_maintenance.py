#!/usr/bin/env python3
"""Bounded maintenance for the private Guest build cache."""

from __future__ import annotations

import argparse
import pathlib
import re
import shutil

KEY = re.compile(r"^[0-9a-f]{64}$")
TEMPORARY = re.compile(
    r"^(?:\.publish\.[A-Za-z0-9]{8}|\.(?:publish|tmp)\.[0-9a-f]{64}\.[A-Za-z0-9]{8})$"
)


def prune(root: pathlib.Path, protect: str, keep: int = 2) -> list[str]:
    if root.is_symlink():
        raise ValueError("invalid cache maintenance boundary")
    root = root.resolve()
    protected = root / protect
    if (
        not root.is_dir()
        or not KEY.fullmatch(protect)
        or keep < 1
        or not protected.is_dir()
        or protected.is_symlink()
    ):
        raise ValueError("invalid cache maintenance boundary")
    candidates = [
        entry
        for entry in root.iterdir()
        if KEY.fullmatch(entry.name) and entry.is_dir() and not entry.is_symlink()
    ]
    temporary = [
        entry
        for entry in root.iterdir()
        if TEMPORARY.fullmatch(entry.name) and entry.is_dir() and not entry.is_symlink()
    ]
    candidates.sort(key=lambda entry: (entry.stat().st_mtime_ns, entry.name), reverse=True)
    retained = {protect}
    for entry in candidates:
        if entry.name == protect:
            continue
        if len(retained) < keep:
            retained.add(entry.name)
    removed = []
    for entry in temporary:
        shutil.rmtree(entry)
        removed.append(entry.name)
    for entry in candidates:
        if entry.name not in retained:
            shutil.rmtree(entry)
            removed.append(entry.name)
    return sorted(removed)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=pathlib.Path)
    parser.add_argument("--protect", required=True)
    parser.add_argument("--keep", type=int, default=2)
    args = parser.parse_args()
    for removed in prune(args.root, args.protect, args.keep):
        print(f"removed_cache_key={removed}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
