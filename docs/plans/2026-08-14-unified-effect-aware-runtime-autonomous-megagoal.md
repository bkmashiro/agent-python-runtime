# Unified Effect-Aware Runtime Autonomous Mega-Goal

> **For Hermes:** This is the active successor roadmap. Read it fully, inspect live
> repository state, and execute coherent verified slices without widening the
> authority model. A green slice or signed checkpoint is not a stopping condition;
> the decision gates below are.

**Status:** Active — Track 0 research contract closed; Track A opportunity census next
**Date:** 2026-08-14
**Owner:** Yuzhe
**Repository:** `~/projects/agent-python-runtime`
**Baseline:** `3bd022fa074f8e8178b9bdd0fd9efaa5e4f8c37c`
**Predecessor:** [`2026-08-14-semantic-execution-autonomous-megagoal.md`](2026-08-14-semantic-execution-autonomous-megagoal.md)
**Architecture recommendation:** [`../research/unified-effect-aware-runtime-architecture.md`](../research/unified-effect-aware-runtime-architecture.md)

**Goal:** Determine, then implement only where justified, whether Pysolate can use
one Host-qualified semantic overlay of a constrained agent-generated Python program
to safely drive semantic pre-dispatch, exact reuse, and pre-execution placement.

**Architecture:** The exact target Guest CPython parses and analyzes source into a
bounded canonical report. The Host strictly decodes it, binds Host-owned capability
contracts and runtime identities, and mints opaque verified provenance. A thin
source-indexed semantic overlay records only facts needed by reusable conservative
legality predicates. Unknown semantics preserve original execution and ordering.

**Tech stack:** CPython 3.14 `ast` in the existing WASI Guest; Go contracts,
validators, placement, Agent Function and capability packages; existing typed Host
Broker and streaming/eager-read mechanisms; deterministic Go/Python tests; bounded
Linux real-Guest evidence; body-free structured research artifacts.

---

## User intent

Yuzhe wants Pysolate to move beyond the weak framing of “Python/WASM Code Mode” or
a smaller sandbox. The research target is a unified effect-aware runtime for
semantically inspectable agent-generated programs. The runtime should derive useful
execution facts from program structure plus explicit Host-owned capability/WASI
contracts and reuse those facts across optimizations.

The objective is not to implement every optimization named in the research handoff.
It is to establish one shared semantic layer, measure whether it exposes real
opportunities, and then add the smallest semantics-preserving runtime passes that the
evidence justifies.

## Research thesis and bounded claim

Candidate thesis:

> Agent-generated code can be a semantic interface to an execution runtime rather
> than an opaque payload. For a qualified subset of generated Python, Pysolate can
> propagate Host-owned effect contracts through program structure and conservatively
> qualify pre-dispatch, execution reuse, and placement without expanding authority.

“Whole-program” always means the accepted analyzable subset of one exact source and
runtime profile. It is not a claim of complete semantics for unrestricted Python.

The intended unifying structure is:

```text
exact target-Guest Python source
        +
Host-owned capability/WASI contracts
        |
        v
verified effect/dependency/capability representation
        |
        +--> legality: preserve order or admit concurrency/code motion
        +--> identity: exact singleflight/reuse
        +--> placement: WASM or native before execution
```

## Value filter

Prefer, in order:

1. sound rejection and observable-equivalence evidence over optimization coverage;
2. a shared representation and shared legality predicates over feature-specific
   heuristics;
3. real generated-program opportunity measurements over constructed happy paths;
4. exact source/inputs/contracts/snapshot identity over semantic similarity;
5. one narrow semantic pre-dispatch consumer over Python rewriting or a general
   compiler;
6. pre-execution placement over runtime replay or migration;
7. target-Guest semantics and Host-owned authority over duplicated Host parsing or
   Guest-authored policy;
8. standard-library/project-owned implementation over heavyweight compiler stacks;
9. small signed, independently reviewable slices with local-first gates.

## Non-negotiable boundaries

