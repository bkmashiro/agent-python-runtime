# Host-Scheduled Calls and Immutable Value Reuse Autonomous Mega-Goal

> **For Hermes:** This is the planned runtime successor to the completed
> [correctness-gated source-bound optimizer Mega-Goal](2026-08-23-correctness-gated-agent-python-optimizer-autonomous-megagoal.md).
> Read this file fully, inspect live Git state, then continue through successive verified
> slices. A green test, signed commit, or rejected candidate is a checkpoint, not a stop
> condition. Stop only at the decision/resource/safety gates below or complete closeout.

**Status:** Planned and approved; implementation has not started. This file becomes the
active runtime execution source of truth when Yuzhe starts its `/goal`.

**Prepared:** 2026-08-24

**Repository:** `/Users/yuzhe/projects/agent-python-runtime`

**Preparation baseline:** `37ab006e06c6d107b364d69fcb14f01328aa99d6`

**Goal:** Preserve ordinary synchronous Agent Python while adding the smallest Host-owned
mechanisms that can overlap high-latency calls, avoid moving unnecessary immutable data,
and reuse expensive immutable intermediates across fresh Runs or agents. Use those
mechanisms to implement only a small catalog of common, adapter-proven source passes;
do not reproduce Stratum's DataOp system or turn Pysolate into a general Python compiler.

**Architecture:** Target-Guest analysis may lower one admitted synchronous call into hidden
Host `submit` and `materialize` operations, but Python never receives a Future or proxy.
The Host owns pending-call state, readiness, errors, cancellation, result storage and
cleanup. A separate late-bound value-slot contract lets a pass request an immutable value
without selecting copy, private-COW or data-local execution itself. Python retains branch,
loop, exception and mutation semantics; the Host tracks only dynamic call/value events
that actually arise.

**Tech stack:** Go runtime/Broker/Wazero, exact target CPython/WASI Guest bootstrap and AST
analysis, existing static source-pass registration, Host-owned prepared/private runtime
primitives, deterministic Go/Guest/E2E tests and artifact-backed benchmarks.

---

## Why this successor exists

The predecessor established a static source-pass seam and retained four narrow passes:
`semantic_pre_dispatch`, `prepared_pure_region`, `pure_scalar_cse` and
`pure_scalar_fold`. It also established an important negative result: cheap scalar
rewrites do not currently repay transform and fresh-Guest overhead. The retained matched
runs measured `pure_scalar_cse` about 14.61% slower and `pure_scalar_fold` about 4.49%
slower; the retained `prepared_pure_region` profiler case was also slower end to end.
Those mechanisms prove legality seams, not a reason to add more textbook peepholes.

The next value frontier is work whose physical cost can dominate compiler/runtime
bookkeeping:

1. high-latency independent Host calls that can overlap without changing synchronous
   Python observation points;
2. large immutable data that can be projected, reduced or kept near its producer instead
   of fully materialized in a Guest;
3. expensive deterministic intermediates that several fresh Runs or agents can consume
   without sharing Python heap or mutable interpreter state.

Stratum is useful here as workload and mechanism evidence, not as an architecture to
copy. Stratum gets broad rewrite power from a lazy DataOp operator graph. Pysolate handles
general Python and does not have that graph, so it will inherit only individual patterns
that are common, statically recognizable and backed by an adapter-declared equivalence
law.

## Research thesis

> Fresh Agent Python can recover useful overlap and immutable-value reuse when the Host
> owns pending work and materialization, while the original dynamic Python occurrences
> still determine logical calls, errors and external effects.

The expected contribution is the combination, not any isolated cache or Future:

- synchronous ordinary Python surface;
- fresh per-Run Python state;
- Host-owned split-phase physical work;
- separate physical-work and logical-effect accounting;
- immutable Host values shared across fresh consumers;
- a few workload-selected source/Host transformations;
- fail-closed fallback before external effects, with no post-effect replay.

## Value filter

Prefer a slice only when it does at least one of the following with measurable end-to-end
value:

- overlaps latency that the original program would otherwise wait for;
- reduces provider requests or repeated expensive deterministic work;
- reduces bytes read, decoded, copied or materialized into a Guest;
- allows several fresh consumers to reuse one immutable physical value;
- simplifies a real high-frequency pattern without broadening Python semantics.

