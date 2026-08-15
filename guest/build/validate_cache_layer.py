#!/usr/bin/env python3
"""Validate a Guest build cache tar before extraction."""

from __future__ import annotations

import argparse
import pathlib
import posixpath
import tarfile

ALLOWED_ROOTS = {"downloads", "tools", "cpython"}


def safe_name(name: str) -> bool:
    if not name or name.startswith("/"):
        return False
    normalized = posixpath.normpath(name)
    parts = pathlib.PurePosixPath(normalized).parts
    return normalized != ".." and not normalized.startswith("../") and bool(parts) and parts[0] in ALLOWED_ROOTS


def validate(path: pathlib.Path) -> None:
    seen_roots: set[str] = set()
    with tarfile.open(path, "r:") as archive:
        for member in archive:
            if not safe_name(member.name) or member.isdev() or member.isfifo():
                raise ValueError(f"unsafe cache member: {member.name}")
            member_path = pathlib.PurePosixPath(posixpath.normpath(member.name))
            seen_roots.add(member_path.parts[0])
            if member.issym():
                target = posixpath.normpath(posixpath.join(str(member_path.parent), member.linkname))
                if not safe_name(target):
                    raise ValueError(f"unsafe cache symlink: {member.name}")
            elif member.islnk() and not safe_name(member.linkname):
                raise ValueError(f"unsafe cache hardlink: {member.name}")
            elif not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
                raise ValueError(f"unsupported cache member: {member.name}")
    if seen_roots != ALLOWED_ROOTS:
        raise ValueError("cache layer is incomplete")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("archive", type=pathlib.Path)
    args = parser.parse_args()
    validate(args.archive)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
