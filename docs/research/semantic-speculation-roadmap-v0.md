# Semantic speculation roadmap v0

Status: design rationale; execution is governed by `docs/plans/2026-08-20-semantic-speculation-optimizer-autonomous-megagoal.md`. No executable region consumer is enabled by this document.

Runtime baseline: `dd40411edef47cc47b7361defe88f630a3b6385b`.

## Research question

How much generation-time overlap is lost by syntax-level conservative gating, and how much can be recovered safely by combining target-Python semantics, Host capability contracts, and explicit isolated state?

The comparison is not "Pysolate also executes chunks eagerly." EAGER executes accepted Python chunks in a persistent interpreter and serializes operations such as network communication. Pysolate's current pass does not execute the source prefix. It uses source facts to prepare one qualified Host read and lets unchanged final Python claim the result at its original dynamic boundary.

The next research phase should test whether the same source-bound semantic layer can support more consumers without silently becoming an unsound second Python runtime.

## Motivating boundary

```python
a = 111
b = tools.call(a)
c = blOw ThIs Up lol
```

Whole-file CPython parsing fails, so serial file execution performs neither assignment nor call.

An aggressive prefix executor may already have run `a` and `b`. A conservative syntax gate may run `a` but defer `b`, losing the expensive overlap. Pysolate may prepare the physical work behind `b` only when the Host has granted explicit speculative authority for that capability. If final source is invalid, Python never runs and the prepared observation is orphaned rather than reported as a logical call.

This does not make an unused physical request unobservable. Provider logs, billing, quota, timing and cache effects remain real. The correctness contract must therefore distinguish:

- **language semantics:** final Python result, exception and reached logical calls;
- **authority semantics:** no unauthorized publication or external mutation;
- **physical work:** started, consumed, cancelled, late or orphaned requests;
- **Host speculative authority:** whether an unconsumed physical attempt is permitted;
- **freshness semantics:** which observation interval the logical read accepts.

AST analysis can prove source-local predicates. It cannot prove that a Host handler is read-only, safe when unconsumed or correctly billed. Those remain reviewed, versioned Host contracts.

## Correcting the proposed effect lattice

A single total order such as `PURE < READ < REVERSIBLE WRITE < IRREVERSIBLE` is too coarse. The scheduler needs independent properties because safety is not monotonic along one axis:

```text
language effect       pure | heap-local | workspace | Host capability | unknown
publication           none | private | externally visible
speculative authority forbidden | prepare-only | execute-private | commit-after-accept
replay                 never | idempotent | deduplicated | compensatable
observation            immutable | snapshot-bound | freshness-bounded | live
result transport       canonical scalar | typed artifact | opaque heap object
failure                total | may-raise | ambiguous external outcome
```

Near-term work should add only the properties required by a measured pass. It should not promise general rollback, speculative HTTP writes or compensation.

## Architecture decision: retain the sidecar, allow a separate execution patch only after a gate

"Sidecar" describes where semantic analysis lives. It does not logically forbid a later compiler consumer from emitting a transformed execution artifact. However, source rewriting would retire one of the current system's strongest properties: unchanged final Python remains the executable program.

Use two explicit classes:

1. **Overlay-only consumers.** Analysis remains a side table and optimisation uses an existing dynamic boundary. Current Host-call pre-dispatch belongs here. Prefer this class whenever possible.
2. **Execution-patch consumers.** The sidecar emits a source-bound patch after full-source validation. The original source remains authoritative, but the native runtime executes a verified derived AST. This is a new trust and equivalence boundary and must be labelled as such.

Do not describe an execution patch as unchanged-source execution.

### Why dynamic injection is not the simple generic answer

At a Host capability call, Pysolate already owns a dynamic boundary and can claim a prepared observation without rewriting source. An arbitrary Python call or expression has no equivalent boundary. Intercepting it requires wrappers, tracing, bytecode hooks, interpreter modification or an AST-inserted helper. These are more implicit than an explicit bounded AST patch and often alter call identity, locals, traceback or performance.

For a pure-region spike, the clearest route is therefore a narrowly verified AST patch, not a generic dynamic interceptor:

