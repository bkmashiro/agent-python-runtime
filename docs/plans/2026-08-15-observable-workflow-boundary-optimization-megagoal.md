# Observable Workflow-Boundary Optimization Autonomous Mega-Goal

> **For Hermes:** This is a prepared `/goal` handoff, not an active instruction in the
> session that authored it. When Yuzhe explicitly starts it with `/goal`, inspect the
> live repository and predecessor evidence before execution. Continue through coherent
> independently reviewed slices; stop only at the decision gates below.

**Status:** Prepared; not started
**Date:** 2026-08-15
**Owner:** Yuzhe
**Repository:** `~/projects/agent-python-runtime`
**Prepared baseline:** `cbd5e4fa00b8f9e678d5a149c2e990425eed9b96`
**Predecessor:** [`2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md`](2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md)

## Mission

Build and evaluate the smallest truthful Pysolate runtime in which repeated explicit
workflow nodes and typed Host tool/WASI boundaries—not executable AST regions—are the
stable units for preissue, declared-independent overlap, in-flight coalescing, retained
reuse, and pre-execution placement. Make every logical request, physical execution,
optimization decision, producer/consumer relationship, and rejection observable through
a sealed read-only Lab timeline.

The final demonstration is a seeded, reproducible batch of heterogeneous prepared Agent
programs submitted in shuffled order. Paired baseline and optimized treatments must show
where time is spent across model invocation, model output streaming, Pysolate WASM
compute, Host execution, tool waits, and optimization decisions, while preserving exact
observable behavior.

## Architectural correction

- Do **not** build executable AST-region reuse, semantic-similarity matching, graph
  containment matching, a region scheduler, a second Python executor, or heap snapshots.
- The target-Guest AST overlay remains analysis-only. It may identify exact capability
  occurrences, prove narrow legality, explain rejection, and improve pre-execution
  placement; it is not a reuse identity or execution authority.
- Reuse identity comes from explicit Harness workflow-node identity plus Host-owned
  artifact, source, inputs, capability spec/plan, canonical arguments, resource,
  freshness, authority, privacy, and policy identities.
- Typed tools are the primary optimization boundary. WASI optimization is permitted only
  for explicit Host-observed operations with equally strong typed identity/effect
  contracts. Ordinary Guest filesystem operations remain Python stdlib over WASI and
  must not be rewrapped as Pysolate tools merely to increase optimization coverage.
- The Host owns effect truth and terminal disposition. `started`, `may_have_started`, or
  ambiguous completion forbids fallback, replay, migration, or duplicate execution.
- Lab is a sealed read-only evidence consumer. It cannot schedule, preissue, retry,
  authorize, cache, or infer effects from presentation data.

## Initial execution split

### Track 0 — Live baseline and claim reset

- Read predecessor plans, mechanism matrix, live code, tests, and checked-in evidence.
- Mark executable AST-region reuse as rejected by the frozen F2 `no_go` result.
- Inventory existing whole-Run reuse, Agent Function retention/single-flight, staged
  observations, pre-dispatch, workflow evaluation, placement, Lab timeline, and trace
  schemas before adding any mechanism.
- Freeze the logical-request versus physical-execution vocabulary and prohibit duplicate
  optimizer-specific identity registries.

**Gate:** a short implementation map showing which mechanisms already exist, which need
only instrumentation/integration, and which genuine gaps remain. Do not reimplement an
existing mechanism under a new name.

### Track A — Identity-bound workstation Guest build cache

- Add a repository-owned build entry point for bounded Linux Guest builds on `gpu31` via
  `shell2`.
- Key reusable CPython/WASI build layers by CPython revision, WASI SDK/toolchain identity,
  target, build scripts, flags, and relevant dependency inputs.
- Rebuild Guest bootstrap embedding/linking when its source changes; force a cold rebuild
  on any cache-identity mismatch.
- Emit explicit cache hit/miss evidence and always regenerate artifact SHA-256,
  `RESULT.READY`, `SHA256SUMS`, and complete build logs.