Every retained optimization must have:

1. a narrow admitted Python/capability subset;
2. an executable pass-off/pass-on semantic oracle;
3. an explicit physical-cost model;
4. matched end-to-end evidence, including overhead and negative cases;
5. unchanged default-off fallback until promotion is separately justified.

A pass that is correct but does not improve its intended cost is a valid negative result.
Do not rescue it by widening the runtime or choosing a synthetic metric that omits startup,
analysis, transfer, cleanup or failed preparation.

## Explicit non-goals

Do not implement these without a new owner decision:

- Stratum's DataOp DSL, operator DAG, relational optimizer or general ML pipeline runtime;
- a general Python IR, whole-program CFG/DAG executor, second Python interpreter or
  planner-generated workflow graph;
- user-visible `Future`, proxy, callback, polling or `.result()` behavior;
- LLM-Tool Compiler-style generated fused tools;
- AWO/meta-tool trace-derived tool synthesis or policy replacement;
- AAFLOW's distributed dataflow runtime, generic AST batch pass or Arrow/Cylon stack;
- more scalar constant folding, scalar CSE, algebraic simplification or other cheap
  peepholes merely to increase pass count;
- generic Pandas/Polars rewrites inferred from method names without an exact adapter law;
- dynamic pass loading, automatic ordering, dependency graphs, fixed points or a generic
  pass manager;
- approximate/lower-fidelity operator substitution;
- shared mutable Python objects, module caches, iterators, open handles or interpreter
  state across Runs;
- early external writes, mutations or approvals; existing compensation plans do not make
  write speculation safe;
- generic rollback, post-effect replay or retry after an ambiguous external effect;
- page-ownership transfer or a page-composed object arena before copy/data-local and
  existing private-COW options are measured insufficient;
- Docker, paid cloud, manual CI triggers, deployment or release work.

## Non-negotiable semantic boundaries

1. **Python stays synchronous.** Source-level `tools.foo(a)` still returns a Python value
   or raises where synchronous source semantics permit. Internal split phase is not a new
   language feature.
2. **No observable Future.** A compiler token is Host-owned and must not become a Python
   value, local variable, truth value, attribute target or introspectable proxy. Initial
   passes reject source/code/frame/trace/namespace introspection that could expose derived
   code or hidden helpers.
3. **Python retains control flow.** Branches, loops and `try` activation happen in Python.
   The Host records only actual dynamic occurrences and their readiness; it does not run a
   duplicate Python CFG.
4. **Physical work is not a logical effect.** Submit/preparation/resource consumption is
   recorded separately from the original dynamic occurrence that may commit a logical
   capability call.
5. **Writes do not move early.** Initial split-phase candidates are pure computation or
   adapter-qualified immutable reads. External writes/mutations remain synchronous and in
   source order.
6. **Failure appears at an allowed observation point.** A prepared call may finish early,
   but its stored error is raised only where the original call/result would be observed.
   Materialization cannot cross code whose execution would differ if the call failed.
7. **No replay after possible effect.** Uncertain start state is ambiguity. Fallback to
   unchanged execution is allowed only while the Host proves no external effect started.
8. **Fresh Runs remain fresh.** Reuse may share immutable physical bytes or Host-side
   computation. It never reuses Python heap, globals, modules, iterators, handles or
   mutable Guest state.
9. **Passes describe semantics; the Host selects mechanics.** A pass may declare a call
   occurrence or value slot. It must not choose `memfd`, page addresses, Wazero module
   lifecycle or cache eviction.
10. **Adapter laws are mandatory.** Projection, reduction or producer reuse is admitted
    only for a named capability/version whose owner states and tests the equivalence,
    exception, ordering, freshness and input-revision law.
11. **Unsupported source is unchanged.** Aliasing, mutation, UDFs, dynamic dispatch,
    introspection, unknown effects or missing cost facts reject the optimization.
12. **Current product defaults remain unchanged.** New mechanisms stay Experimental and
    independently default-off; ordinary CLI/HTTP runs remain fresh and unscheduled.

## Starting facts to reverify from live source

Do not trust this list over current code.

- `runtime/passplugin` provides descriptor-validated static registration; it is not a
  dynamic plugin system.