```text
selected complete prefix
  -> execute one qualified pure region in a scratch Guest
  -> capture a typed result capsule
  -> receive and parse the complete final source
  -> require exact source/AST/region/input/environment identity
  -> pin the ready capsule
  -> replace only the admitted expression/assignment in a derived AST
  -> compile and execute that AST with native CPython
```

If the capsule is not ready and pinned before compilation, execute the original source. Do not inject a racy runtime fallback.

The derived AST should call one narrow trusted helper, conceptually:

```python
__pysolate_materialize_value__(opaque_decision)
```

The embedded value is an opaque per-Run decision identity, not a blob handle, cache key, path or credential. The Host materialisation table must already hold a pinned exact typed result before derived compilation is selected. Missing, stale, consumed, mismatched or unready decisions fail closed and never trigger recomputation. V1 should replace only one exact RHS/single assignment, preserve source locations, reserve the helper binding and reject source capable of shadowing or dynamically mutating it.

This remains a semantic sidecar architecture plus one explicit compiler consumer. It is not overlay-only execution.

## Why this is materialisation, not ordinary constant folding

Classical constant folding evaluates small literal expressions during compilation. The proposed mechanism executes a source region early, captures its live-outs and substitutes a result in final execution. Call it **source-bound region materialisation** or **prepared pure-region result**, not merely AST cache.

Keep three identities separate:

1. **Run-scoped prepared result:** one final source occurrence; latency overlap only.
2. **Single-flight:** explicitly permitted concurrent computations share one physical attempt.
3. **Cross-run completed-result cache:** durable reuse requiring a complete computation identity and invalidation contract.

The first executable spike should implement only (1). Current corpus evidence found no exact materialisable cross-program repeats, so a durable region cache is not yet justified.

## Large result transport and Host ownership

A large prepared result does not imply that arbitrary Python heap pages should move between Guests. Ordinary CPython allocations mix object headers, allocator metadata, unrelated objects and interpreter-local pointers on the same pages. A read-only `PyObject*` is still interpreter-owned and is not a transferable value.

The bounded design keeps one canonical typed body outside every Guest:

```text
producer Guest payload
  -> one bounded copy into Host-owned immutable blob storage
  -> one canonical Host body and typed descriptor
  -> one bounded copy into each consuming Guest
  -> local bytes/NumPy/DataFrame wrappers
```

The Host owns the blob handle, generation, body, computation/content identity, privacy partition, quota, expiry and teardown. Each Guest owns its reconstructed Python wrapper and private local bytes. A dedicated bounded binary Host/Guest copy ABI may avoid JSON/base64 and redundant serialisation, but it does not expose Host pointers or FDs.

This intentionally trades zero-copy fan-out for a smaller and clearer authority boundary. It needs no transferable linear-memory arena, subrange remapping, general CPython allocator change, shared mutable memory or engine fork. NumPy and DataFrame support remains typed: pointer-bearing `dtype=object`, arbitrary extension arrays, pickle and generic Python object graphs are rejected. The implementation must measure producer-to-Host copy, Host-to-Guest copy per consumer, reconstruction, peak memory and recomputation before retaining a large type.

## Transparent multi-agent and cross-session reuse

Generated producer and consumer programs need no blob-handle or sharing protocol. AST facts may qualify an exact pure producer/consumer region; Host orchestration supplies reuse authority. The current subagent descriptor already binds parent lineage, source, inputs, artifact, execution profile, child plan and privacy partition, which is the correct place to authorize a typed `ValueRef`.

Two cases remain distinct:

1. The same exact computation appears in parent/children or sibling children. A complete computation identity may select one Host blob automatically.
2. A parent-produced value is consumed by different child source. The Host binds a typed `ValueRef` to a declared child input and privately materialises it before child execution; the model never sees the storage handle.

Multi-agent status alone never enables reuse. Cross-session retention additionally needs observed exact repeats, durable blob storage, expiry/invalidation/quota, policy and privacy identity, and process-reopen verification. It preserves a value, not an interpreter: every Run constructs a fresh local Python wrapper and does not retain `id()`, aliases, weakrefs, module globals, heap, descriptors or in-place mutation. A modified value can persist only by explicit publication as a new immutable blob generation.

## Computation identity for a prepared region

An overlay or AST digest alone is not a cache key. At minimum bind:

