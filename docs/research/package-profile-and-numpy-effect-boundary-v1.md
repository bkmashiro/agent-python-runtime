# Pysolate package profiles and NumPy effect boundary v1

**Status:** Phase 0 design freeze. This document maps the live boundary at signed baseline `48a17fd0ef7b9207408ea46bbac269d5f479f8cf`; it is not evidence that `numpy-core` has been built.

## Decision

Pysolate will gain a repository-declared package-profile build system beside `base`. The first statically linked native-package profile is `numpy-core`. A profile is a closed build recipe and artifact identity, not a runtime package request.

The profile classes are:

| Class | Initial profile | Package form | Link boundary |
|---|---|---|---|
| base | `base` | CPython stdlib plus `agent_runtime` | existing Pysolate final link |
| pure Python | `attrs-770` | verified VFS package tree | existing Pysolate final link plus VFS pack |
| static native | `numpy-core` | Python package tree plus target static archives and native-module registration | Pysolate's own CPython/runtime/VFS final link |

`base`, `attrs-770`, and `numpy-core` share one manifest/profile contract. The default workflow remains `base`; package profiles are explicit artifacts and do not silently enlarge base.

## What “Pysolate DBI” means in the live implementation

The repository does not contain a DynamoRIO-style instruction tracer or a complete native-call monitor. The enforceable boundary is the composition below. Evidence and paper text must use these exact classes rather than claim universal dynamic-binary visibility.

| Boundary | Live enforcement | What it proves | What it does not prove |
|---|---|---|---|
| Host capability calls | the only typed Host-call import is explicitly instantiated by the wazero engine; `runtime/engine.EffectProbe` marks attempted calls | whether Agent execution attempted Pysolate Host authority | arbitrary native C calls are not individually traced |
| Workspace/temporary Host filesystem | `Engine.moduleConfig` mounts workspace and tmp only when the Engine owns a workspace lease; analyzer and scratch lanes reject authority-bearing Engines | no Host workspace or Host tmp preopen exists in authority-free producer/analyzer lanes | internal VFS reads/writes and linear-memory mutation are not “no instruction executed” claims |
| Network/process | no socket/process Host capability or WASI preview1 network/process preopen is provided | native package code cannot acquire ambient external network/process authority through the configured Guest | source-level purity still requires admission; unavailable calls may trap or return errors |
| Import authority | source validation requires declared imports; a sealed importer and audit event bind admitted roots to the artifact inventory/profile | Agent code cannot dynamically widen imports beyond the exact profile contract | package initialization may internally import its own verified closure |
| Stdout | analyzer/scratch/producer-style lanes use `forbiddenStdout` and reject if used | direct stdout bypass is terminally visible | stderr is bounded diagnostic data and must be dropped from public evidence |
| Result transport | Host copies length-prefixed Guest memory under explicit bounds | no Guest pointer is trusted as durable Host state | current response ABI is not yet an ndarray transport and must be extended deliberately |
| Freshness/isolation | ordinary fresh modules and Linux private-COW slots have explicit lifecycle evidence | consumers receive private Guest linear memory and Python heaps | COW does not mean zero logical copy for an ndarray result |
| Time/entropy | `baseModuleConfig` supplies system clock and crypto entropy by default, or deterministic providers under deterministic verification | exact provider selection can be recorded | default execution cannot claim “no time/random access” merely from Host-call evidence |
| Source/AST admission | bounded Guest analysis plus Host-bound source/AST/pass/profile identities | only preregistered source shapes/APIs enter the producer lane | it is not whole-program native semantic proof |

For NumPy producer closure, the required claim is narrower:

1. exact artifact/profile/import closure;
2. source/AST/API allowlist rejects `numpy.random`, object construction, dynamic imports and unknown calls;
3. no Broker or workspace is attached;
4. zero typed Host-call attempts;
5. forbidden stdout unused;
6. only a validated bounded ndarray payload is published;
7. each consumer gets a fresh private materialization;
8. all blob leases reach consumed/rejected/discarded terminal state.

CPython/NumPy initialization may use the configured clock/entropy providers. That lifecycle activity is recorded separately and is not attributed to Agent authority.

## Static native profile manifest v1

A checked-in profile descriptor must close over:

- schema version and profile ID;
- artifact filename and target;
- base `sources.lock.json` identity;
- package source ID, version, immutable URL, SHA-256, license, upstream commit and tree;
- reference build lineage, if any;
- every build-only Python package/tool used by the recipe, including version, source and hash;
- recipe files and hashes;
- compile definitions and target ABI;
- expected static archives and extension-module names;
- final Python package tree hash and file count after staging;
- target-Guest import inventory and operation qualification IDs;
- Pysolate runtime/CPython patch identities;
- memory model and limits;
- manifest, SBOM and notices inclusion rules.

