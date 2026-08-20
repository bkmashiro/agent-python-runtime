# Semantic speculation roadmap v0

Status: proposed research roadmap; no executable region consumer is enabled by this document.

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

This remains a semantic sidecar architecture plus one explicit compiler consumer. It is not overlay-only execution.

## Why this is materialisation, not ordinary constant folding

Classical constant folding evaluates small literal expressions during compilation. The proposed mechanism executes a source region early, captures its live-outs and substitutes a result in final execution. Call it **source-bound region materialisation** or **prepared pure-region result**, not merely AST cache.

Keep three identities separate:

1. **Run-scoped prepared result:** one final source occurrence; latency overlap only.
2. **Single-flight:** explicitly permitted concurrent computations share one physical attempt.
3. **Cross-run completed-result cache:** durable reuse requiring a complete computation identity and invalidation contract.

The first executable spike should implement only (1). Current corpus evidence found no exact materialisable cross-program repeats, so a durable region cache is not yet justified.

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

The tranches are sequential decision gates. A failed opportunity or cost gate stops later implementation rather than forcing the architecture toward EAGER parity.

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
