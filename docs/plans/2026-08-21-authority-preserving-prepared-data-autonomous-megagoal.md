# Authority-Preserving Prepared Data Autonomous Mega-Goal

> **For Hermes:** Execute this research-prototype goal continuously in `/Users/yuzhe/projects/agent-python-runtime`. Read this file, the closed predecessor goal, the canonical pre-dispatch contract, and live Git/code before editing. Build the smallest falsifiable mechanism, not a production framework. Use focused tests per slice, parallelize file-disjoint work where it has independent value, integrate and verify centrally, make coherent signed commits, push, and continue. Stop only at the gates below, not after one test, commit, negative result, worker return, or context compaction.

**Status:** Active on 2026-08-21; Phases 0–6 complete, Phase 7 active.

**Goal:** Prototype authority-preserving temporal offload for one explicitly Host-declared immutable NumPy dataset load: begin its physical read and bounded decode before final code generation completes, retain the result as one typed Host-owned staged object, let unchanged final Python claim it only at the original dynamic occurrence, and evaluate fresh-Guest private-COW/data-local consumption against serial execution, EAGER-style persistent execution, raw-read-only pre-dispatch, and the existing private-copy path.

**Architecture:** Source analysis may identify a syntactic `numpy.load` candidate but cannot authorize it. Only an exact `PreparedDataContract` in the sealed Host capability plan can admit early physical work. The Host resolves one immutable `.npy` source under that contract; an authority-free loader or equally narrow trusted codec validates and prepares one bounded numeric C-contiguous ndarray; the Host records physical lifecycle separately from logical claim. A package-imported `numpy-core` prepared shard supplies initialized CPython/NumPy state through private COW. A fixed research extent may provide sealed dynamic data pages to fresh consumers; no generic object store, heap transfer, public fanout language, durable cache, or production-default optimizer is required.

**Research thesis:** Pysolate originally moved agent computation into an isolated WASM Guest while retaining Host-owned authority. This prototype moves qualified I/O and authority-free computation earlier on the generation timeline without moving authority outward: **move work earlier, not authority outward**.

**Tech stack:** Go Host/runtime and research harness, CPython 3.14/WASI, wazero experimental memory allocator/private COW on Linux, `numpy-core` artifact, Python AST/source bindings, immutable `.npy`, strict body-safe JSON evidence, signed Git commits.

**Project:** `/Users/yuzhe/projects/agent-python-runtime`

---

## Relationship to earlier goals

The predecessor `docs/plans/2026-08-21-package-profiles-numpy-result-reuse-autonomous-megagoal.md` is complete and historical. Preserve its frozen mechanism/evidence and its truthful P8 `not entered` decision. Do not edit or rerun its canonical P5/P6/P7 evidence.

This successor retains the useful unresolved mechanism hypotheses developed after that closeout:

1. package-imported private-COW shards rather than per-Run package warmup;
2. qualified data loading rather than arbitrary pure-compute inference;
3. early read plus early decode rather than raw-read-only pre-dispatch;
4. Host-owned sealed dynamic pages rather than producer-to-Host-to-consumer full copies;
5. fixed fresh-consumer fanout and data-local execution as experimental controls.

It deliberately does **not** reopen the predecessor's generic run-scoped single-flight P8. Exact coalescing may be reconsidered only if this new workload observes repeated identical prepared-data demand and a separately frozen gate justifies it.

The older semantic-speculation megagoal contains stale unchecked items. Later work already implemented the closed pass-registration seam, Host-owned blob/lease lifecycle, bounded NumPy codec, exact producer admission, and matched campaigns. Do not mechanically execute old checkboxes. The active scope is this file.

The fixed related-work comparator is EAGER, **Executing as You Generate: Hiding Execution Latency in LLM Code Interpreters**, arXiv `2604.00491v2`. EAGER executes accepted complete statements in one persistent interpreter. This prototype must not claim implementation parity: it compares that temporal-overlap model against explicit Host authority, staged typed objects, and fresh Guests.

## User intent

Yuzhe wants a paper-grade prototype, not production readiness. Development should move faster:

- prefer KISS, one narrow `.npy` path, and research-only adapters;
- remove checks that merely repeat an already-owned invariant inside the same trust domain;
- preserve independent validation at actual trust boundaries;
- use parallel development for file-disjoint, independently valuable lanes;
- keep the main controller responsible for architecture, authority spine, integration, final gates, signed commits, and push;
- treat mixed or negative economics as valid research evidence.

