#!/usr/bin/env python3
"""Avoid retaining unused filestat metadata in wasi-vfs's pack file path."""

import argparse
import pathlib


UPSTREAM_STAT = """        let stat = unsafe { wasi::fd_filestat_get(fd) }
            .map_err(|e| e.raw())
            .unwrap();
"""
PATCHED_SIZE = """        let size = unsafe { wasi::fd_seek(fd, 0, wasi::WHENCE_END) }
            .map_err(|e| e.raw())
            .unwrap();
        unsafe { wasi::fd_seek(fd, 0, wasi::WHENCE_SET) }
            .map_err(|e| e.raw())
            .unwrap();
"""
STAT_SIZE_USES = (
    "        if stat.size >= u32::MAX as u64 {",
    '                trace::print(format!("too large file: {} (size {})\\n", path, stat.size));',
    "        let mut buf = vec![0; stat.size as usize];",
    "            if offset == stat.size as usize {",
)
UPSTREAM_FILE_OPEN = """            wasi::FILETYPE_REGULAR_FILE => {
                let oflags = 0;
                let child_fd = unsafe {
                    wasi::path_open(
                        fd,
                        wasi::LOOKUPFLAGS_SYMLINK_FOLLOW,
                        &name,
                        oflags,
                        rights,
                        rights,
                        0,
                    )
                }
                .map_err(|e| e.raw())
                .unwrap();
"""
PATCHED_FILE_OPEN = """            wasi::FILETYPE_REGULAR_FILE => {
                let oflags = 0;
                let file_rights = rights | wasi::RIGHTS_FD_SEEK;
                let child_fd = unsafe {
                    wasi::path_open(
                        fd,
                        wasi::LOOKUPFLAGS_SYMLINK_FOLLOW,
                        &name,
                        oflags,
                        file_rights,
                        file_rights,
                        0,
                    )
                }
                .map_err(|e| e.raw())
                .unwrap();
"""


def patch_source(source: pathlib.Path, output: pathlib.Path) -> None:
    if not source.is_file() or source.is_symlink():
        raise ValueError("source must be a regular non-symlink file")
    text = source.read_text()
    if (
        text.count(UPSTREAM_STAT) != 1
        or text.count(UPSTREAM_FILE_OPEN) != 1
        or text.count("stat.size") != 4
        or any(text.count(snippet) != 1 for snippet in STAT_SIZE_USES)
        or PATCHED_SIZE in text
    ):
        raise ValueError("expected exact unpatched pack file-size shape")
    patched = (
        text.replace(UPSTREAM_FILE_OPEN, PATCHED_FILE_OPEN)
        .replace(UPSTREAM_STAT, PATCHED_SIZE)
        .replace("stat.size", "size")
    )
    if "fd_filestat_get" in patched or "stat.size" in patched:
        raise ValueError("pack file-size patch left stale filestat references")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(patched)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    args = parser.parse_args()
    patch_source(args.source, args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