- exact final source and selected source slice;
- target-Python AST and source span;
- canonical live-in values and immutable dependency identities;
- import closure, Guest artifact, execution profile and native package versions;
- analyser, pass and patch-emitter versions;
- exact executable region;
- result schema and codec;
- privacy/project partition and policy epoch;
- exception and cancellation contract.

Conservative misses after any identity change are acceptable.

## Roadmap

The tranches are sequential decision gates. A failed opportunity or cost gate stops dependent implementation unless the negative result is sealed and the owner explicitly approves a versioned remediation hypothesis. Remediation never changes the frozen input or relabels the failed gate; it must preregister a new gate before collecting successor measurements. The active execution details and current sequencing live in `docs/plans/2026-08-20-semantic-speculation-optimizer-autonomous-megagoal.md`.

**2026-08-20 decision:** R2 semantic/evidence equivalence passed, while its cold-analyzer economics gate failed with 0/35 positive achieved coordinates and 0/35 results ready before finalization. The implementation analyzed every cumulative prefix in a fresh CPython/WASI module and did not use `PreparedRuntime` or Linux COW. The owner approved R3 as a bounded remediation: reduce exact analyzer invocations, use one bounded REPL-like private analyzer session per source-generation Run, add single-use prepared/private-COW lifecycle across Runs and region-local precision, then evaluate a separately preregistered economics gate. The original R2 campaign remains immutable negative evidence. Before remediation measurements, the independent 12-coordinate extension matrix was frozen as `sha256:4cec92655c0f73578f96dc352be13e17aff3376645830ff89f0292e01d15af39` and its profile, metric, mechanism and economics contract as `sha256:d17a78fa49fd8699f2d7ae3ec4f183e6e05e50a18d868f8fe54b26b87899676e`; neither identity may be changed in response to successor results. The first preregistered remediation slice uses a skip-only Host readiness filter: the three-prefix external-read control invokes one exact analyzer and the two-prefix pure-local control invokes none, while formal execution remains fresh and unchanged. The second slice routes all admitted transitions in one source-generation Run through one bounded private analyzer session; the three-prefix unknown-wrapper control now completes two exact requests with one module initialization, while the closed session is discarded before fresh formal execution. The third slice consumes the portable single-use `PreparedRuntime` slot inside that authority-free session, exposes equivalent-capacity preprovisioning, falls back to one fresh private session after consumption, and records provisioning/hit/fallback timing separately from the still-fresh formal execution. The fourth slice attaches that session to the Linux sealed-memory baseline: `gpu31` Exact Guest evidence records a 28.4 ms private clone and 69.9 ms target analysis from equivalently preprovisioned capacity, 324.2 ms source generation for the three-prefix external-read treatment, one COW hit, zero analyzer `runtime_init`, one separately fresh formal Guest, bounded body-free image accounting, and close/discard validation; cold baseline provisioning remains separately visible at about 3.41 s.

### R0: freeze semantics and an adversarial comparison corpus

**Goal:** Make "safe" and "equivalent" executable claims rather than prose.

Add a small fixed corpus covering:

- valid straight-line pure prefixes;
- a later syntax error;
- a later runtime error;
- an early exception before the candidate;
- an unreachable branch;
- custom wrappers around a Host capability;
- immutable/freshness-bounded reads;
- denied writes and unknown effects;
- cancellation before logical claim;
- prepared work that completes late or is never consumed.

For every program record baseline full-file CPython outcome, logical Host calls, terminal workspace state and physical operations separately.

**Gate R0:** Every case has a stable oracle and distinguishes language, authority and physical-work observations.

### R1: strengthen the current Host preparation contract

**Goal:** Defend the current EAGER distinction before adding local execution.

Make explicit, if not already represented by frozen Host identities:

- `speculative_safe_when_unconsumed`;
- freshness/snapshot policy;
- physical-attempt budget and body-size limit;
- billing/privacy partition;
- cancellation, late and orphaned disposition;
- coalescing permission separately from read-only/idempotent;
- prohibition on authority-bearing writes.

Keep the current positive admission model. A capability absent from the sealed plan remains ineligible even when its Python name looks harmless.