## Value function

Prioritise, in order:

1. explicit Host authority and truthful physical/logical side-effect evidence;
2. one real end-to-end overlap path before abstractions;
3. target package-shard and fresh-Guest semantics;
4. eliminate the measured full-body copy path with the smallest viable sealed-page spike;
5. compare moving data with moving compute;
6. obtain falsifiable timing/resource results;
7. keep code, contracts, evidence, and review surface small.

A simpler mechanism with a clear negative result is better than an unfinished generic runtime.

## Core authority rule

A peephole match never creates authority.

```text
AST/source match for np.load
        +
exact verified occurrence and canonical arguments
        +
sealed Host PreparedDataContract
        +
qualified immutable source/freshness/privacy/profile
        =
eligible early physical preparation
```

Without every conjunct, execute the ordinary unchanged path. In particular:

- `np.load`, `load_dataset`, `build_dataset`, variable names, import aliases, type hints, docstrings, tool schemas, and Guest-emitted metadata cannot create or widen authority;
- the Host declaration must be positive, versioned, exact, defensive, and included in capability-plan identity;
- early preparation is a physical attempt, not a logical Python call or cache hit;
- unchanged final Python reaches the original dynamic occurrence before logical claim;
- invalid suffix, earlier exception, branch-not-taken, cancellation, argument/source drift, timeout, and final-source mismatch produce typed rejected/cancelled/late/orphaned disposition and no logical call;
- external observability such as reads, provider logs, billing, quota, timing, and file metadata remains real and must be permitted by the declaration rather than described as effect-free.

## Narrow v1 contract

Admit only one research fixture shape equivalent to:

```python
import numpy as np
dataset = np.load(INPUT_PATH, allow_pickle=False)
```

The exact Python surface may be an explicit research helper or a verified derived patch if transparently intercepting `np.load` would add more complexity than value. Whatever surface is selected must bind the original source occurrence and preserve the baseline result/error/logical-call oracle.

Required source/data bounds:

- one immutable workspace-root or content-addressed regular `.npy` file;
- exact path/resource projection from a Host-authored contract;
- exact source content digest or immutable root plus plan freshness epoch;
- `allow_pickle=False` only;
- bounded little-endian numeric dtype;
- bounded rank, dimensions, element count, header bytes and body bytes;
- C-contiguous canonical array only;
- exact `numpy-core` artifact/profile/import closure and loader/codec version;
- one Run-private staged object identity and explicit terminal disposition.

Reject:

- `dtype=object`, structured/object fields, pickle, arbitrary Python object graphs;
- mutable latest-version remote datasets;
- HuggingFace dataset scripts, `trust_remote_code`, credentials, network, shared writable cache, archive extraction, symlinks or special files;
- pandas, Arrow, Parquet, memmap, arbitrary codecs and generic loader registration;
- direct early Guest workspace/network access;
- generic purity inference for `build_dataset()` or user functions;
- Host pointers/FDs/backing handles exposed to Guest Python;
- shared mutable ndarray state.

## Physical and logical lifecycle

The prototype must expose one explicit state machine:

```text
planned
→ read_issued
→ source_verified
→ decode_running
→ typed_staging
→ sealed
→ claimed | orphaned | cancelled | rejected
→ retired
```

Evidence must distinguish:

```text
physical: read/decode/seal start/end, bytes, cancellation, orphaning
logical: original occurrence reached, exact claim, logical call/result
```

A physical attempt may complete without a logical call. A logical claim may consume only the exact staged identity once. No completed object becomes a durable or cross-Run cache entry.

## Package shard model

The intended shard lifecycle is:

```text
qualified import closure
→ choose smallest repository-declared shard
→ CPython initialized
→ package imported in canonical preparation
→ immutable prepared baseline sealed
→ fresh MAP_PRIVATE clones
```

For `numpy`, select `numpy-core`; do not add a second request-time import-prewarm mechanism.

Current code must be tested rather than assumed: the generic canonical COW path currently calls `_initialize` and `runtime_init({})`; prove whether the resulting baseline already contains imported NumPy state. If not, add the smallest research-only or profile-owned preparation seam that snapshots after the exact trusted package import. Bind the resulting prepared-baseline identity to artifact/profile/import closure. Do not make `base` contain NumPy or make `numpy-core` the default.

