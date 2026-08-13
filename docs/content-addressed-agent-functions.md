# Proposed design: content-addressed Agent Functions

Status: **Design hypothesis; not implemented.**
Date: 2026-08-13

## Purpose and relationship to existing direction

This proposal extends rather than replaces Pysolate's authority-lifecycle
positioning. Fresh single-use Guests, frozen per-Run authority, typed Host
effects, explicit workspace state, evidence, and backend constraints remain the
foundation. The new hypothesis is that their conjunction can expose selected
parts of an Agent workflow as locally reusable computations:

> A content-addressed Agent Function is a fresh Python evaluation whose code,
> runtime, declared inputs, and immutable filesystem view determine an explicit
> result and derived filesystem root, while all live I/O and authority-bearing
> effects remain outside the cacheable boundary.

This is not a claim that arbitrary Python is pure, that current Runs are cached,
or that Pysolate provides a distributed execution graph.

## Initial scope

The first version deliberately has only two classifications:

- `cacheable`: admitted under the complete initial contract below;
- `not_cacheable`: the safe default for everything else.

There is no purity confidence ladder, automatic arbitrary-region extraction,
cross-machine cache synchronization, cross-tenant result reuse, or global result
cache in the initial scope.

The cache is local to one trusted Host. Its entries are private to the configured
user/project partition even when identical bytes exist elsewhere.

## Orthogonality and off-switches

Content-addressed functions are optional. Disabling the local cache executes the
same admitted invocation in a normal fresh Guest. Disabling single-flight causes
concurrent callers to execute independently. Neither fallback may widen
authority, change declared outputs, fabricate capability receipts, or require a
persistent interpreter.

The initial function cache does not require private workspace attempts,
playback, external-write support, prepared runtime, memory COW, Lab, or a second
backend. Conversely, those mechanisms must remain usable without the function
cache when their own dependencies are satisfied.

Workflow re-evaluation is a separate optional consumer of explicit node
identities and local completed-output lookup. It may use the function cache in
the initial implementation, but enabling ordinary function caching must not
silently turn an Agent Run into a durable workflow. The complete mechanism
matrix and fallback requirements are maintained in
[proof-first-authority-roadmap.md](proof-first-authority-roadmap.md).

## Initial execution contract

A cacheable invocation is identified by a canonical digest over at least:

```text
function source / explicit function identity
Guest artifact and execution profile
transitive admitted import closure
canonical structured input
immutable input filesystem root(s)
declared environment and deterministic settings
output schema
cache partition and policy epoch
```

Initial admission is conservative and fail-closed. A function is cacheable only
when the Host can enforce all of the following:

- no Host capability call occurs inside the function;
- no live external read or external write occurs;
- filesystem reads are restricted to declared immutable roots;
- filesystem writes are restricted to a private derived-output root and `/tmp`;
- no shared live workspace is mutated;
- clock and randomness are denied or supplied as explicit frozen inputs;
- dynamic imports and unknown imported behavior are rejected;
- canonical input, output, and failure representations are defined;
- the Guest artifact, profile, and import closure are identity-bound.

A declaration or decorator may request this mode, but declaration alone is not
proof. Any unknown behavior makes the invocation `not_cacheable`.

Internal mutation is allowed. Python objects and private temporary files may be
mutable during the fresh Run; referential transparency is required only at the
Host-visible function boundary.

## Workflow split at I/O boundaries

A target workflow has alternating effect and computation nodes:

```text
live read E1
→ immutable observation O1
→ cacheable compute P1
→ cacheable compute P2
→ live read E2
→ immutable observation O2
→ cacheable compute P3(O1, O2)
→ external-write intent E3
```

Live reads and writes are never hidden by the pure-function result cache. A
completed prior I/O observation remains immutable history for the current
workflow epoch. Only an explicit wakeup or refresh boundary obtains new live
data; its digest becomes an input to downstream cacheable nodes and invalidates
only transitive descendants rather than silently re-running every earlier I/O
operation.

This enables a React-like re-evaluation model without retaining interpreter
state:

1. finish or suspend the workflow at an explicit I/O boundary;
2. persist the workflow skeleton, completed node identities, immutable outputs,
   and filesystem roots;
3. destroy the Guest instance;
4. later create a fresh Guest;
5. re-evaluate the workflow from its entry;
6. return local cached values immediately for unchanged cacheable nodes;
7. execute the next live I/O node under its current freshness/policy rules;
8. use the resulting observation digest to invalidate only changed downstream
   computations.

This is not a Python heap continuation. Locals, frames, module globals, open file
descriptors, Broker objects, `/tmp`, and WASM memory are never continuation
state.

A workflow may therefore release compute capacity while waiting for user input,
a timer, an external event, or a permitted fresh observation. Durable state is
small and explicit rather than a pinned Python process. The alternative waiting
strategies, break-even model, and matched experiment are documented in
[wait-suspension-and-reuse-tradeoffs.md](wait-suspension-and-reuse-tradeoffs.md).

## Why separation can improve single-Host throughput

Purity does not itself make Python instructions faster. It permits the Host to
eliminate or reshape work:

