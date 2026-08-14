# Wait suspension, re-evaluation, and reuse trade-offs

Status: **Bounded Experimental fresh re-evaluation, exact AST-qualified
whole-Run reuse, and continuation-preserving cold-I/O implemented; broader policy
remains deferred.**
Date: 2026-08-14

## Decision summary

Pysolate should not initially persist dirty interpreter pages when an Agent
workflow waits. The preferred first proof is:

```text
explicit workflow nodes
+ immutable completed compute outputs
+ immutable completed I/O observations
+ fresh Guest per active evaluation period
+ Host-selected live refresh/wakeup boundary
```

At a wait, the bounded `runtime/workflow` mechanism now destroys the per-workflow
Guest. On wakeup, its single-wait, versioned synchronous graph re-evaluates in a
fresh Guest: unchanged explicit nodes use local records, one selected live
observation refreshes under current freshness/policy, and only transitive
successors are invalidated. It does not restore Python frames, heap, module
globals, WASM memory, descriptors, Broker objects, or `/tmp`. A general
multi-wait DAG scheduler and real repository-shaped acceptance remain deferred.

Worker-level prepared runtime and optional Linux memory COW remain resident
optimizations shared by many workflows. Destroying one waiting workflow's Guest
must not require discarding that shared baseline.

A separate default-off semantic adapter can now coalesce and retain only one
exact AST-qualified whole-Run result. It binds the full invocation and request
contract, keeps callback/semantic cache provenance separate, and publishes only
a canonical bounded zero-Host-call result from a successful Fresh Guest. This is
not frame restoration, a general workflow scheduler, statement-region extraction,
or permission to reuse live observations/effects.

Dirty-page checkpoint/restore remains a later comparison candidate only if
measurements show that explicit output materialization/recomputation dominates
and that real workflows contain valuable hidden interpreter state that cannot be
made explicit cheaply.

## Four distinct waiting strategies

### A. Keep the live Guest

```text
execute → wait with module alive → continue same interpreter
```

Benefits:

- minimum wakeup latency;
- no serialization, materialization, replay, or recomputation;
- arbitrary Python locals, frames, generators, globals, and open Guest resources
  remain available.

Costs and risks:

- private dirty memory and Host resources remain pinned for the entire wait;
- capacity scales with concurrent waiting workflows rather than active compute;
- stale authority, descriptors, clocks, provider sessions, and hidden mutable
  state survive;
- long waits increase operational leak and worker-affinity risk;
- migration and failure recovery require a live compatible process.

Best fit: very short waits when measured wakeup latency matters more than memory
capacity and the wait cannot outlive its original authority context.

### B. Persist dirty pages and restore the interpreter

```text
quiesce → persist dirty memory/runtime state → destroy → restore → continue
```

This is true checkpoint/restore. It is not the React-like design.

Potential benefit:

- preserves expensive intermediate Python state without recomputation;
- can release RAM during long waits while retaining continuation semantics.

Required state is larger than linear-memory dirty pages:

- Wasm linear-memory bytes and current size;
- mutable globals and tables;
- Wasm execution/value stack or an explicit safe-point continuation;
- CPython frames, heap, GC/allocator state, module caches, and exception state;
- WASI resource table and descriptor policy;
- clock/random state;
- Host callback and Broker sequence state;
- workspace view and `/tmp` policy;
- authority expiry/rebinding behavior.

The historical prepared-state audit rejected linear-memory-only restore for the
then-current CPython/Wazero artifact because mutable state existed outside
exported linear memory and Wazero exposed no complete module clone/restore API.
Persisting dirty pages alone therefore cannot currently support a truthful
Pysolate continuation claim.

Costs:

```text
suspend ≈ quiesce + dirty-page scan + write(D) + metadata
resume  ≈ instantiate shell + read(D) + map/fixup + rebind + validate
storage ≈ D × retention (before compression/dedup)
```