## Dynamic data representation

The Host always owns backing and lifecycle:

```text
Host allocates bounded staging extent
→ one loader gets exclusive write authority or Host fills it directly
→ loader terminal/unmapped
→ Host validates descriptor/length/digest/padding
→ Host seals canonical backing
→ each fresh consumer receives a private mapping
→ written pages COW privately
→ leases terminal
→ Host reclaims backing
```

Guest code sees a normal bounded typed buffer/ndarray, not a Host pointer, FD, backing kind, cache key, or mutable shared-memory handle.

For the paper prototype, a fixed offset/capacity arena, a research-only derived-image composition, or another bounded Linux-only technique is acceptable if it proves the mechanism. Do not build a generic page-composed allocator unless the fixed spike cannot answer the research question and one bounded design review shows the extra seam is smaller than the alternatives.

## KISS and verification budget

### Keep

These checks defend actual trust boundaries and must remain independent:

- sealed Host declaration and exact capability-plan identity;
- source occurrence, canonical arguments, final source and freshness/root joins;
- profile/artifact/import/loader/codec identity;
- pre-decode size/header/body bounds and integer-overflow rejection;
- descriptor/body/hash/padding verification before publication;
- exclusive writer → sealed backing → private consumer typestate;
- one-shot claim/lease and complete terminal disposition;
- no partial state on rejection;
- cancellation, ambiguity, no replay/fallback after physical effects;
- body-safe evidence and consumer mutation isolation.

### Remove or avoid

- repeated digest-format regexes when one package already owns canonical digest validation;
- duplicated flattened binding fields when an existing opaque identity can be reused without weakening independent recomputation;
- multiple evidence documents/checkers for one prototype phase;
- full-suite/race/cross-platform execution after every micro-edit;
- independent review after every phase;
- broad negative combinatorics that repeat the same owner/invariant;
- pass-through wrappers, generic registries, plugin systems, generalized caches, public APIs, and speculative future fields;
- rerunning frozen predecessor campaigns or CI.

### Gate cadence

- Per RED/GREEN slice: focused package tests plus `git diff --check`.
- At an authority, shard, memory-mapping, or evidence boundary: focused race/adversarial tests.
- At coherent integration checkpoints: relevant package suites and one real Guest smoke.
- At final behavior target: full Go tests, vet, race, relevant Guest Python tests, exact artifact checks, macOS mechanism smoke, Linux private-COW campaign, and one independent fixed-target review.

Do not rerun a gate merely because a commit occurred when no code in its scope changed.

## Parallel development policy

Use at most two concurrent workers and only after Phase 0 freezes contracts. Every writer must use an isolated worktree/branch; never permit concurrent writers in the main worktree.

Preferred parallel lanes:

1. **Authority/peephole lane:** Host declaration, source binding, lifecycle and adversarial unit tests.
2. **Runtime/data lane:** package-imported shard probe, fixed extent/COW spike, Linux mechanism evidence.
3. **Harness lane:** source-stream fixture, matched treatments, recorder and report checker.

Run lanes 1+2 or 1+3 in parallel when file ownership is disjoint. The controller integrates one lane at a time, reads every diff, reruns focused gates, and owns contract changes. Workers may not alter frozen evidence, broaden authority, commit generated large bodies, push the main branch, or declare mechanism/paper success.

If a seam spans Host authority, Guest ABI and memory lifecycle together, the controller implements it serially rather than splitting ownership.

---

## Phase 0 — Freeze question, seams and prototype contract

**Promise:** Prevent benchmark-driven contract drift while avoiding a heavyweight production specification.

Tasks:

- [x] Re-read live Git, this file, the closed predecessor, `docs/research/effect-aware-contract-v0.md`, `docs/research/semantic-speculation-roadmap-v0.md`, `runtime/capability`, `runtime/semantic`, `runtime/resultblob`, `runtime/numpycodec`, `runtime/numpyproducer`, `runtime/engine/wazero`, and package-profile placement.
- [x] Map the current exact source-stream → verified overlay → Host pre-dispatch → staged observation → logical claim path.
- [x] Map current `numpy-core` artifact selection and prove whether NumPy is imported before the reusable COW baseline snapshot.
- [x] Freeze `PreparedDataContract v1`, occurrence/input/root/freshness/profile/codec bindings, lifecycle statuses and body limits.
- [x] Freeze one deterministic `.npy` fixture family and expected result/error/logical/physical oracles.
- [x] Freeze matched treatments, metrics, trial counts and promotion thresholds before formal timings.
- [x] Record exact parallel worktree/file ownership before dispatching workers.

