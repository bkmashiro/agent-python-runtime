# Correctness-Gated Source-Bound Agent Python Optimizer Autonomous Mega-Goal

> **Historical/completed roadmap:** Do not resume this file. Its planned runtime
> successor is the
> [Host-Scheduled Calls and Immutable Value Reuse Mega-Goal](2026-08-24-host-scheduled-python-reuse-autonomous-megagoal.md),
> which starts from the negative scalar-pass results and targets high-latency calls,
> data-local operations and immutable values shared across fresh Runs.

> **For Hermes:** This is the execution handoff for the next Pysolate optimizer lane.
> Read this file and the linked correctness contract fully, inspect live Git state, and
> proceed through successive verified slices. Do not stop after writing infrastructure,
> implementing one pass, running one test or producing one benchmark. Stop only at a
> named architecture/evidence gate that requires Yuzhe's decision, an unavailable
> resource/permission, an unsafe effect boundary, or complete closeout.

**Goal:** Turn Pysolate's existing source-bound semantic analysis, streaming-prefix
overlay, effect-aware Host legality, execution-patch path and prepared/private runtime
primitives into a small suite of real correctness-gated passes for ordinary Agent
Python. Reproduce several paper-derived optimization kernels as bounded prefix overlays
or AST transformations while preserving complete-source validation, target-CPython
behavior, sealed authority, logical effects, workspace semantics and fail-closed
fallback.

**Prepared:** 2026-08-23

**Preparation parent:** `8934a0dcf890ef21fe8d84c79bf0d70c214dd6c7`

**Primary contract:**
[`docs/research/correctness-gated-agent-python-passes.md`](../research/correctness-gated-agent-python-passes.md)

## Research thesis

Several agent systems separately implement repeated-work elimination, tool-call fusion,
independent-call parallelism or batch lowering. Pysolate should absorb only the
semantics-preserving optimization kernel that can be represented as a bounded AST
transformation or source-bound occurrence overlay.

The intended claim is:

> Pysolate expresses several point optimization kernels as guarded source-bound passes
> over ordinary Agent Python. Prefix overlays and complete-source transformations share
> one effect, authority, freshness, workspace, failure and fallback contract.

The lane does not claim that schedulers, KV caches, learned planners, model selectors or
workflow induction are compiler passes.

## Non-negotiable invariants

1. **Complete source precedes formal execution.** No transformed or unchanged Agent
   Python executes before the complete source passes exact target-Guest validation.
2. **Early physical work remains narrow.** Before final source, only an exact
   Host-qualified, bounded speculative-safe read may start. It is not Python execution
   or a logical effect and must become consumed, cancelled, late or orphaned evidence.
3. **Writes do not speculate.** Workspace or external writes, approvals and unknown
   effects remain behind final source, dynamic reach, Broker admission and approval.
4. **AST facts do not grant authority.** Every derived call stays inside the original
   sealed capability Plan and exact resource, freshness, privacy and workspace bindings.
5. **Target CPython remains the oracle.** The exact Guest parses, digests, compiles and
   executes admitted derived ASTs. The Host does not grow a second Python parser.
6. **Unsupported means unchanged source.** A missing proof, unknown syntax, alias,
   mutable identity, dynamic argument, exception-motion risk or contract mismatch
   rejects the pass.
7. **No post-effect replay.** If an authority-bearing external effect may have started,
   no backend/pass fallback replays the original program. Ambiguity is surfaced.
8. **Logical and physical work stay separate.** Optimization may reduce physical work;
   it must preserve ordered logical occurrences, budgets, receipts and terminal facts.
9. **Private discard is not generic rollback.** Guest state, prepared memory and private
   workspace branches can be discarded. The project does not invent a generic external
   transaction, compensation or effect platform.
10. **Every pass is independently switchable.** Ordinary CLI/HTTP execution stays fresh
    and unchanged by default. No automatic global optimizer pool or scheduler appears.
11. **No broad framework before evidence.** Implement a third real transformation before
    generalizing the two-name pass registry. Keep the pass driver thin.
12. **Performance claims follow correctness.** Deterministic fixtures may establish
    mechanism behavior. Natural-workload speedup requires a frozen workload, baseline,
    metric and matched artifact-backed evidence.
