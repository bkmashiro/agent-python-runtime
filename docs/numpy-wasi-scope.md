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

## Prior implementation audit

The earlier Shimmy/WLR line contains a real, relevant static-extension implementation rather than only a wheel experiment:

- `bkmashiro/wasi-wheels` commit `184cce0b537088be76e1e8a06d6fe742e2f29ff4`, with NumPy gitlink `7bc18034031f32e5d03bb646c472dabd1623e9d5` (`1.26.0b1`), intercepted distutils shared-library link steps, archived the extension object files, and emitted a pure-Python tree plus static archives;
- `bkmashiro/webassembly-language-runtimes` commit `3afe48cd282d37d86643f26f92db0880deef8b2d` linked the NumPy archives monolithically with CPython 3.14, registered core initializers through `PyImport_AppendInittab` before `Py_Initialize`, and packed the Python tree through wasi-vfs;
- `shimmy-wasm` commit `1a4743d1959e982b9b459195fe10ff930849cf34` retains real NumPy array/reduction/matrix/error/isolation E2E and points to WLR as the artifact source of truth.

The reusable architecture is: compile extension objects for `wasm32-wasip1` → retain them as deterministic static archives rather than dynamically loaded side modules → link the bounded archive set into the reactor → register exact `PyInit_*` functions as built-ins → route dotted built-in submodule imports to `BuiltinImporter` → pack only the matching pure-Python tree into the immutable VFS. This directly matches the current Core Wasm/no-`dlopen` boundary.

The old implementation is evidence and a design reference, not a source dependency. It cannot be copied verbatim because it consumes mutable `latest` release assets, lacks a root LICENSE file despite a README MIT statement, uses NumPy's removed distutils build path, carries `-D__EMSCRIPTEN__`, patches CPython toward dynamic linking elsewhere in the repository, and includes broad test/random stubs and snapshot assumptions outside this product's security model. The current project must independently implement the narrow mechanism from the locked official NumPy source, pin every host helper, preserve fresh instantiate/discard, and fail closed on extension/archive/initializer drift.

For NumPy 2.5.1, the old linker-interception concept may be adapted only as a project-owned Meson probe: expose the pinned host Cython command, let Meson compile with the locked WASI C/C++ toolchain, intercept only extension-link outputs to archive the exact object list, and require a machine-readable mapping from module name to archive and `PyInit_*` symbol. A fallback to official NumPy 1.26 source is a separate owner decision, not an automatic downgrade.

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

After packaging it, the first real artifact reported `platform = wasi-0.0.0-wasm32`, `EXT_SUFFIX = .cpython-314-wasm32-wasi.so`, and `HAVE_DYNAMIC_LOADING = 1`. That last configure variable is not proof that WASI side modules work: CPython's explicit WASI dynamic-linking configure option remains rejected. F2 must perform a real load attempt instead of treating either the suffix list or this macro as capability evidence.

### F1 — Official-source compile probe

Create a Linux-only, fail-closed probe from the pinned NumPy sdist using:

- the existing pinned wasi-sdk;
- NumPy's vendored Meson;
- pinned host-side Cython/build requirements;
- a project-owned Meson cross file;
- `longdouble_format = 'IEEE_QUAD_LE'`, matching Meson's measured 16-byte `long double` and the WebAssembly C ABI's little-endian IEEE binary128 representation;
- no BLAS/LAPACK, threading, SIMD, Highway, or Intel sort.

The first output is an extension/object inventory and exact failure report, not a wheel claim. Run `29949728945` completed setup and compiled through step 293/331 before the first `py.extension_module` dynamic link failed on unresolved CPython symbols. This establishes broad source/object compilation feasibility for the bounded configuration, but not a complete build or importable artifact.

### F2 — Static-extension feasibility

Because CPython WASI rejects explicit dynamic-linking support, enumerate every extension initializer required by the minimal import path. Source inspection of NumPy 2.5.1 narrows the eager core path to `numpy._core._multiarray_umath`: `numpy._core` imports its pure-Python `multiarray` and `umath` wrappers, while top-level `linalg`, `fft`, and `random` are lazy. The broader source tree still defines linalg, FFT, random, SIMD, and test extensions, so this is only a scope hypothesis, not proof that Meson can omit them or that one static module is sufficient.

Prove that the required initializer can be built as a static archive, linked into the existing reactor, and registered with `PyImport_AppendInittab` before `Py_InitializeFromConfig` without changing Host authority or ABI exports. The manual-only `.github/workflows/numpy-wasi-probe.yml` first runs NumPy's vendored Meson against the exact experimental source lock and retains structured setup/compile logs; a compiler failure is diagnostic evidence rather than a support claim.

Stop for a toolchain/scope decision if this requires a broad NumPy fork, opaque binary inputs, unsupported runtime dynamic linking, or an unbounded extension graph. Do not silently replace static integration with a wheel copied into VFS.

### F3 — Product E2E and evidence

Only after F2 succeeds:

- place NumPy pure-Python/package metadata in the immutable VFS;
- bind the exact source and license expression into manifest/SBOM/notices;
- add the real-artifact vertical-slice and freshness tests;
- record artifact size, build time, ready/first/steady latency, retained memory, and unsupported APIs;
- preserve the fresh-instance fallback and capacity-zero default.