Validation is closed-shape and fail-closed. Unknown keys, missing/duplicate sources, undeclared tools, symlinks escaping the staged root, unexpected native archives/modules, tree drift, profile mismatch and restored-cache drift all fail before packing.

The producer accepts only a profile ID known to the repository. It does not accept a URL, wheel, requirement string, arbitrary shell fragment or pip index from a Run.

## NumPy source and reference lineage

The initial source candidate is:

- upstream: `numpy/numpy`;
- commit: `7bc18034031f32e5d03bb646c472dabd1623e9d5`;
- release preparation: NumPy `1.26.0b1`;
- codeload archive SHA-256: `9a34aaef957033ff8a3a865e8f0172eb7de4cf4c2891195a56c13e915fb86014`;
- license: BSD-3-Clause (upstream `LICENSE.txt`).

Reference-only build lineage:

- repository: `bkmashiro/wasi-wheels`;
- commit: `184cce0b537088be76e1e8a06d6fe742e2f29ff4`;
- `numpy/build.sh` SHA-256: `5c6f9b675e4ba5c2027779136c6422cf22a36683a2f37a776e9a38c5985832d7`;
- `numpy/build-static.sh` SHA-256: `d6c1401dc8a1f55e73c48099b26bc6942d49c1f872c678d43b33b640be4540f0`.

Those scripts establish useful WASI compiler flags and a static-archive interception technique. Their prebuilt archives are not Pysolate artifacts and are not accepted as final inputs. Any adapted code must have explicit provenance/license handling; otherwise reimplement the small mechanism from the documented behavior.

## Build ordering

For `numpy-core`, the formal producer must:

1. restore or build Pysolate's exact patched CPython/WASI base toolchain;
2. fetch and verify every profile source/tool before execution;
3. cross-build NumPy against that exact CPython sysconfig and wasi-sdk;
4. produce a deterministic package tree plus declared static archives;
5. link those archives/native module registrations into Pysolate's final `guest/src/runtime.c` artifact, preserving existing runtime exports and import gate;
6. stage the NumPy Python tree into the same VFS pack used by the artifact;
7. rehash the staged tree after copy and before pack;
8. run artifact verification, import inventory and numerical qualification against the final packed Pysolate artifact;
9. bind all identities into manifest/SBOM/notices/cache evidence;
10. prove a clean rebuild and a verified cache hit produce the same artifact identity.

A componentize-py success or standalone wasmtime success from the reference repository is only archaeological input. It cannot substitute for steps 5–10.

## NumPy result boundary v1

The first typed payload admits only:

- `numpy.ndarray`;
- C-contiguous canonical bytes;
- rank `0..8`;
- total payload `0..16 MiB`;
- explicit little-endian numeric dtype from a closed enum;
- shape dimensions whose checked product times itemsize equals `nbytes`;
- no `object`, string, structured, datetime/timedelta, user-defined or unknown dtype;
- no negative stride, non-contiguous view, Fortran-only layout or aliased foreign buffer.

The Host stores one copied immutable body keyed by its digest and a canonical descriptor bound to source, AST, pass/config, artifact/profile/import closure, inputs, Run/privacy partition and producer execution. A consumer receives a one-shot lease, verifies the complete descriptor/body identity, copies into private Guest-owned storage and reconstructs a local ndarray. No pointer, Python object, Guest linear-memory view or allocator state crosses the boundary.

## Campaign interpretation

The frozen matrix is `docs/evidence/numpy-result-reuse-case-matrix-v1.json`; preregistration is `docs/evidence/numpy-result-reuse-preregistration-v1.json`.

The economics campaign contains 240 seeded coordinates:

```text
2 platforms × 2 readiness profiles × 10 economics cases × 2 treatments × 3 trials
```

The matrix varies import-only, elementwise, reduction and matrix work; small/medium/large payloads; 0/10/45 second lead gaps; and 1/2/4 consumers. Eight adversarial controls are mechanism cases and do not become timing cells.

Mechanism closure requires every control and lifecycle invariant to pass. Economics has no universal-positive gate. Mixed and negative cells are retained and interpreted as a break-even surface for the paper.

## Explicit exclusions

This phase does not authorize pandas, SciPy, Arrow, Parquet, pickle, object dtype, arbitrary package installation, generic plugin discovery, Host-owned Python heap state, zero-copy pointer sharing, cross-Run durable cache, implicit replay or broader Broker/workspace authority.
