# Full Composable Runtime deterministic measurement summary

Status: **Mechanism-only deterministic evidence; not a real Agent performance result.**
Date: 2026-08-13

## Bound population

The measurements cover repository fixtures and the real CPython/WASI Guest artifact used by the focused E2E suite. Agent/model decisions, provider latency, external writes, paid services, and repository-shaped real Agent work are excluded. The separate acceptance contract is [real-agent-composable-runtime-acceptance.md](real-agent-composable-runtime-acceptance.md).

## Verified mechanism counts

| Mechanism | Deterministic observed relation |
|---|---|
| Structured fan-out | 2 child executions completed in fresh Wazero Guests before parent EOF |
| Workspace branches | 2 sibling-private child branches; explicit select retained 1 root and destroyed 1 unselected root |
| Parent invalidity | 2 real child Guests cancelled/completed under abort; all private child refs became absent; parent base remained unchanged |
| Function cache | One admitted invocation physically computed and stored; a later exact invocation produced a verified local hit |
| Single-flight | Two concurrent exact invocations produced 1 leader and 1 follower; retention and flight toggles remain independent |
| Workflow wait/resume | The first Guest closed at wait; resume created a distinct Guest; both active-period Guests closed; unchanged explicit work used local lookup |
| Prepared runtime | Explicit prepared mode created 1 never-served initialized slot, consumed it once, then used 1 ordinary fresh fallback; closing an unused Engine destroyed its slot |
| `/tmp` freshness | A file written during the prepared Run was absent from the following fresh fallback Run |
| Memory COW | Not selected; the versioned probe reports fresh fallback and state-reset blockers |
| All mechanisms off | Typed mode evidence reports fallback/off and repeated ordinary evaluation preserves the normalized result without cache hits |

## Body-free measurements available

The Runtime records relative monotonic child start/end, changed and materialized workspace bytes, maximum branch depth, reachable/discarded roots, function hit/miss/write/eviction/stored bytes, single-flight leader/follower/in-flight counts, workflow lookup/recompute/refresh/invalidation/retained-state bytes, Guest creation/destruction, and prepared/fresh counts. `runtime/composable` validates only supported claims over these versioned records and rejects unknown fields or private blocker strings.

## Treatment conclusions

- Fresh execution remains the semantic fallback and passes with all successor mechanisms disabled.
- Portable workspace identity and lineage do not depend on Linux memory COW.
- Fan-out can overlap parent source arrival without allowing children to publish.
- Result retention and single-flight eliminate different work: sequential completed reuse versus concurrent in-flight collapse.
- Fresh re-evaluation releases the Guest during the explicit wait; no interpreter continuation state is retained.
- One prepared slot is implementable without pooling or reuse; it changes lifecycle timing only, not selected result semantics.
- COW is deferred. Historical linear-memory COW code does not establish reset of current module/WASI/static state, and restoring the deleted pool/census/allocator stack is not a minimal change.

## Linux prepared/COW probe

The exact Linux x86_64 result is preserved as
[`evidence/full-composable-linux-prepared-cow.json`](evidence/full-composable-linux-prepared-cow.json).
Prepared/fresh parity passed. The artifact exposed one non-imported memory, but
its memory contract was not fixed; module-instance, WASI Host, and static
non-memory state also remained non-resettable or uncensused. The probe therefore
reported `MemoryCOWCandidate=false`, selected no COW mode, and retained fresh
fallback. Its preparation and test durations are fixture observations only, not
product-performance data.

## Non-claims

Focused E2E wall times are correctness-run durations, not product latency or speedup measurements. This summary does not claim arbitrary Python purity, real Agent productivity, general fan-out benefit, cache benefit across private partitions, provider behavior, exactly-once writes, deterministic replay, production readiness, or memory savings from COW.
