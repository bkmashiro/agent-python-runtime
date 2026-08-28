# Logical-time PLM V1 contract

Status: **Frozen, implemented, exact-Guest verified and default-off after negative economics.**

Architecture source: [Logical-Time-Preserving Split-Phase Execution](logical-time-preserving-split-phase-execution.md)

Execution source: [PLM Autonomous Mega-Goal](../plans/2026-08-28-logical-time-preserving-plm-autonomous-megagoal.md)

Exact evidence:

- Guest artifact SHA-256: `sha256:e9e2416f0cd34b397222267ec18637b6973597a2ec9ec4bb9e9bb526eca40585`
- [matched economics](../evidence/plm-v1-economics.json)
- [body-safe temporal/fault matrix](../evidence/plm-v1-fault-matrix.json)
- [bounded conditional small core](plm-v1-small-core.md)

Executable schema and oracle:

- `runtime/capability/plm_contract.go`
- `runtime/capability/plm_contract_test.go`

## Scope

V1 introduces the semantic distinction needed to migrate the predecessor
`issue_or_reuse` / `collect` prototype:

```text
P: prepare a Host-private candidate
L: at the original call, adopt, terminalise physical ambiguity, or start canonically
M: synchronously return the adopted, ambiguous, or canonical job outcome
```

V1 fixes:

```text
L = M = original source call
```

Delayed materialization is not admitted. The existing compiler may move physical preparation,
but no concrete value or exception crosses the original call as a Python proxy.

## Executable Host slice

The existing Run-owned table now exposes a versioned PLM path without creating a second
attempt framework:

```go
PrepareOrReuse(ctx, slot, request, contract, certificate)
LinearizeAndMaterialize(ctx, slot, logicalContext)
```

`PrepareOrReuse` accepts only the PLM contract sealed into the capability Plan, creates no
Broker call and records no receipt. Resource identity is derived from the contract's
`ResourceReference` and canonical arguments; callers cannot substitute it.

`LinearizeAndMaterialize` is invoked at the original logical source position. Run, Plan,
capability, handler and argument identities are rebuilt by the table. A registry-bound Host
validator produces temporal, provider non-interference and optional stable-failure results.
The logical caller supplies no validation booleans. One internal job then makes exactly one
Broker call. A valid candidate is claimed by that call. A mismatch, invalid proof or ordinary
prepare-time failure cancels and settles the candidate before the same Broker call may execute
the canonical handler. A settled uncertain physical outcome instead becomes one terminal
`provider_outcome_uncertain` result and is never replayed. Neither path creates a second logical
operation.

`CURRENT` uses `PreparePLMTransport` and never invokes the final-value handler before `L`.
`WALLCLOCK_OBSERVING` rejects preparation. Validation cost and provider-visible validation
events are recorded separately from candidate work and Broker receipts.

The predecessor `IssueOrReuse` / `Materialize` entry points and their compiler/Guest/Host bridge
were removed after exact-Guest parity. Sealed-source lowering now runs inside the one final Guest.

## Time and state

The model distinguishes:

- physical time `t`, when requests start, finish or expire;
- logical position `ell`, the ordered synchronous-program point at which a Host call exists.

The external environment is represented abstractly as:

```text
Theta_t = (world, authority, execution context, quota, provider session, ...)
```

A Host call is therefore interpreted as `H(arguments; Theta_t)`. `read_only` means that the
operation does not publish a logical write. It does not imply the same result at two physical
times.

## Trace classes

### Logical visible trace

The proof target includes:

- Python values and exceptions;
- logical Host invocation order;
- logical external effects;
- Guest-visible output;
- final external state.

### Host-private trace

The following may be hidden only after their contract obligations pass:

- candidate creation and completion;
- validation and candidate rejection;
- cancellation and discard;
- internal cache or connection reuse.

### Provider and economic trace

Network requests, billing, quota, rate limits, provider logs and resource contention are not
silently reclassified as Host-private. A preparation adapter must either prove that these events
cannot change later logical outcomes, or expose and bound them as admitted physical cost.
Every non-empty preparation contract names a Host-owned provider non-interference validator;
final-value candidates additionally name an operation-specific temporal validator. At `L`, the
runtime requires successful results from both before adoption. Validator names alone are not
proof.

## Identity

Every candidate is bound to all of:

```text
Run identity
Plan identity
source-seal identity
static site
physical dynamic occurrence
capability
handler identity
canonical argument digest
authority epoch
provider-session identity
```

Any mismatch at `L` rejects candidate adoption and first settles the exact candidate. The
canonical operation starts only when settlement proves there is no uncertain physical outcome.
Compiler-emitted site data and Guest-provided arguments never grant capability authority.

Each reached `(site, occurrence)` linearizes at most once. For two calls whose temporal histories
do not commute, source order requires `L_a` before `L_b`; physical candidate completion order does
not change that logical order. An exception materialized at `L_a` prevents `L_b` from existing when
the baseline program would not reach it.

## Temporal modes

`PLMContract.Version` is `pysolate.plm-contract.v1`.

| mode | V1 preparation | adoption condition |
|---|---|---|
| `IMMUTABLE` | final-value candidate | exact immutable resource identity |
| `SNAPSHOT` | final-value candidate | exact resource and snapshot identity |
| `VERSIONED` | final-value candidate | exact resource and current version |
| `LEASED` | final-value candidate | exact resource and Host clock epoch, `now <= valid_until` |
| `CURRENT` | transport/session preparation only | final-value candidate is never adopted |
| `WALLCLOCK_OBSERVING` | none | unchanged canonical call only |