13. **Streaming and final transforms are distinct stages.** A complete-prefix AST may
    authorize an overlay or safe physical preparation. Only the sealed complete AST may
    authorize formal execution or a derived execution patch.
14. **Defer architecture-breaking candidates.** If a pass requires a substantial change
    to the current authority lifecycle, complete-source gate, fresh/private execution,
    Host-owned effect truth, Prepared Family ownership, default-off path or no-replay
    philosophy, mark that pass `Deferred` with the concrete reason. Do not reshape the
    runtime to rescue it; continue with independent admitted slices.

## Starting facts to verify from live source

Do not trust this list over current code; confirm it before implementation.

- `runtime/passregistration` recognizes only `semantic_pre_dispatch` and
  `prepared_pure_region`.
- `semantic_pre_dispatch` is an overlay consumer for exact occurrence-bound prepared
  observations.
- `prepared_pure_region` is an execution-patch consumer for one bounded scalar region.
- `runtime/semantic` has verified source/AST facts, candidate regions, effect summaries,
  legality decisions and observable differential traces.
- capability Plan metadata admits the current pre-dispatch read contract but rejects
  general coalescing/cache legality.
- the formal execution path validates complete source and derived patches in the exact
  target Guest.
- Prepared Family supports explicit Host-owned bounded NumPy input, private consumers,
  fresh authority/workspace identities and Linux private-COW evidence.
- single-flight, captured observations, private workspace branches and content-addressed
  Agent Functions exist as runtime substrates. Their existence alone does not make an
  AST pass legal.

Run at least:

```bash
git status --short --branch
git log -5 --show-signature --format='%H %G? %s'
go test ./runtime/passregistration ./runtime/semantic ./runtime/capability \
  ./runtime/engine/wazero ./integration/e2e -count=1
go vet ./...
```

Resolve any live drift before freezing new evidence.

## Scope

### Paper-derived pass kernels

| Kernel | Candidate pass | Source systems |
|---|---|---|
| repeated pure/read work | `effectful_cse` | stratum |
| exact projection/predicate pushdown | `capability_projection_pushdown` | stratum |
| map/tool batching | `capability_batch_fusion` | stratum, LLM-Tool Compiler, AAFLOW |
| dependency-safe concurrency | `independent_capability_parallel` | LLMCompiler, APPL |
| deterministic straight-line fusion | `straight_line_meta_tool_fusion` | AWO, limited to existing read-only AST sequences |

### Pysolate-native pass kernels

- `canonical_input_specialization`
- `unreachable_import_elimination`
- `repository_projection_pushdown`
- `streaming_pure_region_prepare`
- `streaming_literal_array_prepare`
- `literal_array_hoisting`
- `pure_function_memoization`
- `loop_invariant_observation`
- `sibling_observation_cse`
- `cohort_common_prefix`

Not every kernel must survive. A typed, evidence-backed rejection is a valid outcome.
The mega-goal is complete when each admitted phase below is implemented or rejected at
its named gate and the retained suite composes under one verified contract.

## Phase 0: Freeze the pass contract and study

**Promise:** Start from a stable claim and test surface rather than retrofitting
correctness after performance work.

- [x] Re-read the primary contract, current semantic/pass docs and exact source owners.
- [x] Freeze a versioned preregistration for pass identities, outcome classes,
  pass-off/pass-on comparator fields and forbidden claims.
- [x] Freeze pass-stage identities for `prefix_overlay`, `hybrid_prepare_patch`,
  `whole_program_patch` and `multi_program_patch`. Do not force them through one
  transform callback.
- [x] Include invalid final suffix, earlier exception, branch not taken, zero iteration,
  cancellation, Plan drift, freshness drift, privacy/workspace drift, mutable alias,
  unsupported syntax and external-write controls.
- [x] Define body-safe evidence for original/derived source and AST digests, pass order,
  logical/physical events, result/exception, workspace disposition and rejection reason.
- [x] Preserve all existing pass registrations and default-off product behavior.
- [x] Record paper-derived kernels and non-pass mechanisms without claiming
  implementation.

**Gate P0:** Frozen contract and matrices validate canonically; focused source/semantic
and current exact-Guest controls pass; independent review finds no authority or effect
hole in the proposed study.

