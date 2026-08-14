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
7. frozen stream/workflow/freshness/expiry/privacy/parent-lineage identities;
8. a non-zero physical-read budget plus an exact Host-authored budget-reservation
   identity.

A successful decision carries an opaque `QualifiedCall`; callers cannot construct its
private proof fields. Public `VerifiedAnalysis` minting accepts only the concrete target
Wazero engine; Host-internal test/composition minting is gated by Go's `runtime/internal`
package boundary, so an external `engine.Runner` plugin cannot self-report properties and
mint authority. `CanClaimStagedObservation` compares a body-free claim against
that exact call, capability/spec/handler/plan/grant, occurrence, argument, epoch,
privacy, lineage and budget-reservation identity. It remains a pure check: Track E must
atomically enforce one-shot state and typed unclaimed disposition.

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
logical trace is unchanged. Unclaimed work needs a typed terminal disposition; consumed
work must bind one-to-one to the exact logical event across capability, effect, arguments,
resource, result, freshness and authority. `PhysicalStarted` must exactly match the
presence of accounted physical events. Unknown effects, malformed/cyclic predecessor
graphs and all write-class speculative physical events are unclassifiable.
Missing or extra logical effects, event-order changes, argument/resource drift, freshness,
authority, workspace, cancellation, ambiguity and replay are classified as typed
divergences. Invalid or unqualified traces fail as `trace_unclassifiable`.

The executable matrix at
[`docs/evidence/effect-aware-differential-oracle.json`](../evidence/effect-aware-differential-oracle.json)
contains 24 cases. All 24 matched their expected result, including equivalent
qualified discard and exact consumed-claim cases plus adversarial terminal, result,
argument, missing/extra effect, sequence/edge order, freshness, authority, workspace,
cancellation, ambiguity, post-effect replay, qualification/claim mismatch, duplicate
claim, physical-start inconsistency,
write-class speculation and invalid/unqualified physical work. Report SHA-256:
`ac0e311244a5c62ac471bfee7626f3396b77f67f50f03dd67c58973a1206a48d`.
Cross-compiled ARM64 semantic and effectgraph test binaries passed on Linux
`6.12.0-202.76.4.1.el9uek.aarch64`; binary SHA-256 values were
`8094054fbaa4a93efba71b46108c652c6dd6a1c8feef0c10850d505b6a00497c` and
`4ea4804e0d94509bdc0ad6b0a4edff1dd31f7de3c8acdfca655f0461fd9f06e0`.
The ARM64 oracle binary independently reproduced the exact report SHA-256 above.

## Opportunity comparison

The v3 machine census joins each real-Guest `VerifiedAnalysis` to the shared
`CanPreissue` predicate. A deliberately weaker call-level baseline asks only whether
an exact overlay call names a capability with an eligible resource contract; it does
not use control reachability. Census v3 legality rows can only be emitted by
`RunVerifiedCensus`, which invokes the shared predicate over opaque verified reports and
derives a distinct budget-reservation identity per call. Both census v3 and the oracle
report validate sorted rows, per-row/aggregate counters, rejection/case consistency and
an unexported run-produced seal before encoding evidence.

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
`72992e95f97d5f19a9363af03d10b529e3e6303ea88be04949eca623afeebb9f`.
The exact Guest artifact and analyzer identities remain those frozen by Track C.

## Boundary for Track E

G1 may admit only a default-off spike for the single exact must-reach read shape. It
must reuse the existing `streaming.StagedObservation`, preserve unchanged Python as
execution authority, atomically claim once at the dynamic Host-call boundary, and
record every unclaimed physical operation as cancelled, late or orphaned. No write,
conditional call, coalescing, cache, hoisting, replay or backend inference is admitted.