A lease is valid only when supplied by the provider or Host adapter as a semantic guarantee.
An empirical TTL is not a strict lease. Lease ticks belong to one Host clock epoch so a serialized
wall-clock timestamp is not treated as monotonic evidence.

## Prepare-effect modes

V1 admits:

- `SILENT_READ` for final-value candidates in immutable, snapshot, versioned or leased modes;
- `TRANSPORT_ONLY` for current reads;
- `NONE` for wall-clock-observing operations.

Writes, prepare/commit publication and one-shot operations are not part of V1.

## Speculation

`NEVER` and `BUDGETED` are explicit Host-authored modes. Source-time value preparation requires
`BUDGETED`; current transport preparation is same-path and uses `NEVER` in V1. Untaken candidates
must be cancelable or finish with an explicit discarded disposition.

## Failure

Default policy is `RETRY_AT_LINEARIZE`:

```text
prepare timeout/error
  -> remains internal
  -> canonical operation starts at L
```

`STABLE_FAILURE` requires both:

1. a Host-authored operation-specific validator identity in the sealed contract;
2. a successful validator result in the linearization context.

A metadata label alone cannot adopt a prepare-time failure. No prepare exception is surfaced to
Python before the original call.

An adapter may return the typed `PLMProviderOutcomeUncertain` error only when it cannot prove
whether the physical provider operation took effect. Once the exact slot/request candidate is
bound at `L`, any restart decision first cancels and settles that candidate. If it reports
uncertainty, PLM materialises `provider_outcome_uncertain` as one logical error even when the
candidate value fails temporal or provider-proof checks. This is not adoption of the candidate
value, and the Host never starts a second canonical provider operation.

## Authority

V1 always uses `RECHECK_AT_LINEARIZE`. Authority used for physical preparation does not grant the
logical call. The candidate's authority epoch must match the Broker-owned epoch checked at `L`.
Revocation or epoch drift rejects the candidate.

## Candidate state machine

```text
PREPARING -> READY -> ADOPTED
          -> FAILED -> ADOPTED    only after stable-failure validation
          -> DISCARDED
READY ----------------> DISCARDED
FAILED ---------------> DISCARDED
```

`ADOPTED` and `DISCARDED` are terminal candidate dispositions. A candidate cannot be adopted by a
second Run or reopened after discard.

## Job state machine

Linearization produces exactly one Run-owned job:

```text
PENDING -> COMPLETED -> MATERIALIZED
        -> FAILED ----> MATERIALIZED
        -> CANCELLED
```

`MATERIALIZED` and `CANCELLED` are terminal. In V1, the transition from completed or failed to
materialized happens at the same original source call that created the job.

## Linearization decision

For contract `K`, candidate certificate `c` and current context `theta_L`:

```text
linearize(K, c, theta_L) =
  adopt(c)       if contract, identity, authority and temporal evidence all validate
  start(H)       otherwise
```

The executable pure oracle is `capability.DecidePLMLinearization`.

For an adopted value:

```text
materialize(linearize(prepare(H, a), a, Theta_L))
  belongs to semantics(H, a, Theta_L)
```

For an invalid candidate, the canonical operation starts at `L`, so behavior returns directly to
ordinary synchronous execution.

## V1 source transformation

The admitted normal form is:

```python
candidate = __pysolate_prepare__(...)
job = __pysolate_linearize__(candidate, actual_request)  # original call
value = __pysolate_materialize__(job)                    # same call in V1
```

The implementation may fuse `linearize + materialize`. It may also fuse all three phases when
preparation is not admitted. The Python program receives only the concrete value or exception.

## Barriers and fallback

V1 does not move physical preparation or materialization across unproved:

- writes or authority changes;
- transaction, cwd, environment or snapshot changes;
- earlier exceptions that suppress the call;
- wall-clock, randomness, signal, thread or reflective observations;
- `eval`, `exec`, frame or locals introspection;
- opaque calls.

Unsupported source and any failed obligation use unchanged sequential execution. Optimizer success
is never required for correctness.

## Conditional proof target

Let `visible` retain logical events and `private` contain only preparation events proven
noninterfering. For the admitted subset, the target is trace refinement:

```text
hide_private(Traces(PLM(program))) subset_of Traces(baseline(program))
```

Equality requires stronger assumptions such as fixed snapshots, deterministic tools and no
wall-clock observation. Gate 8 will mechanize a small core language, not full CPython.

## Executable Gate 1 oracle

The tests freeze:

- valid and invalid temporal/effect combinations;
- exact Run/Plan/source/site/occurrence/request/authority/session binding;
- immutable adoption;
- version invalidation;
- lease expiry and clock-epoch rejection;
- current-read transport-only fallback;
- retry-at-linearize failure policy;
- stable-failure validator requirement;
- candidate and job terminal states;
- deterministic fake-world fallback to the value at `L`.

These tests do not prove the production runtime obeys the contract. Gates 2 through 6 connect and
validate that path.

## Explicitly deferred

- materialization sinking;
- cross-branch value speculation;
- prepare/commit writes;
- provider-specific multi-level preparation;
- global CFG, PDG, SSA or Host scheduling graph;
- online preissue scoring and critical-path optimization;
- full-CPython semantic proof.