**Gate P0:** Contract permits exactly one explicit Host-declared immutable `.npy` preparation lane; source syntax alone remains powerless; the experiment can falsify authority, overlap, memory and economics claims. Commit and push, then continue.

## Phase 1 — Real package-imported NumPy COW shard

**Promise:** Reuse initialized package state rather than paying or pretending to pay request-time NumPy import.

Tasks:

- [x] RED-test the current prepared-baseline state for exact `numpy-core` artifact/profile/import identity and imported module readiness.
- [x] If the baseline lacks imported NumPy state, add one minimal profile-owned or research-only trusted preparation step before COW image seal.
- [x] Prove two fresh clones start NumPy-ready without a second cold import.
- [x] Mutate supported module/array state in clone A and prove baseline and clone B are unchanged.
- [x] Prove `base` remains default and contains no NumPy package state.
- [x] Record initialization/import/image-seal costs outside per-Run critical time and clone costs inside the correct treatment interval.

**Gate P1:** Exact Linux evidence proves a sealed NumPy-ready baseline, fresh private clones, no state leakage, and no request-time full NumPy import. If this requires a broad CPython import-state fork, keep existing artifact initialization and record the measured limitation rather than broadening the prototype.

## Phase 2 — Explicit Host prepared-data declaration and peephole

**Promise:** Admit early loading only through sealed Host authority.

Tasks:

- [x] RED-test a `PreparedDataContract v1` embedded in or cryptographically joined to the sealed capability plan.
- [x] Bind exact projected function/call occurrence, argument/resource selector, immutable source policy, freshness, unclaimed disposition, loader kind/options, artifact/profile/import closure, body budget and privacy partition.
- [x] Let target-Guest analysis emit only authority-free syntax/occurrence facts for the narrow `np.load(..., allow_pickle=False)` form.
- [x] Join overlay facts with the Host contract and reject absent contract, alias ambiguity, dynamic path/options, wrong profile, mutable source, unknown fields and any identity drift.
- [x] Prove Python/tool schema/Guest metadata cannot add the contract.
- [x] Preserve ordinary execution when no exact prepared decision exists before final execution selection.

**Gate P2:** Positive exact declaration prepares one candidate; identical source with no Host declaration starts zero physical work; every one-field authority/source/profile/freshness mutation fails before physical start.

## Phase 3 — Early read, bounded decode and exact logical claim

**Promise:** Move useful work before final source completion without changing logical Python/effect semantics.

Tasks:

- [x] Reuse the existing read pre-dispatch/Broker path for the exact immutable source rather than giving the loader arbitrary filesystem authority.
- [x] Implement one authority-free loader Guest or narrow trusted `.npy` codec; choose the smaller path after a bounded spike and document the semantic claim.
- [x] Parse and validate `.npy` header/body under fixed dtype/shape/size/endianness/order rules.
- [x] Publish only an exact Run-private typed staging identity after successful read/decode/verification; no durable cache.
- [x] Make unchanged final Python claim only at the original dynamic occurrence through an exact source/argument/freshness/object join.
- [x] Record read/decode overlap against source-generation release times.
- [x] RED-test later syntax error, earlier exception, branch not taken, cancellation before/after physical start, late completion, source replacement, body corruption, claim replay and unconsumed orphan cleanup.
- [x] Prove every physical attempt and logical call has a typed terminal disposition and no partial publication.

**Gate P3:** A real Guest run starts authorized read/decode before finalization, returns baseline-equivalent result at exact claim, and all unreached/invalid/cancelled controls preserve logical-call and authority semantics.

## Phase 4 — Fixed sealed dynamic extent/private-COW spike

**Promise:** Determine whether dynamic dataset pages can avoid the full producer/consumer copy path without building a general allocator.

Tasks:

- [x] Decompose current bytes copied, encoded, mapped and materialized for the prepared-data path; retain RSS/PSS, allocation and hashing as formal Phase 6 metrics.
- [x] Resolve the fixed-extent question through the allowed bounded full-derived-image path; do not add a generic or dedicated object allocator when whole-image private COW already answers the experiment.
- [x] Ensure Host owns backing; producer/loader receives only temporary exclusive write authority and no backing handle.
- [x] Seal only after terminal writer state and complete Host validation.
- [x] Map the same sealed body privately into 1/2/4 fresh Linux consumer Guests.
- [x] Construct a supported ndarray view/copy surface without exposing Host pointer/FD or permitting shared mutation.
- [x] Mutate pages in consumer A and prove Host canonical body and consumer B remain unchanged; record private page faults/COW evidence.
- [x] Compare against existing private-copy materialization and recompute.
- [x] Keep a full-derived-image or fixed-arena implementation research-only; do not generalize unless required to answer the experiment.

**Gate P4:** Either one dynamic body is physically shared read-only and privately COWed on writes across fresh Guests with complete cleanup, or the spike produces a precise engine/ABI blocker and measured copy baseline. A generic allocator is not required for goal completion.

## Phase 5 — Fixed fanout and data-local compute controls

**Promise:** Compare moving data, sharing pages, recomputing, and moving computation without committing to a public fanout language.

Tasks:

- [x] Build a research harness with fixed 1/2/4 consumer schedules over one exact prepared object.
- [x] Keep every consumer a fresh Guest with independent authority and terminal lease.
- [x] Add one narrow data-local operation such as exact numeric reduction that returns a small typed scalar while the large object stays in one authority-free Guest.
- [x] Compare `recompute`, `private_copy`, `private_cow_pages`, and `data_local_compute` under identical artifact/profile/source/input/result oracles.
- [x] Record N=0/orphan, N=1 and N>1 lifecycle without adding a public `pysolate.fanout` API.
- [x] Measure physical producer count separately from logical consumers; do not add single-flight coalescing.

**Gate P5:** All controls have result/error/logical-call/authority parity and complete cleanup. The harness can reveal when moving compute is cheaper than moving or mapping data.

## Phase 6 — Preregistered matched prototype campaign

**Promise:** Produce paper-usable mechanism and economics evidence without universal-speedup claims.

Required treatments:

```text
serial_whole_source
EAGER_style_persistent_interpreter
raw_read_only_pre_dispatch
prepared_data_private_copy
prepared_data_private_cow_pages   # Linux where implemented
prepared_data_data_local_compute
```

Required dimensions, kept sparse:

```text
payload       8 MiB core; 64/256 MiB extension not entered
lead gap      0 | 250 ms | 1000 ms; 1000 ms remains partial for COW derive
consumers     1 | 2 | 4 where applicable
platform      macOS mechanism/control | Linux private-COW
```

Tasks:

- [x] Freeze exact artifact, harness, fixture bodies/digests, schedule, trial count, platform and environment contract before formal observation.
- [x] Record the joined lifecycle: P2/P3 release/read/decode/seal/exact claim/teardown evidence plus P4/P5/P6 mapping, copy, compute and same-trial critical-path interval unions.
- [x] Record process max RSS, copy/encoded/mapped bytes, page faults/COW signals, N=0 orphan bytes/work and physical/logical counts; explicitly record that short-process PSS was not sampled.
- [x] Preserve raw records privately and commit canonical identities plus body-safe aggregates only.
- [x] Regenerate 162 records / 54 aggregates from raw reports with strict JSON decoding and fail-closed coordinate, identity, parity and economics checks.
- [x] Compare mechanism correctness separately from economics and mark all 27 EAGER records as weaker-authority controls rather than fresh authority.

**Gate P6:** Every retained treatment has exact parity and complete lifecycle evidence. Report observed positive, mixed or negative cells without interpolation. If no prepared-data treatment wins, close the prototype with the measured cause rather than adding cache/optimizer machinery.

## Phase 7 — Independent review, simplification and closeout

**Promise:** Keep only the mechanism and claims that survived the prototype.

Tasks:

- [ ] Run one independent fixed-target review covering Host declaration authority, physical/logical side effects, source/freshness/profile joins, loader isolation, sealed extent ownership, private mutation, replay, cleanup, timing and evidence privacy.
- [ ] Reproduce valid findings in the controller, add minimal regressions and fix blockers.
- [ ] Remove prototype scaffolding, duplicate validators, pass-through wrappers and unsupported branches that do not contribute to mechanism/evidence.
- [ ] Run final full Go/vet/race, relevant Guest Python/profile tests, exact artifact verification, macOS controls, Linux private-COW campaign/checker and `git diff --check`.
- [ ] Update `docs/research/semantic-speculation-roadmap-v0.md` with Current / Measured / Rejected / Deferred conclusions.
- [ ] Preserve the predecessor P5/P6/P7 files and canonical local evidence unchanged.
- [ ] Sign and push final commits; verify signature, branch/upstream alignment, clean worktree, no runner processes and no stale remote stage.

Final report must distinguish:

```text
explicit Host authority established
physical work allowed and observed
logical semantics preserved
package shard state actually proven
sealed dynamic-page mechanism actually proven
fresh-consumer/data-local controls
observed economics
prototype-only limitations
deferred production/generalization claims
```

---

## Explicit non-goals

Do not implement or claim without new owner approval:

- production-ready API, default optimizer or automatic cost model;
- generic `load_dataset`, pandas, Arrow, Parquet, CSV, archive or remote-Hub support;
- arbitrary Python `build_dataset()` pre-execution or purity inference;
- generic typed object store, cache, serializer, plugin/codec registry or distributed object system;
- durable cross-Run cache, process-reopen retention or cross-project sharing;
- single-flight/coalescing merely because consumer count is greater than one;
- public `pysolate.fanout`, continuation transform, nested fanout or arbitrary closure capture;
- complete page-composed linear-memory allocator unless the fixed spike cannot answer the question and the user explicitly reopens it;
- Python heap/frame/pointer/FD transfer, POSIX fork, snapshot/restore or shared mutable memory;
- workspace-output publication, external writes, compensation or commit protocols;
- paper/thesis/slides editing, production deployment, package release or manual CI.

## Deferred goals and their value

- **Public structured fanout and typed capture:** potentially high long-term value, but high compiler/runtime complexity and unnecessary for this paper mechanism. Use a fixed harness now; reconsider after positive prepared-data evidence.
- **Generic page-composed allocator:** potentially high performance value, but substantial wazero/ABI/lifecycle work. A fixed extent is sufficient for the prototype.
- **Single-flight:** medium value only if repeated exact concurrent demand is observed. The closed predecessor correctly did not enter it.
- **Durable dataset cache:** potentially useful product feature, but requires invalidation, process reopen, quota, privacy and source-version policy. No current evidence justifies it.
- **Generic pure-compute pre-dispatch:** research-interesting but risks becoming an unsound second Python runtime. Keep ordinary `build_dataset()` runtime-only.
- **Isolated workspace preparation:** independently valuable for file-producing tasks but orthogonal to prepared dataset loading; keep as a separate future goal.
- **External write commit protocols:** important but separate authority/ambiguity/reconciliation research; never infer them from read preparation.

## Per-slice protocol

For each executable slice:

1. inspect live Git/code and the current execution pointer;
2. write one meaningful RED test or record why the slice is measurement-only;
3. implement the smallest behavior;
4. run focused gates only;
5. simplify duplicate same-owner checks without weakening trust-boundary recomputation;
6. update checkboxes and completion log;
7. integrate worker branches serially and inspect every diff;
8. run proportional integration gate;
9. signed commit and push;
10. verify signature/status and continue immediately.

## Global final gates

```bash
cd /Users/yuzhe/projects/agent-python-runtime

git diff --check
go test ./... -count=1
go vet ./...
env -u AGENT_RUNTIME_GUEST go test -race ./... -count=1
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s scripts/tests -p 'test_*.py'
```

Also run the exact new evidence checker, real `numpy-core` artifact verification, and named macOS/Linux mechanism commands created by this goal. Do not manually trigger GitHub Actions.

## Hard stop conditions

Stop only when one is proved and recorded with live Git state and evidence:

1. all executable phases are complete and final gates/review pass;
2. explicit Host declaration cannot be joined to the original dynamic call without weakening logical/effect semantics after one narrow explicit-helper or derived-patch alternative;
3. package-imported NumPy baseline requires a broad/unmaintainable CPython or wazero fork;
4. safe dynamic sealed pages require exposing Host pointers/FDs, shared mutable memory, Python heap transfer or a generic allocator outside prototype scope;
5. exact source/freshness/profile/body/claim identity cannot prevent stale or cross-authority substitution;
6. required Linux private-COW hardware/toolchain is unavailable and no bounded alternative can answer the mechanism question;
7. a product/architecture decision is required between incompatible authority contracts;
8. work would require Docker, paid cloud, production access, publication or manual CI.