**Gate R1:** The motivating syntax-error program may start an authorised physical read, records no logical call, publishes no workspace state and produces a typed orphaned/cancelled disposition.

### R2: build a matched EAGER comparison harness

**Goal:** Measure the actual boundary rather than compare headline speedups.

Use the same source streams and operation latencies for:

1. serial whole-file CPython;
2. EAGER-style complete-statement execution with syntax/name gating;
3. current Pysolate qualified Host pre-dispatch;
4. analysis-only perfect-effect oracle upper bound.

The oracle arm is not an executable system and must not be included as achieved speedup.

Report:

- safely overlapped critical-path time;
- admitted and rejected operation counts by reason;
- false-conservative time under the frozen Host oracle;
- logical versus physical operation counts;
- orphaned work, bytes, billing units and cancellation;
- final result/exception and authority-state parity.

**Gate R2:** Demonstrate a non-trivial workload slice where syntax-level gating serializes qualified external reads and Host semantics recover useful overlap after all analysis and Broker costs.

### R3: improve region precision before adding a consumer

**Goal:** Determine whether pure/local materialisation has enough opportunity.

The v0 census rejected all 69 candidate regions, partly because static materialisability requires an effect-free whole-module summary. Improve analysis only enough to obtain sound region-local top-level effect coverage. Preserve `may_raise`, heap mutation, unknown call and opaque-control barriers.

**2026-08-20 region-local slice:** The target-Guest analyzer now recognizes only exact `bool`/`int` constants and `+`/`-`/`*` chains over names previously proven by the same top-level scan. This lets a scalar assignment remain locally eligible even when a later independent statement performs a Host effect. Input-derived unknown types, division, calls, heap mutation and opaque control remain rejected. Exact source spans, canonical live-ins/live-outs, producer dependencies, effects, barriers and analyzer/source/AST bindings remain explicit; local eligibility carries no Host execution or materialisation authority. Exact Guest `sha256:cdb440e794b5865878e602eeebf4fe8198a20b33a140f7d4e87a679b1fa89191` passed the positive and adjacent negative controls. The independent 12-case region matrix is now frozen as `sha256:fc3c3cdbf62eac9cde8c17625b6c60de1709d8a61b464872820021068813f6ee`, with preregistration `sha256:81a3110d66c8f84dc1be9bfea057049cbbe7af9214e52b6c4348ffabdedaf234`; the known two-operator pilot is excluded from the opportunity gate. Its first Exact Guest mechanism run exposed one alias/identity false positive; the minimal `identity_alias` remediation then passed all 12 unchanged focus classifications with artifact `sha256:8780338cf3b4330371b13f06a2846006077c3ff99ee89d7fb618ea19e252d242`. A subsequent gate review found the focus-only validator insufficient and the `mail.send` projection mismatched to the matrix's `sinks.demo_publish` symbol. The corrected v2 validator now proves every frozen control tag, effect position and rejection reason across all 12 cases; v1 remains retained as superseded evidence. The bounded target-Guest cost study then passed its narrower monotonic gate: 16/64/128 multiply-chain medians were 3,458 / 14,041 / 28,292 ns, while fresh whole constructed-program medians remained about 2.23 seconds. This establishes a measurable cost shape, not end-to-end economics or enough work for a materialisation consumer. The subsequent frozen 360-trial cold/preprovisioned Phase 4 campaign completed all 360 records and 120 matched cells. Independent validation passed mechanism and economics in both profiles: 3/5 eligible cold coordinates passed with median savings of 432.9--460.6 ms, and 3/5 equivalently pre-provisioned coordinates passed with 2,435.4--2,471.4 ms savings; every passing coordinate was ready before finalization in 5/5 trials. Two prior runs that used the network-mounted workspace are retained as failed `_initialize` timeout evidence and were not selectively merged into the complete host-local run. This supports the named synthetic regime and permits only the narrow run-scoped materialisation spike; it does not establish natural prevalence or production-general speedup. Canonical evidence is `docs/evidence/semantic-speculation-phase4-campaign-evidence-v1.json`.

Run a fixed natural-agent corpus census. Report:

- candidate and admitted regions;
- estimated compute cost and generation lead time;
- canonical live-in/live-out rate;
- result-shape distribution;
- repeated fingerprints separately from one-run opportunities;
- rejection reasons.

