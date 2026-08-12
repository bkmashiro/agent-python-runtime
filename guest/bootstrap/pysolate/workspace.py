"""Bounded semantic replacements for common workspace binaries.

Every path is relative to the fixed Guest ``/workspace`` root. The module never
starts a process, opens a network connection, or accepts a Host path.
"""

from __future__ import annotations

import difflib
import builtins
import hashlib
import os
from pathlib import Path, PurePosixPath
import re
import shutil
from typing import Any, Iterator

_ROOT = Path("/workspace")
MAX_TEXT_BYTES = 1 << 20
MAX_TOTAL_READ_BYTES = 8 << 20
MAX_FILES = 4096
MAX_MATCHES = 1000
MAX_PATTERN_BYTES = 256
MAX_DIFF_LINES = 10000


def _relative(name: str, *, allow_root: bool = False) -> str:
    if not isinstance(name, str) or "\x00" in name or "\\" in name or len(name.encode("utf-8")) > 4096:
        raise ValueError("invalid workspace path")
    value = PurePosixPath(name)
    if value.is_absolute() or any(part in {"", ".", ".."} or part.startswith("..") or part == ".git" for part in value.parts):
        if allow_root and name in {"", "."}:
            return "."
        raise ValueError("invalid workspace path")
    normalized = str(value)
    if normalized != name:
        raise ValueError("workspace path must be canonical relative POSIX")
    return normalized


def _path(name: str, *, allow_root: bool = False) -> Path:
    relative = _relative(name, allow_root=allow_root)
    if relative == ".":
        return _ROOT
    current = _ROOT
    for component in PurePosixPath(relative).parts:
        current = current / component
        if current.is_symlink():
            raise ValueError("workspace symlinks are not supported")
    return current


def _file(name: str) -> Path:
    target = _path(name)
    if not target.is_file() or target.is_symlink():
        raise ValueError("workspace path is not an ordinary file")
    return target


def _bounded_text(name: str) -> str:
    target = _file(name)
    size = target.stat().st_size
    if size > MAX_TEXT_BYTES:
        raise ValueError("workspace text exceeds bound")
    return target.read_text(encoding="utf-8")


def read_text(path: str) -> str:
    """Return one bounded UTF-8 workspace file (``cat`` equivalent)."""
    return _bounded_text(path)


def write_text(path: str, content: str) -> dict[str, Any]:
    if not isinstance(content, str) or len(content.encode("utf-8")) > MAX_TEXT_BYTES:
        raise ValueError("workspace text exceeds bound")
    target = _path(path)
    if not target.parent.is_dir() or target.parent.is_symlink():
        raise ValueError("workspace parent does not exist")
    target.write_text(content, encoding="utf-8")
    return {"path": _relative(path), "bytes": len(content.encode("utf-8"))}


def list(path: str = ".") -> builtins.list[dict[str, Any]]:
    directory = _path(path, allow_root=True)
    if not directory.is_dir() or directory.is_symlink():
        raise ValueError("workspace path is not a directory")
    rows = []
    for child in sorted(directory.iterdir(), key=lambda item: item.name):
        if child.is_symlink() or child.name == ".git":
            raise ValueError("workspace contains an unsupported entry")
        relative = child.relative_to(_ROOT).as_posix()
        rows.append({"path": relative, "kind": "directory" if child.is_dir() else "file"})
    return rows


def walk(path: str = ".", *, max_files: int = MAX_FILES) -> builtins.list[dict[str, Any]]:
    if not isinstance(max_files, int) or not 1 <= max_files <= MAX_FILES:
        raise ValueError("invalid max_files")
    root = _path(path, allow_root=True)
    if not root.is_dir() or root.is_symlink():
        raise ValueError("workspace path is not a directory")
    rows = []
    for current, directories, files in os.walk(root, followlinks=False):
        directories[:] = sorted(
            name for name in directories
            if name != ".git" and not (Path(current) / name).is_symlink()
        )
        for name in directories + sorted(files):
            item = Path(current, name)
            if item.is_symlink():
                raise ValueError("workspace contains a symlink")
            rows.append({"path": item.relative_to(_ROOT).as_posix(), "kind": "directory" if item.is_dir() else "file"})
            if len(rows) > max_files:
                raise ValueError("workspace walk exceeds bound")
    return rows


