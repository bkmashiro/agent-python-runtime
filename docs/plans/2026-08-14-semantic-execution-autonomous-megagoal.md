# Semantic Execution Experimental Autonomous Mega-Goal

> **For Hermes:** This is the active long-running `/goal` handoff. Read this file
> fully, inspect live repository state, and execute multiple independently verified
> slices. A green slice, signed checkpoint, push, or context boundary is not a
> stopping condition.

**Status:** Active; Tracks 0-D implemented and verified, Track E closeout in progress
**Date:** 2026-08-14
**Owner:** Yuzhe
**Repository:** `~/projects/agent-python-runtime`
**Baseline:** `cd20d78e7dda1f5b01b2df40f662e34f640f0160`
**Predecessor:** [`../megagoal-full-composable-agent-runtime.md`](../megagoal-full-composable-agent-runtime.md)
**Long-term inventory:** [`../proof-first-authority-roadmap.md`](../proof-first-authority-roadmap.md)

**Goal:** Prove, with the smallest truthful implementation, that Pysolate can use
its target-Guest Python AST, frozen capability semantics, and execution-profile
identity to build a conservative semantic execution plan and apply one real reuse
optimization across Agents, while independently testing whether a live Guest
blocked on long I/O can retain its continuation but reduce physical residency.

**Architecture:** Keep ordinary fresh execution as the semantic control. Add a
small target-Guest AST analyzer and a thin, versioned semantic plan; do not build a
traditional compiler IR. Reuse the existing Agent Function identity, single-flight,
and local bounded store for an AST-qualified whole-function/whole-Run reuse pass.
Treat continuation-preserving cold residency as a separate Linux/COW lifecycle
experiment, not as workflow replay or cache state.

**Tech stack:** CPython `ast`/`symtable` in the existing WASI Guest; Go Host
contracts and validators; existing typed capability Plan, Agent Functions,
Wazero experimental allocator, Linux `madvise`, deterministic Go/Python tests, and
bounded Linux evidence runs.

---

## User intent and value filter

Yuzhe wants an Experimental mechanism that validates the paper hypothesis without
turning Pysolate into a general Python compiler, workflow engine, or distributed
cache. The valuable claim is:

> Harness identity plus sandbox-enforced typed effects lets Pysolate recover a
> conservative semantic plan from Agent-generated Python and safely coalesce or
> reuse qualified computation.

Prefer, in order:

1. a real end-to-end mechanism over broad optimizer coverage;
2. fail-closed effect legality and exact identity over hit rate;
3. whole-function/coarse regions over arbitrary statement subgraphs;
4. reuse of existing capability/cache/streaming/COW substrates over parallel
   abstractions;
5. measured optimizer economics and negative evidence over assumed speedups;
6. small standard-library implementations and independently removable toggles.

The second hypothesis is independent: a Guest waiting at one controlled Host I/O
boundary may remain the same execution while Linux reclaims or pages out cold
linear-memory pages. This is useful only if measured RSS/PSS reduction outweighs
pageout/refault cost.

## Current verified implementation

At the baseline:

- every physical execution is fresh/single-use and authority is frozen and bound to
  artifact/profile/Run identity;
- Tool `Spec` already carries Host-owned `EffectClass`, `Playback`, `ReadOnly`,
  `Idempotent`, and `SpeculativeSafe` semantics;
- the target Guest already uses `ast.parse`, `ast.NodeTransformer`, and `compile`
  for authoritative source validation and qualified eager-read rewriting;
- streaming staged reads preserve source occurrence, arguments, capability spec,
  plan/grant, freshness, privacy, and terminal ownership;
- explicit Agent Functions already have canonical invocation identity, bounded
  project-private retention, corruption/eviction handling, and retention-independent
  single-flight;
- real Fresh Guest Agent Functions bind source/input/output schema, artifact,
  execution profile, deterministic profile, import closure, no workspace, no
  Broker, and runtime Host-call observation, but currently reject completed-result
  retention;
- explicit single-wait fresh re-evaluation destroys the Guest and reconstructs
  from typed state; it does not preserve Python continuation;