Do not stop because a treatment is slower, a worker fails, a focused test exposes a bug, a coherent commit is complete, or a platform-specific path becomes an honest negative result.

If blocked, report the exact blocker, modified files, tests run, Git status and safest narrow alternative.

## Completion log

- 2026-08-21: Goal prepared from clean `main` at `eeacf5bf4bccb1e62131db84eb741925da6808cf`. Predecessor NumPy result-reuse goal remains complete; P8 remains `not entered`. No implementation or formal observation has started.
- 2026-08-21: Phase 0 froze `docs/research/authority-preserving-prepared-data-contract-v1.md` and canonical preregistration `sha256:9f7baa064eff8e19c93651b41decf4f855673fcc5ae767716f023d3de4702bd6`. The exact current lifecycle is verified-prefix analysis → sealed Plan join → one-shot physical read → staged observation → unchanged Broker claim. Static source archaeology also proved the generic Wazero COW canonical image calls `_initialize` and `runtime_init({})` but does not explicitly import NumPy; P7 warm-engine capacity is not evidence of a package-imported baseline. The matched core is one deterministic 8 MiB `<i8` `.npy`; larger payloads require a pre-observation extension. P1 owns `runtime/engine/wazero` package-ready preparation, P2 owns the authority/peephole contract, and the later harness owns only `research/prepareddataset` plus `cmd/prepared-data-*`.
- 2026-08-21: Before implementation or observation, P2 seam review corrected the streaming identity split: speculative preparation binds stream epoch, admitted-prefix digest, exact span, canonical arguments and Host contract; only the later claim adds the sealed final-source digest and proves the occurrence remained unchanged. The earlier draft incorrectly placed a not-yet-known final digest in speculative-start authority; no code or data depended on it.
- 2026-08-21: Phase 1 is complete at mechanism target `d99c182ac37825b8ae95d99960e1b7401cc9c8d7` (`tree 71d87ea7090d4efe6fbc742efcb4e186d3387963`). A bounded Host-trusted fragment is applied before the existing Linux COW image seal; default preparation is unchanged, authority-bearing engines are rejected, and an already-bound engine rejects different non-empty source identity. Focused unit/race/vet, Linux cross-compilation and base/default shard tests pass. The clean `vcs.modified=false` probe `sha256:d02f2ddc1fb166fe99c1163c8a63e31db2e4584935351abd5b82695b42b65161` ran on gpu31 against artifact `sha256:2753cde560f3961a483df53aec334c8fdbb084934e5a62a56d436aea1ae557ad`: both fresh Guests used baseline alias `np` and NumPy `1.26.0b1` without source import, A's module mutation was absent from B, private COW was selected with zero fallback, and prepare/clone times are recorded separately. Body-free evidence identity is `sha256:e17dbfaa68004a60bb68430da84fecf9029b0337d4aec83edda56aec9e44981a`.
- 2026-08-21: A bounded P3 control-plane pilot reused the existing Host `materialize_value` token and per-Run table while a private trusted prepare monkeypatched only the admitted fresh Guest's `np.load`. The final Agent source remained unchanged. On gpu31 the reached case returned shape `[2,3]` and sum `15` with one claim/consumption; branch-not-taken and earlier-exception made zero claims and each discarded the unconsumed token at teardown. This validates dynamic claim timing only; it is not 8 MiB decode, authority or economics evidence.
- 2026-08-21: The P4 full-derived-image correctness mechanism first passed at `4368d80fb541b011fd8c15982744871a5e94aa10`; its v1 9.66 s observation combined package and dataset preparation and is retained only as a corrected pilot. The two-stage mechanism at `c4820d15b14c1608cbb006f710aeb4eb1a8b1177` retains one immutable package parent and derives each dataset image from a fresh parent clone, never from a prior dataset image. Clean gpu31 v2 evidence separates one-time package-shard prepare (**7.286 s**, excluded from per-load critical path) from 8 MiB dataset derivation (**2.233 s**), then records fresh consumers at **64.7 ms / 51.0 ms**, zero body transfer per consumer, private-COW page faults, independent parent/dataset identities and A→B mutation isolation. Body-safe v2 evidence identity is `sha256:f79ec931ef87f6945817c1be3c1ba8e1a09e11866c71d5a7e548c05416f560b7`. P4 remains open until the 1/2/4 harness and private-copy/recompute controls join in P5; no generic allocator or page-composed path was added.