where `D` is the actual retained private dirty state, not the nominal Wasm memory
size. Compression reduces bytes but adds CPU and tail latency. Concurrent
snapshots contend for storage bandwidth and can produce latency spikes.

Best fit: only if future measurements find a large, expensive-to-reconstruct,
well-defined continuation state and a qualified backend can restore every state
class. It should not be the first implementation.

### C. React-like fresh re-evaluation

```text
explicit nodes → wait → destroy Guest
              → wake → fresh Guest
              → completed nodes return immutable local results
              → execute the selected live boundary
              → recompute only changed descendants
```

Persisted state:

- workflow and node identities;
- dependency edges or deterministic occurrence identities;
- immutable completed compute outputs;
- immutable completed I/O observations;
- content-addressed filesystem roots;
- explicit structured continuation inputs;
- policy/freshness decisions.

Not persisted:

- Python frames, heap, locals, module globals, Wasm memory, FDs, Broker objects,
  `/tmp`, or execution identity.

Benefits:

- waiting workflows consume no private Guest instance memory;
- no interpreter restore correctness problem;
- no sticky worker or Sandbox identity;
- completed pure work can be shared locally across workflows where policy allows;
- new observations invalidate only their transitive descendants;
- prepared runtime/memory COW can make each fresh re-evaluation cheap without
  becoming workflow state.

Costs:

- node-key computation, cache lookup, output materialization, and workflow
  dispatch still cost time;
- cached outputs consume local storage and page cache;
- large intermediate outputs may cost more to serialize/materialize than keeping
  a live heap;
- dynamic Python control flow needs explicit node/occurrence semantics;
- cache eviction turns wakeup into safe recomputation but increases latency;
- cache bugs can return stale or wrongly partitioned results.

This is the preferred initial research path.

### D. Explicit DAG continuation without replay from entry

```text
completed node graph → wait → destroy Guest
                     → wake → schedule next ready node directly
```

This stores the same explicit immutable state as strategy C but does not replay
the code-first workflow skeleton from its entry. It is likely the lowest-cost
execution form once the Harness already owns a workflow DAG.

Trade-off:

- lower wakeup overhead and simpler cache behavior;
- less natural for arbitrary generated Python written as ordinary sequential
  code;
- requires a stronger Harness/IR programming model.

C and D should share node identity and storage contracts. C is a code-first
front-end; D is the direct execution form. A compiler may eventually lower C to
D, but the initial proof can implement one explicit synchronous workflow
without claiming a general compiler.

## Freshness is not ordinary cache invalidation

A naive restart that obtains fresh data at every I/O point is incorrect:

```text
read E1 → compute P1 → wait E2
restart → read E1 again (possibly changed) → control flow may diverge
```

The Host must distinguish:

1. **historical observation** — a completed I/O result that belongs to the
   current workflow epoch and is reused as immutable history;
2. **wakeup observation** — the event/result that caused the current resume;
3. **explicit refresh** — a policy decision to replace a named prior observation
   with a new one and create a new downstream evaluation lineage;
4. **live write/effect** — never treated as a cached pure result.

Therefore a wait/resume contract needs stable node/occurrence identity and an
explicit refresh frontier. A new observation digest invalidates only transitive
downstream pure nodes. Unrelated and upstream nodes remain reusable.

This differs from a normal TTL cache. TTL expiry alone must not silently rewrite
a durable workflow's historical observations.

## Cost model

Let:

- `W`: expected wait duration;
- `M`: private resident memory retained by one waiting Guest;
- `R_keep`: live continuation wakeup latency;
- `D`: bytes of complete restorable private checkpoint state;
- `S(D)`, `L(D)`: snapshot-store and restore-load/fixup costs;
- `P`: local prepared-slot acquisition/start cost;
- `K`: workflow/node-key and lookup cost;
- `O`: immutable output/observation materialization cost;
- `U`: uncached recomputation after invalidation/eviction;
- `Q`: queueing cost caused by retained waiting instances or refill pressure.