1. **Qualified subset, not arbitrary Python.** Dynamic dispatch/import, reflection,
   `eval`/`exec`, unknown builtins or extensions, unresolved aliasing, higher-order
   callbacks and unsupported control/object boundaries become explicit unknowns.
2. **Target Guest owns Python syntax semantics.** AST/CFG/def-use extraction runs in
   the exact packaged target Guest CPython. Do not add a second Python parser in Go.
3. **Guest reports carry no authority.** The Host strictly decodes and validates the
   report, binds it to artifact/profile/import/capability-plan/schema identities,
   and alone decides legality, retention and placement.
4. **One canonical capability source.** Extend the existing Host-owned
   `capability.Spec`/sealed Plan only when a proven legality question needs more
   metadata. Do not create an optimizer-only competing tool registry.
5. **Ordinary filesystem stays Python/WASI.** Do not wrap ordinary Guest stdlib file
   operations as Pysolate tools. Model known WASI behavior conservatively; unknown
   behavior is opaque.
6. **Unknown means preserve order/off.** Failure to prove legality never authorizes
   parallelism, hoisting, reuse, prefetch, retry or weaker execution.
7. **Exceptions and cancellation are observable.** Code motion must not introduce a
   call, exception, cancellation outcome, resource lifetime or external observation
   on a path where the baseline would not have produced it.
8. **GET/read-only is not pure.** Freshness, snapshot identity, resource footprints,
   determinism, coalescing and sharing scope must be explicit and identity-bound.
9. **No effect-after replay.** Backend choice occurs before execution. Implicit
   fallback is permitted only after a Host-authored outcome proves workspace and
   effects both `not_started`. No exception-text fallback and no transparent replay
   after possible execution/effect.
10. **No authority widening.** Optimization cannot add capabilities, imports,
    workspace roots, network targets, budget or policy. A semantic report is never a
    grant.
11. **No arbitrary automatic caching.** Exact whole-Run reuse remains the control.
    A smaller region is reusable only after canonical live-ins/live-outs,
    effect/dependency identity and runtime publication gates are proved.
12. **No cross-tenant cache.** Initial sharing remains project/private and
    worker-local. Cross-tenant existence and freshness leakage is out of scope.
13. **Experimental and independently default-off.** Overlay analysis, semantic
    pre-dispatch, region reuse and semantic placement each have an explicit off-state.
14. **No general SSA/compiler stack.** Do not add LLVM, MLIR, a broad optimizer
    framework, arbitrary heap materialization or distributed scheduling. A minimal
    CFG/def-use form must earn its complexity through measured opportunity.
15. **No unverified related-work claims.** Treat the supplied handoff as research
    hypotheses until primary papers and pinned source are checked.
16. **No paid/deployment side effects.** Use local and approved bounded Linux/ICL
    research hosts; do not manually trigger scarce GitHub Actions, publish releases
    or deploy services.

## Current state discovered at baseline

Live inspection at `3bd022f` established:

- `runtime/semantic` has strict bounded `Analysis`, `EffectSummary`, `Plan`,
  `Region`, `Dependency`, census and opaque verified provenance contracts;
- the Guest analyzer already uses exact target-Guest `ast`, detects direct typed
  capability calls, summarizes function call SCCs and rejects broad dynamic/control
  constructs;
- current effects are four coarse booleans: publish, observe-live, suspend and
  unknown;
- current plans expose only whole-function/whole-Run regions; actual integrated
  reuse is exact whole-Run only;
- `capability.Spec` is already the canonical Host-owned source for effect class,
  playback, handler/version identity, schemas, Python projection, and current
  read-only/idempotent/speculation qualification;
- existing semantic projections expose only capability name, effect class, playback
  and Python call name; they do not expose resource expressions, freshness,
  exception or cancellation semantics;
- `runtime/placement` already performs pre-execution static import/requirements
  routing and permits only typed `not_started/not_started` L2 promotion;
- existing exact semantic reuse has opaque verified provenance, strict identity,
  worker-local singleflight/retention, compatibility admission and runtime zero-
  Host-call publication gating;
