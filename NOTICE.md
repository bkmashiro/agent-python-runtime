# Notices and provenance status

This repository is currently an implementation workspace. No runtime binary or package has been released.

Planned third-party inputs include:

- CPython 3.14.x — Python Software Foundation License 2.0;
- WASI SDK 33 / LLVM toolchain components — Apache-2.0 with LLVM exception and component licenses;
- wasm-tools — Apache-2.0 with LLVM exception;
- wasi-vfs 0.6.3 — Apache-2.0;
- NumPy — BSD-3-Clause when the NumPy tranche is activated;
- wazero — Apache-2.0 when the Go Host module is introduced.

Exact versions, URLs, SHA-256 values, roles, and artifact inclusion are recorded in `guest/build/sources.lock.json` and the generated artifact manifest. Before any release, replace this planning notice with reviewed license texts/attributions and an SBOM generated from the actual build.

No source from `webassembly-language-runtimes/python/reactor/py_reactor.c` or `wasi-wheels` build scripts has been copied into this repository.