**Gate R3:** Proceed only if multiple naturally generated programs contain expensive, straight-line, effect-free regions with canonical inputs and transportable outputs. No-go is a valid result.

### R4: run-scoped scalar materialisation spike

**Goal:** Prove the smallest execution-patch path without a durable cache.

Initial legality envelope:

- one straight-line top-level assignment;
- exact complete source region;
- canonical literal or captured immutable live-ins;
- no capability occurrence;
- no heap mutation, opaque call, import, control flow or `may_raise`;
- one canonical JSON scalar/small structured live-out;
- no object-identity-sensitive result;
- exact final-source and environment match.

Execute in a scratch Guest, retain a bounded typed capsule, wait for successful full-source parse, pin the capsule, emit a derived AST replacing only the admitted RHS, preserve source locations, then execute with target CPython. A miss selects the original unmodified source before Guest execution.

Do not use `pickle` and do not add a generic Python interception hook.

**2026-08-20 bounded helper slice:** The run-scoped `prepared_region` contract now binds source/AST/region/live-in/environment/profile/import/plan/pass/codec identities before any consumer exists. A Host-owned single-use table and separate bounded WASM import feed only canonical JSON `bool`/`int64` to the reserved trusted helper; missing, unready, mismatched, stale and repeated claims fail closed with no Broker/workspace authority or recomputation. Exact Guest positive, missing and repeated-claim controls passed on macOS and Linux with artifact `sha256:bb3cd9464f54b242ec908e143ee3fbb359a05b7b9db6fe8f30053aed5dc0366c`. Region execution, final-source-bound AST patch emission and lane economics remain unimplemented, so R4 has not passed.

**2026-08-20 target-Guest patch emitter:** Analyzer v7 adds a whole-source `reserved_helper_binding` rejection for direct helper references/bindings and dynamic namespace access. In the same bounded private analyzer session, `runtime_emit_prepared_region_patch` validates the Go-sealed decision, exact final source/region bytes and one top-level single-name assignment, then emits only a canonical patch binding after replacing that RHS and preserving its source location. The Host strictly decodes and seals that binding; no AST, source body, capsule payload or authority credential crosses this response. macOS/Linux Exact Guest controls passed with artifact `sha256:154bdc3058000cfc5acee4d80dfc4a29547651a78c7eee0738cea6b878fe8dbe`. Scratch execution, typed capsule publication and final derived execution are still absent, so R4 remains open.

**2026-08-20 fresh scratch Guest:** Region execution occurs in a separate fresh one-shot Guest rather than the analyzer session. Canonical live-ins are sealed into the decision; the scratch evaluator accepts only bool/int64 constants and names with `Add`/`Sub`/`Mult`, and rejects as soon as any intermediate leaves int64. It returns typed `ready`/`rejected`/`failed`/Host-`cancelled` terminal state, and only `ready` may publish a bounded decision-bound capsule. Exact Guest execution/publication and live-in drift controls passed on macOS/Linux with artifact `sha256:aae56baf0ea4b977cdc764e8246e7f358aea1ce3af87105e0f56c2af95af9c73`; each attempt instantiated one fresh module with no Broker or workspace. Final program selection and matched economics remain absent, so R4 remains open.

**2026-08-20 pre-execution derived selection:** The Host seals an exact ready capsule, target-Guest patch, final source and derived AST into a single `pysolate.prepared-region-execution-selection.v1` decision before final execution. The final Guest validates the unchanged original Run source, reconstructs the one-RHS patch itself, and compiles the derived tree only after all identities match. The experimental Engine-only path rejects Broker/workspace authority, prevalidates the exact ready table entry without consumption, and never falls back or replays original execution after commitment. Artifact `sha256:621f5fcec3f4bc7fc3550aa8fd1a275e7a6c09017518f535395c5bae84a297cb` produced the same result `42` as baseline on macOS/Linux; invalid suffix drift caused zero claims and zero consumption. Remaining adversarial parity and matched economics keep R4 open.

