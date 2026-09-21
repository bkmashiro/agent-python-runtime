# Integration tests

This directory tests the exported `pysolate` API against the real Wasm Guest artifact. These tests deliberately behave like an external consumer and must not reach package internals.

Set `PYSOLATE_GUEST` to override the default `../dist/pysolate.wasm` artifact.

White-box lifecycle and protocol tests remain next to the root package. Linux mmap/memfd tests live with their implementation in `internal/cowmem`.