- Experimental Linux COW uses a sealed sparse `memfd`, bounded growable
  `MAP_PRIVATE` mappings, and single-use terminal teardown;
- no AST-wide effect summary, semantic region plan, automatic Guest retention,
  continuation-preserving parked/cold slot, or adaptive optimizer exists.

These facts are Current/Implemented Experimental only at the cited baseline.
Everything introduced by this file remains Proposed until code and evidence land.

## Desired future state

When this Megagoal closes:

1. Pysolate has a versioned, bounded semantic-analysis result produced by the exact
   target Guest parser and bound to source, artifact/profile, import closure, and
   capability Plan identity.
2. Direct typed capability sites are colored from the sealed Host Plan; supported
   Python functions receive conservative transitive summaries; recursive call
   components are collapsed; unresolved dynamic behavior is an explicit barrier.
3. A thin semantic plan represents only coarse whole-function/whole-Run regions,
   source/AST identity, effect summary, dependencies, eligibility, and rejection
   reasons. It is not SSA and does not attempt arbitrary Python equivalence.
4. One `ReusePass` admits an AST-qualified whole-function/whole-Run invocation to
   existing single-flight and bounded worker-local completed-result retention.
5. Concurrent identical invocations perform one physical Guest computation; a
   later exact invocation may return a retained immutable result; identity or
   effect changes miss/reject rather than guess.
6. Optimizer-off runs the original ordinary fresh path with equal semantic output,
   authority, effect ordering, and terminal disposition.
7. A separate Linux spike demonstrates or falsifies continuation-preserving
   parked-hot/parked-cold/pageout behavior at one Host I/O suspension point, with
   same-slot resume and honest RSS/PSS/swap/refault measurements.
8. The long-term roadmap records later optimizer families and their activation
   evidence without implementing them prematurely.

## Non-negotiable boundaries

1. **No arbitrary-Python purity claim.** Analysis is sound only for the declared
   Experimental source/profile subset. Unsupported constructs remain opaque.
2. **One canonical effect source.** Capability semantics come from the sealed Host
   `capability.Plan`; do not create competing Tool effect declarations in Guest or
   optimizer packages.
3. **WASI is conservative.** Known qualified builtins/imports may receive bounded
   summaries. Unknown stdlib/native/WASI behavior is a barrier, not assumed pure.
4. **No authority widening.** Analyzer output and cache state cannot grant Tools,
   workspace access, imports, network, clock/random, or any other authority.
5. **No skipped writes/live effects.** External writes, live reads, unknown calls,
   clock/random, dynamic imports, and undeclared filesystem dependencies make the
   first reuse profile ineligible.
6. **No post-effect replay.** Analysis/optimization failure may select ordinary
   execution before start. Once execution or an effect starts, preserve its real
   terminal/ambiguity disposition; never transparently rerun to manufacture
   success.
7. **Runtime verification is a publication gate.** If the actual Guest attempts a
   forbidden Host effect, no result enters single-flight retention or cache.
8. **Exact identity first.** Initial keys bind exact source/AST schema, canonical
   inputs/output schema, dependencies, artifact/profile/import closure,
   deterministic settings, policy epoch, project, and privacy partition.
9. **No cross-partition existence leak.** Cache and in-flight identity remain
   project/private; no global/distributed cache.
10. **No semantic exception caching initially.** Errors, traps, cancellation,
    timeout, OOM, analyzer disagreement, and oversized output are not retained.
11. **Parked is not pooled.** A parked/cold execution keeps the same slot identity,
    private mapping, Host-call context, and authority; it never returns to a ready
    pool or serves another request.
12. **No destructive page discard on resumable state.** Do not use
    `MADV_DONTNEED` on private dirty pages that must resume. Test `MADV_COLD` and
    `MADV_PAGEOUT`; terminal slots still `munmap`.
13. **No heavyweight compiler stack.** Do not add LLVM, MLIR, a Python parser in
    Go, a general SSA, distributed scheduler, or broad framework.