- current real-workload descriptors are mechanism fixtures, not a corpus of natural
  generated programs or evidence of semantic pre-dispatch opportunity;
- the predecessor roadmap is complete and its optimizer families were deliberately
  deferred. This file is a successor, not a reinterpretation of unfinished work.

## Desired future state

Completion means all of the following, with unsupported items explicitly rejected or
falsified rather than silently omitted:

1. A source-pinned related-work matrix states exact similarities and differences for
   AsyncFC, Agent JIT, PASTE, CaMeL, A1, workload-aware caching, ARIES and public
   Cloudflare material without novelty overclaim.
2. A private/body-safe corpus of real or faithfully replayed generated Python plus
   adversarial fixtures measures analyzable coverage, node/call classes, unknown
   reasons and candidate pre-dispatch/reuse/placement opportunities.
3. One versioned bounded semantic overlay is emitted by the exact target Guest and
   strictly validated/bound by the Host. It represents only the facts justified by
   the accepted subset.
4. Host-owned capability contracts provide the minimum resource/freshness/
   determinism/exception/cancellation metadata required by the chosen legality
   questions. Missing metadata fails closed.
5. Shared Host legality predicates answer at least `CanPreissue`,
   `CanClaimStagedObservation`, `CanCoalesce`, `CanCache` and `RequiredBackend`;
   `CanHoist` remains absent or conservative until exception/control legality is
   demonstrated.
6. One default-off semantic pre-dispatch consumer, if the decision gate admits it,
   uses existing staged observations without rewriting Python, proves unchanged
   logical results/effects on positive and adversarial fixtures, and demonstrates a
   real latency/critical-path benefit on a bounded workload.
7. Existing exact whole-Run reuse consumes the shared semantic facts without
   weakening its identity or publication gates. Region-level reuse is implemented
   only if exact live-in/live-out materialization is demonstrated.
8. Placement may consume verified semantic capability requirements before execution;
   it never turns runtime errors into replay permission.
9. Optimizer-off remains the semantic control and preserves current public behavior.
10. Structured evidence separates coverage, correctness, opportunity, performance,
    placement and overhead claims.

## Decision gates

These gates deliberately prevent the roadmap from becoming a predetermined compiler
rewrite.

### Gate G1 — opportunity and representation gate

After Tracks 0–B, stop for Yuzhe discussion before adding a runtime consumer if any
is true:

- the candidate corpus is too synthetic to support a research claim;
- useful call/dependency coverage is rare or requires broad Python semantics;
- resource/freshness contracts cannot be expressed without destabilizing the
  canonical capability model;
- a call-level pre-dispatch baseline captures essentially all measured opportunity;
- the proposed overlay duplicates current `Analysis`/`Plan` without enabling a new
  falsifiable legality question.

If G1 passes, record the exact accepted subset and first runtime consumer. Do not
widen it opportunistically during implementation.

### Gate G2 — semantic pre-dispatch gate

After the first pre-dispatch prototype and differential/adversarial evidence, stop for
Yuzhe discussion if results would change the overlay or execution model.
Proceed to region reuse/placement integration only if:

- zero known observable divergences remain;
- exception/control/cancellation policy is explicit;
- at least one bounded workload shows non-trivial opportunity and benefit;
- default-off fallback and authority equivalence are verified.

**G2 decision:** pass on frozen implementation `5210ac9`. No known observable
divergence remains in the admitted shape; cancellation and terminal dispositions are
typed and serialized; a real unchanged-Guest five-by-two experiment preserved exact
results with one physical call and measured a non-trivial critical-path reduction; and
the final independent post-fix review reported no Blocking, High or Medium finding.
This gate does not widen or enable the consumer: it remains default-off and accepts
only one exclusive exact live-only read.

### Gate G3 — paper-scope gate

Before combining pre-dispatch, reuse and placement into a paper claim, decide whether
the strongest honest contribution is:

- shared semantic overlay + one runtime consumer;
- shared overlay + pre-dispatch and exact reuse;
- shared overlay + pre-dispatch/reuse/placement;
- or a negative result about the cost of conservatism.

