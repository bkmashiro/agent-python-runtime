# Package Profiles and NumPy Result Reuse Autonomous Mega-Goal

> **For Hermes:** Execute this plan continuously in `/Users/yuzhe/projects/agent-python-runtime`. Read the whole file and live Git/code before editing. Prefer the thinnest real vertical slice, use TDD for behavior, make coherent signed commits, push after verified phases, and continue without waiting after ordinary checkpoints. Stop only at a named architecture/toolchain/safety gate below, not after a test, commit, artifact build, negative economics result or context compaction.

**Status:** Prepared on 2026-08-21; implementation not started.

**Goal:** Make package-bearing CPython/WASI Guests a first-class Pysolate artifact-profile capability alongside `base`, prove it with an exact `numpy-core` artifact built through Pysolate's own patched/instrumented runtime chain, then implement and evaluate source-bound typed NumPy result reuse across fresh Guests without sharing Python heap, pointers, authority or workspace state.

**Architecture:** A profile selects a repository-declared, source-locked build recipe before execution. Pysolate builds the package and native archives against the exact CPython/WASI/sysconfig/toolchain used by its own Guest, links them into the same reactor as `guest/src/runtime.c` and the Pysolate observation/denial boundary, packs verified Python package files into the VFS, and binds all sources, patches, native modules, qualification operations and profile identities into the distribution evidence. NumPy result reuse moves one bounded C-contiguous numeric array through a Host-owned immutable blob and reconstructs a private local array in each fresh consumer. The Host owns admission, storage, leases and evidence; no Guest object, memory alias or executable state crosses Runs.

**Tech stack:** Go Host/runtime, CPython 3.14/WASI, wasi-sdk, wazero/private-COW, C/native extension archives, NumPy, Python AST, strict JSON evidence, repository build/review scripts, signed Git commits.

**Project:** `/Users/yuzhe/projects/agent-python-runtime`

---

## User intent and research interpretation

Yuzhe wants the current scalar mechanism result interpreted as a fixed-cost control, not as evidence against expensive package workloads. NumPy import and computation are the first real package workload. Mixed or negative economic cases are still useful paper evidence when the mechanism is correct and the applicability boundary is measured honestly.

The existing external `bkmashiro/wasi-wheels` NumPy build is a reference implementation, not the artifact to ship inside Pysolate. Its current fixed reference is:

```text
repository    https://github.com/bkmashiro/wasi-wheels
commit        184cce0b537088be76e1e8a06d6fe742e2f29ff4
NumPy source  numpy/numpy@7bc18034031f32e5d03bb646c472dabd1623e9d5
release asset numpy-wasi.tar.gz
asset SHA-256 9482a6e05c3a71f0959636b7fa3a4c53705170d4b157f8204f19b148ce4f3af1
reported stack wasi-sdk 33, CPython 3.14.0, NumPy 1.26.0b1
```

Use that commit to understand required compiler definitions, disabled BLAS/LAPACK/SVML paths and static archive interception. Do not trust or repackage its prebuilt component artifact as Pysolate evidence. Do not mutate or publish the external repository in this megagoal. If code is copied rather than independently reimplemented, verify an explicit compatible license first and retain attribution; a README license claim without a repository license file is not enough.

Pandas, Arrow, Parquet, SciPy and arbitrary native packages are out of scope. A dormant or incomplete `pandas/` directory in the reference repository is not evidence of a supported Pysolate profile.

## Baseline

Prepared against:

```text
branch       main
HEAD         4d2c6cec14a6800ae2fd51dfdde8c6b2dc29a832
upstream     origin/main aligned
worktree     clean
P5 historical gate  frozen no-go, 6 pass / 5 fail, 0 timing observations
P5R mechanism gate  macOS 11/11 and Linux private-COW 11/11 pass
```

Current package/build facts:

- `guest/build/build-guest.sh` builds Pysolate's patched CPython/WASI reactor, compiles `guest/src/runtime.c`, links static CPython dependencies and wasi-vfs, embeds the Python/VFS tree, runs real-Guest import inventory/qualification, and emits manifest/SBOM/notices/checksums.
- `base` is the default artifact profile.
- `attrs-770` is a successful package-bearing profile but is special-cased throughout the shell/Python build path and is pure Python.
- `guest/build/profiles/attrs-770.lock.json` and `guest/build/extension_profile.py` already establish useful source/patch/tree/qualification controls.
- the normal workflow intentionally builds only `base`.
- no repository-supported `numpy-core` artifact/profile currently exists.
- P5R's final artifact is `base`; it cannot establish NumPy claims.
- the external NumPy build produces WASI package/native outputs but does not itself establish Pysolate runtime exports, source/import gates, Pysolate DBI/effect boundary, distribution identity or fresh-Guest authority invariants.

## Value function

Prioritise, in order:

1. preserve Pysolate's authority, source, import and effect-observation/denial boundaries;
2. make package profiles reproducible, source-locked and fail closed;
3. prove a real NumPy artifact inside the Pysolate reactor, not beside it;
4. preserve fresh Guest and private-COW semantics;
5. transport one narrow typed ndarray safely and measurably;
6. produce truthful break-even evidence across compute, result size, lead gap and consumer count;
7. extract only the shared pass/consumer seams demonstrated by two real consumers;
8. keep `base` small, unchanged by default and independently verifiable.

A negative or mixed economics surface is a successful paper result. Mechanism correctness is mandatory; universal speedup is not.

## Frozen boundaries

### Artifact and package profiles

- Profiles are repository-declared build inputs, not runtime `pip`, package resolution or arbitrary user-supplied build recipes.
- `base`, `attrs-770` and `numpy-core` are explicit sibling profile IDs.
- `base` remains the default workflow artifact and must not absorb NumPy bytes or imports.
- Every package profile binds exact package source, source/archive SHA-256, source commit where available, build-recipe identity, patch identities, build dependencies, target ABI/sysconfig, installed tree, linked native archive/module inventory, qualification operations and final artifact identity.
- Build caches include all effective profile and recipe identities. Cache hits rerun complete distribution verification.
- Native extensions must be built against Pysolate's exact CPython/WASI build and linked into Pysolate's own final reactor. Componentize-py success or an external wheel import is only prior art.
- Preserve Pysolate's CPython timer/import patches, `guest/src/runtime.c` exports, wasi-vfs behavior, source validation, restricted body imports and actual DBI/effect boundary. Phase 0 must map the repository's concrete instrumentation points; do not infer coverage from the label alone.
- Unknown profile, package, recipe, native module, import operation, ABI or manifest fields fail closed before Guest execution.

### NumPy result capsule v1

Admit only:

- one `numpy.ndarray` produced under exact `numpy-core` artifact/profile/import identities;
- C-contiguous data;
- owned or copied canonical storage with no external mutable alias;
- fixed allowlisted numeric dtypes, initially `bool`, signed/unsigned fixed-width integers, `float32` and `float64` only when exact bitwise/NaN policy is defined;
- explicit little-endian canonical representation or a typed normalization step;
- bounded rank, dimensions, element count and encoded bytes;
- canonical descriptor containing schema, codec, dtype, shape, order, nbytes, data SHA-256, producer decision/source/AST/input/environment/profile/import/pass identities and Host blob identity;
- one Host-owned immutable canonical body;
- one bounded copy from producer Guest to Host and one bounded copy into each consuming fresh Guest;
- a Host-minted one-shot consumer lease per logical consumer, with explicit consumed/rejected/discarded terminal evidence.

Reject:

- `dtype=object`, Python objects, structured/object fields, arbitrary extension dtypes or executable reducers;
- pickle, marshal, cloudpickle, raw pointers, borrowed buffers, shared linear memory, transferable arenas or Python heap snapshots;
- negative/overlapping strides, views with mutable aliases, Fortran-only layout in v1, memmap or filesystem-backed arrays;
- unknown endianness/dtype/shape, integer-overflow in size calculation, descriptor/body mismatch, corruption or partial copy;
- producer Host calls, Broker/workspace authority, time/random/network/process or unclassified DBI/WASI effects;
- cross-artifact/profile/import/pass/config/input/environment reuse;
- post-effect fallback/replay or implicit retry unless the Host proves `not_started/not_started`.