14. **Experimental/off by default.** Public/default HTTP behavior remains ordinary
    fresh execution unless an embedding profile explicitly selects the mechanism.
15. **Truthful evidence.** Separate analyzer coverage, semantic correctness,
    resource observations, and performance claims. A correctness run is not a
    speedup benchmark.
16. **No production/paid remote operations.** Local and approved bounded Linux
    research hosts are allowed; do not deploy, publish releases, expose secrets,
    or manually trigger metered GitHub Actions.

## Explicitly deferred optimizer roadmap

Record and preserve these families, but do not implement them during this goal
unless a completed earlier slice proves they are required for the agreed minimum:

- statement-level maximal straight-line region extraction;
- full control-flow/data-flow/exception graphs;
- arbitrary object/heap live-in/live-out materialization;
- AST alpha-renaming or semantic-equivalence normalization;
- adaptive region splitting, fusion, specialization, speculation, or migration;
- automatic reordering of ordinary sequential Tool calls;
- broader read scheduling beyond existing Host-qualified eager semantics;
- distributed/cross-node/cross-project/cross-user result caches;
- arbitrary module/native-extension purity summaries;
- Python frame/heap checkpoint files or cross-process continuation restoration;
- general multi-wait workflow scheduling;
- optimizer-driven external-write retry, commit, or compensation.

Future experiments should progress only from exact source to position-stripped AST,
then restricted normalization, measuring incremental reuse at each step.

## Semantic analysis contract v0

The analyzer may use `ast` and `symtable` inside the exact target Guest. It should
emit a bounded canonical report, not executable authority. The initial effect
summary is intentionally small:

```text
MayPublish
MayObserveLive
MaySuspend
MayBeUnknown
```

Eligibility is derived, not stored as a user assertion:

```text
reusable =
  !MayPublish
  && !MayObserveLive
  && !MaySuspend
  && !MayBeUnknown
  && every dependency is version-bound

coalescible = reusable && live inputs/outputs are canonicalizable
```

Initial dependencies are limited to existing stable identities:

- canonical structured inputs;
- immutable root/captured-observation digests when explicitly supported;
- exact source/AST-analysis schema;
- artifact, execution profile, deterministic profile, and import closure;
- output schema, project/privacy partition, and policy epoch;
- sealed capability Plan identity.

Direct projected Tool names may be resolved from the sealed Plan. Statically
resolved local function calls receive summaries by fixed point. Recursive/mutually
recursive functions are collapsed into strongly connected components. Dynamic
call targets, rebinding of projected Tool names, `eval`, `exec`, dynamic import,
unsupported decorators/descriptors, and unresolved ambient behavior set
`MayBeUnknown` or reject analysis.

The AST is a tree; the call graph may contain cycles; the first semantic plan may
be a DAG only after recursive/loop compounds are collapsed into opaque regions.
Do not describe the raw AST or arbitrary Python call graph as a DAG.

## Autonomous execution queue

### Track 0 — Truth reset and contracts

**Promise:** Current, Proposed, and Deferred claims remain unambiguous.

- [x] Re-read live source and history; reconcile this baseline if the branch moved.
- [x] Add typed feature/evidence names only after identifying the smallest existing
  configuration seam; default all new mechanisms off.
- [x] Freeze canonical analyzer and semantic-plan schemas, bounds, identities, and
  rejection reasons before implementation.
- [x] Add tests that reject unknown fields, malformed identities, oversized plans,
  duplicate region/function IDs, invalid source spans, and eligibility assertions
  unsupported by effect/dependency summaries.
- [x] Update mechanism matrix and roadmap status without promoting Proposed work.

**Likely files:** `runtime/mechanisms.go`, `runtime/composable/evidence.go`, a small
new semantic contract package if justified, Guest bootstrap schemas/tests, docs.

**Gate:** focused schema/feature tests plus `git diff --check`.

### Track A — Continuation-preserving cold-I/O spike

**Promise:** Determine whether one live COW Guest can wait on Host I/O, reduce
working-set residency, and resume the same Python continuation without replay.

