# Reviewable tool compensation v1

Status: **Implemented Experimental Host contract.**

`runtime/compensation` plans semantic undo for tool effects that have already
been published outside a private workspace. It does not turn external systems
into transactions and it does not execute Agent-authored inverse Python.

## Terms and boundary

- **Rollback** discards private, unpublished workspace state. That remains a
  workspace lifecycle operation and does not use this package.
- **Compensation** is a new Host-authorized tool effect that responds to an
  earlier published effect.
- **Reconciliation** determines whether an operation with an ambiguous provider
  outcome committed before any compensation is considered.
- **Undo** is the user-facing request that may result in rollback, compensation,
  reconciliation, or guidance.

The v1 package is deliberately separate from `runtime/semantic`, source
optimizers, and compiler passes. AST effect semantics may later help explain or
group receipts, but receipts remain the source of executed-effect facts and the
Host remains the source of tool policy and authority.

## Tool companion contracts

A Host adapter may register a `ToolContract` for an original capability. This is
a companion registry rather than a field on `capability.Spec`, so compensation
policy does not change capability-plan or compiler-pass identity.

Each contract contains up to sixteen uniquely ranked strategies. The Host tries
them in descending rank order:

| Semantics | Meaning |
|---|---|
| `exact` | Restore the prior state only while the adapter's version and identity preconditions still hold. |
| `compensating` | Perform a forward counter-action such as cancel, close, restore, or refund. It does not erase history. |
| `best_effort` | Reduce impact without claiming full restoration. |
| `guidance` | Return bounded adapter-authored next steps; no provider mutation is available. |
| `irreversible` | State explicitly that the effect cannot be safely reversed. |
| `unknown` | The original outcome is ambiguous; reconcile before selecting a compensation. |

Executable strategies name a Host-owned operation. The executor receives the
original `EffectReceipt` and selected `Strategy`; the Agent review projection
contains only effect and strategy IDs. Provider target IDs, versions, and
arguments are not copied into that Python surface.

Each strategy may also include bounded precondition and risk text. These fields
are review metadata, not executable predicates; the adapter's read-only
validator and provider-side compare-and-swap remain authoritative.

A strategy may require Agent review or separate user approval. Agent review is
not authority. `Authorizer.AuthorizeCompensation` must validate fresh Host
authority and any approval identity against the exact plan digest.
The reviewer Run must differ from every original effect Run.

## Receipt input and order

`Preview` accepts Host-owned receipts from one effect group. The v1 receipt
contains the original Run, executed capability, target identity, applied
version when available, argument digest, successful result digest, operation
index, outcome, and actual effect dependencies. An ambiguous receipt may omit
both the applied version and result digest because the Host has not yet
established whether the provider committed.

The planner rejects duplicate IDs or operation indices, missing dependencies,
cycles, malformed digests, mixed groups, unknown contracts, oversized dependency
graphs, and oversized serialized plans. It orders
compensation by reverse topological order of the executed dependency graph;
reverse operation index only breaks ties between independent effects. It never
blindly reverses a transcript.

V1 also rejects more than one receipt for the same globally scoped
`TargetID`. Undoing one mutation can itself advance the provider version and
invalidate an earlier mutation's preview. Repeated mutations of one resource
remain out of scope until the planner can project those version transitions
across a compensation chain.

An `ambiguous` original receipt becomes a `reconciliation_required` guidance
step. It is not passed to an executable compensation strategy.

## Dry run

Two dry-run modes share the same planner:

### `plan`

- uses receipts and the static tool contract;
- chooses the highest-ranked declared strategy;
- performs no provider validation and no mutation;
- marks preconditions as `not_checked`.

### `validate`

- calls the adapter's read-only `Validator` for executable strategies;
- falls back when a stronger strategy is no longer applicable;
- records rejected strategies, reasons, and the observed current version;
- stops at bounded guidance if no executable strategy remains;
- performs no provider mutation.

Both modes return `pysolate.compensation-plan.v1`, including:

- a SHA-256 identity over the complete plan;
- source receipt digests;
- reverse-topological steps;
- selected semantics, operation, precondition, risk, approval, validation state,
  and fallback rejections;
- exact/compensating/best-effort/guidance/irreversible/reconciliation counts;
- a Python review projection.

Example review projection:

```python
compensation_plan = [
    {"effect": "effect-calendar-1", "strategy": "cancel_and_notify", "mode": "apply"},
    {"effect": "effect-mail-1", "strategy": "send_correction", "mode": "guide"},
]
```

This data-only Python is a presentation artifact, not an executable tool API.
Editing it neither changes the plan nor grants execution. Any structured plan
edit must be previewed again and receive a new digest.

## Execution

`Execute` requires the exact stored plan, matching review digest, reviewer Run
identity, syntactically valid authority identity, and any required approval
identity. A Host-provided `Authorizer` then validates those identities.

Before the first provider mutation, the controller:

1. verifies that the plan was created by this controller;
2. atomically claims the plan and reserves bounded journal capacity;
3. obtains Host authorization, releasing that claim if authorization fails;
4. revalidates every executable step.

If any precondition is stale, execution returns `stale_plan` before invoking the
executor. The provider operation must still enforce its own expected version or
compare-and-swap condition because state can change after validation.

Successful compensation creates a new receipt with:

- the plan digest;
- `compensates` pointing to the original effect;
- strategy and semantics;
- reviewer Run, authority, and approval identities;
- provider receipt digest.

Only an explicit `not_applied` outcome with an adapter evidence digest produces
`failed`. An unclassified executor error, malformed success receipt, response
loss, or otherwise ambiguous compensation produces `reconciliation_required`;
the controller retains a provider observation/receipt digest when one is
available but does not invent one. Re-executing a completed plan returns its
stored result and does not invoke the provider again. A different plan cannot
preview or execute against an effect already compensated or currently in
flight. An ambiguous compensation outcome blocks every different plan until a
separate reconciliation plane resolves it; v1 intentionally provides no blind
retry or local clear operation.

The v1 executor is intentionally conservative: it executes the ordered prefix
until the first guidance, reconciliation, or provider failure, then records the
remaining steps as `blocked`. A later version may safely continue independent
branches once dependency-aware partial execution has its own tests. V1 rejects
new plans for those blocked effects rather than allowing callers to bypass the
original order.

## Current evidence and non-claims

Deterministic fake-provider tests cover:

- exact-to-compensating fallback after version drift;
- plan-only versus read-only validation;
- reverse-topological ordering;
- plan tampering, Host authority denial, and user approval;
- execute-time stale-state rejection before mutation;
- partial failure and replay without a second mutation;
- cross-plan already-compensated and in-flight effect guards;
- fail-closed handling of unclassified provider errors;
- rejection of repeated mutations to one resource until version-chain planning
  exists;
- concurrent cross-plan journal-capacity reservation before provider mutation;
- guidance-only, irreversible, and ambiguous original outcomes;
- ambiguous compensation outcomes requiring reconciliation.

This slice does **not** add a production external-write journal, persistent
controller state, real mail/payment/calendar adapters, provider-native preview,
plan expiry, or crash recovery. `runtime/compensation` is the contract and
deterministic Host state machine needed before those integrations. The
production external-write
effect plane remains out of scope until a separate effect-intent, durable
journal, and reconciliation goal is approved.