- `runtime/sourcepatch` and Guest `source_pass.py` validate final-source-bound patches.
- `runtime/passpipeline` routes and records outcomes; it is not a transform executor.
- the current Guest `host_call` path is synchronous through `Broker.Call`; no Host-owned
  pending-call ABI currently permits overlap.
- `semantic_pre_dispatch` already separates qualified early physical read work from the
  later logical occurrence, but only for its narrow streaming-prefix contract.
- `prepared_pure_region` already inserts a hidden scalar materialization helper and
  `PreparedRegionTable` has unready/ready/consumed/discarded and single-use claim behavior.
- the current scalar materialization payload is small; prepared NumPy ingress copies into
  Guest memory and is not a general immutable-object transfer API.
- Linux Prepared Family uses sealed whole-memory images with private COW. It does not
  transfer ownership of independently composed object pages from producer Guest to Host.
- current single-flight/reuse, Broker receipts, Plan binding, workspace disposition and
  Prepared Family are possible lowering substrates. Their existence alone does not make
  a new pass legal or economical.

Before changing runtime code, run at least:

```bash
git status --short --branch
git log -5 --show-signature --format='%H %G? %s'
go test ./runtime/passplugin ./runtime/sourcepatch ./runtime/passpipeline \
  ./runtime/preparedregion ./runtime/capability ./runtime/engine/wazero -count=1
go test ./integration/e2e -count=1
go vet ./...
```

If Guest bootstrap or Wazero/ABI behavior changes, build and verify an exact Guest artifact
from the implementation commit before claiming E2E behavior. Do not reuse an older Guest
artifact as proof of new Guest code.

Phase 0 froze the first implementation in
[`host-scheduled-call-contract-v1.md`](../research/host-scheduled-call-contract-v1.md)
and
[`host-scheduled-call-preregistration-v1.json`](../evidence/host-scheduled-call-preregistration-v1.json).

## Desired future state

### Source/compiler

- A narrow pass can replace one admitted synchronous Host call with hidden
  `submit(decision)` and `materialize(decision)` helpers.
- No token is stored in an Agent-visible Python object; a Host decision/occurrence ID is
  sufficient for the first straight-line lane.
- The exact target Guest validates original and derived source before formal execution.
- Movement across statements uses conservative use/def, exception, mutation,
  introspection and effect barriers from existing target-Guest analysis.
- Pass registration remains static, bounded, independently switchable and default-off.

### Host scheduler and call lifecycle

- The Host owns a small pending-call table and ready queue rather than a general workflow
  engine.
- Initial lifecycle is explicit and finite, for example:

  ```text
  allocated -> submitted -> running -> ready | failed | cancelled
                                     -> consumed | discarded
  ```

- Every entry binds the original source occurrence, dynamic occurrence, capability and
  arguments, Plan/freshness/privacy/workspace context, deadline/budget and terminal fact.
- Independent qualified work may overlap under Host/provider budgets.
- If an earlier call fails, later prepared pure/read work can be discarded without
  inventing a later logical call.
- Ordinary Broker receipt, budget and effect accounting remains per original dynamic
  occurrence even when physical work is shared or prepared.

### Immutable value materialization

- A source pass can emit a bounded `ValueSlot` declaration for scalar, bytes/blob or one
  qualified immutable object without choosing the transfer backend.
- A Host materializer can choose inline scalar, bounded copy, existing qualified private
  COW, or data-local computation under the same consumer contract.
- A sealed immutable intermediate can have several fresh, separately tracked consumers.
- Producer/input revision, implementation identity, privacy partition and mutability are
  sufficient to prevent stale or cross-boundary reuse without adding duplicate audit
  layers that do not support correctness or the measured claim.
- Cleanup is local: unclaimed/failed/cancelled values are discarded; consumer Run and
  workspace outcomes remain independent.

### Workload-selected Stratum-derived passes

Pysolate does not reproduce Stratum. It may retain only these pattern classes after a
workload/cost gate:

1. **Capability projection/predicate pushdown** — fold a fixed read + static projection or
   predicate into one adapter-declared read that returns fewer bytes.
2. **Data-local reduction** — replace a fixed immutable read + supported scalar reduction
   with a named Host operation that returns only the reduced value.
