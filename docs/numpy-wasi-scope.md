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

Prove that the required initializer can be built as a static archive, linked into the existing reactor, and registered with `PyImport_AppendInittab` before `Py_InitializeFromConfig` without changing Host authority or ABI exports. The manual-only `.github/workflows/numpy-wasi-probe.yml` runs NumPy's vendored Meson against the exact experimental source lock and retains structured setup/compile/link/registration reports; workflow success alone is not a support claim. Run `29952158028` completed all 331 compile/archive steps and retained 19 manifests. After rejecting a WebAssembly-EH artifact that current wazero could not parse, run `29961023913` completed the selective no-EH core link and current-wazero compilation. Run `29962371833` then instantiated the 49,371,053-byte reactor and obtained registration exit `0` from `PyImport_AppendInittab`; its link and registration reports agree on SHA-256 `941ec695f89a7ef5bbdf1cbbc5685f6bc998ed2582da357ca2794ce409f4ce24`. This proves the bounded F2 registration claim only: Python, the NumPy initializer, and module import were deliberately not executed, and the no-EH `bad_alloc` behavior remains non-production-qualified.

Stop for a toolchain/scope decision if this requires a broad NumPy fork, opaque binary inputs, unsupported runtime dynamic linking, or an unbounded extension graph. Do not silently replace static integration with a wheel copied into VFS.

### F3 — Product E2E and evidence

Only after F2 succeeds, first cross the initializer/import boundary in an experiment-only artifact. Run `29964718389` performed a real Meson install, deterministically staged 381 NumPy package files, packed the CPython standard library plus that tree, registered the core builtin, and entered its initializer under current wazero. Its first runtime trap was resolved with the locked SDK's own `libc-printscan-long-double.a`, preserving the measured 16-byte `long double` / `IEEE_QUAD_LE` target ABI instead of using the historical `--wrap=strtold` binary64 shim. Run `29965554161` then initialized Python and crossed that core initializer path; its complete traceback identified the required second builtin, `numpy.linalg._umath_linalg`. Run `29966525059` linked only that manifest-bound archive and its deduplicated inputs, then completed an actual top-level `import numpy` with version `2.5.1` under current wazero. Run `29967618919` additionally passed deterministic integer array/reduction checks, demonstrated a `long double` value above one that rounds to one as `double`, and computed the expected 2×2 determinant through `_umath_linalg`. Schema-3 runtime evidence reports `numeric_succeeded` with no guest stderr.

This closes bounded feasibility, not blanket production qualification. The diagnostic probe still has an unqualified no-EH allocation-error adaptation, supports only qualified feature profiles, and is not itself a release path. The exact `core` closure has now crossed into an explicit opt-in normal artifact profile with immutable VFS, manifest/SBOM/notices, downloaded-bundle verification, and real product E2E; `random` remains diagnostic-only, and neither profile has been approved as the default or as a released/deployed production artifact.

Feature selection is build-time, never runtime dynamic linking. `feature-profiles.json` declares inheritance plus exact dotted modules, `PyInit_*` symbols, and archive manifests. The resolver validates those manifests against the build root, deduplicates transitive static inputs, generates the builtin registry header, and emits a digest-bound selection report. Extension archives and support archives are separate roles: only extension archives are forced through `--whole-archive`, while support libraries remain normal archives so the linker extracts only unresolved objects. `core` contains only the proven core/linalg closure; `random` inherits it and adds the nine extensions eagerly imported by `numpy.random`. FFT, test modules, and unrelated optional extensions remain outside both profiles.

Run `29969280083` remotely requalified the generated `core` profile without changing its selected two extensions/five total link inputs or established import/numeric result. The first `random` attempts then exposed two real package-adapter boundaries rather than being papered over: forcing support archive `libnpyrandom.a` through `--whole-archive` duplicated distribution objects already carried by `mtrand.a`, and locked Cython 3.2.8's default compressed module strings required the unavailable optional `zlib` extension. The role-separated linker fixed the former. The target build now sets locked Cython's documented `CYTHON_COMPRESS_STRINGS=0`, selecting its generated uncompressed fallback instead of adding a zlib stub or unverified native module.

Run `29971269500` qualified the explicit-seed random path. Its profile selects 11 extension archives (core/linalg plus nine random modules) and four support archives. Schema-4 evidence reports registration, import, core numeric, and random exits `0`, including deterministic checks for `default_rng(123456789)`, MT19937, PCG64, Philox, SFC64, and legacy `RandomState`; the 111,869,607-byte packed Wasm has SHA-256 `a6055ccc6735bd6b49003962ddab55d409dccfa7b295f7a9b8661a10e81ef742`.