- Keep job-private workspaces, trap cleanup, bounded storage, and a verified cold-build
  fallback. Never let a cache hit hide a skipped or failed build step.

**Gate:** cold and warm builds produce byte-identical or explicitly identity-distinct
artifacts as expected; both pass real Guest E2E. Record measured cold/warm times and disk
cost without claiming a speedup before measurement.

### Track B — Canonical optimization observation contract

Define a versioned, bounded, body-free Host evidence model before changing the Lab UI.
Every observation must bind enough information to answer “which logical task requested
this, which physical operation served it, and why was that legal?”

Minimum identities and relations:

- experiment, treatment, seed, workload, task, Run, Agent and workflow-node IDs;
- model invocation and output-stream spans, without private chain-of-thought;
- Guest artifact/profile/source and WASM compute spans;
- capability occurrence, canonical arguments, resource, freshness, authority, privacy,
  plan/grant and policy identities;
- logical request ID, physical execution ID, producer Run/task/node, and all consumers;
- decision kind: ordinary, preissued, declared-independent overlap, coalesced, retained
  reuse, placement, or rejected;
- reason codes, issue/claim/complete times, terminal disposition and evidence
  completeness.

A preissued request is shifted earlier, not “removed.” Parallel requests remain distinct
physical executions. Coalesced/reused logical requests remain visible and link to their
single producer. Rejected opportunities remain visible with canonical reasons.

**Gate:** mutation, truncation, cross-privacy, cross-authority, source/artifact drift and
mixed-generation tests fail closed; sealed projections contain no source body, tool
result body, prompt, model output, or chain-of-thought unless a separate explicit private
local view already authorizes it.

### Track C — Existing mechanism integration at stable boundaries

- Route existing exact whole-Run/Agent Function single-flight and retained reuse through
  the canonical observation contract.
- Bind repeated explicit workflow nodes to existing exact invocation identity rather than
  AST-region fingerprints.
- Instrument the existing narrow necessarily-reached tool pre-dispatch path from issue to
  claim, including unused/late/cancelled terminal outcomes.
- Admit overlap only for Harness-declared or independently Host-proved tool/workflow
  requests. Do not infer automatic sibling parallelism from arbitrary AST structure.
- Keep every mechanism independently disableable and prove the all-off path remains
  ordinary fresh execution.

**Decision gate:** if repeated workflow use lacks an explicit stable Harness identity,
stop and present the smallest protocol addition with compatibility and authority impact.
Do not synthesize identity from approximate source/AST similarity.

### Track D — Minimal tool-boundary optimization gaps

Use the workload and instrumentation to identify missing behavior. Implement only gaps
needed for one end-to-end story:

1. necessarily-reached exact read preissue;
2. declared-independent tool overlap;
3. retention-independent in-flight coalescing;
4. freshness-safe bounded retained reuse for exact completed reads or explicit workflow
   computations.

For every mechanism, prove exact identity partitioning, cancellation isolation,
leader/producer failure cleanup, expiry/eviction, privacy separation, authority changes,
and no post-effect replay. External writes remain non-preissued and non-reused.

**Gate:** each enabled treatment is observably equivalent to ordinary execution and
reduces a measured physical call or critical-path interval on at least one prepared
fixture. Otherwise retain the instrumentation and mark the mechanism no-go/default-off.

### Track E — Semantic pre-execution placement

- Measure current imports/requirements placement against semantic-overlay-derived
  requirements on the prepared corpus.
- Use only `SUPPORTED_BY_PYSOLATE`, `REQUIRES_NATIVE`, and `UNKNOWN`; unknown must choose
  native or unavailable before execution.
- Integrate semantic placement only if it safely improves measured precision.
- Permit implicit fallback only when the Host proves both logical and physical
  `not_started`; forbid migration/replay after any possible effect.
- Add WASM/native capability conformance, negative replay, artifact/profile identity and
  workspace-boundary tests.

