# Shared effect-aware legality and differential oracle v0

Status: **Track D implemented; G1 review pending**

Track D introduces no runtime transformation. It provides a single fail-closed Host
join for verified Guest facts, sealed capability-plan v5 metadata and frozen per-Run
identity, plus an executable observable-trace comparator. No predicate starts work.

## Shared legality

`runtime/semantic/legality.go` defines typed decisions for:

- `CanPreissue`
- `CanClaimStagedObservation`
- `CanCoalesce`
- `CanCache`
- `RequiredBackend`

`CanPreissue` is the only positive v0 predicate. It requires the conjunction of:

1. opaque `VerifiedAnalysis` provenance;
2. the exact sealed capability-plan identity;
3. one exact source-bound call-site ID;
4. `necessarily_reached=true` and `dynamic_occurrence=1`;
5. canonical scalar arguments and an instantiable logical resource identity;
6. a sealed eligible `PreDispatchContract` and exact observation binding;
7. frozen stream, workflow, freshness, expiry, privacy and parent-lineage identity;
8. a non-zero physical-read budget.

A successful decision carries an opaque `QualifiedCall`; callers cannot construct its
private proof fields. `CanClaimStagedObservation` compares a body-free claim against
that exact call, capability/spec/handler/plan/grant, occurrence, argument, epoch,
privacy and lineage identity. It remains a pure check: Track E must atomically enforce
one-shot state and typed unclaimed disposition.

Capability-plan v5 has no coalescing, durable-cache or backend-requirement contract.
`CanCoalesce`, `CanCache` and `RequiredBackend` therefore return typed rejections.
`CanHoist` is deliberately absent.

## Observable trace and differential oracle

`runtime/semantic/trace.go` defines bounded traces for:

- result or exception terminal class and payload;
- ordered logical Host events and required predecessor edges;
- capability/resource/argument/result identity;
- freshness and authority identity;
- workspace start/final state and disposition;
- cancellation, ambiguity, reconciliation and effect disposition;
- qualified speculative physical work and consumed/cancelled/late/orphaned outcomes;
- explicit post-effect replay rejection.

The comparator ignores only qualified physical scheduling differences whose logical
trace is unchanged and whose unclaimed work has a typed terminal disposition. Missing
or extra logical effects, event-order changes, argument/resource drift, freshness,
authority, workspace, cancellation, ambiguity and replay are classified as typed
divergences. Invalid or unqualified traces fail as `trace_unclassifiable`.

The executable matrix at
[`docs/evidence/effect-aware-differential-oracle.json`](../evidence/effect-aware-differential-oracle.json)
contains 16 cases. All 16 matched their expected result, including one equivalent
qualified discard and adversarial terminal, result, argument, missing/extra effect,
sequence/edge order, freshness, authority, workspace, cancellation, ambiguity,
post-effect replay and invalid/unqualified-physical cases. Report SHA-256:
`83dc9cbf71bd922916113cb14c6d3627a50595c30f5d28c415d41368a952664a`.
Cross-compiled ARM64 semantic and effectgraph test binaries passed on Linux
`6.12.0-202.76.4.1.el9uek.aarch64`; binary SHA-256 values were
`3ab52e4bb693c43bf3474b64a6ceb93cbdc089fcc81eb010940b0fe3ad053a28` and
`c2b7f47fb2f8619ece23628ba593b504e662de8ffc9ac699bdfd2d472c94c7ee`.
The ARM64 oracle binary independently reproduced the exact report SHA-256 above.

## Opportunity comparison

The v2 machine census joins each real-Guest `VerifiedAnalysis` to the shared
`CanPreissue` predicate. A deliberately weaker call-level baseline asks only whether
an exact overlay call names a capability with an eligible resource contract; it does
not use control reachability.

| Stage | Accepted calls |
|---|---:|
| Structural pre-dispatch annotations | 11 |
| Exact overlay calls | 4 |
| Call-level resource-contract baseline | 4 |
| Shared `CanPreissue` legality | 1 |

The other three exact calls are rejected as `call_not_necessarily_reached`. This is
the intended value of the semantic join: call-level metadata alone would over-admit
three of four annotated calls for pre-execution issue.

Current machine census SHA-256:
`fe82029a703af6619b172b30817e6721e53c7533cab5bd56ceebe5937b6d0c1e`.
The exact Guest artifact and analyzer identities remain those frozen by Track C.

## Boundary for Track E

G1 may admit only a default-off spike for the single exact must-reach read shape. It
must reuse the existing `streaming.StagedObservation`, preserve unchanged Python as
execution authority, atomically claim once at the dynamic Host-call boundary, and
record every unclaimed physical operation as cancelled, late or orphaned. No write,
conditional call, coalescing, cache, hoisting, replay or backend inference is admitted.