Each consumer receives a private local ndarray. Mutation by one consumer must not alter the Host blob or any sibling consumer.

### Mechanism separation

- run-scoped early preparation, single-flight coalescing and durable cache remain separately named mechanisms and identities.
- The first vertical slice may use one producer and one consumer. Fan-out later creates independent consumer leases over one immutable Host blob.
- Durable cross-Run retention is not implied by a run-scoped capsule and requires a separate preregistered subphase.
- `semantic_pre_dispatch` remains overlay-only and leaves final Python unchanged.
- `prepared_pure_region`/typed-result consumers remain execution-patch consumers with exact final-source validation.
- Shared registration code must not erase this distinction or become a generic compiler/plugin framework.

## Explicit non-goals

Do not implement or claim:

- pandas/DataFrame/Arrow/Parquet/SciPy support;
- PyPI-compatible runtime package installation or dependency solving;
- arbitrary wheels, arbitrary native extensions or user-defined build scripts;
- generic object transport or automatic serialization;
- zero-copy NumPy between Guests;
- shared Python interpreter, heap, allocator or module state;
- arbitrary NumPy purity inference;
- universal profitable reuse;
- production-default activation;
- package publication, GitHub release mutation or manual CI triggering;
- changes to `pysolate-thesis`, papers, slides or PDFs during implementation;
- changes to frozen P5 matrix/preregistration/artifact/no-go evidence;
- a generic IR, SSA, bytecode optimizer or dynamic pass plugin loader.

## Autonomy and execution rules

- Main controller owns architecture, implementation and final verification.
- Delegate only bounded read-only archaeology or post-fix review with independent value. Never allow a second writer in the shared worktree.
- Use RED → GREEN → refactor for executable behavior. Record the exact RED and expected missing behavior in the active plan before implementation.
- Prefer coherent vertical-slice commits over checkbox commits. Sign and push each independently useful verified phase, then continue.
- Do not stop after local unit tests, an artifact build, a Linux run, a negative benchmark row or a commit.
- Use local macOS for normal development. Use `ssh shell2` → `gpu31` for bounded Linux x86_64 Guest builds and private-COW execution. Reuse approved toolchain/cache roots; clean run-scoped staging and processes.
- Do not manually trigger GitHub Actions. No Docker, paid cloud or production resources without approval.
- Raw sources, build logs, request/result bodies and large artifacts stay under `~/.hermes/evidence/pysolate/`; commit only body-safe identities, aggregate evidence, recipes/locks and necessary source patches.
- Never rewrite preregistration after formal results are observed. Supersede evidence append-only.

## Global verification

Use focused gates during implementation. At shared build-contract changes and final closeout run:

```bash
cd /Users/yuzhe/projects/agent-python-runtime

git diff --check
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
go test ./... -count=1
go vet ./...
env -u AGENT_RUNTIME_GUEST go test -race ./... -count=1
```

If the full suite has a known bounded timeout, preserve exact partial/targeted output and do not relabel it PASS. Real-Guest claims require named non-skipping tests under the exact frozen artifact.

---

## Phase 0 — Live archaeology, DBI map and preregistration

**Promise:** Freeze the actual seams and experiments before implementation.

Tasks:

- [ ] Re-read this plan, live Git status/history, P5R closure, `docs/unified-execution-profiles.md`, attrs profile evidence and the semantic-speculation roadmap.
- [ ] Trace the base reactor from source locks through patched CPython, `guest/src/runtime.c`, static link, VFS pack, manifest, import qualification and wazero execution.
- [ ] Identify every concrete Pysolate DBI/effect observation or denial point relevant to native package execution. Record what is observed at Host/WASI boundaries and what is unavailable; do not claim arbitrary native memory instruction tracing unless it exists.
- [ ] Inspect `bkmashiro/wasi-wheels@184cce0...` read-only and map only the NumPy/WASI build facts needed for a Pysolate-native recipe.
- [ ] Verify upstream NumPy source commit/version/license and all host build dependencies. Pin URLs and SHA-256 values through the existing locked-source machinery.
- [ ] Define the package-profile schema, `numpy-core` qualification oracle and build acceptance matrix before seeing final artifact results.
- [ ] Preregister the ndarray capsule mechanism/economics matrix before formal timing.

