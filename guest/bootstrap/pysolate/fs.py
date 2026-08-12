"""Bounded semantic replacements for common filesystem binaries.

Paths must explicitly name one Guest-visible mount: ``/workspace`` for task
state or ``/tmp`` for per-Run scratch. The module never starts a process, opens
a network connection, or accepts a Host path.
"""

from __future__ import annotations

import builtins
import difflib
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import shutil
from typing import Any

_MOUNTS = {"/workspace": Path("/workspace"), "/tmp": Path("/tmp")}
MAX_TEXT_BYTES = 1 << 20
MAX_TOTAL_READ_BYTES = 8 << 20
MAX_FILES = 4096
MAX_MATCHES = 1000
MAX_PATTERN_BYTES = 256
MAX_DIFF_LINES = 10000


def _canonical(name: str) -> tuple[str, str]:
    if not isinstance(name, str) or "\x00" in name or "\\" in name or len(name.encode("utf-8")) > 4096:
        raise ValueError("invalid filesystem path")
    value = PurePosixPath(name)
    if not value.is_absolute() or str(value) != name:
        raise ValueError("filesystem path must be canonical absolute POSIX")
    parts = value.parts
    if len(parts) < 2:
        raise ValueError("filesystem path must name a visible mount")
    mount = "/" + parts[1]
    if mount not in _MOUNTS or any(part in {".", "..", ".git"} for part in parts[2:]):
        raise ValueError("filesystem path is outside the visible mounts")
    return mount, "/".join(parts[2:])


def _display(mount: str, relative: str = "") -> str:
    return mount if not relative else mount + "/" + relative


def _path(name: str) -> tuple[str, str, Path]:
    mount, relative = _canonical(name)
    current = _MOUNTS[mount]
    for component in PurePosixPath(relative).parts:
        current = current / component
        if current.is_symlink():
            raise ValueError("filesystem symlinks are not supported")
    return mount, relative, current


def _file(name: str) -> tuple[str, str, Path]:
    mount, relative, target = _path(name)
    if not target.is_file() or target.is_symlink():
        raise ValueError("filesystem path is not an ordinary file")
    return mount, relative, target


def _bounded_text(name: str) -> str:
    _, _, target = _file(name)
    size = target.stat().st_size
    if size > MAX_TEXT_BYTES:
        raise ValueError("filesystem text exceeds bound")
    return target.read_text(encoding="utf-8")


def read_text(path: str) -> str:
    """Return one bounded UTF-8 file (``cat`` equivalent)."""
    return _bounded_text(path)


def write_text(path: str, content: str) -> dict[str, Any]:
    if not isinstance(content, str) or len(content.encode("utf-8")) > MAX_TEXT_BYTES:
        raise ValueError("filesystem text exceeds bound")
    mount, relative, target = _path(path)
    if not relative or not target.parent.is_dir() or target.parent.is_symlink():
        raise ValueError("filesystem parent does not exist")
    target.write_text(content, encoding="utf-8")
    return {"path": _display(mount, relative), "bytes": len(content.encode("utf-8"))}


def list(path: str) -> builtins.list[dict[str, Any]]:
    mount, _, directory = _path(path)
    if not directory.is_dir() or directory.is_symlink():
        raise ValueError("filesystem path is not a directory")
    rows = []
    root = _MOUNTS[mount]
    for child in sorted(directory.iterdir(), key=lambda item: item.name):
        if child.is_symlink() or child.name == ".git":
            raise ValueError("filesystem contains an unsupported entry")
        relative = child.relative_to(root).as_posix()
        rows.append({"path": _display(mount, relative), "kind": "directory" if child.is_dir() else "file"})
    return rows


def walk(path: str, *, max_files: int = MAX_FILES) -> builtins.list[dict[str, Any]]:
    if not isinstance(max_files, int) or not 1 <= max_files <= MAX_FILES:
        raise ValueError("invalid max_files")
    mount, _, root = _path(path)
    if not root.is_dir() or root.is_symlink():
        raise ValueError("filesystem path is not a directory")
    mount_root = _MOUNTS[mount]
    rows = []
    for current, directories, files in os.walk(root, followlinks=False):
        directories[:] = sorted(name for name in directories if name != ".git" and not (Path(current) / name).is_symlink())
        for name in directories + sorted(files):
            item = Path(current, name)
            if item.is_symlink():
                raise ValueError("filesystem contains a symlink")
            relative = item.relative_to(mount_root).as_posix()
            rows.append({"path": _display(mount, relative), "kind": "directory" if item.is_dir() else "file"})
            if len(rows) > max_files:
                raise ValueError("filesystem walk exceeds bound")
    return rows