Approximate latency:

```text
keep live:         R_keep
page checkpoint:   L(D) + rebind/validation
re-evaluation:     P + K + O + U
explicit DAG:      scheduler + P + input materialization + U
```

Approximate resource-time:

```text
keep live:         M × W + pinned Host resources
page checkpoint:   D × W in storage + snapshot/restore CPU and I/O
re-evaluation:     immutable outputs × W + restart/recompute work
explicit DAG:      same stored explicit state, less replay work
```

Destroying an instance does not save request latency by itself. It saves
memory/resource-time and can reduce `Q` by admitting more active work. It adds
wakeup latency unless a retained live instance would otherwise have waited in a
more congested pool.

A useful empirical break-even condition for React-like suspension is:

```text
capacity value of releasing M for W
+ queueing avoided
>
P + K + O + expected(U) + storage overhead
```

This cannot be reduced to one universal wait threshold. It depends on dirty
memory, output size, cache hit rate, worker pressure, profile readiness, and the
latency objective.

## Existing evidence and what it implies

Historical, fixture-scoped measurements—not Current implementation claims—show
why shared preparation matters:

- basic Python: WASI fresh request `5,647.797 ms`; single-use CPython-ready
  request `1.705 ms` on the earlier accepted fixture;
- NumPy fixture: WASI fresh `11,728.494 ms`; NumPy-ready COW hit `3.863 ms`;
- an earlier base prepared strategy recorded `7.492 s` factory-to-ready and
  `1.854 ms` steady execute;
- a NumPy-ready COW shard recorded `18,438.994 ms` factory construction and
  `3.863 ms` steady request;
- the historical snapshot-shell optimization measured replacement
  `InstantiateModule + COW restore` at about `0.623 ms` per slot for its exact
  artifact/host, versus much higher full-module replacement work;
- active dirty workloads showed that private memory scales with mutation: at 16
  active consumers, doubling controlled dirty bytes from 16 MiB to 32 MiB raised
  process PSS by about 256 MiB in that historical workload.

These measurements are not directly combinable into a new performance claim,
and the corresponding prepared/COW paths were archived from Current. They do
support three design hypotheses to remeasure:

1. destroying a per-workflow instance is attractive only if the expensive
   prepared baseline remains worker-shared;
2. constructing a prepared profile per resume would be far too expensive;
3. dirty-page retention cost depends on actual request mutation and can erase
   density even when the canonical baseline is shared.

The first implementation must produce matched measurements on the current
artifact and workflow rather than reuse these historical ratios.

## Per-mechanism trade-offs, risks, and implementation path

### 1. Whole-Run local content-addressed cache

Value:

- eliminates identical repeated Runs;
- provides safe recomputation lineage;
- enables local reuse without workflow machinery.

Risks:

- incomplete invocation identity;
- stale mutable filesystem/environment inputs;
- private partition mistakes;
- storage growth and cache poisoning;
- cache lookup/materialization slower than cheap computation.

Path:

1. binary `cacheable | not_cacheable` admission;
2. one explicit no-capability fixture over structured input/immutable root;
3. canonical key and immutable output record;
4. cache off/on equivalence and corruption/eviction tests;
5. retain only when measured saved compute exceeds lookup/materialization cost.

### 2. Concurrent single-flight

Value:

- collapses concurrent identical work without retaining results indefinitely;
- useful under Agent/subagent fan-out.

Risks:

- cancellation ownership;
- one caller's deadline or failure poisoning all waiters;
- unbounded waiter lists;
- tenant/project partition mistakes;
- head-of-line blocking when keys are too coarse.

Path:

1. separate in-flight coordination from durable retention;
2. independent caller cancellation, bounded waiters, result fan-out;
3. remove entry after terminal outcome;
4. compare duplicate execution count and tail latency under bursts.

### 3. React-like workflow re-evaluation

Value:

- releases instances during long waits;
- preserves code-first sequential presentation;
- incremental invalidation avoids repeating unchanged compute.