The preregistration must vary at least:

```text
compute class     import-only | elementwise | reduction | matrix/FFT-like supported kernel
payload bytes     small | medium | large within bound
lead gap          0 | partial-overlap | enough-to-hide-provisioning
consumers         1 | 2 | 4
lane              recompute | run-scoped typed reuse
platform          darwin_arm64 | linux_amd64 private-COW where supported
```

Every coordinate binds exact source, inputs, dtype, shape, operation, artifact/profile/import closure, trial count, shuffle seed, timeout, warm/cold state, output oracle and exclusions.

**Gate P0:** Body-safe preregistration and source/toolchain/DBI map review PASS. If the concrete DBI boundary cannot observe or deny the forbidden effect classes required for publication, stop with the smallest missing boundary rather than pretending the external build inherits it.

## Phase 1 — General repository-declared package profile contract

**Promise:** Replace attrs-only branching with a small explicit profile contract while preserving exact base behavior.

Likely seams, subject to live archaeology:

```text
guest/build/profiles/
guest/build/package_profile.py
guest/build/build-guest.sh
scripts/build-guest-workstation.sh
scripts/verify-workstation-build.py
guest/build/{write-manifest.py,verify-artifact.py,write-supply-chain.py}
guest/build/{import_inventory.py,import_qualification.py}
guest/tests/
scripts/tests/
```

Tasks:

- [ ] RED-test exact profile schema, canonical identity, unknown fields, duplicate keys, source/recipe/patch/module/qualification drift and path/symlink attacks.
- [ ] Define explicit profile kinds for `base`, pure-Python package and statically linked native package without creating an arbitrary plugin API.
- [ ] Make profile selection table-driven enough that `build-guest.sh` does not grow package-name conditionals for each new supported package.
- [ ] Bind profile recipe identity into CPython/final cache keys and distribution evidence.
- [ ] Preserve final-cache verification, effective source lock, SBOM, notices, import inventory and qualification.
- [ ] Migrate `attrs-770` to the shared contract with byte/identity-equivalent behavior where practical; retain its private patch requirement and existing oracle.
- [ ] Prove `base` build inputs, default workflow and output naming remain unchanged and contain no package bytes.

**Gate P1:** Base and attrs profile contract tests PASS; one real cached or fresh attrs build verifies under the new path; base/attrs mismatches fail before execution. Signed commit and push, then continue.

## Phase 2 — Pysolate-native `numpy-core` artifact

**Promise:** Build NumPy as part of Pysolate's reactor/profile pipeline, not as an external component.

Tasks:

- [ ] Add a locked `numpy-core` profile and recipe using the exact Pysolate CPython 3.14 WASI sysconfig and wasi-sdk.
- [ ] Adapt only necessary reference-build facts: target flags, `NPY_NO_SIGNAL`, disabled SVML/BLAS/LAPACK, deterministic native archive production and stale-build cleanup.
- [ ] Build NumPy native module archives reproducibly and link the exact required archives into the final Pysolate reactor at the existing static link stage.
- [ ] Pack verified NumPy Python files into `/usr/lib/python3.14/site-packages/numpy` and reject placeholder `.so` files or unlinked native modules that only appear importable by path.
- [ ] Extend manifests/SBOM/notices with upstream source, build recipe, patches, archive/module inventory and installed tree identities.
- [ ] Add bounded profile-specific memory values only if measured NumPy import/operation requires them; bind them into artifact identity and do not raise base defaults.
- [ ] Verify the final module imports inside the exact `apyrun` Pysolate Guest, not componentize-py.

Required qualification operations include:

- `import numpy` and exact `numpy.__version__`;
- deterministic array construction, reshape, slicing/copy and dtype checks;
- elementwise add/multiply;
- reduction with a deterministic exact or tolerance-bound oracle;
- a nontrivial supported matrix operation;
- repeated fresh Runs with no module-state leakage;
- forbidden/undeclared import and profile mismatch controls;
- actual Pysolate DBI/WASI forbidden-effect probes with no publishable result.

**Gate P2:** Exact `numpy-core` artifact builds and verifies from locked inputs, imports under Pysolate, passes the oracle twice in distinct fresh Guests, and preserves DBI/effect evidence. If NumPy requires an unmaintainable CPython/wazero fork, dynamic loader weakening or unavailable authority channel, record the blocker and stop before result transport.

## Phase 3 — Minimal typed pass/consumer seam

**Promise:** Two real consumers share only proven provenance/admission machinery.

Consumers:

```text
semantic_pre_dispatch  overlay_only
prepared_pure_region   execution_patch
```

Tasks:

- [ ] RED-test exact pass schema/version/config/consumer registration and unknown-combination rejection.
- [ ] Introduce one immutable registration shape carrying pass identity, analyzer identity, config identity, consumer kind and required bindings.
- [ ] Keep verified analysis/decision handles opaque; never expose mutable AST or authority handles through a generic registry.
- [ ] Preserve consumer-specific legality, launch, final-source validation, claim and teardown code.
- [ ] Migrate both consumers without changing frozen P5 evidence or existing observable behavior.
- [ ] Reject duplicate registration, version/config drift, consumer-kind confusion and overlay/patch substitution.

**Gate P3:** Both consumers pass focused/adversarial tests through the shared seam; old direct bypasses are removed or explicitly internal; no generic plugin loader, IR or pass scheduler exists. Run full Go/vet/race gates, sign, push and continue.

## Phase 4 — Host-owned immutable blob and lease lifecycle

**Promise:** Establish a package-neutral bounded byte transport before NumPy semantics.

Tasks:

- [ ] RED-test canonical blob descriptor, byte/entry/count bounds, SHA-256 verification, immutable copy ownership and exact identity joins.
- [ ] Implement a Host-owned in-memory run-scoped blob store with one canonical body and typed metadata.
- [ ] Producer publication is atomic: failure, cancellation, timeout, forbidden effect, incomplete copy or hash mismatch publishes nothing.
- [ ] Create one-shot consumer leases with `ready → claimed → consumed` and `ready/claimed → rejected/discarded` terminal states.
- [ ] Prove sibling leases cannot consume each other and teardown projects every terminal disposition.
- [ ] Prove returned/caller-provided byte slices cannot mutate stored content.
- [ ] Add explicit resource cleanup and bounded aggregate retained bytes; no durable cache yet.
- [ ] Attach body-safe observation records without logging payloads.

**Gate P4:** Unit/race/adversarial lifecycle tests PASS, including cancellation and concurrent claim; Host blob and Guest/caller buffers do not alias. Signed commit and push.

## Phase 5 — NumPy ndarray codec and fresh-Guest materialization

**Promise:** Move one supported ndarray value safely between independent exact-profile Guests.

Tasks:

- [ ] Define `numpy_ndarray_c_v1` descriptor and exact dtype/shape/order/endianness rules.
- [ ] Add producer-side validation and canonical bytes extraction under `numpy-core`.
- [ ] Copy producer bytes across the existing bounded Guest/Host ABI; do not expose Guest addresses or raw memory mappings.
- [ ] Seal descriptor/body/profile/source/input/pass bindings in the Host.
- [ ] In each fresh consumer, copy bytes into private local storage and construct a C-contiguous ndarray with correct dtype/shape.
- [ ] Make consumer mutation isolation executable: mutate consumer A, then prove Host hash and consumer B remain unchanged.
- [ ] Reject object dtype, views/strides outside v1, dtype/shape/nbytes overflow, body corruption, wrong profile/artifact/import closure and stale source.
- [ ] Preserve original-vs-derived result/error/traceback/log parity and exact final-source patch binding.
- [ ] Extend body-free evidence with copy bytes/durations, materialization duration, lease lifecycle and fresh Guest identities.