3. **Immutable producer reuse** — lower one expensive deterministic producer to a
   `ValueSlot`/sealed object that several fresh Runs or agents may consume.

The first implementation must target one named, versioned capability family. It must not
match arbitrary dataframe syntax. If no existing real workload and exact adapter law
justify such a capability, record a no-go rather than adding a demonstration-only public
API.

### Evidence

- Semantic differential tests cover values, exceptions, branches, loops, zero iteration,
  cancellation, deadlines, earlier failure, mutation, introspection and unsupported
  source.
- Two ledgers show physical submit/run/ready/discard separately from logical
  occurrence/receipt/terminal outcome.
- Performance evidence includes analysis, scheduling, Host call, transfer,
  materialization, Guest startup and cleanup overhead.
- Shared-object evidence reports producer work, bytes copied/mapped, consumer count,
  dirty behavior where relevant and per-consumer isolation.
- Claims distinguish synthetic mechanism evidence, retained natural/representative
  workload evidence and negative/no-go results.

## Autonomous execution queue

### Phase 0 — Freeze the successor contract and workload gates

**Promise:** Do not build an async subsystem or data API before the observable semantics
and likely value are explicit.

- [x] Re-audit the live Broker/Guest host-call path, source-pass lowering,
  `PreparedRegionTable`, Prepared Family, single-flight and receipt/effect owners.
- [x] Write a runtime-owned split-phase design contract covering hidden helper shape,
  lifecycle, dynamic occurrence identity, two-ledger events, exception point,
  cancellation/deadline/orphan behavior and safe fallback.
- [x] Freeze adversarial controls for earlier exception, later prepared work, branch not
  taken, zero loop iterations, first loop failure, timeout, cancellation, introspection,
  mutable arguments, writes and unknown effects.
- [x] Inventory representative current workloads/capabilities for independent high-latency
  calls, large immutable reads, projection/reduction and repeated deterministic producers.
  Frequency is a selection input, not permission to infer semantics from traces.
- [x] Choose the smallest first call capability and, separately, at most one first
  data-operation family. Record no-go if no concrete family has an exact adapter law and
  plausible end-to-end savings.
- [x] Preregister matched baselines, cost columns and retain/reject criteria. Include
  startup, analysis, transfer and cleanup.

**Gate P0:** The contract can explain the result/error/logical-effect trace of every
positive and adversarial fixture without a Python Future, full Python DAG or early write.
At least one split-phase call fixture has plausible latency value. Data-pass work proceeds
only if one named capability family passes the workload/law gate.

### Phase 1 — Adjacent hidden submit/materialize

**Promise:** Establish the Host ABI and lifecycle without changing source ordering or
claiming concurrency benefit.

- [x] Add RED tests for one straight-line qualified pure/read call lowered to adjacent
  hidden submit and materialize operations.
- [x] Implement a small Host-owned pending-call table with bounded entries, deadlines,
  explicit terminal states and deterministic close/discard.
- [x] Add Guest/Host helpers keyed by a source-bound decision and dynamic occurrence;
  no Agent-visible token or public Python API.
- [ ] Keep submit and materialize adjacent. Prove value, exception, Broker budget,
  receipt, Plan/freshness/privacy/workspace and cleanup equivalence against the original
  synchronous call.
- [x] Reject writes, mutable argument capture, introspection, loops and unsupported control
  in v1 rather than generalizing early.
- [ ] Prove all-off and pre-effect failure select the unchanged path; no fallback replays a
  possibly started external effect.

**Gate P1:** Adjacent lowering is semantically equivalent and independently removable.
The pending-call mechanism has no user-visible Future and no scheduler/IR framework beyond
what later overlap requires.

### Phase 2 — Independent-call overlap with source-order observation

**Promise:** Overlap physical latency while preserving the original dynamic Python
occurrences and exception-visible order.

- [ ] Add RED fixtures for two or more independent qualified calls followed by source-order
  uses, including first-call failure and later prepared success/failure.
- [ ] Hoist a submit only after its arguments are available. Normally the Python path must
  already be active; a later qualified pure/immutable read may be staged before an earlier
  materialization only under the explicit physical-only contract, then discarded if
  Python never reaches its logical occurrence.
- [ ] Sink materialization only across a proven local region with no mutation, external
  effect, exception-timing change, namespace/frame/source introspection or observable use.