Risks:

- unstable dynamic node occurrence identity in loops/branches;
- accidental replay of live I/O or writes;
- historical versus refreshed observation confusion;
- large output materialization;
- source edits between suspension and wakeup;
- cache eviction causing latency spikes.

Path:

1. explicit synchronous nodes only;
2. fixed workflow source/runtime identity for one epoch;
3. historical observations plus explicit wakeup/refresh frontier;
4. destroy Guest and re-evaluate with cache hits;
5. mutate one observation and verify downstream-only invalidation;
6. compare with direct DAG continuation and keep-live controls.

### 4. Explicit DAG continuation

Value:

- lower wakeup overhead than replaying the workflow skeleton;
- clean scheduling, branching, and fault recovery.

Risks:

- larger Harness/IR surface;
- less transparent ordinary-Python programming model;
- version migration of persisted graph state;
- temptation to build a general workflow engine.

Path:

1. reuse the same node/output identity as React-like re-evaluation;
2. represent only one bounded workflow;
3. schedule next ready node directly;
4. compare retained bytes and wakeup latency against re-evaluation;
5. decide whether code-first replay has enough usability value to retain.

### 5. Immutable filesystem roots and branch DAG

Value:

- movable state independent of workers/interpreters;
- cheap recursive subagent branches when deltas are small;
- cache keys can bind complete filesystem inputs;
- compare/select/merge without live Sandboxes.

Risks:

- metadata/chunk amplification for many tiny files;
- hashing and materialization cost;
- garbage collection and reference accounting;
- symlink/path/privacy leakage;
- merge conflict semantics;
- large generated binaries may dominate storage.

Path:

1. extend current manifest/Capsule identity into immutable root + parent lineage;
2. file-level CAS before chunk-level dedup;
3. child roots store only changed files/metadata;
4. explicit compare/select; bounded three-way merge later;
5. benchmark branch bytes, hashing, materialization, and GC.

### 6. Prepared Runtime

Value:

- amortizes expensive CPython/profile initialization;
- makes fresh re-evaluation practical;
- remains portable relative to Linux-specific memory COW if implemented as
  never-served prepared instances.

Risks:

- large idle memory per queued instance;
- refill CPU spikes;
- stale artifact/profile generations;
- shutdown races;
- hidden state if a served instance is accidentally reused.

Path:

1. restore the historical single-use safety contract behind an off switch;
2. keep capacity bounded and zero/default-off initially;
3. pool only never-served instances;
4. measure factory, hit, miss, refill, RSS/PSS, failure recovery;
5. retain ordinary fresh instantiation as fallback.

### 7. Worker-local memory COW

Value:

- many fresh instances share clean prepared pages;
- improves density without becoming workflow state;
- combines naturally with short compute periods separated by destroyed waits.

Risks:

- Linux/backend/artifact specificity;
- CPython refcounts, allocator, GC, imports, and buffers dirty more pages than
  expected;
- virtual mapping metrics can be mistaken for physical memory;
- refill saturation and private-page growth;
- implementation coupling to Wazero internals.

Path:

1. recover current-artifact page/state census;
2. restore single-use MAP_PRIVATE strategy only after qualification;
3. measure mapping PSS/private dirty by phase;
4. compare prepared without COW, prepared+COW, and ordinary fresh;
5. do not attempt served-slot reset or partial dirty restore initially.

### 8. Pure/I/O split and late effects

Value:

- retries and wakeups reuse pure work;
- speculative branches need no write authority;
- write execution is narrow and explicit;
- effect model becomes an optimizer barrier, not only audit metadata.

Risks:

- `read_only` tools may still be live/nondeterministic;
- accidental tool call inside a cacheable node;
- output/intents derived from stale observations;
- approval policy drift;
- over-segmentation increases orchestration cost.

Path:

1. cacheable nodes receive no Broker authority;
2. live reads produce immutable observations outside the node;
3. pure nodes consume observation/root digests;
4. writes are immutable intents outside the cache;
5. effect dispatch remains optional/denied until a qualified adapter exists.

### 9. Local online optimizer (`JIT`)

Value:

- learns which nodes merit retention, single-flight, fusion, or prepared-profile
  placement from aggregate local behavior;
- optimizes many workflows rather than one invocation.

Risks:

- optimizer overhead exceeds savings;
- unstable workload makes historical hotness misleading;
- fusion reduces cache reuse or increases invalidation scope;
- statistics leak private activity;
- adaptive decisions make benchmarks and incidents harder to reproduce.

Path:

1. no automatic optimization initially; collect bounded counters only;
2. add measured admission/eviction for explicit nodes;
3. add single-flight independent of retention;
4. test explicit A→B fusion with rollback to unfused execution;
5. record every decision and keep deterministic off mode;
6. defer arbitrary Python region extraction and global sharing.

### 10. Dirty-page checkpoint/restore

Value:

- may preserve expensive hidden intermediate state when explicitization is too
  costly.

Risks:

- highest correctness and backend coupling risk;
- snapshot I/O/storage and restore tail latency;
- authority/credential/resource resurrection;
- version/address drift;
- checkpoint storms under correlated waits;
- competes with the project's fresh-execution advantage.

Path:

1. do not implement in the initial roadmap;
2. first measure `D`, explicit output size, recomputation, and waiting durations;
3. require a real workload where C/D lose materially;
4. require a complete safe-point/state inventory and fault-injection plan;
5. implement as an independent optional backend strategy, never as required
   workflow semantics.

## Matched experiment matrix

Use one current artifact, machine, workflow, observation fixture, and filesystem
input. Do not combine older fixture numbers into the result.

Controls:

1. keep live Guest across wait;
2. destroy and rerun with cache disabled;
3. destroy and React-like re-evaluate with local cache;
4. destroy and direct-DAG continue;
5. repeat 2–4 with prepared runtime off/on;
6. on qualified Linux, repeat prepared on with memory COW off/on;
7. dirty-page checkpoint only as a later comparison if justified.

Sweep:

- wait: immediate, short, medium, and human-scale simulated waits;
- pure-prefix compute: cheap to expensive;
- intermediate output: small structured value to large filesystem root;
- invalidation: none, one downstream observation, broad change;
- concurrency: one workflow to many waiting/awakening workflows;
- cache: hit, miss, eviction, corruption;
- filesystem mutation: low to high dirty working set.

Measure:

- suspend and wakeup p50/p95/p99;
- time to next live I/O and time to final result;
- Guest instance-seconds and private MiB-seconds;
- process PSS/private dirty and ready-pool memory;
- bytes written/read/materialized;
- cache and single-flight hit rates;
- recomputed node count;
- CPU spent on refill, hashing, serialization, and recomputation;
- queueing, rejection, and completion rate under correlated wakeups;
- semantic equivalence, freshness frontier, authority/effect transcript.

## Initial recommendation

Implement and evaluate in this order:

```text
whole-Run local cache + single-flight
→ explicit pure/I/O workflow nodes
→ compare React-like re-evaluation with direct DAG continuation
→ immutable filesystem roots/branches
→ prepared single-use runtime
→ optional Linux memory COW
→ measured local retention/fusion optimizer
```

Keep-live is the latency control. Dirty-page checkpoint/restore is not part of
the initial path. Private workspace attempts, external-effect truth, playback,
verification, and Lab remain independently switchable supporting tracks.

The first paper-worthy claim should be conditional rather than universal:

> For workflows whose waits are long relative to local fresh-start and
> materialization cost, and whose reusable state is representable as immutable
> node outputs/filesystem roots, Pysolate can retire per-workflow Guests during
> waits and reconstruct progress through fresh execution without interpreter
> checkpointing. Prepared runtime and memory COW may independently reduce the
> cost and increase the density of those active evaluation periods.
