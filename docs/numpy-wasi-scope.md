# NumPy WASI official-source scope

## Decision context

Exact cross-build equality for the current Wizer-packed Core Wasm is explicitly deferred. The strict comparator and three retained negative runs remain; this track must not describe the artifact as reproducible. Track F may proceed because per-run artifact identity, locked-source provenance, SPDX/notices, downloaded-bundle verification, and real-artifact E2E are independently green.

## Authoritative support evidence

- NumPy's stable cross-compilation documentation says Meson cross files and `crossenv` are known to work, but describes cross-compilation generally rather than WASI support: <https://numpy.org/doc/stable/building/cross_compilation.html>.
- NumPy's official WASI support request remains open: <https://github.com/numpy/numpy/issues/25859>.
- NumPy PR 27669 merged a wasm32 runtime calling-convention fix, but its own rationale explicitly says `wasm32-wasip1` is not supported: <https://github.com/numpy/numpy/pull/27669>.
- CPython 3.14.0's `configure.ac` rejects `--enable-wasm-dynamic-linking` for WASI with `WASI dynamic linking is not implemented yet`: <https://github.com/python/cpython/blob/v3.14.0/configure.ac>.
- CPython's WASM build documentation explains that shared extension modules are not generally implemented and extension modules are statically linked: <https://github.com/python/cpython/blob/v3.14.0/Tools/wasm/README.md>.

Therefore this project does **not** claim official NumPy WASI support. A normal NumPy wheel copied into the immutable VFS is not an acceptable implementation: NumPy's core is compiled extension code, while the locked CPython WASI build cannot dynamically load those extensions.

## Exact official-source candidate

The current stable PyPI source distribution selected for the bounded spike is:

- package: NumPy
- version: `2.5.1`
- Python requirement: `>=3.12` (includes the project's CPython 3.14 target)
- source: <https://files.pythonhosted.org/packages/22/fd/89965aa4ac08c74998539fcbf24fa3540f3e15237fbeb6bcf9c908f4aade/numpy-2.5.1.tar.gz>
- SHA-256: `a48a113e6afea91f5608793bafa7ef2ad481fefbda87ec5069f483de61cb9fa3`
- size: `20,755,553` bytes
- installed-source license expression declared by NumPy: `BSD-3-Clause AND 0BSD AND MIT AND Zlib AND CC0-1.0`

The sdist itself is the only NumPy source candidate. No pre-release wheel, wasi-wheels binary, or copied third-party build recipe is allowed. It enters the production source lock only when a build step actually consumes it; recording an unused source as packaged evidence would be false provenance.

## Minimal promised vertical slice

A successful first slice must support, on the real immutable Guest artifact:

1. `import numpy as np`;
2. construction of small `int64` and `float64` arrays from Python lists;
3. `shape`, `ndim`, and `dtype` inspection;
4. elementwise addition and multiplication;
5. `sum` and `mean` reductions;
6. a 2×2 matrix multiplication using NumPy core operations;
7. bounded Python exceptions for incompatible shapes and invalid dtype input;
8. cross-Run freshness: mutations in one Run are absent from the next fresh/single-use instance.

This is a compatibility slice, not a performance claim.

## Explicit non-goals

The first slice does not promise:

- arbitrary `pip install` or runtime package installation;
- dynamic `.so` loading;
- `numpy.linalg`, external BLAS/LAPACK, OpenBLAS, or Fortran;
- FFT, random, F2PY, `ctypes`, subprocesses, sockets, `mmap`, or filesystem-backed arrays;
- threading, OpenMP, CPU dispatch, SIMD, Highway, or x86-specific sort paths;
- the full NumPy test suite or general third-party C-extension compatibility;
- official upstream WASI support;
- exact byte-for-byte reproducibility of the final Wizer snapshot.

The candidate build should use NumPy's own no-BLAS fallback and disable threading and CPU/SIMD optimization where the official Meson options permit it.

## Proven prerequisites and execution order

### F0 — Target sysconfig completeness

A real `f991f2d` Guest probe found that `sysconfig.get_config_var(...)` fails with:

```text
ModuleNotFoundError: No module named '_sysconfigdata__wasi_wasm32-wasi'
```

The current VFS copies `CPython/Lib` but not the generated target `_sysconfigdata_*.py` from the cross-build directory. Package that generated target file deterministically and add exact-artifact E2E before attempting NumPy.

### F1 — Official-source compile probe

Create a Linux-only, fail-closed probe from the pinned NumPy sdist using:

- the existing pinned wasi-sdk;
- NumPy's vendored Meson;
- pinned host-side Cython/build requirements;
- a project-owned Meson cross file;
- `longdouble_format = 'IEEE_DOUBLE_LE'` as documented by NumPy;
- no BLAS/LAPACK, threading, SIMD, Highway, or Intel sort.

The first output is an extension/object inventory and exact failure report, not a wheel claim.

### F2 — Static-extension feasibility

Because CPython WASI rejects dynamic linking, enumerate every extension initializer required by the minimal import path. Prove that they can be statically linked into the existing reactor and registered with `PyImport_AppendInittab` before `Py_InitializeFromConfig` without changing Host authority or ABI exports.

Stop for a toolchain/scope decision if this requires a broad NumPy fork, opaque binary inputs, unsupported runtime dynamic linking, or an unbounded extension graph. Do not silently replace static integration with a wheel copied into VFS.

### F3 — Product E2E and evidence

Only after F2 succeeds:

- place NumPy pure-Python/package metadata in the immutable VFS;
- bind the exact source and license expression into manifest/SBOM/notices;
- add the real-artifact vertical-slice and freshness tests;
- record artifact size, build time, ready/first/steady latency, retained memory, and unsupported APIs;
- preserve the fresh-instance fallback and capacity-zero default.