- [ ] Add a bounded Host ready queue and concurrency/provider budget. Do not add a planner
  or static workflow DAG.
- [ ] Store early failures and raise them at the allowed materialization point. Cancel or
  discard later physical work when Python would not reach its logical occurrence.
- [ ] Record one physical timeline and one logical occurrence timeline; prove no invented,
  dropped or reordered logical calls.
- [ ] Benchmark matched one-call, two-call and N-call cases, including scheduler overhead
  when latency is too small to repay it.

**Gate P2:** At least one preregistered latency-dominated case improves end-to-end time and
all semantic controls pass. If overlap is economically negative, retain the correctness
result as Experimental/off and do not widen the scheduler.

### Phase 3 — Dynamic Python activation, cancellation and composition

**Promise:** Let Python remain the controller while the Host handles actual pending events.

- [ ] Extend only the proven forms needed by real fixtures: branch-local calls, bounded
  loops or direct dependent calls. Reject forms not required by the selected workload.
- [ ] Allocate dynamic occurrence identities at runtime; a static source occurrence alone
  must not merge loop iterations.
- [ ] Preserve zero-iteration behavior, first failing iteration, `try`/`except` timing,
  cancellation and deadline propagation.
- [ ] Admit a dependent call only when its arguments exist. Do not represent unresolved
  Python expressions in a Host dataflow DSL.
- [ ] Reconcile any overlap with existing `semantic_pre_dispatch`: reuse the two-ledger
  occurrence/claim model where it fits, but do not force prefix speculation and final
  split-phase calls through one callback.
- [ ] Add deterministic teardown evidence for ready, failed, cancelled, late and unclaimed
  entries.

**Gate P3:** The Host event graph contains only actual dynamic call/value events and does
not duplicate Python control flow. If loops/dependencies require a general Python CFG or
proxy values, leave them rejected and continue with independent admitted work.

### Phase 4 — Generalize the scalar materialization hole to bounded value slots

**Promise:** Decouple pass semantics from value-transfer mechanics before attempting
cross-Run object reuse.

- [x] Re-audit whether `PreparedRegionTable` can be minimally generalized or should remain
  behind a small adapter; do not refactor for naming alone.
- [x] Define `ValueSlot`/consumer declarations with only fields needed for correctness:
  source/dynamic occurrence, producer/input identity, value kind/size, immutability,
  privacy, claim policy and bounds.
- [ ] Prove scalar and bounded immutable bytes/blob materialization through fresh Guests.
- [x] Put inline/copy/data-local/existing private-COW choice behind a Host materializer;
  passes cannot select OS/Wazero mechanics.
- [x] Preserve single-use and multi-consumer lifecycle explicitly. A multi-consumer object
  does not merge logical Run/call outcomes.
- [ ] Measure transfer, allocation and cleanup costs before considering a page-composed
  arena or producer-page ownership transfer.

**Gate P4:** Two materially different value types use the same small semantic slot
contract, consumer isolation holds, and the Host can choose at least two existing/simple
materialization strategies without exposing backend details to the pass.

### Phase 5 — One high-frequency projection or data-local reduction pass

**Promise:** Inherit a useful Stratum pattern as a narrow ordinary-Python pass, not a
DataOp optimizer.

- [x] Select exactly one named/versioned immutable data capability from P0 evidence.
- [x] Have the adapter owner declare and test one equivalence law, including result schema,
  ordering, encoding/dtype/null behavior, exceptions, revision/freshness and bounds.
- [x] Add RED pass-off/pass-on fixtures plus alias, extra-use, mutation, UDF, dynamic
  predicate/column, exception and introspection controls.
- [x] Implement the smallest target-Guest/source patch for either static
  projection/predicate pushdown or one data-local scalar reduction.
- [x] Keep arbitrary Pandas/Polars operations opaque. No generic operator graph or
  framework appears.
- [ ] Measure physical bytes read/returned, Host-to-Guest bytes, Guest peak memory and
  end-to-end latency against unchanged source.
- [ ] Reject or leave default-off if the intended physical cost does not improve after all
  overhead.

**Gate P5:** One fixed high-frequency pattern either has retained end-to-end benefit under
an exact adapter law or is closed as a truthful no-go. Completion does not require adding
a second pattern.

