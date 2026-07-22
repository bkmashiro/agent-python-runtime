#!/usr/bin/env python3
"""Copy one source tree into deterministic VFS staging metadata/order."""

import argparse
import os
import pathlib
import shutil
from typing import List


def excluded(relative: pathlib.Path) -> bool:
    return "__pycache__" in relative.parts or relative.suffix in {".pyc", ".pyo"}


def normalize(path: pathlib.Path, epoch: int, executable: bool = False) -> None:
    os.chmod(path, 0o755 if path.is_dir() or executable else 0o644)
    os.utime(path, (epoch, epoch), follow_symlinks=False)


def copy_source(source: pathlib.Path, destination: pathlib.Path, epoch: int) -> List[str]:
    source = pathlib.Path(source)
    destination = pathlib.Path(destination)
    if source.is_symlink():
        raise ValueError(f"symlink inputs are forbidden: {source}")
    if source.is_file():
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        normalize(destination, epoch, executable=bool(source.stat().st_mode & 0o111))
        return [destination.name]
    if not source.is_dir():
        raise ValueError(f"source does not exist or is not a regular file/directory: {source}")

    destination.mkdir(parents=True, exist_ok=True)
    copied: List[str] = []
    entries = sorted(source.rglob("*"), key=lambda path: path.relative_to(source).as_posix())
    for path in entries:
        relative = path.relative_to(source)
        if excluded(relative):
            continue
        if path.is_symlink():
            raise ValueError(f"symlink inputs are forbidden: {path}")
        target = destination / relative
        if path.is_dir():
            target.mkdir(parents=True, exist_ok=True)
            copied.append(relative.as_posix())
        elif path.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
            normalize(target, epoch, executable=bool(path.stat().st_mode & 0o111))
            copied.append(relative.as_posix())
        else:
            raise ValueError(f"special-file inputs are forbidden: {path}")

    all_directories = [destination] + [path for path in destination.rglob("*") if path.is_dir()]
    for path in sorted(all_directories, key=lambda item: len(item.parts), reverse=True):
        normalize(path, epoch)
    return copied


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=pathlib.Path)
    parser.add_argument("destination", type=pathlib.Path)
    parser.add_argument("--epoch", type=int, default=None)
    args = parser.parse_args()
    epoch = args.epoch
    if epoch is None:
        raw = os.environ.get("SOURCE_DATE_EPOCH")
        if raw is None or not raw.isdigit() or int(raw) <= 0:
            parser.error("--epoch or a positive SOURCE_DATE_EPOCH is required")
        epoch = int(raw)
    copied = copy_source(args.source, args.destination, epoch)
    print(f"copied {len(copied)} deterministic entries")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