**Decision gate:** if semantic evidence does not improve placement precision or requires
weakening authority/effect guarantees, record `no_go` and continue to the experiment and
Lab using the existing placement path.

### Track F — Seeded heterogeneous workload and paired treatments

Build a deterministic experiment driver, not a runtime scheduler:

- define a small grammar of prepared programs with different mixes of model invocation,
  streamed model output, WASM compute, tool reads, waits, duplicate workflow nodes,
  independent requests, freshness partitions and deliberate near-match negatives;
- generate a manifest from a fixed seed and shuffle submission order reproducibly;
- run the exact same manifest as baseline/all-off and as explicitly enabled treatments;
- use deterministic local/replayed model fixtures for reproducible evidence. Any paid or
  live provider run is optional demonstration evidence and requires separate approval;
- bind every result to artifact, corpus, manifest, seed, treatment and runtime identities;
- compare terminal output/effects with the existing observable-divergence oracle.

The workload must contain both optimization-positive cases and visually similar requests
that are correctly rejected because arguments, freshness, resource, privacy, authority,
source, artifact or workflow identity differs.

**Gate:** complete paired evidence with no unclassifiable divergence. Actual baseline
measurements, not invented counterfactual durations, are the source of savings claims.

### Track G — Lab storytelling surface

Extend the existing Lab rather than building a second viewer. Provide:

1. an experiment overview showing shuffled task arrivals and treatment identity;
2. a multi-lane timeline for model request/generation, model output, WASM compute, Host
   execution, tool waits and terminal outcomes;
3. visible logical-request spans linked to physical executions;
4. overlays for preissued, overlapped, coalesced, reused and rejected decisions;
5. task detail showing producer/consumer provenance and canonical reason codes;
6. baseline-versus-optimized comparison using measured call-count and critical-path
   deltas;
7. clear incomplete/truncated/private evidence states.

Use “model invocation/generation,” never “model thoughts,” unless only referring to a
measured API interval. Do not expose hidden reasoning. Optimized-away logical work must
remain visible as a linked logical span rather than disappearing.

**Gate:** canonical fixtures and a real paired experiment render without console errors,
source/result leakage, inferred effects, or UI-created execution authority. Perform
visual QA at overview, task-detail and narrow viewport sizes.

### Track H — Final evaluation and truthful closeout

Report, by treatment and workload class:

- logical requests versus physical executions;
- preissue lead time, overlap and critical-path change;
- coalescing followers and retained reuse hits;
- analysis/identity/observation overhead;
- latency distribution, CPU, memory and bounded storage;
- placement coverage and conservative rejection cost;
- cancellation/failure/expiry behavior;
- observable-divergence results and evidence completeness.

Update the architecture, threat model, mechanism matrix, user-facing Lab narrative, and
paper boundary. Independently review every Medium+ correctness/privacy/provenance issue,
run Linux real-Guest evidence, sign each coherent commit, push, verify `HEAD == @{u}` and
leave a clean worktree.

**Final claim boundary:** Pysolate may claim exact workflow/tool-boundary optimizations
only for the measured qualified contracts. It must not claim arbitrary Python semantic
reuse, AST-region execution, universal tool parallelism, hidden-thought observability,
or representative performance beyond the frozen workload.

## Cross-track stop conditions

Stop for joint discussion rather than widening the design if any result requires:

- executable AST regions, semantic similarity/containment matching or heap recovery;
- implicit task spawning or automatic sibling scheduling;
- a second effect/capability registry;
- Guest-authored authority or policy;
- write preissue/reuse, post-effect replay, or ambiguous fallback;
- cross-privacy reuse or source/result-body publication;
- a broad Harness protocol change not isolated behind an explicit versioned contract;
- a paper claim that depends on synthetic estimates presented as measurements.

A failed mechanism or placement gate is a valid result. Continue with independent
instrumentation, workload, Lab and evaluation work while preserving the no-go evidence.