### Phase 6 — Cross-Run and cross-agent immutable intermediate reuse

**Promise:** Reuse physical immutable values without reusing Python state or merging
logical consumer outcomes.

- [x] Define one expensive deterministic producer family with explicit input revision,
  implementation identity, privacy partition, size bound and mutation rejection.
- [ ] Add RED tests for two fresh Runs/agents requesting the same producer, near misses,
  changed revision/implementation/privacy, failed producer, cancelled consumer, consumer
  mutation and independent workspace/result disposition.
- [ ] Materialize one sealed Host value once and provide fresh consumers through bounded
  copy, existing private COW or data-local computation selected by the Host.
- [x] Preserve separate Run identities, Plans, Broker budgets/receipts and terminal facts.
  Sharing physical bytes must not invent one shared logical Python call.
- [ ] Add a small cost policy based on measured producer cost, value size, consumer count,
  transfer cost and, for COW, dirty behavior. Do not build a general cache service.
- [ ] Prove deterministic cleanup when producer or any consumer fails/cancels and when a
  value is never claimed.
- [ ] Compare recompute, copy, COW/data-local and shared-value treatments on the same fresh
  Run cohort.

**Gate P6:** At least one preregistered expensive immutable producer shows positive
end-to-end cohort value while every consumer remains fresh and isolated. If reuse is
negative, retain the object/materializer correctness seam only if another admitted phase
needs it; otherwise close it as Experimental/no-go.

### Phase 7 — Composition and cost-guided selection

**Promise:** Compose only mechanisms that survived independently; do not create a general
optimizer manager.

- [ ] Test split-phase calls with the retained data pass and/or shared intermediate without
  changing logical occurrence and exception behavior.
- [ ] Add static, explicit ordering only if two retained passes actually conflict or depend
  on each other. Otherwise keep independent selection.
- [ ] Use observable workload/cost facts to reject cheap or low-incidence transformations
  before provisioning scratch Guests or large materializers.
- [ ] Test all-off, each-pass-only and admitted composed modes against the same baseline.
- [ ] Confirm no default CLI/HTTP behavior changes and each mechanism can be disabled or
  deleted independently.

**Gate P7:** Composition produces additional measured value or simpler execution without
new semantic machinery. If not, keep the surviving mechanisms independent.

### Phase 8 — Evaluation, independent review and closeout

**Promise:** Finish with truthful mechanism and performance claims, including negative
results.

- [ ] Freeze exact source, Guest artifact, configs, workload/capability versions and
  environment metadata for retained experiments.
- [ ] Run deterministic semantic/adversarial matrices and matched end-to-end measurements.
- [ ] Separate physical work, logical effects and published consumer outcomes in evidence.
- [ ] Run focused race tests, `go test ./...`, `go vet ./...`, exact-Guest E2E and
  platform-specific private-COW gates when touched.
- [ ] Request independent post-fix review of call lifecycle, exception timing, logical
  effect accounting, value isolation, adapter law and fallback/replay boundaries.
- [ ] Resolve all Blocking/High/Medium findings and rerun affected gates.
- [ ] Update Current/Experimental/Proposed docs and related-work wording. Do not promote
  this successor into the existing paper claim source of truth without separate evidence
  review.
- [ ] Record retained, rejected and deferred candidates; close the roadmap with no
  unchecked executable item.

**Gate P8:** Exact implementation/evidence targets pass their declared gates, final review
has no open Blocking/High/Medium issue, product defaults remain unchanged, and all claims
match retained end-to-end evidence.

## Global gates

Run focused tests after every implementation slice. Before each behavior commit, run the
smallest relevant package/E2E/race set plus:

```bash
git diff --check
go test ./... -count=1
go vet ./...
```

Additionally:

- Guest/bootstrap or ABI changes require a newly built, verified exact Guest artifact and
  exact-Guest E2E tests from the implementation source commit.
- concurrency/table/scheduler changes require focused `go test -race` coverage.
- Linux private-COW changes require the existing bounded Linux workstation gate; do not
  substitute macOS copy-path evidence.
- documentation-only roadmap slices may use readback, `git diff --check` and Git status/diff
  review unless a canonical docs validator covers the changed file.
