# Unified effect-aware runtime: architecture recommendation

Status: **Accepted research direction; implementation remains decision-gated**

Date: 2026-08-14

Baseline: `3bd022fa074f8e8178b9bdd0fd9efaa5e4f8c37c`

Active execution roadmap: [`../plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md`](../plans/2026-08-14-unified-effect-aware-runtime-autonomous-megagoal.md)

Frozen research contracts:

- [source-pinned related-work truth matrix](effect-aware-related-work-matrix.md)
- [observable-semantics and divergence contract](effect-aware-observable-semantics.md)

## Decision

Evolve Pysolate toward:

> **a unified effect-aware runtime for semantically inspectable agent-generated
> programs**

The runtime should treat generated Python as a bounded semantic interface rather than
only an opaque sandbox payload. The exact target Guest derives program-structure
facts; the Host combines those facts with frozen capability/WASI contracts and uses
one verified overlay to answer conservative pre-dispatch, exact reuse and
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
- A minimal versioned source-indexed semantic overlay for an accepted Python subset.
- Minimal Host-owned resource/freshness/exception/cancellation contract extensions.
- Shared fail-closed legality predicates.
- One default-off semantic pre-dispatch consumer backed by existing staged
  observations if the opportunity gate passes.
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

Track C keeps the existing `Analysis` and adds the smallest measured overlay rather
than a general graph. `pysolate.semantic-analysis.v2` contains bounded source-indexed
call records for exact top-level projected calls with scalar literal arguments. Only a
first executable module call may assert `necessarily_reached`; later straight-line
calls remain structural but not necessarily reached, while calls under control flow,
aliases and dynamic arguments are omitted as unknown.

Each record binds exact source occurrence, capability, module-entry control region,
canonical arguments and one dynamic occurrence. The Host recomputes its source and
control identities, checks canonical argument names against the sealed projection and
requires conservative effect coverage. The complete report remains bound to analyzer
schema, artifact, execution profile, import closure and capability Plan through opaque
`VerifiedAnalysis`.

See [the verified semantic overlay v0](verified-semantic-overlay-v0.md). CFG, def-use,
SSA, arbitrary heap/resource aliasing, conditional reachability, executable authority,
physical scheduling decisions and cached result bodies remain deliberately absent.
They may be added only when a later legality predicate cannot stay sound without them.

## Canonical effect contracts

The existing `capability.Spec` remains the only canonical typed capability definition.
Capability-plan v5 adds one optional, bounded `PreDispatchContract` beside effect
class, playback, handler/version identity, schemas, `ReadOnly` and `Idempotent`. The
contract names one argument- or constant-keyed logical read resource, admits only the
exact `plan_epoch` freshness mode, and requires unclaimed physical work to end with a
typed discard disposition. The old undifferentiated `SpeculativeSafe` bit is removed.

This is the minimum Host-owned metadata required by the Track A question. It does not
by itself prove control reachability, argument availability, resource non-conflict or
claim identity. Later contract fields should be added only when a measured legality
question requires them:

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

## Representation boundary

The target Guest analyzes the ordinary CPython AST directly. Guest-local facts may
live in side tables keyed by exact source-located AST occurrences. Only a bounded,
canonical source-indexed semantic overlay crosses the Guest/Host boundary so the Host
can validate references, bind capability contracts and mint opaque provenance.

The overlay is an intermediate representation only in the broad sense of being an
analysis report. It is not SSA, bytecode, a rewritten Python program or an executable
IR. Its digest is qualification/provenance input, not a semantic-equivalence or cache
key by itself. Original Python remains the executable authority.

## Shared legality API

Optimization passes should consume pure Host predicates with typed rejection reasons:

```text
CanPreissue(call, context)
CanClaimStagedObservation(call, observation, context)
CanCoalesce(region, context)
CanCache(region, context)
RequiredBackend(program, context)
```

`CanHoist` should not be implemented initially. Moving a call across control flow can
change whether it executes, exception order, cancellation, freshness and resource
lifetime. The first runtime consumer does not rewrite Python or execute a graph. It
may pre-dispatch only exact calls whose canonical arguments, resource proof and
Host-owned `read_only + idempotent + pre_dispatch{resource, plan_epoch,
discard_with_disposition}` contract all admit. The result enters the existing
one-shot, run-scoped staged-observation path and unchanged Python claims it at the
original call boundary.

Unknown input to any predicate returns rejection, never a weaker assumption.

## Execution model: pre-dispatch, then claim

The semantic overlay is analysis-only and ordinary execution remains the exact
original Python source. After verified analysis and before Guest execution, the Host
may issue a bounded set of exact qualified reads. Each result enters the existing
`runtime/streaming.StagedObservation` mechanism. When original Python reaches the
corresponding Host-call boundary, it claims that one-shot result instead of issuing a
duplicate physical request.

This is not durable result caching. The existing observation identity already binds:

- source and suite range;
- dynamic occurrence and canonical arguments;
- capability spec, handler, Plan and grant policy;
- freshness/expiry and privacy partition;
- stream/workflow lineage.

Full-source semantic pre-dispatch should reuse and, only where required, generalize
that identity rather than create a second cache. Equal repeated calls remain distinct
occurrences by default. Cancellation, timeout, late completion and a call that is
never dynamically reached terminate as explicit physical dispositions; they do not
become logical calls or retained records.

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
5. **First runtime consumer:** if admitted, pre-dispatch exact qualified reads into
   the existing run-scoped staged-observation path; unchanged Python claims the
   result at the original call boundary. No source rewrite or graph execution is
   involved.
6. **Decision:** inspect correctness, opportunity and benefit before extending exact
   region reuse or placement.

A low opportunity or high conservatism result is a legitimate research outcome.

## Related-work claim discipline

The supplied handoff identifies comparisons with AsyncFC, Agent JIT, PASTE,
workload-aware caching, CaMeL, A1, ARIES and public Cloudflare systems. Their
public claims are now frozen in the
[related-work truth matrix](effect-aware-related-work-matrix.md); unpublished
behavior remains unknown.

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

- Implement pre-dispatch, hoisting, region caching and placement simultaneously.
- Build a broad CFG/SSA/alias framework before measuring opportunity.
- Duplicate capability effects in an optimizer registry.
- Parse target Python on the Host with a different parser.
- Treat current whole-Run reuse economics as evidence of natural overlap.
- Use unqualified future-call prediction or rewrite Python source for the first
  pre-dispatch experiment.
- Wrap all WASI/stdlib filesystem operations as typed Pysolate tools.
- Add transparent VM replay after a dynamic capability failure.
- Use semantic similarity or cross-tenant caching.

## Open questions resolved by the roadmap

1. How much natural generated-program coverage survives conservative analysis?
2. Which missing contract field blocks the most valuable safe opportunity?
3. Does an overlay materially outperform call-level resource annotations?
4. Can qualified pre-dispatch preserve exception/cancellation boundaries while
   producing useful overlap?
5. Does one shared representation actually reduce duplicated policy across reuse and
   placement?
6. Is the strongest paper result pre-dispatch, exact reuse, placement, their shared
   representation, or the measured cost of conservatism?

These questions are decision gates, not assumptions to code around.