Do not implement breadth merely to fill a three-item contribution list.

## Global verification policy

Code slices use focused RED/GREEN tests, then proportionate gates. Final behavior
commits run:

```text
go test ./... -count=1
go test -race ./runtime/... ./cmd/... -count=1
go vet ./...
PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
python3 -m unittest discover -s tests -p 'test_*.py'
git diff --check
```

Additional requirements:

- Guest semantic changes require a rebuilt exact artifact and Linux real-Guest E2E;
- concurrency changes require focused race tests and deterministic cancellation/
  exceptional-order tests;
- placement changes require WASM/native matrix tests and explicit
  `not_started/not_started` negative coverage;
- performance evidence needs an off/control treatment, repeated bounded trials and
  structured machine-readable output;
- docs-only research slices use readback, link checking and `git diff --check` unless
  generated contracts require more;
- independent read-only review is required at G1, after the first runtime consumer,
  and before final closeout. Any edit makes an earlier review stale.

## Autonomous execution queue

### Track 0 — Truth reset and research contract

**Promise:** The successor starts from live implementation facts and verified related
work, not from the supplied handoff's unchecked assumptions.

- [x] Inspect live semantic, capability, placement, reuse and workload seams.
- [x] Write the architecture recommendation and successor roadmap.
- [x] Pin and verify primary papers/source; produce a claim-by-claim related-work
  matrix with evidence anchors and prohibited formulations.
- [x] Define the exact research questions, observable semantics and divergence
  oracle before implementing a graph.

**Primary files:**

- `docs/research/unified-effect-aware-runtime-architecture.md`
- `docs/research/effect-aware-related-work-matrix.md`
- `docs/research/effect-aware-observable-semantics.md`
- `docs/research/multi-agent-shared-execution-next-step.md`
- `docs/product-direction.md`
- this roadmap

**Gate:** document links, source pins, claim review, `git diff --check`.

### Track A — Generated-program opportunity corpus — **Complete**

**Promise:** Decide from data whether program-level semantic optimization has enough
coverage and opportunity to justify a bounded semantic overlay.

- [x] Define a body-safe versioned corpus schema binding source digest, target
  artifact/profile, tool contract set, provenance class and expected oracle class.
- [x] Add public synthetic adversarial fixtures for independent calls, conflicts,
  conditionals, exceptions, freshness, random/time, aliases, loops and dynamic calls.
- [x] Derive private real-workload seeds from existing Harness/session evidence where
  permitted; checked-in projections remain body-free.
- [x] Run the existing analyzer over the corpus and report accepted/opaque constructs,
  direct capability sites, whole-Run reuse eligibility and placement classes.
- [x] Produce an opportunity census for exact pre-dispatchable call sites, overlap
  windows, exact repeated regions and capability-driven placement, labeling
  structural candidates separately from proved legality.

**Likely files:**

- `research/effectgraph/`
- `research/evaluationworkloads/`
- `docs/evidence/effect-aware-opportunity-census.json`
- `docs/research/effect-aware-opportunity-census.md`

**Decision:** proceed to a minimal Track B/C slice. The exact 18-program census found
10 structural pre-dispatch call sites and five overlap windows, but proved no
legality. Ten programs were opaque; all three public runtime workloads hit barriers.
See the [Track A report](../research/effect-aware-opportunity-census.md) and
[machine evidence](../evidence/effect-aware-opportunity-census.json). This supports a
falsifiable overlay experiment, not a population-level opportunity claim.

**Do not:** expose private source/bodies; treat structural candidates as legal; call a
small fixture set representative.

### Track B — Minimal canonical effect-contract extension — **Complete**

**Promise:** Add only the Host-owned metadata that a measured legality question
requires.

Accepted v0 fields after Track A:

- one Host-authored logical read resource, keyed by an exact Python argument or a
  Host constant;
- exact `plan_epoch` freshness;
- mandatory `discard_with_disposition` handling for unclaimed physical work.

Determinism, coalescing/shareability, multi-resource sets, writes, arbitrary freshness,
exception transformation and backend requirements remain absent until measured.