**P0 closeout:** Exact commit `02ec31ef5e02a07186a9b4fa8fe3d2fd9ef0dba1`
(tree `c72b5bf741c1d6dd543053fce55db0cc42322ee6`) passed independent
post-fix review after exact matrix equality and recursive duplicate-key rejection closed
the predecessor findings. Focused, race, vet, canonical generator/artifact and independent
identity probes passed.

## Phase 0M: Minimum pass-pipeline substrate

**Promise:** Remove the next obvious hard-coded seams before the third pass without
building a general optimizer framework.

- [x] Add typed stage and outcome records for `prefix_overlay`,
  `hybrid_prepare_patch`, `whole_program_patch` and `multi_program_patch`; preserve the
  existing registration identities and consumers.
- [x] Add a small Host-owned `PassPipeline` that routes the current
  `semantic_pre_dispatch` and `prepared_pure_region` lanes, records typed rejection and
  applied outcomes, and enforces explicit per-pass/all-off controls.
- [x] Keep stage-specific entry points. Do not introduce one universal transform
  callback, plugin loading, dependency resolution, fixed-point iteration or a new IR.
- [x] Freeze pipeline bounds for pass count, source/AST growth, preparation bytes and
  reanalysis. Reject overflow before formal Agent execution.
- [x] Prove the current two passes remain byte/trace compatible through the new shell;
  all-off must take the existing unchanged-source path.

**Gate P0M:** Existing overlay and patch behavior is unchanged, the third pass no longer
needs another top-level hard-coded branch, and the substrate remains small enough to
delete without migrating runtime semantics. General ordering/composition stays deferred
to Phase 7 after at least three retained passes expose the real shared seam.

Checklist legend: `[x]` is completed and `[-]` is explicitly deferred by the decision
paragraph in that phase. A deferred item is closed for this megagoal, not implemented.

## Phase 1: Third real pass, pure scalar CSE

**Promise:** Validate a reusable AST-in/AST-out pass shape without introducing external
effects or a large optimizer framework.

- [x] Add RED fixtures for repeated exact `bool`/`int64` scalar expressions and controls
  for division, calls, heap mutation, alias-sensitive values, exceptions and opaque
  control.
- [x] Implement a bounded `pure_scalar_cse` transformation using target-Guest analysis.
  It may replace only repeated proven scalar values whose evaluation is total and whose
  identity is unobservable.
- [x] Bind pass name/version/config and original/derived source and AST through the
  registration/patch lineage. Execution profile, imports and Plan remain with the owning
  Engine and unchanged `RunRequest`; the pure pass does not duplicate them.
- [x] Compile the derived AST before one formal execution; failure selects unchanged
  source before Agent execution.
- [x] Differentially compare result and output metadata; inapplicable effect/control cases
  remain unchanged, and the generic source-patch lane admits no Broker or workspace.
- [x] Measure matched synthetic fresh-Guest runtime without making an agent-workload
  speedup claim.

**Gate P1:** Every positive and negative case matches target-CPython behavior; the pass
has one exact off-state and fails closed. If the third pass requires a broad IR or cannot
reuse the existing candidate/patch machinery, stop and present the smallest concrete
seam before adding infrastructure.

**Decision:** **Implemented in the approved plugin follow-up.** A compile-time static
registry now accepts new definitions without a central name switch, and one generic
authority-free whole-program selector executes exact-Guest-produced source patches
against the unchanged original `RunRequest`. `pure_scalar_cse` proves the seam with
adjacent identical, statically evaluable int64/bool expressions; calls, division,
mutable values, control flow and non-adjacent reuse remain unchanged. See
[`source-pass-plugins.md`](../source-pass-plugins.md).

**Paper-pass follow-up:** **Implemented.** A bounded audit of stratum, LLMCompiler,
APPL, LLM-Tool Compiler, AWO/Meta-tools and AAFLOW admitted only stratum's exact
constant-folding kernel without a new Host execution contract. `pure_scalar_fold` is a
second independent whole-program plugin over a closed top-level bool/signed-int64
program. Programs with imports, calls, control flow or compiled-code introspection are
not applicable.
Batching, projection, parallel calls and composite tools remain deferred for their typed
Host semantics; see
[`paper-pass-absorption-v1.md`](../research/paper-pass-absorption-v1.md).