- do not manually trigger GitHub Actions. Query CI once after push only when a remote gate
  is materially relevant; local evidence is primary.

## Per-slice execution protocol

For every executable slice:

1. inspect live code, tests, Git status and the current execution pointer;
2. write a failing test, or state why a docs/evidence-only slice cannot use RED;
3. implement the smallest mechanism that satisfies the admitted contract;
4. run the focused gate and inspect real output;
5. update this roadmap checkbox, completion log and next pointer;
6. run the relevant global gates;
7. make a signed commit and push unless a named permission/safety boundary forbids it;
8. verify signature, `main == origin/main` and clean status;
9. immediately continue to the next independent admitted slice.

Do not leave an intentionally failing test across unrelated slices. Do not modify thesis,
slides or the companion explanation repository unless the active slice explicitly requires
claim synchronization and the owner has approved that scope.

## Roadmap tracking rules

- This file becomes the active runtime source of truth only after the `/goal` starts.
- Add a `Current execution pointer` below before the first implementation edit.
- Change `[ ]` to `[x]` only after real gate evidence.
- Mark a rejected candidate `[-]` with its measured/legal reason; do not erase it.
- Append a concise completion log entry with date, slice, focused/global gates and signed
  commit identity.
- Trust live Git and test output over the preparation baseline and historical prose.
- A failed Stratum-derived candidate is not permission to stop while split-phase/value
  work or another independently admitted phase remains.
- A successful phase, signed push or negative result is not a stop condition.

## Stop conditions

Stop only when one of these is true:

1. all phases are closed and no unchecked executable work remains;
2. the next required step would expose Future/proxy semantics to Python, build a general
   Python/DataOp IR, share mutable interpreter state, or replay a possibly started effect;
3. no named capability/workload can support an exact adapter law or plausible value, after
   one bounded alternative, and no independent admitted phase remains;
4. a required exact Guest/Linux resource is unavailable after one bounded alternative;
5. a product, privacy, credential, licensing, deployment or architecture decision requires
   Yuzhe;
6. repeated gate failures reveal an unresolved correctness conflict that cannot be fixed
   without broad redesign.

Do not stop because one pass is rejected, one benchmark is negative, one optional platform
path is unavailable, or one phase ends at a clean signed checkpoint. Record the result and
continue with independent admitted work.

If blocked, report the exact blocker, modified files, tests/gates run, live Git status and
the safest decision or alternative. Never weaken an acceptance condition to manufacture
completion.

## Current execution pointer

`Phase 1: the bounded Host table, default-off mechanism, exact-Guest source transform and
Guest/Host helper code pass local unit/package gates. Build a new exact Guest artifact from
the implementation commit, then run adjacent/overlap/failure E2E before closing P1/P2.`

## Completion log

- 2026-08-24: Successor roadmap prepared from live clean baseline
  `37ab006e06c6d107b364d69fcb14f01328aa99d6`. Direction approved; implementation not
  started.
- 2026-08-24: Phase 0 closed. Live owners, the fixed `sources.read(path) -> body` call
  adapter, fixed NumPy data-local reduction candidate, adversarial matrix, cost columns and
  retain/reject rules are frozen in the linked contract and preregistration. Baseline
  runtime packages, exact-Guest E2E and vet passed before implementation.
- 2026-08-24: Phase 1 local RED/GREEN checkpoint. Added a bounded Run-private
  `SplitPhaseTable`, call-ID-targeted Broker join, default-off mechanism, static
  `split_phase_sources_read` pass and hidden Guest/Host helper ABI. Capability table,
  runtime/source-pass/engine packages and 178 Guest unit tests pass. Exact rebuilt-Guest
  E2E remains open.

## Short `/goal`

```text
/goal Read docs/plans/2026-08-24-host-scheduled-python-reuse-autonomous-megagoal.md fully and execute it from live Git state in /Users/yuzhe/projects/agent-python-runtime. Preserve synchronous ordinary Python, fresh Runs, Host-owned pending work and separate physical/logical accounting. Implement only workload-selected high-frequency passes; do not build a Future API, full Python/DataOp IR, Stratum replica, generic batch/meta-tool system, or share mutable Python state. Continue through verified slices, signed commits and pushes until complete or genuinely blocked. Do not use Docker, paid cloud or manually trigger CI.
```
