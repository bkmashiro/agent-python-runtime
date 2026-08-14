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

The comparator ignores only qualified pure/read physical scheduling differences whose
logical trace is unchanged and whose unclaimed work has a typed terminal disposition.
Unknown effects and all write-class speculative physical events are unclassifiable.
Missing or extra logical effects, event-order changes, argument/resource drift, freshness,
authority, workspace, cancellation, ambiguity and replay are classified as typed
divergences. Invalid or unqualified traces fail as `trace_unclassifiable`.

The executable matrix at
[`docs/evidence/effect-aware-differential-oracle.json`](../evidence/effect-aware-differential-oracle.json)
contains 17 cases. All 17 matched their expected result, including one equivalent
qualified discard and adversarial terminal, result, argument, missing/extra effect,
sequence/edge order, freshness, authority, workspace, cancellation, ambiguity,
post-effect replay, qualified-write speculation and invalid/unqualified-physical
cases. Report SHA-256:
`4df33cbc8d446153ee7523377beb921fe5fae7b78815269910d70cc55b62f52f`.
Cross-compiled ARM64 semantic and effectgraph test binaries passed on Linux
`6.12.0-202.76.4.1.el9uek.aarch64`; binary SHA-256 values were
`a75ffa82dbc090914f931d750278378e76df33540cfdfb9be5f5c42869d104b9` and
`58e608e69aa89e4b500f44b7f26257ebdbfbba5487a2a6262ee328de635e8cb1`.
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