def glob(pattern: str, *, path: str = ".", max_files: int = MAX_FILES) -> builtins.list[str]:
    if not isinstance(pattern, str) or not pattern or "\x00" in pattern or "\\" in pattern or pattern.startswith("/") or any(part in {"..", ".git"} or part.startswith("..") for part in PurePosixPath(pattern).parts):
        raise ValueError("invalid workspace glob")
    root = _path(path, allow_root=True)
    matches = []
    for item in root.glob(pattern):
        if item.is_symlink() or not item.is_file() or ".git" in item.relative_to(_ROOT).parts:
            continue
        matches.append(item.relative_to(_ROOT).as_posix())
        if len(matches) > max_files:
            raise ValueError("workspace glob exceeds bound")
    return sorted(matches)


def search(pattern: str, *, path: str = ".", glob: str = "*", regex: bool = False, case_sensitive: bool = True, max_matches: int = 200) -> builtins.list[dict[str, Any]]:
    if not isinstance(pattern, str) or not pattern or len(pattern.encode("utf-8")) > MAX_PATTERN_BYTES:
        raise ValueError("invalid search pattern")
    if not isinstance(max_matches, int) or not 1 <= max_matches <= MAX_MATCHES:
        raise ValueError("invalid max_matches")
    flags = 0 if case_sensitive else re.IGNORECASE
    compiled = re.compile(pattern if regex else re.escape(pattern), flags)
    matches = []
    total = 0
    for name in globals()["glob"]("**/" + glob, path=path):
        target = _file(name)
        size = target.stat().st_size
        total += size
        if size > MAX_TEXT_BYTES or total > MAX_TOTAL_READ_BYTES:
            raise ValueError("workspace search exceeds byte bound")
        for line_number, line in enumerate(target.read_text(encoding="utf-8").splitlines(), 1):
            found = compiled.search(line)
            if found is None:
                continue
            matches.append({"path": name, "line": line_number, "column": found.start() + 1, "match": found.group(0), "text": line})
            if len(matches) >= max_matches:
                return matches
    return matches


def stat(path: str) -> dict[str, Any]:
    target = _path(path, allow_root=True)
    if target.is_symlink() or not target.exists():
        raise ValueError("workspace path is unavailable")
    info = target.stat()
    return {"path": _relative(path, allow_root=True), "kind": "directory" if target.is_dir() else "file", "size": 0 if target.is_dir() else info.st_size, "executable": bool(info.st_mode & 0o111)}


def digest(path: str) -> str:
    target = _file(path)
    hasher = hashlib.sha256()
    total = 0
    with target.open("rb") as stream:
        while True:
            chunk = stream.read(65536)
            if not chunk:
                break
            total += len(chunk)
            if total > MAX_TOTAL_READ_BYTES:
                raise ValueError("workspace digest exceeds byte bound")
            hasher.update(chunk)
    return "sha256:" + hasher.hexdigest()


def diff(before: str, after: str, *, context: int = 3) -> str:
    if not isinstance(context, int) or not 0 <= context <= 20:
        raise ValueError("invalid diff context")
    left = _bounded_text(before).splitlines(keepends=True)
    right = _bounded_text(after).splitlines(keepends=True)
    lines = builtins.list(difflib.unified_diff(left, right, fromfile=_relative(before), tofile=_relative(after), n=context))
    if len(lines) > MAX_DIFF_LINES:
        raise ValueError("workspace diff exceeds line bound")
    return "".join(lines)


def mkdir(path: str, *, parents: bool = False) -> dict[str, Any]:
    target = _path(path)
    target.mkdir(parents=parents, exist_ok=False)
    return {"path": _relative(path), "created": True}


def copy(source: str, destination: str) -> dict[str, Any]:
    origin = _file(source)
    target = _path(destination)
    if not target.parent.is_dir() or target.exists():
        raise ValueError("workspace copy destination is unavailable")
    shutil.copyfile(origin, target)
    return {"from": _relative(source), "to": _relative(destination), "bytes": target.stat().st_size}


def move(source: str, destination: str) -> dict[str, str]:
    origin = _path(source)
    target = _path(destination)
    if not origin.exists() or origin.is_symlink() or not target.parent.is_dir() or target.exists():
        raise ValueError("workspace move is unavailable")
    origin.rename(target)
    return {"from": _relative(source), "to": _relative(destination)}


def remove(path: str, *, recursive: bool = False) -> dict[str, Any]:
    target = _path(path)
    if target.is_symlink() or not target.exists():
        raise ValueError("workspace path is unavailable")
    if target.is_dir():
        if recursive:
            shutil.rmtree(target)
        else:
            target.rmdir()
    else:
        target.unlink()
    return {"path": _relative(path), "removed": True}