- [x] Establish Linux capability/host facts: page size, swap/zswap availability,
  `MADV_COLD`, `MADV_PAGEOUT`, and observable `/proc` counters.
- [x] Add one bounded Host-call suspension fixture that writes a known private
  linear-memory state, copies Host-call arguments out of Guest memory, enters a
  verified quiescent barrier, waits, and resumes with the same slot identity.
- [x] Model `running → parked_hot → parked_cold/pageout → resuming → terminal` with
  one owner/lease; reject park after close, concurrent resume, pool publication,
  and new-request acquisition.
- [x] Add Linux-only memory advice behind capability detection. Never expose raw
  mapping slices across the suspension boundary.
- [x] Compare control, `MADV_COLD`, and `MADV_PAGEOUT`; record RSS, PSS,
  `Shared_Clean`, `Private_Dirty`, swap, faults, park cost, and resume latency.
- [x] Verify Python object/locals/global state and execution position survive;
  cancellation/timeout/OOM remain terminal; final mapping residue is zero; a
  subsequent request receives a fresh clean slot.
- [x] Run focused Linux tests and one bounded Zao outcome/resource observation.
- [x] Classify result as useful, kernel-dependent/neutral, or negative. Do not
  productize a scheduler or swap policy in this goal.

**Likely files:** `runtime/engine/wazero/cow_memory_linux.go`, Linux-specific
companion lifecycle files/tests, one bounded capability/Guest fixture, and a new
`docs/evidence/` JSON record.

**Decision gate:** A negative or host-dependent result does not block Tracks B-D.
Record it and stop cold-residency productization. A positive result still requires
future joint approval before adding general pressure scheduling.

### Track B — Target-Guest AST coloring and function summaries

**Promise:** The exact Guest parser produces a conservative, inspectable map of
known effects and unknown barriers without changing execution.

- [x] Write RED Guest tests for direct projected Tool calls, local pure/local
  mutation, live reads/writes, aliases/rebinding, local function calls,
  recursion/mutual recursion, dynamic calls/imports, clock/random, and compound
  control flow.
- [x] Implement a small analyzer module using the target Guest's built-in `ast`;
  reuse the sealed Plan projection and existing source/import contract.
- [x] Bind every report to source digest, analyzer schema, Guest artifact/profile,
  import closure, and capability Plan digest.
- [x] Propagate summaries across statically resolved local calls by SCC/fixed point;
  unsupported or unresolved behavior becomes `MayBeUnknown`.
- [x] Produce stable source spans, direct-call evidence, function summary,
  dependencies, and bounded rejection reasons. Never include private bodies in
  Host-facing evidence.
- [x] Add deterministic canonicalization tests across equivalent parse runs and
  mutations proving identity changes when source/profile/Plan changes.
- [x] Keep analyzer-only mode behaviorally inert and prove original source still
  executes unchanged when optimization is off.

**Likely files:** a focused module under
`guest/bootstrap/agent_runtime/`, `guest/tests/`, generated/Host ABI bindings only
if needed, and a small Host validator rather than analysis duplicated in Go.

**Gate:** Guest unit tests, relevant Go contract tests, real Guest rebuild when ABI
or packaged Guest behavior changes.

### Track C — Thin semantic plan and coarse regions

**Promise:** Analysis becomes one bounded optimizer input without becoming a
traditional compiler IR.

- [x] Define one root whole-Run region ID, source/AST identity, direct
  dependencies, effect summary, canonical input/output boundary flags, derived
  eligibility, and exact rejection reasons; v0 has no child control parent.
- [x] Treat loops, `try`, async/generator behavior, unresolved calls, and complex
  object boundaries as opaque in v0.
- [x] Form only coarse maximal whole-function/whole-Run candidates. Do not enumerate
  arbitrary subgraphs or split statements.
- [x] Validate that every region belongs to exactly one source/analysis identity
  and cannot claim weaker effects than its function/SCC summary.
- [x] Add a deterministic analyzer/report-only CLI or test surface suitable for
  offline trajectory census without executing optimizations.
