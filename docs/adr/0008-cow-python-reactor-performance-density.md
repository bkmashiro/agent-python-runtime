# ADR 0008: COW Python reactor performance-density

- Status: Accepted target; implementation is evidence-gated
- Date: 2026-07-27

## Context

Agent Python Runtime is intended to be a common generated-Python execution layer for many agents. Fresh instantiation and never-served single-use preparation prove the portable safety baseline, but they duplicate initialization work and prepared Guest memory. The owner has confirmed that fast Python execution and high ready/active density are coupled primary product goals, not a stateful-session side project.

The exact CPython-WASI reactor may mutate linear memory through user allocations, reference counts, pymalloc/GC metadata, caches, namespaces, tracebacks, and I/O buffers. Therefore “only user-created pages become private” is a target to measure and improve, not an initial claim. Linear-memory restoration also does not by itself prove restoration of mutable globals, tables, WASI resources, or Host state.

## Decision

1. Linux prepared-memory COW is the primary runtime-performance direction. `fresh-instance` remains the portable fail-closed baseline and emergency fallback.
2. A dispatcher-owned flyweight may share immutable artifact, VFS, wazero Runtime/CompiledModule, Host definitions, and an exact prepared image. Live modules and all mutable execution, WASI, request, broker, buffer, health, and lease state remain per slot or per request.
3. The correctness-first COW mechanism is a sealed canonical memory image with one `MAP_PRIVATE` mapping per slot. Whole-image remap/discard is the baseline reset. Dirty-aware partial reset is optional and must beat whole remap after tracking overhead.
4. Promotion is staged and truthful:
   - `cow-ready-single-use` shares prepared pages but discards every served slot;
   - `cow-full-remap-restore` reuses a slot only after complete mutable-state proof;
   - `cow-locality-optimized` measurably confines request mutations;
   - `cow-adaptive-reset` selects a proven reset mechanism;
   - `cow-performance-density-candidate` passes Linux correctness, density, load, failure, and soak gates.
5. Page-write profiling and exact state census precede allocator, subinterpreter, UFFD, or compression work. CPython-internal changes are later comparison spikes, not prerequisites for the initial COW path.
6. Performance and density are evaluated together. Memory capacity, ready slots, active slots, load concurrency, throughput, and stable capacity are distinct metrics.
7. No implementation, benchmark, or capacity result from another repository is evidence for this repository. No code is copied from another repository without separate provenance and license review.

## Safety and fallback

- Explicit COW qualification must prove the active strategy and reject silent fallback.
- Non-Linux or unsupported memory shapes fail explicit COW selection; ordinary fresh behavior remains available when selected by Host policy.
- Timeout, trap, cancellation uncertainty, growth, address/size drift, unknown mutable state, reset failure, or invariant mismatch retires the affected slot.
- Served-slot reuse is forbidden until every mutable state class is unchanged, restored through supported contracts, or classified as a retirement condition.
- OS-specific details remain inside the wazero adapter and do not leak into the Guest request ABI.

## Consequences

- Runtime-performance work resumes under the active COW performance-density roadmap; the prior session/capsule roadmap remains historical and closed.
- The first safe product milestone is COW-backed never-served single-use readiness plus fresh-process density evidence, not an immediate served-slot reuse claim.
- Whole-remap COW, request-page locality, custom allocators, subinterpreters, UFFD, and compression are compared on one latency-density Pareto frontier and may be rejected independently.
- Guest source/profile changes require exact producer/consumer, source-lock, and Linux/WASI qualification.
- This ADR authorizes implementation and evidence work only. It does not approve release, deployment, production capacity, stateful sessions, persistence, or a production SLA.

## Rejected alternatives

### Continue fresh/single-use as the product center

Rejected because it does not target the required density and recurring initialization costs, though it remains the correctness baseline.

### Claim reusable COW after restoring linear memory only

Rejected because mutable globals, tables, WASI, Host state, growth, and failure paths require explicit proof.

### Start with a CPython fork or custom object allocator

Rejected as the first step. Page-write attribution and lower-risk flyweight/COW layers must show where private pages arise before interpreter-internal changes are justified.

### Treat memory-only slot estimates as concurrency

Rejected. Stable concurrency requires measured load, queue, latency, error, CPU, memory, and recovery evidence.