## Phase 1S: Unify the existing streaming overlay contract

**Promise:** Make the already implemented semantic pre-dispatch lane a first-class pass
stage without rewriting unchanged final Python or reviving historical prefix execution.

- [x] Bind `semantic_pre_dispatch` explicitly to `prefix_overlay` in the pass evidence
  model while preserving the existing `overlay_only` consumer identity.
- [x] Prove that only exact parser-accepted complete-prefix AST snapshots reach semantic
  admission; incomplete chunks and suites produce no pass decision.
- [x] Preserve the current prefix-readiness filter, bounded analyzer session and
  prepared/COW analyzer capacity as compiler-service optimizations, not counted passes.
- [x] Re-run invalid-suffix, earlier-exception, branch-not-taken, cancellation,
  freshness/Plan drift and orphan accounting controls.
- [x] Verify the formal Guest starts only after final source seal and executes unchanged
  source once.

**Gate P1S:** The unified pass report represents both the current prefix overlay and
whole-program patch without weakening either contract. No Agent Python prefix executes,
no write is preissued and no analysis-service optimization is mislabeled as a pass.

## Phase 2: Effectful CSE for frozen observations

**Promise:** Reduce repeated physical reads while retaining every original logical call.

- [-] Freeze a coalescing contract distinct from pre-dispatch. It must bind exact
  capability/spec/handler/Plan/grant, resource identity, arguments, freshness epoch,
  privacy partition, workspace root, result codec and logical occurrence set.
- [-] Keep live/latest reads, writes, unknown effects and mutable identity out of the
  admitted subset.
- [-] RED-test two exact repeated reads, changed arguments, changed roots, intervening
  private write, live freshness, first-call failure, second-call failure, cancellation,
  result mutation and Plan drift.
- [-] Implement one physical read plus detached per-occurrence materialization. Preserve
  source-order logical events, budgets, receipts and exception behavior.
- [-] Account late/orphaned physical work; never hide a speculative or single-flight
  attempt behind a cache hit.
- [-] Run exact-Guest pass-off/pass-on and race tests.

**Gate P2:** Positive rows record fewer physical reads with identical ordered logical
trace and terminal/workspace state. Every adversarial row rejects or matches unchanged
source. No external write path can construct a qualified CSE decision.

**Decision:** **Deferred.** The sealed capability contract fixes coalescing to
`forbidden`, live workspace reads have no frozen-root identity and the Broker has no
per-occurrence logical/physical split. Implementing the pass would change the
capability lifecycle; see
[`optimizer-deferred-pass-decisions-v1.md`](../research/optimizer-deferred-pass-decisions-v1.md).

## Phase 3: Repository projection pushdown

**Promise:** Reduce Host-to-Guest bytes for a coding-agent read pattern under one exact,
versioned rewrite law.

- [-] Audit the real capability surface and select one useful bounded coding pattern.
  Prefer an exact file range/line projection; do not invent a generic query DSL.
- [-] Freeze Python semantics for encoding, newline handling, slicing, missing files,
  oversized results and exceptions.
- [-] Add one adapter-declared rewrite law and bind it into Plan/pass identity.
- [-] Transform only the exact AST shape. Dynamic indices, method rebinding, unknown
  encoding, mutable intermediate values and live roots reject.
- [-] Differentially test result/error and compare physical bytes, calls and allocations.

**Gate P3:** One real coding fixture reduces transferred bytes under exact semantic
parity. If the existing capability surface cannot express an exact law without a broad
new API, record the no-go and continue without projection pushdown.

**Decision:** **Deferred.** The workspace surface has no versioned range/line law. The
generic source-patch selector now exists, but adding a projection capability without a
concrete workload and adapter-owned exact law would only widen the product surface.

## Phase 4: Ordered read-only batch fusion

**Promise:** Turn a bounded map/list-comprehension of identical safe operations into one
physical batch while retaining per-item logical behavior.

- [-] Freeze `map(single, args) == batch(args)` semantics including empty input, order,
  duplicate arguments, first visible failure, partial Host failure, cancellation and
  per-item result bounds.