- 2026-08-21: Phase 2 is complete at `a960a5cc27537d072d86a7f5338a307ab0bfb8c8`. The target-Guest analyzer consumes a deterministic import-line-neutral analysis overlay while the original prefix/final source digests remain Host-bound; candidate `numpy.load` and physical `sources.read` are distinct identities. The clean macOS probe records zero starts without a Host contract, one start after exact sealed join, and a final-source claim identity. Every authority/source/profile/freshness/result-budget mutation fails before physical start in focused regressions. Body-safe evidence identity is `sha256:ed350dc700b58d821c48344ebdd02e1791bf995ed38aa361e336a6b7be52489d`.

- 2026-08-21: Phase 3 is complete at `b05a94d58937fda5c39126cd60a34471da3ac414`. A real macOS Guest run begins the exact Host-authorized immutable file read and strict 8 MiB decode before final-source release, keeps the body out of the Broker response, seals Run-private typed staging, and dynamically claims once at unchanged `np.load`; shape `[1024,1024]` and sum `549755289600` match the serial oracle. Clean gpu31 controls prove reached `1/1/0`, branch-not-taken `0/0/1`, and earlier-exception `0/0/1` claim/consume/discard dispositions. Focused tests cover later syntax error, cancellation before/after read issue, late completion, source replacement, corruption, replay and orphan cleanup. Evidence identity is `sha256:172317c7bbfcf51f7a90087718f7a4c5c54bfec3fb4145084f4be2399a12e543`.

- 2026-08-21: Phases 4–5 are complete as mechanism slices at runner `a36398b4a945371a414e35ccda6e2278bf93074c` and checker `09a0bb12e5d0ffc160dd526b737c378db17bb77e`. One immutable NumPy package shard and one dataset image serve fixed N=0/1/2/4 schedules. The 16-coordinate Linux report proves fresh Guests, exact sum parity, COW mutation isolation, zero per-consumer body copy for COW, 8/16/32 MiB private-copy growth, mapped-byte accounting, N=0 orphan bytes, and a data-local reduction followed by fresh scalar consumers. The dedicated object-extent allocator was not built: the allowed bounded full-derived-image path answered the question. Evidence identity is `sha256:d403fa73f54997eb051878657a18659cbe011091910a746697920f4ae6f982e3`. Economics remain explicitly unentered because this single ordered run contains first-observation warmup confounding.

- 2026-08-21: Phase 6 formal manifest was frozen before retained observation at `docs/evidence/prepared-data-phase6-manifest-v1.json` (`sha256:7e6644b73e569ebcd503259dd8356e1e5cee133cfc9eb25602011ebe9e1065f0`). It fixes harness `ccef2d9875ab2f289434012bbdfb4015b99db6b1`, the canonical artifact/fixture, Linux gpu31 and Darwin mechanism environment contracts, 3 trials, N=1/2/4, gaps 0/250/1000 ms, warmed component records, and no 64/256 MiB extension.

- 2026-08-21: Phase 6 closed with body-safe evidence `docs/evidence/prepared-data-phase6-linux-v1.json` (`sha256:673ee697c50892bc3e07951377ed46e42e50f9e72bc93e68cadb8a8945794fd7`). The pinned builder regenerated 162/162 parity-and-cleanup records and 54 medians from six private raw files. Private COW and data-local beat serial at N=2/4 for every gap; at N=1 they require the 1000 ms coordinate. EAGER is fast at fanout but explicitly weaker-authority. Cheap canonical recompute remains 61.8/123.6/245.8 ms for N=1/2/4, so no production optimizer or 64/256 MiB extension is justified.

## Short prompt to start this mega-goal

```text
Read docs/plans/2026-08-21-authority-preserving-prepared-data-autonomous-megagoal.md fully and execute it in /Users/yuzhe/projects/agent-python-runtime. Build the KISS research prototype through multiple verified slices; use focused gates and file-disjoint parallel work, keep explicit Host authority and physical/logical side-effect evidence, update the roadmap, signed commit and push, then continue until complete or genuinely blocked. Do not productionize, rerun frozen predecessor evidence, or trigger CI.
```