- [x] Measure exact-source and position-stripped-AST candidate frequency,
  concurrency overlap, duration, input/output size, and unknown-barrier rate on a
  bounded private local corpus when available; do not publish private source.

**Gate:** schema/property tests, deterministic snapshots, no-behavior-change real
Guest control.

### Track D — AST-qualified whole-function `ReusePass`

**Promise:** One semantic-plan pass eliminates real duplicate Guest computation
without suppressing or fabricating effects.

- [x] Write RED integration tests where two sibling Agents submit the same
  AST-qualified whole-function/whole-Run invocation and a third later repeats it.
- [x] Convert only an eligible semantic region into the existing
  `agentfunction.Invocation`; bind semantic-plan/analyzer identity in the canonical
  key without weakening existing fields.
- [x] Route concurrent exact invocations through the existing independent
  single-flight: one physical Fresh Guest leader, copied immutable follower result.
- [x] Enable bounded completed-result retention for the real Fresh Guest path only
  after static eligibility and runtime effect-probe publication checks pass.
- [x] Prove the later exact invocation is a local retained hit and reports no
  fabricated physical execution/effect.
- [x] Prove source/AST, input, output schema, artifact/profile, import closure,
  deterministic settings, dependency root, Plan, policy epoch, project, or privacy
  changes miss/reject independently.
- [x] Prove Host call, live read/write, clock/random, dynamic import, unknown call,
  noncanonical output, error, trap, cancellation, timeout, OOM, corruption, and
  oversized result never publish retained state.
- [x] Keep `single-flight=off`, `retention=off`, and optimizer-off controls
  independent and semantically equal.
- [x] Record lookup, analysis, serialization/materialization, compute, waiter,
  hit/miss/write/eviction, and physical-compute counters; apply no adaptive policy
  beyond a documented bounded test threshold.

**Likely files:** `runtime/agentfunction/guest.go`, cache/single-flight tests, the
semantic-plan adapter, `integration/e2e/composable_test.go` or a focused semantic
integration test, and mechanism/evidence records.

**Gate:** focused Guest/Go/integration tests, race tests on stateful packages, and a
real Guest run for the exact selected profile.

### Track E — Independent post-fix review, evidence, and roadmap closeout

**Promise:** The paper claim is bounded by real evidence and future optimizer work
is discoverable without becoming current scope.

- [ ] Run an independent read-only security/correctness review of analyzer
  soundness boundaries, identity binding, cache publication, lifecycle races, and
  parked-slot teardown; close P0/P1 findings with tests.
- [ ] Run all global gates and Linux-specific gates supported by changed code.
- [ ] Produce structured Experimental evidence for analyzer coverage, exact
  coalescing/retention behavior, semantic parity, and cold-I/O measurements.
- [ ] Update README, architecture/threat model, mechanism matrix, wait trade-offs,
  Agent Functions, and long-term roadmap with Current/Observed/Proposed/Deferred
  labels.
- [ ] Preserve the deferred optimizer family list and activation evidence. Do not
  silently start statement-region extraction, adaptive fusion, or distributed
  caching.
- [ ] Verify every final commit signature, remote branch SHA, clean worktree, and
  no credential/private-body leakage.

## Global gates

Every code slice requires focused RED/GREEN evidence. Before each behavior commit,
run the relevant subset and before closeout run:

```text
go test ./... -count=1
go test -race ./runtime/... ./cmd/... -count=1
go vet ./...
python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s tests -p 'test_*.py'
git diff --check
```

Real Guest behavior changes require a rebuilt Linux x86_64 artifact and the
relevant real-Guest acceptance. Linux COW/pageout claims require a real Linux run;
macOS compile/static review is not evidence. Prefer local and approved bounded
Linux validation over manually triggering GitHub Actions.

Performance/resource claims require:

- mechanism-off control;
- repeated bounded trials when claiming a difference;
- structured machine-readable output;
- separate correctness and performance/resource labels;
- RSS/PSS/private/shared/swap distinctions rather than aggregate virtual size;
- artifact/profile/environment identity and explicit limitations.

## Per-slice execution discipline

