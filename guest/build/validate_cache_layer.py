#!/usr/bin/env python3
"""Validate a Guest build cache tar before extraction."""

from __future__ import annotations

import argparse
import pathlib
import posixpath
import tarfile

ALLOWED_ROOTS = {"downloads", "tools", "cpython"}


def safe_name(name: str, allowed_roots: set[str] = ALLOWED_ROOTS) -> bool:
    if not name or name.startswith("/"):
        return False
    normalized = posixpath.normpath(name)
    parts = pathlib.PurePosixPath(normalized).parts
    return normalized != ".." and not normalized.startswith("../") and bool(parts) and parts[0] in allowed_roots


def validate(path: pathlib.Path, allowed_roots: set[str] = ALLOWED_ROOTS) -> None:
    seen_roots: set[str] = set()
    seen_members: dict[str, str] = {}
    with tarfile.open(path, "r:") as archive:
        for member in archive:
            if not safe_name(member.name, allowed_roots) or member.isdev() or member.isfifo():
                raise ValueError(f"unsafe cache member: {member.name}")
            member_path = pathlib.PurePosixPath(posixpath.normpath(member.name))
            normalized = member_path.as_posix()
            if normalized in seen_members:
                raise ValueError(f"duplicate cache member: {member.name}")
            for parent in member_path.parents:
                parent_name = parent.as_posix()
                if parent_name == ".":
                    break
                if parent_name in seen_members and seen_members[parent_name] != "directory":
                    raise ValueError(f"cache member descends through non-directory: {member.name}")
            if member.isdir():
                kind = "directory"
            elif member.isfile():
                kind = "file"
            elif member.issym():
                kind = "symlink"
            else:
                kind = "hardlink"
            if kind != "directory" and any(name.startswith(normalized + "/") for name in seen_members):
                raise ValueError(f"non-directory cache member replaces a parent: {member.name}")
            seen_roots.add(member_path.parts[0])
            if member.issym():
                target = posixpath.normpath(posixpath.join(str(member_path.parent), member.linkname))
                if not safe_name(target, allowed_roots):
                    raise ValueError(f"unsafe cache symlink: {member.name}")
            elif member.islnk() and not safe_name(member.linkname, allowed_roots):
                raise ValueError(f"unsafe cache hardlink: {member.name}")
            elif member.islnk() and seen_members.get(posixpath.normpath(member.linkname)) not in {"file", "hardlink"}:
                raise ValueError(f"cache hardlink target is not a prior regular node: {member.name}")
            elif not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
                raise ValueError(f"unsupported cache member: {member.name}")
            seen_members[normalized] = kind
    if seen_roots != allowed_roots:
        raise ValueError("cache layer is incomplete")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("archive", type=pathlib.Path)
    parser.add_argument("--root", action="append", dest="roots")
    args = parser.parse_args()
    roots = set(args.roots) if args.roots else ALLOWED_ROOTS
    if not roots or any("/" in root or root in {".", ".."} for root in roots):
        parser.error("cache roots must be simple names")
    validate(args.archive, roots)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