- memoize repeated invocations;
- collapse concurrent identical invocations with single-flight;
- reuse common prefixes across Agent and subagent branches;
- retry a live I/O/effect node without repeating unchanged compute;
- destroy Guests while a workflow waits and reconstruct progress from cached
  nodes;
- schedule functions near locally resident artifact and filesystem data;
- fuse chains of small pure nodes to avoid Guest, serialization, and storage
  overhead;
- split data-parallel pure nodes when the scheduling benefit exceeds overhead;
- safely duplicate expensive pure work for straggler mitigation;
- reconstruct evicted intermediate outputs from content-addressed lineage.

The first performance target is one machine because Pysolate's historical
prepared-runtime and memory-COW work is a local density optimization. Semantic
correctness must not depend on Linux memory COW: immutable filesystem roots and
function identities are portable truth, while prepared memory is an optional
worker-local acceleration.

## Population-aware optimizer, initially local

"JIT" in this design means online workflow optimization, not initially a new
Python machine-code compiler. One Host observes completed invocations and keeps
bounded statistics such as:

```text
function and invocation identity
call frequency and concurrency
execution time and peak memory
input/output byte sizes
cache and single-flight hits
Guest startup and materialization cost
node adjacency and intermediate fan-out
admission violations and failures
```

The first optimizer may make only four decisions:

1. retain or evict a local result;
2. single-flight concurrent identical invocations;
3. fuse a repeatedly adjacent chain of explicit cacheable nodes;
4. prefer a local prepared artifact/profile for recurring functions.

It does not rewrite arbitrary Python regions. Evolution, gated by measurement:

```text
whole-Run cache
→ explicit Agent Function nodes
→ explicit workflow graph and I/O boundaries
→ measured local fusion/splitting/specialization
→ only then research automatic hot-region extraction
```

A later system may learn across many users, but global result sharing is not the
first mechanism. Safer fleet-wide reuse candidates are public code artifacts,
compiled modules, resource profiles, and optimization decisions. Private result
reuse remains partitioned unless a future publication/privacy contract proves a
stronger scope.

## Harness/runtime composition

The Harness owns logical workflow structure:

- Agent and subagent creation;
- explicit node/function boundaries;
- model calls and conversation state;
- waits, user interaction, branch/join, and winner selection;
- deciding when current external data is required.

Pysolate owns execution truth:

- fresh Guest construction and destruction;
- artifact/profile and immutable-input admission;
- local cache key construction and partitioning;
- enforcement that cacheable nodes make no Host calls or undeclared reads;
- derived filesystem output roots;
- typed live I/O/effect boundaries;
- evidence sufficient to explain a cache hit or miss.

An LLM call is not automatically a cacheable Agent Function. A captured model
response may become an immutable input to a later cacheable Python computation.

## Preserved Pysolate-specific conjunction

Content-addressed functions do not supersede earlier mechanisms. They compose as:

```text
fresh disposable Python Guest
× no ambient Host/OS authority
× frozen typed capability/effect boundary
× explicit immutable observations
× movable content-addressed filesystem roots
× local result cache and single-flight
× Harness-visible workflow boundaries
× optional prepared-memory COW density
× Host-owned late commit and evidence
```

The authority model is what makes the Host able to enforce a cacheable boundary;
the content-addressed layer gives that authority model a performance and
orchestration use beyond audit. Workspace/Capsule portability supplies immutable
inputs and outputs. Fresh execution removes hidden continuation state. Typed
effects define where caching must stop. Historical memory COW may make many
short evaluations dense, but is not the semantic branch mechanism.

## Initial proof

Use one real synchronous workflow on one Host:

```text
live fixture read O1
→ explicit cacheable function P1 over immutable filesystem root
→ explicit cacheable function P2
→ wait boundary; Guest destroyed
→ fresh Guest re-evaluates and hits P1/P2
→ second live fixture read O2
→ only downstream P3 recomputes when O2 changes
→ stage an external-write intent; do not dispatch it
```

The proof must demonstrate:

- identical invocation single-flight under concurrency;
- a later fresh Guest obtains identical cached outputs without restoring Python
  state;
- a changed live observation invalidates only its downstream nodes;
- a Host call attempted inside a cacheable function fails closed and is not
  cached;
- private filesystem input changes alter the invocation identity;
- cache entries never cross the configured local user/project partition;
- eviction causes safe recomputation;
- measured cold, warm, single-flight, and fused/unfused costs;
- prepared-memory COW, if used, changes performance but not results or identity.

Stop or narrow the direction if cache lookup/materialization overhead dominates,
real Agent workloads exhibit little repeated pure computation, function
boundaries require unnatural model-generated code, or conservative admission
marks almost every useful node `not_cacheable`.

## Deferred questions

- automatic extraction of arbitrary Python regions;
- cross-machine CAS and cache coherence;
- global/multi-tenant result reuse and equality side channels;
- public Function registry and publication review;
- transparent hardware specialization;
- arbitrary Python heap/frame checkpointing;
- general asynchronous Python execution;
- claims of universal determinism or semantic equivalence.