- [-] Require one capability, Plan, freshness, privacy partition and workspace root.
- [-] RED-test external writes, approval-gated calls, changing roots, dynamic callable,
  generators with side effects, short-circuit behavior and oversized batches.
- [-] Implement the minimum list-comprehension or simple-loop rewrite supported by the
  target AST analyzer.
- [-] Expand one physical batch outcome into ordered logical receipts without inventing
  success for unperformed items.

**Gate P4:** Batch-positive fixtures reduce Host crossings and preserve complete logical
trace. Partial-failure evidence is typed and no stateful capability is admitted.

**Decision:** **Deferred.** One Broker call currently owns one operation index and one
receipt. A physical batch cannot represent the original ordered logical calls without
a new Broker item lifecycle. The available source-patch lowering cannot manufacture
per-item outcomes or receipts.

## Phase 5: Independent read parallelization

**Promise:** Parallelize only calls whose external execution is safe even when an earlier
source-order call later fails.

- [-] Extend the dependency/effect proof only as far as required for adjacent assignments
  or another predeclared bounded basic-block shape.
- [-] Freeze source-order exception selection, cancellation, late/orphaned work and
  physical budget semantics before implementation.
- [-] Reject writes, approvals, unknown effects, live reads, dynamic calls and any
  data/control predecessor.
- [-] Lower to a bounded Host helper without adding a scheduler or general DAG runtime.
- [-] Compare against unchanged serial execution under matched delays.

**Gate P5:** Exact outcomes and logical traces match for every row; at least one frozen
read-only coordinate shows reduced critical path after pass/analyzer/Host overhead. A
negative economics result retains the semantic mechanism only if it has independent
value and low maintenance cost.

**Decision:** **Deferred.** The synchronous Guest/Broker path has no bounded helper for
source-order exception selection, sibling cancellation and late/orphan accounting.
Adding it would create an intra-program concurrent execution subsystem.

## Phase 6: Prepared literal and array hoisting

**Promise:** Connect a genuine AST hoisting pass to the existing bounded Prepared Family
substrate.

- [-] Select one exact immutable literal or NumPy construction shape already supported
  by the `numpy-core` profile and descriptor/body contract.
- [-] RED-test dtype/shape/layout drift, mutable aliasing, source/input/profile/Plan drift,
  oversized body, unavailable profile, consumer cancellation and family close.
- [-] Rewrite construction to one-shot materialization while retaining a fresh private
  Python object for each consumer.
- [-] Compare ordinary fresh, private-copy and matched Linux private-COW lanes. Report
  only exact platform modes observed.
- [-] Keep prepared body out of source, RunRequest, Broker JSON, receipts and public
  evidence.

**Gate P6:** Real-Guest fixtures match values and mutation isolation; lifecycle/body
release gates pass; exact Linux evidence is bound to one source commit/tree before any
private-COW claim.

**Decision:** **Deferred.** Prepared NumPy ingress installs a private global, and the
generic final-source patch can now remove or replace source. The missing piece is a
typed join between that patch and the prepared materialization, including mutation
isolation and lifecycle ownership. The closed Prepared Family data plane and COW claims
remain unchanged.

## Phase 6S: Streaming preparation promotion

**Promise:** Determine whether an authority-free pure region or bounded literal/array
construction can be prepared after its complete prefix arrives and consumed only after
the complete final AST admits the normal execution patch.

- [-] Freeze positive and negative chunk schedules before timing. Include suffix drift,
  invalid final syntax, later mutation/dependency, profile/Plan drift, cancellation and
  preparation completing after finalization.
- [-] Reuse the final-source region/array patch contract. Prefix analysis may start
  physical pure/private work but cannot pre-authorize the final patch.
- [-] Invalid, abandoned or changed final source discards preparation and records its
  physical cost without a logical claim.
- [-] Do not execute the visible prefix as Agent Python. Scratch execution, if needed,
  remains authority-free, output-bounded and separate from the formal Guest.
- [-] Measure whether remaining source-generation time overlaps enough preparation cost
  to justify retaining the hybrid pass.

**Gate P6S:** Retain a hybrid streaming pass only if every semantic/identity/lifecycle
control passes and at least one preregistered fixture obtains real overlap after analyzer
and preparation overhead. A negative result leaves complete-source hoisting unchanged.