**Gate P5:** A non-skipping real `numpy-core` producer→Host→fresh consumer test passes on macOS and Linux private-COW where supported, plus all adversarial cases. No shared Python/linear-memory state and no authority expansion.

## Phase 6 — NumPy source-bound producer admission

**Promise:** Admit one narrow deterministic NumPy producer without claiming arbitrary NumPy purity.

Tasks:

- [ ] Start from the existing prepared-region decision/patch contract and exact final-source validation.
- [ ] Define a tiny explicit/allowlisted NumPy producer subset or an equally narrow source-bound declaration whose legality can be validated soundly by exact-Guest analysis and Host policy.
- [ ] Bind imported module/version, operation, canonical inputs, dtype/shape limits, immutable roots, execution profile and capability plan.
- [ ] Reject unknown calls, random/time, mutation of external inputs, file/network/process access, dynamic import, callbacks, object dtype and unsupported NumPy APIs.
- [ ] Execute producer in a fresh authority-free `numpy-core` Guest, publish only after successful DBI/effect and output validation, and execute final derived code in another fresh Guest.
- [ ] Preserve unchanged original lane and prove no fallback/replay after producer effects or ambiguous terminal state.

Do not grow an allowlist merely to make the benchmark pass. A small honest producer subset is sufficient.

**Gate P6:** Positive and adversarial real-Guest cases prove exact admission, parity, freshness, no effect, source/AST/input/profile binding and complete blob/lease lifecycle. Independent post-fix review has no unresolved blocker.

## Phase 7 — Preregistered break-even campaign

**Promise:** Measure where typed NumPy reuse wins or loses; do not require universal positive economics.

Tasks:

- [ ] Freeze artifact, harness, source cases and shuffled trial schedule before timing.
- [ ] Compare matched recompute and reuse lanes with equivalent prepared capacity.
- [ ] Separate and report: analyzer, producer provisioning/import/compute, Guest→Host copy, hash/seal/store, consumer provisioning, Host→Guest copy, ndarray reconstruction, final execution and teardown.
- [ ] Record critical-wall intervals without summing overlapping stages.
- [ ] Measure one, two and four consumers and lead gaps that expose zero, partial and full overlap.
- [ ] Include at least one cheap negative control, one compute-heavy/small-result candidate and one payload-heavy candidate expected to expose copy costs.
- [ ] Verify outputs by exact bytes where appropriate and tolerance policy where floating operations require it; bind oracle policy in preregistration.
- [ ] Run macOS and Linux private-COW formal matrices with bounded trials. Record host load/environment evidence; do not attribute residual Linux cost without stage evidence.
- [ ] Produce a body-free break-even surface rather than one aggregate speedup.

Required paper-safe outcomes:

- mechanism correctness is reported separately from economics;
- mixed/negative cells remain in the result;
- claims identify exact NumPy/profile/version, dtype/shape/operation, consumers and lead gap;
- scalar P5R remains the fixed-cost control, not a general workload proxy;
- no claim extends to pandas, arbitrary arrays, durable cache or production default.

**Gate P7:** Campaign evidence/reviewer validates identities, parity, equivalent capacity, timing accounting and complete rows. Economic loss does not block closeout. It changes applicability recommendations only.

## Phase 8 — Optional run-scoped fan-out/single-flight decision

Enter only if Phase 7 shows repeated producer identity or multiple consumers in the preregistered cases.

- [ ] Reuse existing single-flight identity/lifecycle machinery rather than inventing a second coalescer.
- [ ] Keep producer execution identity separate from each logical consumer lease.
- [ ] Prove 2/4 logical consumers produce one physical producer only for exact identity matches.
- [ ] Reject coalescing on input/root/profile/artifact/pass/config/privacy/authority drift.
- [ ] Keep storage run-scoped. Do not add durable cross-Run retention unless separately preregistered and justified by observed repetition.

If no repeated opportunity is observed, record `not entered`; do not manufacture a cache feature.