**2026-08-20 adversarial parity:** Using the same immutable artifact, baseline and derived lanes returned identical exception type, message, logs and line-3 traceback for both pre-region `ValueError` and post-region `LookupError` cases on macOS/Linux. Pre-region failure and pre-cancellation made zero claims and discarded their capsules; post-region failure made exactly one claim and consumed exactly once. Together with suffix-drift rejection and the positive result-42 control, the authored authority-free semantic matrix now has result/failure/location/consumption parity with no Broker, workspace or execution replay. Matched cold/pre-provisioned economics remain absent, so R4 remains open.

**2026-08-20 frozen P5 campaign:** The timing campaign is now frozen before observation. Matrix `sha256:e4025295cc47cdc62925f4a4e0b0d3f072726de9aff983c75a0b9187fd355cee` contains one excluded pilot, four economics positives and six typed/adversarial controls; preregistration `sha256:9db34a4fa8091bd9875132457dfcf9515fbf78802a5f0453029a4f52e1f776c6` fixes cold and treatment-capacity-preprovisioned profiles, original/derived treatments, five trials, seeded order, stage-level clocks, memory/discard accounting, separate mechanism/economics gates and a fail-closed no-go action. The four positive coordinates yield 80 matched records across 0/250/1000/6000 ms finalization gaps. An independent strict reviewer validates canonical bytes, source/region identities, Python syntax/AST/operator counts, immutable P3/P4 lineage, profile/treatment schedule and zero observation fields; its freeze receipt reports zero timing samples. R4 remains open until the exact frozen campaign is implemented and run.

**2026-08-20 body-free trial contract:** Canonical `pysolate.semantic-speculation-phase5-trial.v1` records are now restricted to the 80 economics coordinates and bind all frozen/artifact/source/region identities plus an exact harness identity. They expose only outcome/log/traceback hashes, never bodies. Twelve stage intervals distinguish critical-path measurement, pre-clock capacity and inapplicable stages; an interval-union calculation handles intentional finalization/scratch overlap and requires an explicit unattributed remainder to reconcile exactly with elapsed critical-path time. Validators also enforce original-versus-derived identity separation, single capsule claim/consumption, the 256-byte capsule bound, exact analyzer/scratch/formal Guest and runtime-init counts, nonzero memory observation, zero calls/orphans/authority, unmounted workspace, and profile-specific provisioning disposition. No sample was created while defining or testing this contract. The campaign harness and its identity must be fixed before any Exact timing run.

**2026-08-20 monotonic recorder:** A run-private `Phase5StageRecorder` now owns the frozen stage lifecycle. It accepts an injectable monotonic clock, permits intentional overlap, separates pre-clock capacity from critical work, and is one-shot: duplicate/unknown stages, wrong-side-of-clock disposition, active finalization, stale tokens, reuse and time regression fail closed. It emits the exact ordered stage vector and reconciles interval-union coverage plus unattributed elapsed time into the body-free record. Deterministic fake-clock and race tests include direct trial-contract acceptance. Portable peak-RSS observation uses `getrusage` with correct Darwin/Linux units. This still observes no campaign timing; execution wiring and a sealed harness identity remain prerequisites.

**2026-08-20 pre-provisioned scratch:** The derived treatment now has an explicit scratch-capacity primitive rather than relying on request-time instantiation. One initialized, never-served, authority-free Guest is leased from a dedicated Engine and consumed on the first attempt; a second attempt rejects, while unserved `Close` records one discard. Missing prepared capacity fails instead of falling back. Execution-time evidence reports zero instantiate and runtime-init calls. Linux Exact Guest verification requires and observed a private COW hit; macOS used the single-use PreparedRuntime slot. Both returned canonical `42` with no Broker/workspace. This enables honest cold versus treatment-capacity-preprovisioned instrumentation, but the coordinate runner and harness identity remain open and no campaign timing was sampled.

**Gate R4:** Baseline and patched lanes match result, exception class, logical calls and authority state across positive and adversarial cases. Net latency remains positive after scratch execution, capsule transport, final validation, AST emission and loading.

### R5: decide whether to retain the execution-patch class

**Goal:** Avoid expanding a mechanism that wins only on authored fixtures.

Retain it only if R3/R4 show:

- useful natural eligibility;
- meaningful net overlap;
- bounded waste under invalid suffix/cancellation;
- a small verifier and patch surface;
- no requirement to reproduce arbitrary Python heap semantics.