**Decision:** **Deferred with Phase 6.** Streaming preparation cannot be promoted
without an admitted complete-source array patch. Starting speculative work now would
add lifecycle without a legal final consumer.

## Architecture decision gate: cohort common-prefix factoring

After P6, inspect whether several sibling ASTs expose a useful common pure prefix that
can be factored without adding shared Python authority or a scheduler.

Defer the pass without changing Prepared Family architecture if the spike requires any
of:

- a shared mutable Python heap;
- generic heap snapshot/serialization;
- cross-member Plan or Broker reuse;
- automatic cohort scheduling or RunMany;
- post-effect replay;
- automatic workspace merge/publish;
- a new source language or broad IR.

If none is required, proceed with one bounded multi-AST `cohort_common_prefix` fixture.
Otherwise record `Deferred` with the exact incompatible assumption, leave Prepared
Family unchanged and continue to Phase 7.

**Decision:** **Deferred.** Existing authored residual programs can consume Prepared
Family globals, but factoring arbitrary sibling sources requires a multi-program patch.
No shared heap, Plan/Broker reuse, scheduler or workspace publication is introduced.

## Phase 7: Thin pass composition

**Promise:** Compose proven passes without building a second compiler.

- [-] Extract only the shared registration, ordering, identity and bounded transform
  driver exercised by at least three retained passes.
- [-] Freeze a deterministic pass order and reject duplicate/incompatible consumers.
- [-] Re-run target analysis after transformations when a later pass relies on changed
  AST facts; never reuse stale analysis by digest convention alone.
- [-] Add pairwise and selected multi-pass differential cases for specialization, fold,
  CSE, pushdown, batch and parallel order interactions.
- [-] Add pass-level disable flags and one all-off unchanged-source oracle.
- [-] Record rejection reasons and final derived AST identity without source bodies in
  public evidence.

**Gate P7:** Pass order is deterministic; stale-analysis, authority-widening and
pass-order mutation tests fail closed; all-off execution remains byte/trace compatible
with the ordinary path.

**Decision:** **Deferred beyond the v0 shell.** The registry now retains two adapters and
two independently runnable whole-program plugins, but no accepted fixture needs pass
ordering, overlap resolution or reanalysis across transformed ASTs. Explicit one-pass
dispatch, stage-confusion rejection and all-off behavior remain the complete manager
surface; pass composition will be added only for a concrete interacting pair.

## Phase 8: Agentic coding workload evidence

**Promise:** Separate mechanism correctness from prevalence and economics.

- [x] Freeze a deterministic authored corpus covering repeated repository reads, bounded
  projections, batch reads, independent reads, pure parsing and prepared array setup.
- [x] If a natural coding-agent corpus is available, freeze source/privacy/license and
  pass-opportunity census before timing. Otherwise report authored mechanism evidence
  only.
- [x] Run matched pass-off and each-retained-pass mechanism controls. Author explicit
  synthetic positive controls for mechanism capacity; keep them separate from natural
  prevalence. Record all-admitted treatment as not applicable when no one fixture
  qualifies for both passes.
- [x] Report result/error parity, logical effects, physical calls, exact-Guest census,
  rejected candidates and applicable historical timing without promoting structural
  candidates to executed passes.
- [x] Do not aggregate unrelated workloads into one universal speedup.
- [x] Independently recalculate every quoted historical median from raw evidence.

**Gate P8:** All retained passes preserve the frozen observable contract. Performance
claims name exact workloads, artifacts, platforms and treatments. A pass with no observed
opportunity or negative net value is documented as limited/rejected rather than kept for
story count.

**P8 closeout:** The six coding-shaped cases and the natural 36-event sample contain no
admitted streaming opportunity. That is a prevalence result, not a gate on synthetic
mechanism evaluation. The v2 exact-Guest corpus adds one deliberately eligible control;
its single call is admitted with no rejection. Two matched synthetic timing fixtures show
1.914x-1.923x speedup in their fixed 1.5-second-read/1.4-second-tail coordinate. Retained
prepared-region controls also pass, but no fixture qualifies for both mechanisms.
[`source-bound-pass-workload-evidence-v2.md`](../research/source-bound-pass-workload-evidence-v2.md)
separates positive mechanism capacity from sampled prevalence.

