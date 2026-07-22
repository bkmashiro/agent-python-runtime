#!/usr/bin/env python3
"""Fix the monotonic clock used by the pack-only wasi-vfs CLI context."""

import argparse
import pathlib


IMPORT_SHAPE = """use std::path::PathBuf;

use anyhow::Result;
"""

IMPORT_PATCH = """use std::path::PathBuf;

use anyhow::Result;

struct DeterministicPackMonotonicClock;

impl wasmtime_wasi::clocks::HostMonotonicClock for DeterministicPackMonotonicClock {
    fn resolution(&self) -> u64 {
        1
    }

    fn now(&self) -> u64 {
        0
    }
}
"""

CONTEXT_SHAPE = """    let mut wasi = wasmtime_wasi::WasiCtxBuilder::new();
    wasi.inherit_stdio();
    wasi.env("__WASI_VFS_PACKING", "1");
"""

CONTEXT_PATCH = """    let mut wasi = wasmtime_wasi::WasiCtxBuilder::new();
    wasi.inherit_stdio();
    wasi.env("__WASI_VFS_PACKING", "1");
    wasi.monotonic_clock(DeterministicPackMonotonicClock);
"""


def patch_source(source: pathlib.Path, output: pathlib.Path) -> None:
    text = source.read_text()
    valid = (
        text.count(IMPORT_SHAPE) == 1
        and text.count(CONTEXT_SHAPE) == 1
        and IMPORT_PATCH not in text
        and CONTEXT_PATCH not in text
    )
    if not valid:
        raise ValueError("expected exact unpatched pack clock shapes")
    patched = text.replace(IMPORT_SHAPE, IMPORT_PATCH).replace(CONTEXT_SHAPE, CONTEXT_PATCH)
    if patched.count(IMPORT_PATCH) != 1 or patched.count(CONTEXT_PATCH) != 1:
        raise ValueError("deterministic pack clock patch did not produce exact replacements")
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