Unseeded randomness is a distinct Host policy. Source and integration audit found wazero's default module configuration uses a deterministic fake source seeded with `42`, which is unsuitable both for `default_rng()` entropy and CPython hash randomization. The wazero adapter and diagnostic verifier now explicitly provide the Host's `crypto/rand.Reader`; a real minimal WASI `random_get` test proves two fresh instances receive different 32-byte values. Final run `29971940228` records schema-5 `entropy_source: host_crypto_rand`, `entropy_called: true`, `entropy_exit: 0`, `entropy_validated: true`, and `outcome: entropy_succeeded`, while retaining all explicit-seed and numeric checks. Its selection report has SHA-256 `dc55419582df331a7e03171709103357f7aef997f5440dd21750280bdd96d2d3`; the 111,869,346-byte packed Wasm has SHA-256 `98cc0d89e983a2c759d2ca0db16608b97ecb980d7ddda0603e830d9e4ffc76a4`.

Run `29974530096` adds actual diagnostic cross-instance evidence instead of resetting one module. Two simultaneously live fresh Wasm modules each initialized CPython, registered/imported the selected profile, asserted that a Guest marker from the other instance was absent, and emitted different Host-entropy 64-bit words. Schema-6 reports `fresh_instances_validated: true`, `outcome: freshness_succeeded`, no stderr/error, and 128 MiB linear memory per instance. Wazero compilation took 6.074s; complete two-instance validation took 24.919s; first/second NumPy imports took 8.964s/8.909s. With both instances live, the Go process snapshot reported 396.64 MiB `HeapAlloc` and 703.5 MiB `HeapSys`; these are one-run Go heap snapshots, not RSS or peak-memory claims. The probe's own official-source phases took 261.699s total, including 221.427s NumPy compile and 11.465s VFS pack, excluding the preceding CPython base build. The 111,870,514-byte Wasm has SHA-256 `bfe7fc008086edcf6f0062957e549659b9c99e565a3d27e093195df353e7073d`; downloaded transport SHA-256 is `9eeeb15416b4082643c24c3b52d85d0411664214b6ce8440239e6a9d07c322f4`.

The measured random artifact is 53.27 MiB larger than the then-current 56,008,018-byte production Guest and is 1.997× its size. Therefore promotion must be an explicit artifact profile, with the existing no-NumPy artifact remaining the default; silently embedding NumPy into every Guest is rejected. Signed commits `c930e83` and `351c02b` implement that contract for `base|numpy-core` only. HEAD base run `29977484647` retains the canonical filename, `extension_profile: null`, no NumPy/Cython provenance, and full producer/consumer/benchmark success. Manual `numpy-core` run `29977488572` completed the normal producer and downloaded-consumer chain: the 63,626,531-byte artifact SHA-256 is `2e183948a65071d0116ffb439f08ee174bc9a893d34a4ae416e9e0f8ead10493`, the exact two-module/five-input selection report SHA-256 is `278a00ff728089a2cb9d39970741735f7582ada56f8f522caaa5acc945dc7a12`, and real Host/guest NumPy core E2E passed. NumPy/Cython appear in the profile-specific SBOM/notices as packaged/build-only inputs respectively. Production-safe benchmarks and the benchmark-dependent evidence index are intentionally skipped for this candidate; no default/release/deployment approval or performance promise follows.

Signed commits `562b22c` and `76b4915` complete the profile-aware evidence and product-E2E closure. Run `29980168692` binds Host, Guest, `profile-candidate`, and `numpy-core` to commit `76b49158cc6c4824491561531bfe7e34872cb820` and artifact SHA-256 `90c27951b2d8c2c7a8b42705b365cb4231c6dad207aad5260d55d2f9a85f1034`. Normal artifact E2E now covers selected arrays/shapes/reductions/linalg, a real reshape `ValueError`, and absence of a NumPy-module marker across two Runs on the same Host Runner. Fresh compile and execute medians were 5.492s and 4.627s; prepared factory-to-ready, first execute, steady execute, capability, and retained Guest linear memory were 9.996s, 1.789ms, 1.706ms, 27.223ms, and 128 MiB. These are descriptive candidate measurements without thresholds or approval. Base run `29980164790` remained green and continued to own the production-safe evidence path.

Remaining promotion work is policy rather than an unverified build or measurement claim:

- decide whether the explicit `numpy-core` candidate should remain manual-only or receive a recurring non-default gate;
- preserve the fresh-instance fallback and capacity-zero default;
- keep `random`, FFT, test-only extensions, and arbitrary packages excluded until they independently pass the same normal producer/consumer and policy gates.