- [x] Write strict v0 contract and identity/versioning tests before changing built-in
  specs.
- [x] Keep absent/unknown metadata maximally conservative.
- [x] Extend sealed Plan/spec identity so semantic changes invalidate analysis and
  execution identity.
- [x] Annotate only a checked-in capability fixture and the Track A research fixture;
  leave production built-ins unqualified until a real freshness snapshot exists.
- [x] Verify generated Python/direct tool surfaces remain presentation only and
  receive no authority from metadata.

**Primary files:**

- `runtime/capability/registry.go`
- `runtime/capability/plan_test.go`
- `runtime/semantic/analyzer.go`
- capability fixtures and tests

**Decision:** capability-plan v5 replaces the undifferentiated `SpeculativeSafe` bit
with one exact `PreDispatchContract{resource, plan_epoch,
discard_with_disposition}`. It is necessary but never sufficient for legality; Track C
must supply source occurrence, canonical arguments, control and dependency facts. See
[the Track B contract](../research/effect-aware-contract-v0.md).

**Do not:** infer purity from HTTP method, capability name, `ReadOnly`, or
`Idempotent`; create a second effect registry.

### Track C — Target-Guest Verified Semantic Overlay v0

**Promise:** Attach bounded source-indexed facts to the target-Guest CPython AST and
emit only the canonical verified overlay needed for one real legality question. The
overlay is not a general compiler IR or an executor.

Minimum candidate content:

- stable source-located nodes for pure compute, capability/WASI call, branch/merge,
  return and raise;
- basic-block/control-region identity;
- bounded definitions/uses and conservative data dependencies;
- control dependencies or explicit branch containment;
- effect/resource/capability projection references;
- exception/cancellation markers;
- explicit unknown/barrier reasons.

The implementation deliberately selected only the measured subset needed for the
first question: exact top-level capability call records, canonical literal arguments,
module-entry control identity and a strict must-reach bit. Pure-compute nodes, general
branch/merge regions, definitions/uses and exception graphs remain deferred because
v0 does not consume them.

- [x] Freeze schema, bounds, canonical ordering and identity.
- [x] Add Guest RED tests for straight-line independent calls and adversarial
  constructs.
- [x] Implement target-Guest extraction for only the accepted subset.
- [x] Add strict Host decoder/validator with overlay consistency and effect-coverage
  checks.
- [x] Bind overlay provenance through `VerifiedAnalysis` with unchanged opacity and
  Runner-property validation.
- [x] Run real-Guest parity/negative E2E and corpus census.

**Primary files:**

- `guest/bootstrap/agent_runtime/semantic.py`
- `guest/tests/test_semantic_analysis.py`
- `runtime/semantic/contract.go`
- `runtime/semantic/analyzer.go`
- `runtime/semantic/verified.go`
- `integration/e2e/semantic_test.go`

**Decision:** G1 continues to Track D with the runtime consumer still disabled. The
19-program follow-up narrows 11 structural annotations to four exact overlay call
records and one necessarily-reached record; Linux ARM64 reproduces the exact local
machine evidence. See [overlay v0](../research/verified-semantic-overlay-v0.md) and the
[census follow-up](../research/effect-aware-opportunity-census.md).

**Do not:** add SSA, general alias analysis, arbitrary heap regions, statement
execution, or Host-side Python parsing.

### Track D — Shared legality engine and divergence oracle

**Promise:** One set of fail-closed predicates consumes the verified representation;
optimization passes do not invent their own semantics.

- [x] Define explicit observable trace model: result/exception, ordered Host effects,
  workspace disposition, capability calls, freshness/snapshot identity,
  cancellation and terminal ambiguity.
- [x] Implement pure predicates with typed rejection reasons:
  `CanPreissue`, `CanClaimStagedObservation`, `CanCoalesce`, `CanCache`,
  `RequiredBackend`.
- [x] Keep `CanHoist` disabled until branch/exception reachability is represented.
- [x] Build baseline-vs-candidate trace comparator and adversarial matrix.
- [x] Compare overlay predicates with a call-level resource-annotation baseline.
- [x] Run G1 review and decision gate.