For every executable slice:

1. inspect live source/status and nearest tests;
2. add a RED test or document why a measurement-only spike cannot use one;
3. implement the smallest mechanism preserving existing contracts;
4. run focused gates and inspect actual output;
5. update this roadmap checkbox, current pointer, and completion log;
6. run proportional global gates;
7. make a signed commit and push unless a real remote blocker exists;
8. verify signature, remote SHA, and worktree status;
9. continue immediately to the next safe executable slice.

Use the main controller for shared architecture and integration. Bounded independent
workers may perform read-only review or one file-disjoint evidence question, but
the controller owns design, diffs, tests, signatures, push, and final claims.

## Roadmap tracking

**Current execution pointer:** Track E — run independent closure review, global and
real-platform gates, then close the evidence and roadmap without widening scope.

After each slice:

- change only gate-backed `[ ]` items to `[x]`;
- append date, slice, focused/global gates, and signed commit to the log;
- record negative findings and deferred decisions instead of deleting them;
- update the current pointer to the next concrete executable item;
- trust live Git and current source over this baseline narrative;
- do not promote an Experimental mechanism to default without a separate decision.

### Completion log

- 2026-08-14: Roadmap approved and materialized against
  `cd20d78e7dda1f5b01b2df40f662e34f640f0160`; implementation intentionally
  unstarted.
- 2026-08-14: Track 0 complete. Added default-off `semantic_analysis`,
  `semantic_reuse`, and `cold_io_continuation` mechanism identities with explicit
  dependencies and bumped mechanism evidence to v2. Added strict bounded
  `pysolate.semantic-analysis.v0` and `pysolate.semantic-plan.v0` Host contracts,
  canonical identities, coarse-region validation, derived reuse eligibility, and
  malformed/unknown/oversized/duplicate/weaker-effect negative tests. Focused
  runtime tests, race tests, `go vet ./runtime/...`, and `git diff --check` pass.
  Current pointer is Track A.
- 2026-08-14: Track 0 was committed and pushed as `c06f09e`. Track A added
  Host-owned bounded cold-I/O policy, same-slot wait state, and body-free evidence.
  Linux COW Host calls now copy request bytes before asynchronous waiting, advise
  only the live logical range, resume before response write, and retain terminal
  one-shot teardown. A synthetic Wazero continuation preserved dirty state and
  instruction position; cancellation terminated cleanly and a fresh mapping was
  zero-clean. Zao control/cold/pageout observations over 96 MiB private dirty state
  found no immediate RSS reduction for `MADV_COLD`; `MADV_PAGEOUT` moved all
  98,304 KiB to swap, restored it intact, and raised full refault from about 1 ms
  to 16.38 ms. This is positive but kernel/swap-dependent Experimental evidence,
  not approval for a scheduler. The official CPython/WASI Guest then passed the
  same-slot check with a 200,000,000-byte allocation, preserved Python object and
  global identity across one capability wait, recorded successful cold/pageout
  advice, and proved the following slot clean. Independent review found that a
  context-ignoring handler could otherwise pin the slot after cancellation; the
  wait now releases the copied-argument slot on `ctx.Done()`, discards any late
  result, and has a bounded regression test. The official Guest also returned a
  structured `MemoryError` above its declared maximum and served a clean slot
  afterward. Track A was committed and pushed as `ebdbf9e`. Its delayed
  independent review then found that independently armed timers could select
  pageout before cold advice and that the checked resource evidence no longer
  matched the emitting test schema. Pageout is now armed only after cold advice,
  evidence accounting is stricter, the three Zao observations were regenerated
  directly from the test output, and the Darwin e2e skip occurs before engine
  construction. No outstanding Track A P0/P1 finding remains.
