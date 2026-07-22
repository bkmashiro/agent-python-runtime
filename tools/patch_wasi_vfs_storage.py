#!/usr/bin/env python3
"""Zero-initialize wasi-vfs linked-storage structs before Wizer snapshots them."""

import argparse
import pathlib


STRUCTS = (
    "struct wasi_vfs_node",
    "struct wasi_vfs_link",
    "struct wasi_vfs_dirent",
    "struct wasi_vfs_embed_linked_storage",
)


def patch_source(source: str) -> str:
    patched = source
    for struct_name in STRUCTS:
        old = f"malloc(sizeof({struct_name}))"
        if patched.count(old) != 1:
            raise ValueError(f"expected exactly one allocation for {struct_name}")
        patched = patched.replace(old, f"calloc(1, sizeof({struct_name}))")
    if "malloc(sizeof(struct wasi_vfs_" in patched:
        raise ValueError("unrecognized wasi-vfs struct allocation remains")
    return patched


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    args = parser.parse_args()
    patched = patch_source(args.source.read_text())
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(patched)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