**G1 decision:** pass. The accepted subset is exactly one module-entry,
necessarily-reached, single-occurrence scalar `sources.read` shape with a sealed
read-only pre-dispatch contract, exact resource/freshness/authority/lineage/privacy
identity and a Host-owned budget reservation. The first consumer may only be a
default-off Track E spike over `runtime/streaming.StagedObservation`; it must atomically
consume the budget and claim the observation once at the unchanged dynamic Python
Host-call boundary. Writes, conditional calls, durable caching, coalescing, hoisting,
replay and backend inference remain excluded. The reviewed comparator treats every
unclaimed physical operation as a divergence.

**Likely files:**

- `runtime/semantic/legality.go`
- `runtime/semantic/legality_test.go`
- `research/effectgraph/`
- `integration/e2e/semantic_legality_test.go`

### Track E — Experimental semantic pre-dispatch

**Promise:** If G1 passes, exact Host-qualified reads may start before unchanged
Python reaches their call boundary; the dynamic call claims the matching run-scoped
staged observation instead of issuing a duplicate physical request. The overlay is
analysis-only and the original source remains executable authority.

- [x] Spike by connecting verified semantic call facts to the existing
  `runtime/streaming.StagedObservation`; add no second cache or execution ABI.
- [x] RED-test data/control/effect conflicts, exception ordering, cancellation,
  freshness, aliases and unknown contracts.
- [x] Implement default-off pre-dispatch for one exact call shape; leave original
  Python and its dynamic Host-call boundary unchanged.
- [x] Preserve deterministic result placement and baseline exception/effect order.
- [x] Add runtime observations distinguishing logical calls, issue/start/finish,
  physical operations and rejected opportunities.
- [x] Run exact Guest differential E2E and bounded latency/critical-path experiment.
- [x] Independent post-fix review; G2 passed on frozen `5210ac9` with no Blocking,
  High or Medium finding.

**Do not:** rewrite Python, execute the overlay, pre-dispatch writes or unknown calls,
turn one-shot observations into durable cache records, merge repeated logical calls
without an explicit coalescing contract, or abandon cancellation/late/orphaned
physical requests without a typed terminal disposition.

### Track F — Shared identity and exact region reuse

**Promise:** Reuse consumes the same verified semantic facts and never weakens the
current exact whole-Run contract.

- [x] Script repeated focused/full/Guest/Lab gates and read-only delivery verification;
  keep commit, push and deployment as explicit separate actions.
- [x] Refactor current whole-Run qualification to consume shared legality outputs
  without behavior change; full/race/Guest gates preserve one physical compute across
  leader/waiter/retained outcomes.
- [x] Define one analysis-only candidate region graph with exact source spans,
  control/effect barriers, canonical live-ins/live-outs, capability occurrences and
  stable rejection reasons. It carries no execution authority and must not require
  Python heap capture.
- [x] Project the candidate graph into Lab through a versioned, bounded, privacy-safe
  read model. Lab Web must highlight the selected region in the recorded Python source,
  render control/data edges, effect classes and opaque barriers, and explain per-consumer
  eligibility/rejection without gaining any Runtime control or authority.
- [ ] Measure natural same-agent/cross-agent exact overlap and region materializability
  in the corpus using that candidate region contract.
- [ ] Admit one executable region form only if the measured graph has canonical
  live-ins/live-outs and exact source boundaries without Python heap capture.
- [ ] Bind contract/resource snapshot/freshness identity, project/privacy/policy and
  runtime versions.
- [ ] Preserve zero-effect runtime publication probe, failure nonpublication,
  cancellation, corruption/eviction and size gates.
- [ ] If region identity cannot be made exact, record the negative result and retain
  whole-Run-only reuse.

**Primary files:**

- `runtime/agentfunction/semantic.go`
- `runtime/semanticreuse/reuse.go`
- `runtime/semantic/`
- `integration/e2e/semantic_reuse_test.go`
- `research/labview/`
- `apps/lab-web/`

