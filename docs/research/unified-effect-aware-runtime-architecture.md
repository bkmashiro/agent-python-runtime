# Unified effect-aware runtime: architecture recommendation

Status: **Accepted research direction; implementation remains decision-gated**

Date: 2026-08-14

Baseline: `3bd022fa074f8e8178b9bdd0fd9efaa5e4f8c37c`

Active execution roadmap: [`../plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md`](../plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md)

## Decision

Evolve Pysolate toward:

> **a unified effect-aware runtime for semantically inspectable agent-generated
> programs**

The runtime should treat generated Python as a bounded semantic interface rather than
only an opaque sandbox payload. The exact target Guest derives program-structure
facts; the Host combines those facts with frozen capability/WASI contracts and uses
one verified representation to answer conservative scheduling, exact reuse and
pre-execution placement questions.

This direction complements rather than replaces the authority-lifecycle runtime.
Semantic visibility may reduce or reorganize physical work, but it never creates
execution authority, changes workspace/effect truth, or weakens terminal disposition.

## Why this direction

Generic code execution, isolation, prepared runtimes and filesystem snapshots are
valuable substrate mechanisms but weak top-level differentiation. Pysolate's more
specific conjunction is:

1. the Harness sees agent-generated Python before execution;
2. the exact target Guest can parse that Python with its own language semantics;
3. externally authoritative operations cross frozen Host-owned boundaries;
4. capability definitions already carry identity and effect-related metadata;
5. the Host already binds analysis, artifact/profile/import/capability identity;
6. exact whole-Run singleflight/reuse and pre-execution placement already provide
   narrow consumers for richer semantic facts.

The research question is whether these facts can support a useful shared semantic
layer without turning Pysolate into a general Python compiler.

## Current, observed and proposed

### Current

- Fresh CPython/WASI Runs with frozen per-Run authority and Host-owned workspace,
  effect and evidence disposition.
- Canonical `capability.Spec` and sealed Plan identity shared by registration,
  Broker validation and Python/direct-tool presentation.
- Target-Guest AST semantic analyzer with function/SCC effect summaries and explicit
  barriers.
- Strict Host `Analysis`/`Plan` validation and opaque verified provenance.
- Experimental exact whole-Run qualified singleflight/result retention.
- Static import/requirement placement and typed `not_started/not_started` native
  promotion.

### Observed

- One constructed exact workload demonstrated one physical Guest for concurrent
  duplicate logical invocations plus a later retained hit.
- Current semantic summaries are sufficient to reject unknown/live/publishing Runs
  for whole-Run reuse.
- Current descriptors do not establish natural program-level scheduling opportunity,
  graph-analysis coverage or general workload benefit.

### Proposed

- A body-safe opportunity corpus and census.
- A minimal versioned Semantic Execution Graph for an accepted Python subset.
- Minimal Host-owned resource/freshness/exception/cancellation contract extensions.
- Shared fail-closed legality predicates.
- One default-off straight-line sibling-call scheduler if the opportunity gate passes.
- Later exact region identity and semantic placement integration only after explicit
  decision gates.

## Trust and authority architecture

```text
Agent-generated source
        |
        v
exact packaged target Guest CPython
  AST / bounded CFG / def-use / call-site facts
        |
        | canonical bounded report; no authority
        v
Host strict decoder + graph verifier
        + exact source/artifact/profile/import binding
        + sealed capability/WASI contract binding
        + policy/privacy/snapshot identity
        |
        v
opaque VerifiedSemanticProgram
        |
        +--> legality predicates
        +--> exact identity qualification
        +--> pre-execution placement
        |
        v
selected runtime path + ordinary Host enforcement
```

The Guest cannot assert that an operation is pure, shareable, fresh, authorized or
safe to reorder. It reports syntax-derived structure and references. The Host owns
contract meaning and rejects reports that under-approximate effects, violate graph
consistency or mismatch frozen runtime identities.

## Semantic representation recommendation

Do not replace the existing `Analysis` and `Plan` immediately. First measure whether
a graph enables a legality question they cannot answer. If justified, add a thin
versioned graph containing only:

- stable source-located node identity;
- node category such as pure compute, capability/WASI call, branch/merge, return or
  raise;
- basic-block or conservative control-region identity;
- bounded definitions and uses;
- data dependencies;
- control dependencies or explicit branch containment;
- references to Host-owned effect/resource contracts;
- capability/backend requirements;
- exception and cancellation markers;
- explicit unknown/barrier reasons.

The first graph should not contain:

- general SSA or phi semantics beyond what one legality predicate needs;
- arbitrary object/heap aliasing;
- normalized semantic program equivalence;
- executable authority or Host grants;
- physical scheduling decisions;
- cached values or result bodies.

Graph identity must bind the exact source region, analyzer/schema, artifact, execution
profile, import closure and sealed capability Plan. Host-decoded reports remain
bounded, canonical and unknown-field rejecting.

## Canonical effect contracts