## Phase 9: Independent review and truthful closeout

**Promise:** Code, evidence, paper notes and product boundary agree.

- [x] Run independent frozen-target review of each effect-changing pass and the composed
  pipeline. Fix every Blocking/High/Medium finding with RED tests and rerun affected
  evidence.
- [x] Run full Go, focused race, Guest Python, scripts, vet and `git diff --check` gates.
- [x] Run exact real-Guest tests for every retained Guest transform. Missing
  `AGENT_RUNTIME_GUEST` skips do not count as evidence.
- [x] Use gpu31 only for bounded Linux private-COW or exact Linux behavior that cannot be
  established locally; use shared toolchain/cache paths.
- [x] Update main docs and `pysolate-explained` paper notes to separate implemented,
  rejected, substrate and related-work claims.
- [x] Do not modify thesis/report/deck artifacts unless separately requested.
- [x] Sign and push each stable slice. Do not manually trigger CI; prefer local and ICL
  gates under the existing resource policy.
- [x] Verify branch clean/upstream-aligned and every relevant commit signed.

**Gate P9:** No unchecked executable item remains. Each candidate is Current, Rejected
with evidence, Deferred by explicit non-goal or Blocked by a named owner decision. Final
post-fix review has no open Blocking/High/Medium finding.

**P9 closeout:** Independent review of frozen target
`b920e5d162c38a3cc5ec7c129fbda739f8f70e4a` reproduced the four predecessor probes and
confirmed that frozen-field drift, count overflow, contradictory outcomes and duplicate
terminal projections now fail closed. Focused race/vet checks, canonical evidence
recalculation and exact-Guest prepared-region tests passed. The authored census remains
historical evidence from harness source `5b8329cf32a1320f17df185de32205391072da4d`;
it was not relabelled as a final-target performance campaign. The review reported no
open Blocking, High or Medium finding.

## Per-slice protocol

1. inspect live source, tests, docs, evidence and Git state;
2. add a failing test or frozen preregistered probe;
3. run it and confirm the intended failure;
4. implement the minimum complete vertical behavior;
5. run focused success and adversarial tests;
6. inspect source/AST, authority, effects, freshness, workspace, cancellation and body
   handling;
7. update this roadmap and current pointer;
8. run proportional global gates and `git diff --check`;
9. make a signed commit and push;
10. verify signature, upstream and clean state;
11. continue to the next admitted slice without waiting for routine approval.

Do not leave an intentionally failing test across unrelated slices. If context runs low,
finish or revert the slice, leave a clean signed checkpoint, and resume from live Git.

## Stop conditions

Stop only when one of these is true:

1. all phases are closed;
2. no independent admitted phase remains after architecture-breaking candidates have
   been marked `Deferred`, and a new project direction would require Yuzhe's decision;
3. safe implementation requires arbitrary Python equivalence, generic heap snapshot,
   shared mutable authority, generic transactions or post-effect replay;
4. a required exact Guest/platform/resource is unavailable after one bounded alternative;
5. source/privacy/licensing prevents the requested evidence.

Do not stop because one pass is rejected. Record the result and continue with independent
admitted phases.

## Current execution pointer

`Complete: P0, P0M, the retained P1S streaming overlay and P8 are verified. The P1-P7
implementation candidates that require a new source-patch, effect, scheduling, workspace
or composition contract are explicitly Deferred. P9 post-fix review and closeout gates
passed with no open Blocking/High/Medium finding.`

## Short `/goal`

```text
/goal Read docs/plans/2026-08-23-correctness-gated-agent-python-optimizer-autonomous-megagoal.md fully and execute it from live Git state in /Users/yuzhe/projects/agent-python-runtime. Continue through verified source-bound-pass, exact-Guest, evidence, independent-review, signed-commit and push slices. Defer any pass that requires a substantial architecture change or breaks current design assumptions; record the reason and continue with independent slices. Stop only when no independent admitted work remains, at an unavoidable safety/resource decision, or complete closeout. Preserve complete-source validation, Host-owned authority/effect truth, private workspace semantics and no post-effect replay. Do not modify thesis/slides, use paid cloud or Docker, or manually trigger CI.
```
