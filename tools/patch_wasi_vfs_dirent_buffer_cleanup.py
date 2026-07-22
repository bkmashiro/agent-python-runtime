#!/usr/bin/env python3
"""Clear wasi-vfs's fd_readdir scratch buffer before it is dropped."""

import argparse
import pathlib


UPSTREAM = """            _ => {}
        }
    }
    Ok(())
}
"""

PATCHED = """            _ => {}
        }
    }

    // fd_readdir may leave truncated-entry or beyond-capacity bytes in this
    // reusable scratch allocation. Clear the complete buffer before its storage
    // is returned to the allocator and captured by Wizer.
    for byte in &mut buffer {
        unsafe {
            core::ptr::write_volatile(byte, 0);
        }
    }
    Ok(())
}
"""


def patch_source(source: pathlib.Path, output: pathlib.Path) -> None:
    text = source.read_text()
    if text.count(UPSTREAM) != 1 or PATCHED in text:
        raise ValueError("expected exact unpatched walk_dir tail")
    patched = text.replace(UPSTREAM, PATCHED)
    if patched.count(PATCHED) != 1:
        raise ValueError("dirent buffer cleanup patch did not produce one exact replacement")
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