The existing `capability.Spec` remains the only canonical typed capability definition.
Its current fields—effect class, playback, handler/version identity, schemas,
`ReadOnly`, `Idempotent` and `SpeculativeSafe`—are not sufficient by themselves to
prove parallelism, hoisting or reuse.

Only after the opportunity census identifies the first concrete legality question,
consider bounded additions for:

- canonical resource reads and writes;
- determinism;
- coalescing/shareability scope;
- freshness or captured-snapshot semantics;
- exception behavior;
- cancellation behavior;
- backend requirements.

Rules:

- absence means unknown and blocks the optimization;
- `GET`, `ReadOnly` and `Idempotent` never imply purity or freshness;
- every semantics-relevant change affects sealed Plan/spec identity;
- resource expressions are Host-defined templates over schema-validated arguments,
  not arbitrary Guest expressions;
- ordinary Python/WASI filesystem operations remain ordinary stdlib calls and are
  modeled separately without tool wrappers.

## Shared legality API

Optimization passes should consume pure Host predicates with typed rejection reasons:

```text
CanParallelize(left, right, context)
CanCoalesce(region, context)
CanCache(region, context)
RequiredBackend(program, context)
```

`CanHoist` should not be implemented initially. Moving a call across control flow can
change whether it executes, exception order, cancellation, freshness and resource
lifetime. The first scheduler should handle only necessarily executed sibling calls
whose arguments are independently available and whose effects/resources do not
conflict.

Unknown input to any predicate returns rejection, never a weaker assumption.

## Observable-equivalence boundary

Baseline and optimized execution must be compared over at least:

- canonical result or exception;
- ordered Host capability/effect observations;
- workspace start/final identity and disposition;
- freshness/snapshot identity used by external reads;
- cancellation and timeout outcome;
- physical/logical operation identities;
- terminal ambiguity or reconciliation state.

A digest or static graph validates identity and structure, not semantic correctness.
Runtime Host enforcement and differential/adversarial evidence remain necessary.

## Execution placement boundary

The existing placement layer is the owner of WASM/native choice. Verified semantic
capability requirements may improve its pre-execution input, but they do not create a
new fallback mechanism.

Allowed:

```text
verified source requirements -> pre-execution WASM/native/unavailable decision
```

Also allowed is the existing typed L2 promotion when the Host proves:

```text
workspace = not_started
and effects = not_started
```

Forbidden:

- exception-text routing;
- rerunning after code or an effect may have executed;
- describing a new native attempt as continuation;
- generic rollback of external effects;
- mid-program authority escalation or backend migration.

## Initial falsifiable sequence

1. **Truth and opportunity:** source-pin related work, define observables, collect a
   body-safe generated-program corpus, and measure current analyzer coverage plus
   structural opportunities.
2. **Minimum contract:** add only metadata required by one measured candidate.
3. **Graph:** implement the smallest target-Guest graph and strict Host verifier that
   can answer that candidate's legality question.
4. **Legality and oracle:** compare shared predicates against baseline traces and a
   call-level annotation baseline.
5. **First transformation:** if admitted, schedule only straight-line necessarily
   executed sibling calls; no conditional hoisting.
6. **Decision:** inspect correctness, opportunity and benefit before extending exact
   region reuse or placement.

A low opportunity or high conservatism result is a legitimate research outcome.

## Related-work claim discipline

The supplied handoff identifies likely comparisons with AsyncFC, Agent JIT, PASTE,
workload-aware caching, CaMeL, A1, ARIES and public Cloudflare systems. Those
comparisons are hypotheses until their primary papers and pinned public sources are
rechecked.

The intended distinction is not “others are unsafe” or “others do not inspect code.”
It is whether Pysolate can combine exact generated-program structure and Host-owned
effect contracts into one runtime representation reused across more than one
correctness-aware decision.

Do not claim:

- invention of safe parallel tool execution;
- automatic inference of tool effects;
- novelty from AST access alone;
- formal verification without semantics and proof;
- general arbitrary-Python optimization;
- Cloudflare or another system lacks non-public internal behavior.

## Rejected immediate approaches

- Implement scheduling, hoisting, region caching and placement simultaneously.
- Build a broad CFG/SSA/alias framework before measuring opportunity.
- Duplicate capability effects in an optimizer registry.
- Parse target Python on the Host with a different parser.
- Treat current whole-Run reuse economics as evidence of natural overlap.
- Use speculative future-call prediction as the first scheduler.
- Wrap all WASI/stdlib filesystem operations as typed Pysolate tools.
- Add transparent VM replay after a dynamic capability failure.
- Use semantic similarity or cross-tenant caching.

## Open questions resolved by the roadmap

1. How much natural generated-program coverage survives conservative analysis?
2. Which missing contract field blocks the most valuable safe opportunity?
3. Does a graph materially outperform call-level resource annotations?
4. Can exception/cancellation order be preserved by a useful sibling-call subset?
5. Does one shared representation actually reduce duplicated policy across reuse and
   placement?
6. Is the strongest paper result scheduling, exact reuse, placement, their shared
   representation, or the measured cost of conservatism?

These questions are decision gates, not assumptions to code around.