- 2026-08-14: Tracks B-C reached analysis-only closure. The exact packaged Guest
  now exports a bounded `runtime_analyze_source` ABI backed by built-in `ast`,
  typed Plan projections, conservative direct/WASI coloring, recursive SCC/fixed-
  point summaries, and opaque dynamic barriers. The Host decodes the report
  strictly, verifies all source/artifact/profile/import/Plan bindings, and forms
  one effect-covering whole-Run region plus a body-free census. Rebuilt target
  artifact `d5fb9f1...` passed the real Linux ARM64 semantic e2e. An 11-case
  synthetic census produced 10 exact-source and 9 position-stripped AST identities,
  5 reusable candidates, and 3 unknown-barrier cases in 635 microseconds total;
  concurrency overlap was intentionally unrepresented, so this is mechanism
  evidence rather than a profitability claim. The bounded independent review
  timed out without a final summary, but its completed probes exposed missing
  definition-time defaults, class/complex-control opacity, and higher-order
  builtin callback barriers; all were closed with focused tests. Host admission
  now also requires the requested artifact/profile identities to match the live
  analyzer engine before Guest analysis starts. Tracks B-C were committed and
  pushed as `08e2eb5`.
- 2026-08-14: Track D complete. A default-off exact whole-Run `ReusePass` now
  requires a reusable one-region Plan, exact canonical-input and immutable-root
  dependencies, and strict source compatibility before entering the existing
  Agent Function single-flight/store. Analyzer, analysis, Plan, region, source,
  artifact, profile, import, input, root, deterministic, output, project, privacy,
  and policy identity plus the untrusted compatibility/requirements contract are
  bound without weakening the existing key. Publication
  still requires a successful canonical bounded Fresh Guest result and a zero
  Host-call effect probe; failures, cancellation, timeout, OOM, panic, corruption,
  oversize, live/unknown static effects, and ordinary unqualified Guest retention
  remain fail-closed. The rebuilt real Guest on Zao proved two exact siblings and
  one later repeat used one physical execution (leader/waiter/retained). The
  retained path took 335 microseconds versus a 10,331,129-microsecond physical
  batch, stored 274 bytes, and reported no physical execution. Including the
  separate 10,429,083-microsecond analyzer phase, this bounded three-call trial
  saved 10,232,840 microseconds (33.0%) versus three physical computations; this
  is exact-workload evidence, not an adaptive policy. Independent Track D review
  reported six P1s and no P0s. Closure replaced the forgeable retention entry
  with a Host-minted opaque qualification token, bound compatibility/requirements,
  rejected trusted prepare and arbitrary result decoders, added per-hit size and
  cancellation checks, separated callback/semantic cache provenance, and aligned
  emitted stats with checked-in evidence. Focused race and real-Guest reruns pass.

## Stop conditions

Continue automatically after each verified slice. Stop only when:

1. all executable Tracks 0-E are complete and global/real-platform gates pass;
2. a concrete product/resource/permission decision is required;
3. gates repeatedly fail in a way that requires user choice rather than debugging;
4. continuing would require the explicitly deferred arbitrary-Python compiler,
   general scheduler, destructive continuation checkpoint, distributed cache, or
   another unsafe broad rewrite.

A negative `MADV_PAGEOUT` experiment is not a blocker: record it and continue AST
work. Low AST coverage or negligible reuse may legitimately stop cache expansion,
but only after the analyzer and minimum `ReusePass` evidence expose the reason.
A signed checkpoint, successful push, context boundary, or one completed track is
not a stopping condition.

If blocked, report the exact blocker, current pointer, modified files, tests and
real outputs, Git status, safest next step, and which independent work was already
exhausted.

## Final reporting format

Return only:

- implemented mechanisms and honest status;
- key correctness/resource outcomes and limitations;
- deferred/negative decisions;
- gates and real-platform evidence;
- signed commits/remote state;
- exact remaining blocker, if any.

## Short prompt to start this Mega-Goal

```text
Read `docs/plans/2026-08-14-semantic-execution-autonomous-megagoal.md` fully, then execute it in `~/projects/agent-python-runtime`. Keep updating it after every verified slice; controller owns design/review/gates/signed push. Do not stop after one slice—continue until all executable Tracks 0-E are complete or a real product/resource/risk blocker satisfies its stop conditions. Keep optimizer/COW features Experimental and fail-closed; never widen authority or replay after effects.
```