def glob(pattern: str, *, path: str, max_files: int = MAX_FILES) -> builtins.list[str]:
    if not isinstance(pattern, str) or not pattern or "\x00" in pattern or "\\" in pattern or pattern.startswith("/") or str(PurePosixPath(pattern)) != pattern or any(part in {"..", ".git"} for part in PurePosixPath(pattern).parts):
        raise ValueError("invalid filesystem glob")
    if not isinstance(max_files, int) or not 1 <= max_files <= MAX_FILES:
        raise ValueError("invalid max_files")
    mount, _, root = _path(path)
    mount_root = _MOUNTS[mount]
    matches = []
    for item in root.glob(pattern):
        if item.is_symlink() or not item.is_file() or ".git" in item.relative_to(mount_root).parts:
            continue
        matches.append(_display(mount, item.relative_to(mount_root).as_posix()))
        if len(matches) > max_files:
            raise ValueError("filesystem glob exceeds bound")
    return sorted(matches)


def search(pattern: str, *, path: str, glob: str = "*", regex: bool = False, case_sensitive: bool = True, max_matches: int = 200) -> builtins.list[dict[str, Any]]:
    if not isinstance(pattern, str) or not pattern or len(pattern.encode("utf-8")) > MAX_PATTERN_BYTES:
        raise ValueError("invalid search pattern")
    if not isinstance(max_matches, int) or not 1 <= max_matches <= MAX_MATCHES:
        raise ValueError("invalid max_matches")
    flags = 0 if case_sensitive else re.IGNORECASE
    compiled = re.compile(pattern if regex else re.escape(pattern), flags)
    matches = []
    total = 0
    for name in globals()["glob"]("**/" + glob, path=path):
        _, _, target = _file(name)
        size = target.stat().st_size
        total += size
        if size > MAX_TEXT_BYTES or total > MAX_TOTAL_READ_BYTES:
            raise ValueError("filesystem search exceeds byte bound")
        for line_number, line in enumerate(target.read_text(encoding="utf-8").splitlines(), 1):
            found = compiled.search(line)
            if found is None:
                continue
            matches.append({"path": name, "line": line_number, "column": found.start() + 1, "match": found.group(0), "text": line})
            if len(matches) >= max_matches:
                return matches
    return matches


def stat(path: str) -> dict[str, Any]:
    mount, relative, target = _path(path)
    if target.is_symlink() or not target.exists():
        raise ValueError("filesystem path is unavailable")
    info = target.stat()
    return {"path": _display(mount, relative), "kind": "directory" if target.is_dir() else "file", "size": 0 if target.is_dir() else info.st_size, "executable": bool(info.st_mode & 0o111)}


def digest(path: str) -> str:
    _, _, target = _file(path)
    hasher = hashlib.sha256()
    total = 0
    with target.open("rb") as stream:
        while True:
            chunk = stream.read(65536)
            if not chunk:
                break
            total += len(chunk)
            if total > MAX_TOTAL_READ_BYTES:
                raise ValueError("filesystem digest exceeds byte bound")
            hasher.update(chunk)
    return "sha256:" + hasher.hexdigest()


def diff(before: str, after: str, *, context: int = 3) -> str:
    if not isinstance(context, int) or not 0 <= context <= 20:
        raise ValueError("invalid diff context")
    left = _bounded_text(before).splitlines(keepends=True)
    right = _bounded_text(after).splitlines(keepends=True)
    lines = builtins.list(difflib.unified_diff(left, right, fromfile=before, tofile=after, n=context))
    if len(lines) > MAX_DIFF_LINES:
        raise ValueError("filesystem diff exceeds line bound")
    return "".join(lines)


def mkdir(path: str, *, parents: bool = False) -> dict[str, Any]:
    mount, relative, target = _path(path)
    if not relative:
        raise ValueError("cannot create a visible mount")
    target.mkdir(parents=parents, exist_ok=False)
    return {"path": _display(mount, relative), "created": True}


def copy(source: str, destination: str) -> dict[str, Any]:
    _, _, origin = _file(source)
    mount, relative, target = _path(destination)
    if not relative or not target.parent.is_dir() or target.parent.is_symlink() or target.exists():
        raise ValueError("filesystem copy destination is unavailable")
    shutil.copyfile(origin, target)
    return {"from": source, "to": _display(mount, relative), "bytes": target.stat().st_size}


def move(source: str, destination: str) -> dict[str, str]:
    source_mount, _, origin = _path(source)
    destination_mount, relative, target = _path(destination)
    if source_mount != destination_mount:
        raise ValueError("cross-mount move is unavailable; use copy then remove")
    if not relative or not origin.exists() or origin.is_symlink() or not target.parent.is_dir() or target.parent.is_symlink() or target.exists():
        raise ValueError("filesystem move is unavailable")
    origin.rename(target)
    return {"from": source, "to": _display(destination_mount, relative)}


def remove(path: str, *, recursive: bool = False) -> dict[str, Any]:
    mount, relative, target = _path(path)
    if not relative or target.is_symlink() or not target.exists():
        raise ValueError("filesystem path is unavailable")
    if target.is_dir():
        if recursive:
            shutil.rmtree(target)
        else:
            target.rmdir()
    else:
        target.unlink()
    return {"path": _display(mount, relative), "removed": True}
