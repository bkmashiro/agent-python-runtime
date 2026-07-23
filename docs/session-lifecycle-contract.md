# Stateful session lifecycle contract

## Status

This document defines the Host-owned lifecycle boundary selected by [ADR 0006](adr/0006-execution-session-lifecycle.md). It is a retained accepted design boundary, not an active implementation queue or implementation claim. The repository still has no stateful session manager, capsule payload, persistence, restore, or migration path.

The existing backend-neutral `engine.Runner.Run` remains stateless. Fresh-instance remains its portable fail-closed baseline, and every served prepared module remains single-use.

## Why this is a separate contract

`RunRequest` is untrusted generated data. It cannot select or grant durable state. In particular, it does not gain any of these fields:

- session identity;
- lease or fencing token;
- capsule/base identity;
- persistence tier;
- credentials, capabilities, targets, or budgets;
- capture, suspend, resume, migration, or deletion policy.

A future Host entry point supplies session ownership separately from the untrusted request. The neutral function Runner remains usable without a session plane.

## Host-owned identities

A future implementation must distinguish:

- **SessionID** — generated and authorized by the Host; never accepted from model-produced request JSON as authority;
- **Lease epoch/token** — proves one current writer and fences stale owners;
- **BaseID** — binds Guest artifact/profile, trusted preparation recipe, evaluator/schema where applicable, and backend/runtime ABI;
- **CapsuleID** — content/integrity identity for one exact-memory or logical capsule;
- **RunID** — remains request/receipt correlation and does not imply session ownership.

This document does not freeze the concrete encoding of those identities. That requires a later TDD slice after the baseline evidence contract.

## Lifecycle operations

The future Host-owned operation set is intentionally small:

| Operation | Required precondition | Success boundary | Failure boundary |
| --- | --- | --- | --- |
| Create | authorized Host config and qualified artifact profile | one new live session at a quiescent prepared boundary | no durable identity is published |
| Invoke | current lease, live/quiescent session, untrusted RunRequest validated independently | bounded result returned and session reaches a new quiescent boundary | uncertain/trapped session is retired, not reused |
| Quiesce | current lease and no competing lifecycle transition | no Guest call/Host callback is in flight | report `not_quiescent`; do not capture |
| Suspend | current lease, proven quiescence, all state classes classified | validated capsule or explicitly retained live tier is committed before release | live session remains owned or is retired; never publish a partial capsule |
| Resume | current lease/fencing decision and exact compatibility validation | one live session passes semantic validation before publication | reject capsule/base/runtime mismatch; do not expose a partial session |
| Close | current lease or Host administrative authority | live resources and uncommitted capsule staging are released | repeated close is idempotent; cleanup errors remain observable |

No operation serializes raw credentials. Capabilities and credentials are rebound from current Host policy on each live invocation; stale serialized authority is never restored from a capsule.

## State machine

The design state machine is:

```text
creating → quiescent ↔ invoking
              │            │
              │            └─ uncertainty/trap/cancel → retiring → closed
              ├─ suspending → suspended
              └─ closing ─────────────────────────────→ closed

suspended → resuming → quiescent
                    └─ incompatibility/corruption → suspended or closed
```

Only `quiescent` may begin capture. `invoking`, `suspending`, `resuming`, `retiring`, and `closed` reject competing transitions. A future implementation must make transition ownership explicit and race-test invoke/suspend/resume/close.

## Idempotency and fencing

- Create requires a Host-owned idempotency identity before it may have external durability.
- Suspend may retry only against the same session version and lease epoch.
- Resume must acquire/fence one writer before exposing a live session.
- A stale lease returns `lease_conflict`; it never silently observes or mutates the new owner's session.
- Close is idempotent but must not hide cleanup failures from the first close.
- Cross-node movement remains impossible until local fencing and crash recovery are proven.

## Unsupported-reason envelope

The stable rejection envelope is defined by:

- JSON Schema: `session/v1/unsupported-reason.schema.json`;
- Go type and validator: `runtime/session.UnsupportedReason`.

It contains only:

```json
{
  "schema_version": 1,
  "operation": "capture",
  "code": "mutable_state_unsupported",
  "state_class": "mutable_global"
}
```

The envelope intentionally carries no session ID, credentials, capabilities, payload, or free-form Guest text. Host logs may attach bounded diagnostics separately, but callers branch only on the stable code.

Capture codes:

- `not_quiescent`;
- `mutable_state_unsupported` with a known `state_class`;
- `external_resource_active`;
- `lease_conflict`.

Resume codes:

- `mutable_state_unsupported` with a known `state_class`;
- `artifact_mismatch`;
- `base_mismatch`;
- `runtime_mismatch`;
- `architecture_mismatch`;
- `capsule_invalid`;
- `lease_conflict`.

Known state classes are `linear_memory`, `mutable_global`, `table`, `wasi_resource`, `host_state`, and `external_handle`. Unknown classes fail closed instead of being accepted as an open string.

## Capsule boundary

This contract does not define capsule bytes. Before that schema exists, Phase 1 must measure the current runtime and Phase 3 must prove the complete state census and cross-fresh-process round trip.

- An **ExactMemoryCapsule** will be valid only for an exact compatibility tuple and must include all required non-memory state.
- A **LogicalCapsule** will require an explicit Guest export/import schema and cannot claim arbitrary Python serialization.
- Dirty linear-memory pages alone are never a valid capsule.
- Active external handles are absent, closed, or explicitly rebound; they are not copied implicitly.

## Current non-claims

Activation of this design does not claim:

- a stateful session API or manager;
- safe served-instance reuse;
- complete CPython snapshot/restore;
- COW, dirty-page tracking, pageout, compression, or UFFD;
- local or object-store persistence;
- cross-node migration;
- promotion of `numpy-core` or any profile to production/default status.
