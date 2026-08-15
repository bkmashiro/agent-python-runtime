# Workflow-boundary mechanism integration v0

Status: **Minimal integration; no new scheduler or tool cache**
Date: 2026-08-15

## Existing mechanisms retained

The successor does not add a second optimization stack. It keeps:

- semantic pre-dispatch for one exact necessarily reached typed live read;
- explicit Agent Function invocation identity;
- in-flight Agent Function single-flight;
- bounded project/private retained Agent Function results;
- explicit workflow graph/node identity and declared independence;
- ordinary fresh execution when every optional mechanism is off.

## Provenance gap closed

Agent Function cache record v3 persists the producer
`physical_execution_id`. Leader, waiter and retained results now expose the same producer
identity. The field is correlation evidence only: it does not enter invocation identity,
authorize a cache hit, or affect result bytes. Missing producer identity remains valid for
legacy callback callers that never bind one, but such a result cannot be admitted into a
complete workflow-boundary observation report.

Malformed stored producer IDs invalidate the cache record and cause ordinary recompute.
Old v2 records likewise fail closed after the schema bump.

## Mechanism boundary decisions

- `preissued` is observed around the existing semantic pre-dispatch start/claim seam;
  no hidden task is created.
- `coalesced` is the existing Agent Function leader/waiter relation.
- `reused` is the existing retained-result relation.
- `declared_parallel` is reported only when independently declared workflow requests
  actually overlap; observation never creates that overlap.
- tool-level retained reuse is not added in v0. Capability Plan v5 does not yet carry a
  canonical shareability/retention contract, so `CanCoalesce` and `CanCache` continue to
  reject rather than infer safety from read-only/idempotent labels.
- ordinary WASI calls are not wrapped as tools or cached. Only a future typed,
  Host-observed WASI boundary with an exact contract could enter the same observation
  model.

This gives the final experiment truthful examples of tool pre-dispatch plus workflow-node
parallelism, coalescing and retained reuse without expanding authority or inventing a
general workflow scheduler.