## Phase 9 — Final review, documentation and closeout

Tasks:

- [ ] Run an independent bounded review of build trust, native archive/module completeness, DBI/effect preservation, profile isolation, blob ownership, ndarray codec bounds, source/AST/profile/input binding, lease lifecycle, replay, timing fairness and evidence privacy.
- [ ] Reproduce every valid finding in the main controller, fix it, add a regression and rerun proportional gates.
- [ ] Run global verification and exact artifact reviewers.
- [ ] Verify `base` default and frozen P5 historical files are unchanged.
- [ ] Write concise Current / Measured / Rejected / Deferred documentation.
- [ ] Update the active roadmap and this completion log with exact artifact/harness/evidence identities.
- [ ] Keep large/private evidence local and commit body-safe summaries only.
- [ ] Sign final coherent commits, push, verify `main == origin/main`, clean local/remote staging and report clean status.

Final report must distinguish:

```text
Supported artifact profile
Supported ndarray codec/type/shape bounds
Mechanism correctness
Measured break-even cells
Negative/mixed cells
DBI/effect visibility actually established
Deferred package/type/cache claims
```

## Hard stop conditions

Stop only if one of these is proved and recorded with commands/output/current Git status:

1. preserving Pysolate's runtime/DBI/import/authority boundary requires an unmaintainable CPython or wazero fork after one bounded lower-risk build alternative;
2. NumPy cannot be linked/imported into the exact Pysolate CPython/WASI artifact without weakening dynamic import/native symbol or artifact-verification contracts;
3. safe ndarray transport requires shared mutable memory, pointer transfer, pickle or arbitrary Python heap continuation;
4. the exact typed descriptor cannot prevent aliasing/corruption/shape-size ambiguity within bounded implementation scope;
5. a required external source/toolchain is unavailable and no locked reproducible alternative exists;
6. implementation exposes a genuine architecture decision between incompatible authority/security contracts;
7. work would require package publication, production access, paid resources, Docker or manual CI not authorized here;
8. all phases are complete and final closeout gates pass.

Do **not** stop because:

- NumPy reuse loses for one or many workload cells;
- Linux is slower than macOS;
- a package build takes a long but bounded time;
- a focused bug needs another RED/GREEN slice;
- a commit/push/artifact/review checkpoint completed;
- a subagent times out but its concrete findings can be inspected and independently verified.

## Completion log

- **P0–P2, complete:** preregistration and package/effect boundary freeze landed in `f091186`; the closed profile registry landed in `5e82cb7`; the locked `numpy-core` producer and mechanism closeout landed in `47070b1` and `a51ce3a`. The canonical artifact is `sha256:1b759febddb9dd0d65760c74b21b07c4f04fa992ce761639fbb9baef155926aa`.
- **P3, complete:** `fc39f0e` added the closed shared registration contract for `semantic_pre_dispatch` and `prepared_pure_region` without changing either consumer's projection or authority.
- **P4, complete:** `2db7b8f` added the Host-owned run-scoped blob/lease lifecycle. Follow-up boundedness fixes are included in the P5 mechanism commit; full unit/race gates and the Linux/386 max-`uint32` runtime regression pass.
- **P5, complete:** `16c141d051c43a2a89383336b3c4ca11fe9bb0c5` (`tree c9bd3255325b12c6ad54b1a78772b439ddb0d9dd`) seals bounded `numpy_ndarray_c_v1` bodies and exact producer/consumer joins. macOS evidence is `sha256:e22a5db558c250877475c648a93734b325f92f689392c5bdc6df67c4beac7ae9`; Linux private-COW evidence is `sha256:a974a959964a24e7e20d9f0aa7f6d278907c074b738f9f51c7afbcdc83dfcd38`. Matched result and error/log lanes pass, consumer mutation is isolated, every Guest records zero capability calls, all seven Linux Guests select private-COW without fallback, and teardown retains zero bytes.
- **Next:** P6 narrow source-bound NumPy producer admission. No timing in P2/P5 is admitted into the frozen economics campaign.
