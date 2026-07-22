#!/usr/bin/env python3
"""Use a deterministic hasher for wasi-vfs EmbeddedFs.open during research builds."""

import argparse
import pathlib


REPLACEMENTS = (
    (
        "use std::{collections::HashMap, path::Path};",
        "use std::{\n"
        "    collections::HashMap,\n"
        "    hash::{BuildHasherDefault, DefaultHasher},\n"
        "    path::Path,\n"
        "};",
    ),
    (
        "    opens: HashMap<Vfd, FdEntry<S>>,",
        "    opens: HashMap<Vfd, FdEntry<S>, BuildHasherDefault<DefaultHasher>>,",
    ),
    (
        "            opens: HashMap::new(),",
        "            opens: HashMap::with_hasher(BuildHasherDefault::default()),",
    ),
)


def patch_source(source: pathlib.Path, output: pathlib.Path) -> None:
    if not source.is_file() or source.is_symlink():
        raise ValueError("source must be a regular non-symlink file")
    text = source.read_text()
    if any(text.count(old) != 1 for old, _ in REPLACEMENTS) or any(
        new in text for _, new in REPLACEMENTS
    ):
        raise ValueError("expected exactly one unpatched EmbeddedFs hasher shape")
    patched = text
    for old, new in REPLACEMENTS:
        patched = patched.replace(old, new)
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