### Track G — Semantic pre-execution placement

**Promise:** Verified capability requirements may improve the existing placement
decision before execution, never authorize replay.

- [ ] Compare current import/requirements routing with overlay-derived capability
  requirements.
- [ ] Extend decision identity and reasons only if semantic analysis adds measurable
  precision.
- [ ] Route `SUPPORTED_BY_PYSOLATE`, `REQUIRES_NATIVE`, or `UNKNOWN`; unknown selects
  native/unavailable before execution according to Host policy.
- [ ] Preserve typed L2 promotion only for proven `not_started/not_started`.
- [ ] Add cross-backend capability/profile conformance and negative replay tests.

**Primary files:**

- `runtime/placement/placement.go`
- `runtime/placement/placement_test.go`
- `runtime/semantic/`
- native/Wazero integration tests

### Track H — Evaluation, paper boundary and closeout

**Promise:** The final claim reflects measured conjunctions rather than a list of
implemented mechanisms.

- [ ] Run G3 and choose the narrowest defensible paper scope.
- [ ] Evaluate baseline vs enabled treatments for latency, critical path, tool calls,
  physical executions, cache/coalescing, CPU/memory, placement and analysis overhead.
- [ ] Report optimization coverage and conservative rejection cost.
- [ ] Run adversarial observable-divergence suite and independent P0/P1 review.
- [ ] Update architecture, threat model, mechanism matrix, product direction and
  related work with Current/Experimental/Observed/Deferred labels.
- [ ] Verify exact Guest artifacts, structured evidence, signatures, clean tree and
  `HEAD == @{u}`.

## Per-slice execution protocol

For every implementation slice:

1. inspect live Git and the governing contract;
2. add a focused failing test, or record why a research/docs-only slice has no RED;
3. implement the minimum change;
4. run focused tests and relevant real Guest/platform evidence;
5. inspect the diff for authority or claim widening;
6. update this roadmap immediately with evidence and next pointer;
7. run proportionate global gates;
8. request bounded independent review at the declared boundaries;
9. signed commit and push;
10. verify signature, upstream and clean status;
11. continue unless a decision gate or stop condition applies.

## Roadmap tracking rules

- Trust live source/Git over this narrative after any interruption.
- Mark `[x]` only after real artifact/gate evidence.
- Keep structural opportunity, static legality, runtime enforcement and performance
  evidence separate.
- Record source/artifact/profile/contract identities for real Guest observations.
- A signed commit is a checkpoint, not proof of the next track.
- If results change the overlay, effect model or paper thesis, stop at G1/G2/G3 and
  discuss rather than silently rewriting later tracks.
- Do not append implementation history to the completed predecessor roadmap.

## Stop conditions

Stop only when:

1. all accepted executable tracks and final gates are complete;
2. G1, G2 or G3 requires a research/architecture decision from Yuzhe;
3. a required private corpus, exact Guest artifact, approved Linux environment or
   external source cannot be obtained safely;
4. repeated gates expose a design contradiction requiring user choice;
5. continuation would require authority widening, effect replay, a broad compiler
   rewrite, or another explicitly prohibited mechanism.

If blocked, report the exact decision/evidence needed, modified files, tests run, Git
status and safest alternatives. A low opportunity result is a valid research outcome,
not permission to manufacture a broader optimizer.

## Current execution pointer

**Track F:** first script the repeated gate/evidence workflow and refactor existing
whole-Run reuse to consume shared legality without behavior change. Then implement an
analysis-only candidate region graph and its read-only Lab visualization before any
region-level runtime consumer. Use the graph to measure materializability and natural
overlap; do not admit region execution/reuse if it requires Python heap capture or
weakens exact identity.

## Short prompt to start/resume this Mega-Goal

```text
Read `docs/plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md` fully, then execute it in `~/projects/agent-python-runtime`. Keep target-Guest analysis, Host-owned contracts, no authority widening, and no replay after possible effects. Update the roadmap, run real gates, signed commit and push each coherent slice; continue until a named G1/G2/G3 decision gate, real blocker, or completion.
```
