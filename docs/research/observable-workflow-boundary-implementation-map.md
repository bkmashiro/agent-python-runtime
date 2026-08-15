# Observable workflow-boundary implementation map

Status: **Implemented and evaluated; executable region reuse and semantic placement rejected**
Date: 2026-08-15
Baseline: `906016685ad75e1da261d094be52daa827f53829`

## Frozen vocabulary

- A **logical request** is one Host-visible request made by one task/Run/workflow node.
  It remains observable even when no new physical work is started.
- A **physical execution** is one actual model, Guest, Host capability, tool, or explicit
  workflow compute operation. It has a Host-minted identity and terminal disposition.
- **Preissue** moves one qualified physical issue earlier; it does not remove the logical
  request.
- **Declared-independent overlap** keeps separate physical executions and overlaps only
  requests whose independence is explicit in the Harness/workflow contract or separately
  proved by the Host.
- **Coalescing** maps concurrent exact logical requests to one in-flight physical
  execution. One producer and every waiter remain visible.
- **Retained reuse** maps a later exact logical request to a completed producer while its
  freshness, authority, privacy, policy, artifact and input identities remain valid.
- **Placement** is a pre-execution Host decision. It cannot authorize replay or migration
  after logical or physical work may have started.

These terms describe observation and legality. They carry no execution authority.

## Existing mechanisms to preserve

| Mechanism | Live implementation | Existing identity/evidence | Required successor work |
|---|---|---|---|
| Host mechanism off/selected/fallback | `runtime/mechanisms.go` | `pysolate.mechanisms.v2`; zero value is all-off | Add new observation consumers without changing the off-state |
| Staged observation | `runtime/streaming/observation.go` | exact source occurrence, arguments, capability spec/handler/plan/grant, freshness, privacy and lineage; one-shot terminal ownership | Project issue/claim/terminal relations into the canonical optimization observation |
| Semantic pre-dispatch | `runtime/semantic/predispatch.go` | verified occurrence plus Host budget reservation; aggregate physical/logical counters and typed disposition | Add stable logical/physical identities and event timestamps; do not widen its one-call shape |
| Exact Agent Function reuse | `runtime/agentfunction/cache.go` | canonical invocation identity binds project, source, artifact/profile/imports, inputs, roots, deterministic settings, output schema, privacy and policy | Emit producer/consumer observations for independent, leader, waiter and retained outcomes |
| In-flight single-flight | `runtime/agentfunction/singleflight.go` | exact invocation key; cancellation isolation; no completed retention | Reuse the Agent Function identity and expose leader/waiter-to-physical mapping |
| Explicit workflow graph | `runtime/workflow/evaluator.go` | graph digest binds `WorkflowID`, ordered node IDs, node versions, kinds and dependencies; state records bind node identity/value/freshness/policy | Use this explicit identity at the observation seam; do not infer workflow identity from AST similarity |
| Whole-Run semantic reuse | `runtime/semanticreuse`, `runtime/agentfunction/semantic.go` | opaque verified whole-Run proof narrows the existing Agent Function invocation | Instrument the existing consumer only; no smaller executable region |
| Placement | `runtime/placement/placement.go` | request, static analyzer, shard, state and parent decision identities; only typed L2 `not_started/not_started` promotion | Measure semantic precision before any v2 decision contract |
| Runtime correlation | `runtime/execution_ref.go` | Host-authored Agent Run, turn/output/segment, invocation/attempt and execution IDs | Bridge these IDs into the new evidence model without placing them in `RunRequest` or authority identity |
| Composable evidence | `runtime/composable/evidence.go` | body-free mechanism, observation, child, cache, flight, workflow, Guest and COW aggregates | Keep as historical mechanism evidence; do not overload it with the new experiment schema |
| Lab projection/UI | `research/labview`, `apps/lab-web` | bounded read models, completeness/privacy refs, causal trace adapters and timeline fixtures | Add a separate versioned optimization projection and measured paired-treatment views |

## Frozen negative decisions

The F2 evidence in `docs/evidence/python-region-census-v0.json` measured 19 programs,
69 candidate regions, zero conservatively materializable regions and zero cross-program
materializable exact overlap. `consumer_admitted=false` is final for this successor.

Therefore this Mega-Goal will not add:

- executable AST regions or region cache identities;
- semantic similarity or graph containment matching;
- automatic AST-derived sibling scheduling;
- Python heap snapshots or recovery;
- a second Python parser/executor;
- ordinary filesystem calls rewrapped as tools for optimization coverage.

The region graph remains analysis-only explanation and placement input.

## Genuine gaps

1. There is no single canonical body-free record mapping logical requests to physical
   executions, producer/consumer relationships, optimization decisions and rejection
   reasons across mechanisms.
2. Existing mechanism statistics are mostly aggregates. They cannot reconstruct a
   multi-task optimization timeline or prove which task supplied a reused result.
3. Existing explicit workflow node identity is sufficient to avoid a broad Harness
   protocol change, but it is not yet bridged into Agent Function/pre-dispatch evidence.
4. Capability plan v5 intentionally lacks tool coalescing and retained-read contracts.
   No such behavior may be inferred from read-only/idempotent metadata. Track D must add
   a contract only if the prepared workload proves a necessary bounded case.
5. `semantic.RequiredBackend` remains `UNKNOWN`; placement currently uses static
   imports/requirements only. Precision must be measured before changing routing.
6. No repository-owned identity-bound `gpu31` build-cache entry point exists.
7. No seeded shuffled paired-treatment workload binds model/Guest/Host/tool phases into
   sealed evidence.
8. Lab cannot yet show logical-to-physical links, preissue lead time, overlap,
   coalescing/reuse provenance, rejection reasons or measured baseline deltas.

## Track 0 gate

The stable identity prerequisites already exist: explicit workflow graph/node identity,
exact Agent Function invocation identity, Host invocation/execution references, staged
observation identity and typed placement outcomes. Track C therefore does not require a
broad Harness protocol change.

Proceed to Track A, then define one new bounded canonical observation contract in Track B.
Do not implement a new optimizer until the contract can represent existing mechanisms and
the seeded workload exposes a measured gap.

## Closeout mapping

| Initial gap | Resolution |
|---|---|
| Canonical logical→physical record | `pysolate.workflow-boundary-observation.v0`; every physical-sharing consumer requires an admitted coalesced/reused decision |
| Producer/consumer timeline | Sealed reports bind measured Host spans, producer and complete consumer reverse indexes |
| Workflow identity bridge | Existing explicit `WorkflowID`/node/occurrence identity is used directly; no AST-derived identity or protocol widening |
| Tool optimization gap | Prepared Harness admits only exact preissue, explicit independence, exact in-flight coalescing and exact retained reads; external writes remain denied |
| Semantic placement unknown | Measured `no_go`: zero safe gains and 19 baseline regressions; current router retained |
| Workstation build cache | Identity-bound layer plus exact-source final cache; bounded 2+2 keys and verified cold/warm evidence |
| Seeded workload | 14 fixed-seed shuffled tasks, 25→23 physical reads, zero observable divergence |
| Lab projection | Existing Lab extended with a sealed read-only paired timeline and no execution controls |

The final quantitative and claim boundary is
[`observable-workflow-boundary-evaluation-v0.md`](observable-workflow-boundary-evaluation-v0.md).
