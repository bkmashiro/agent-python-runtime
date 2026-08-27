# Unified split-phase execution V1 contract

Status: **Frozen by Gate Contract for implementation.**

This is the small executable contract for the roadmap in
`docs/plans/2026-08-27-unified-split-phase-execution-roadmap.md`. The longer
formal report in `docs/research/pysolate-issue-collect-formal-report.md` remains
a proof sketch and design reserve. V1 does not implement collect sinking,
unresolved-branch speculation or prepare/commit writes.

## Observable contract

For an admitted program, baseline synchronous execution and transformed
execution must agree on:

1. returned value or uncaught Python exception;
2. ordered logical Broker calls and receipts;
3. terminal workspace state;
4. whether later Python and Host calls are reached after an earlier failure.

Physical issue time, completion order and discarded private attempts are
separate Host evidence. They may differ, but every physical attempt must have a
bounded terminal disposition.

## Minimal ABI

```text
issue_or_reuse(site, occurrence, request) -> compiler-owned handle
collect(handle) -> ordinary typed result or original logical error
finalize(success) -> terminal dispositions for every attempt
```

The current Guest bridge may encode `site + occurrence` as one dynamic slot.
That encoding is an implementation detail; Python source cannot supply
Host authority through it.

## Static site and dynamic occurrence

A V1 static site is derived from the original call expression span:

```text
site = s<start-line>c<start-column>-e<end-line>c<end-column>
base slot = slot-<site>
base call = split-<site>
```

The table is Run-private and Plan-bound, so V1 does not add a source hash or
capability digest to the site. Exact request matching checks the capability and
canonical arguments. Source streaming may only extend an admitted prefix;
seal-time validation rejects or discards candidates whose span/call no longer
matches final source.

Each execution of the compiler-emitted issue node allocates the next positive
occurrence for that static site:

```text
slot-<site>-1
slot-<site>-2
...
```

The corresponding `call_id` receives the same occurrence suffix. The Host does
not know loop variables, branch conditions or dependency edges.

## Positive early-issue admission

A physical attempt may begin early only when the sealed Plan gives the
operation an eligible `PreDispatchContract` and the current Host context proves:

- canonical concrete arguments;
- stable Run/Plan/authority binding;
- admitted effect class;
- freshness and observation-time policy;
- privacy and billing partition;
- bounded physical attempts, cost and result bytes;
- explicit unconsumed/discard policy.

Unknown evidence rejects early issue. Rejection selects a later runtime issue
when the same contract becomes provable, otherwise the unchanged synchronous
call. A write or approval-gated operation is never submitted by the V1
split-phase path.

## `issue_or_reuse`

For one dynamic slot:

| Existing table entry | Incoming canonical request | Result |
|---|---|---|
| none | admitted | start one physical attempt |
| exact match | same | reuse the existing attempt |
| any | mismatch | fail closed; never start a second attempt |
| terminally consumed/discarded | any | fail closed |

Source-time and runtime issue call this same operation. Origin is evidence, not
a second result table or a different consumption protocol.

## `collect`

V1 emits `collect` at the original logical call site. Collect:

1. reaches the unchanged Broker path;
2. applies permission, budget, logical order, schema and receipt semantics;
3. claims exactly the matching table entry;
4. waits only if physical work is incomplete;
5. exposes provider failure only at this logical position.

An earlier Python exception naturally prevents later runtime issue nodes from
executing. It does not require a Host dependency relation.

## Control flow

- Straight-line source-known calls may preissue before seal.
- Runtime-derived arguments issue at the earliest compiler-emitted point after
  ordinary Python makes them concrete.
- Branch-local calls issue only after CPython enters the branch.
- Loop-local calls receive one occurrence per reached iteration.
- Cross-path speculation and cross-iteration batching are deferred.

## Finalization

Every table entry ends as collected, discarded, cancelled, failed or uncertain.
A failed/unused attempt is never replayed implicitly. Only Host-proven
`not_started` may fall back to a later physical start.

## Unsupported source

The seal-time compiler must reject the whole transformation before Guest
execution when it cannot prove the V1 shape, site identity, argument evaluation
order or helper integrity. The unchanged source then executes synchronously.
There is no mixed partially verified rewrite.

## Gate Contract corpus

The executable tests must cover:

- source-time issue followed by exact runtime reuse;
- runtime-derived `A -> Python -> B` issue;
- independent calls and ordered collect;
- branch taken and not taken;
- zero and repeated loop occurrences;
- request mismatch and changed final source;
- earlier Python exception;
- physical success followed by logical denial;
- cancellation, late completion and unused issue;
- write rejection;
- unchanged-source sequential fallback.
