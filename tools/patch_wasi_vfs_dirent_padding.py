#!/usr/bin/env python3
"""Zero wasi::Dirent tail padding before wasi-vfs parses fd_readdir output."""

import argparse
import pathlib


UPSTREAM = """        let data = &buffer[offset..capacity];
        let dirent_size = core::mem::size_of::<wasi::Dirent>();

        // when dirent is truncated, re-read it
        if data.len() < dirent_size {
            offset = capacity;
            continue;
        }

        let (dirent, data) = data.split_at(dirent_size);
        let dirent = unsafe { core::ptr::read_unaligned(dirent.as_ptr() as *const wasi::Dirent) };
"""

PATCHED = """        let data = &mut buffer[offset..capacity];
        let dirent_size = core::mem::size_of::<wasi::Dirent>();

        // when dirent is truncated, re-read it
        if data.len() < dirent_size {
            offset = capacity;
            continue;
        }

        // WASI Preview 1 dirent fields occupy 21 bytes in a 24-byte repr(C)
        // structure. wasmtime-wasi raw-copies the host representation, including
        // its three undefined tail-padding bytes, into this guest buffer. Scrub
        // only that padding before it can enter the Wizer snapshot.
        const DIRENT_FIELD_BYTES: usize = core::mem::size_of::<u64>() * 2
            + core::mem::size_of::<u32>()
            + core::mem::size_of::<u8>();
        assert_eq!(DIRENT_FIELD_BYTES, 21);
        assert_eq!(dirent_size, 24);
        for byte in &mut data[DIRENT_FIELD_BYTES..dirent_size] {
            unsafe {
                core::ptr::write_volatile(byte, 0);
            }
        }

        let (dirent, data) = data.split_at(dirent_size);
        let dirent = unsafe { core::ptr::read_unaligned(dirent.as_ptr() as *const wasi::Dirent) };
"""


def patch_source(source: pathlib.Path, output: pathlib.Path) -> None:
    text = source.read_text()
    if text.count(UPSTREAM) != 1 or PATCHED in text:
        raise ValueError("expected exact unpatched dirent parsing shape")
    patched = text.replace(UPSTREAM, PATCHED)
    if patched.count(PATCHED) != 1:
        raise ValueError("dirent padding patch did not produce one exact replacement")
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
