# ADR 0006: Execution slots and stateful session lifecycle

- Status: Accepted architecture boundary; session lifecycle is not implemented
- Date: 2026-07-23

## Context

The V1 runtime executes one bounded, generated-Python `RunRequest`. Its safe baseline is a fresh instance, and its optional prepared pool contains never-served, single-use modules. Every served module is discarded. This makes run-local freshness explicit and avoids claiming that linear-memory restoration covers mutable globals, tables, WASI resources, Host state, or external effects.

Separate Linux experiments in Shimmy demonstrate that one wazero Runtime and CompiledModule can instantiate many independent modules and that prepared linear-memory pages can be physically shared with `memfd` plus `MAP_PRIVATE`. Those results are useful mechanism evidence, but they do not change this repository's product contract and are not proof that an arbitrary Python session can be restored from dirty linear-memory pages alone.

A future product may need long-lived Python sessions, warm hibernation, cold storage, and migration. Adding a session identifier to `RunRequest` or retaining served modules in the existing prepared pool would conflate two security and lifecycle contracts.

## Decision

### 1. Keep V1 function execution stateless

`engine.Runner.Run` remains a stateless function-execution contract:

- generated code and run-local Python state do not survive the Run;
- fresh-instance remains the portable fail-closed fallback;
- a prepared slot is never served twice unless a separately proven reset mode is introduced;
- timeout, trap, cancellation, restore uncertainty, or resource uncertainty discards the module;
- session identity, hibernation, and migration are not added to `RunRequest` or inferred from receipt identity.

### 2. Introduce stateful sessions only through a separate future contract

A stateful session plane, if implemented, must use a distinct Host-owned API and lifecycle. It may borrow the neutral artifact, capability, provenance, and receipt contracts, but it must not weaken the existing Runner semantics or expose backend-specific wazero types through neutral packages.

Execution capacity and session durability are separate concepts:

```text
trusted immutable base(s)
        │
        ├── bounded, sessionless hot execution slots
        ├── explicitly bound live sessions when latency requires them
        ├── compressed warm session capsules
        └── versioned/encrypted cold session capsules
```

Hot-slot count follows active concurrency and resume SLOs, not total session count. "Thousands of hot instances" is a measurement outcome, not an architectural requirement.

### 3. Define two capsule classes

A future session plane may support:

- **ExactMemoryCapsule** — binds exact Guest artifact digest, runtime/backend ABI, architecture, page size, BaseID, capsule schema, and all required non-memory state. It is suitable only where this compatibility tuple matches.
- **LogicalCapsule** — contains application state exported and imported through an explicit Guest contract. It is the preferred migration format when the application can define one, but the runtime must not claim that arbitrary Python objects are generically serializable.

Neither class exists until a schema and cross-fresh-process round-trip gate pass.

### 4. Treat linear-memory deltas as partial state

Dirty linear-memory pages alone are never called a complete session snapshot. Before hibernation or resume, the exact artifact must account for:

- mutable globals and tables;
- every linear memory and growth behavior;
- WASI descriptors, offsets, VFS, clock, and RNG state;
- Host buffers, capability state, receipts, and credentials;
- active calls, traps, cancellation, and close state;
- external handles and side effects.

A session may be captured only at a Host-controlled quiescent boundary. Active external handles must be absent, closed, or reconstructed through an explicit Host-owned rebind contract. Unknown state fails closed.

### 5. Keep bases trusted and tenant-neutral

A shared base is built only from a deterministic, reviewed bootstrap/preparation recipe. Its identity includes at least:

```text
Guest digest
+ artifact profile/dependency manifest
+ trusted init/prepare recipe digest
+ evaluator/schema identity where applicable
+ backend/runtime ABI
```

No page or object from an arbitrary live user session may be promoted into a shared base. Profile-guided promotion may propose trusted recipes offline; it may not publish live session memory as a base.

### 6. Stage memory optimization by evidence

The promotion order is:

1. measure current fresh and single-use prepared behavior;
2. add Host admission, pressure watermarks, bounded refill, and recycle rules;
3. if justified, evaluate physical sharing only for independently initialized, never-served prepared slots;
4. prove complete state capture/restore before retaining stateful session deltas;
5. implement eager delta restore before any lazy fault-in mechanism;
6. add local cold storage before object storage or migration;
7. consider UFFD, prefetch, deduplication, and automatic base promotion only when phase timings prove they are the bottleneck.

Linux-specific mechanisms remain optional backend strategies. They never become the neutral safety baseline.

### 7. Separate memory accounting domains

Evidence must distinguish:

- Go heap, GC, and scheduler metrics;
- Guest mappings, PSS, private dirty pages, page tables, VMAs, and faults;
- process/cgroup memory current, peak, events, and pressure;
- fixed per-runtime/base cost, idle per-slot cost, active dirty cost, and capsule storage cost.

Guest mmap pages are not Go heap. Large in-memory capsules must not be placed on the Go heap by default without measuring heap pacing and `GOMEMLIMIT` behavior.

### 8. Bound deduplication and migration

Immutable trusted bases may be globally content-addressed. Session deltas are tenant-scoped by default. Cross-tenant convergent encryption or equality-revealing deduplication is rejected without a separate privacy review.

Exact-memory migration requires an exact compatibility tuple, integrity verification, encryption, ownership lease/fencing, and absence or reconstruction of external handles. Logical migration is preferred when available.

## Activation dependency

The session-lifecycle implementation roadmap is a planned successor. It must not preempt the active NumPy artifact-profile and final-hardening tracks unless the owner explicitly reprioritizes them. Phase 0 design documentation may land now; runtime implementation begins only from the successor roadmap's activation gate.

## Consequences

- Existing callers retain a simple one-Run freshness contract.
- The current single-use prepared pool remains truthful and useful even if session work is deferred.
- COW mechanism evidence can inform a bounded backend spike without importing Shimmy product semantics or copying code without provenance review.
- Session storage becomes a versioned data/security subsystem, not an incidental pool feature.
- Warm/cold capacity can scale independently from active execution slots.
- Some arbitrary Python sessions may remain non-hibernatable or same-build-only; the runtime reports that boundary instead of fabricating portability.

## Rejected alternatives

### Add `session_id` to `RunRequest`

Rejected because untrusted request data must not select durable Host state or change lifecycle/authority semantics.

### Reuse served prepared modules and save only dirty memory pages

Rejected because memory pages do not cover all mutable module, WASI, and Host state.

### Build UFFD lazy restore first

Rejected because it adds fault-handler and tail-latency complexity before eager restore has been proven to dominate resume latency.

### Make one live module per durable session

Rejected as the default because session count would determine execution capacity and retained Host metadata. Live-bound sessions remain an explicit low-latency tier, not the universal representation.

### Automatically promote hot live sessions into shared bases

Rejected because session memory may contain tenant data, credentials, nondeterministic state, and external-resource assumptions.