Otherwise close the lane and keep Pysolate overlay-only. This is an acceptable and likely outcome.

### R6: typed large-result capsules, one type at a time

**Goal:** Test large-object economics only after scalar materialisation succeeds.

A DataFrame/local-analysis workload means generated scripts that load tabular data and perform pandas/NumPy filtering, joins, group-by, aggregation, plotting or model fitting inside one persistent interpreter. EAGER is naturally strong here because later chunks directly reuse live in-memory objects.

Pysolate cannot generically transfer a DataFrame while retaining clean final execution. It must materialise a typed artifact and reconstruct the object. Evaluate codecs separately:

1. bytes/text/JSON;
2. NumPy arrays with an exact dtype/shape/version contract;
3. tabular Arrow/Parquet only when the Guest profile already contains the required package.

Never use generic pickle as a cross-Guest cache format. Measure serialization, storage, hashing, loading, peak memory and semantic limitations. Compare against recomputation and against EAGER's persistent-object path.

**Gate R6:** Add a type only when `early compute + serialize + final load` beats final recomputation at a predeclared size/cost range. Do not claim generic object caching.

### R7: isolated workspace preparation

**Goal:** Explore private file-producing work without importing Shimmy claims.

Pysolate and Shimmy are separate systems. Do not claim that Pysolate currently has Shimmy's WASM Image COW or general rollback. A bounded future consumer may run in a private Pysolate workspace and export selected file contents only.

Start with one deterministic file-producing adapter whose outputs are private until accepted. Bind exact input files, output paths/content hashes, limits and final-source identity. Discarding the private workspace is not compensation for an external effect.

**Gate R7:** Invalid suffix and rejection leave official workspace state unchanged; accepted execution imports only allowlisted output content; no Python heap/process state crosses the boundary.

### Deferred: external writes and commit protocols

HTTP POST, email, remote deletion and compensating transactions are not extensions of the current read pass. They require independent authority, idempotency-key, ambiguous-outcome, reconciliation and commit protocols. Keep them on ordinary execution until a separate research goal is approved.

## Benchmark design

Use matched source, Host capability, operation delay, target Python, Guest artifact and environment. Separate these claims:

- **coverage:** what static/runtime predicates admit;
- **mechanism:** admitted work really starts and is consumed or discarded as recorded;
- **equivalence:** final language and authority outcomes match the declared contract;
- **performance:** critical-path overlap exceeds analysis/materialisation cost;
- **resource cost:** wasted work, bytes, memory, billing and quota;
- **prevalence:** eligibility in a natural fixed corpus.

Recommended primary metric:

```text
safe overlap coverage =
  admitted critical-path work actually overlapped with generation
  / eligible serial critical-path work
```

Do not use total execution time as the denominator when work cannot overlap the source-generation window. Report p50 and a tail statistic plus rejection-reason counts. Treat a small deterministic fixture as mechanism evidence, not production prevalence.

Ablation order:

```text
complete-source syntax only
+ region dependency facts
+ Host capability semantics
+ explicit speculative authority/freshness
+ typed result materialisation
+ private workspace isolation
```

Snapshot/rollback and commit deferral are not implied by this sequence.

## Recommended near-term priority

1. R0 and R1: sharpen the current contribution and safety contract.
2. R2: produce the matched EAGER comparison.
3. R3: rerun a more precise opportunity census.
4. Stop and review the evidence.
5. Attempt R4 only if R3 gives a positive natural opportunity signal.

Do not begin with DataFrames, arbitrary pure Python, durable caching, external writes or a general pass manager. The current semantic envelope is already sufficient to ask the opportunity question; the missing risk lies in the executable consumer and result transport, not in naming more passes.

## Positioning if the roadmap succeeds

The strongest supported statement would be:

> EAGER exploits complete generated statements when their Python-level effects can be conservatively admitted. Pysolate separates source analysis, Host speculative authority and final execution: it can prepare bounded work that syntax-only gating must serialize, and can optionally materialise narrowly proven pure regions through a source-bound execution patch.

Until R4 passes, omit the second clause. The currently demonstrated contribution remains qualified external-read pre-dispatch with unchanged final Python.
