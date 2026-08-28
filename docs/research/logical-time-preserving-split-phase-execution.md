# Logical-Time-Preserving Split-Phase Execution

Status: **Implemented Experimental V1; default-off after negative matched economics.**

This document records the architecture implemented by the active
[PLM autonomous megagoal](../plans/2026-08-28-logical-time-preserving-plm-autonomous-megagoal.md).
The versioned contract, body-safe evidence and conditional small core are
[`logical-time-plm-v1-contract.md`](logical-time-plm-v1-contract.md),
[`plm-v1-economics.json`](../evidence/plm-v1-economics.json),
[`plm-v1-fault-matrix.json`](../evidence/plm-v1-fault-matrix.json) and
[`plm-v1-small-core.md`](plm-v1-small-core.md).

## Decision

Move from a two-stage description:

```text
issue early
collect at or after the logical call
```

to a three-stage normal form:

```text
prepare early
linearize at the original logical call
materialize at the latest proven-safe demand point
```

This is the general normal form. Implemented V1 fixes `tM = tL` at the original call; materialization sinking remains Deferred.

The reason is temporal semantics. A read-only operation can still depend on a changing
external world. Starting it before the source call and later returning that value is not
strictly equivalent unless the Host can prove that the candidate still denotes an
outcome allowed at the source call's logical point.

## Migration relationship

The implemented V1 uses:

```text
PrepareOrReuse              = Host-private candidate preparation
LinearizeAndMaterialize     = original-point validation plus adopt/restart
V1 materialization          = original logical call
```

It establishes the following bounded properties:

- source-time and original-point preparation share one Run-owned candidate/job table;
- the compiler lowers sealed source without exposing a Python Future;
- one exact final Guest performs lowering and ordinary synchronous CPython execution;
- branch and loop occurrences remain ordinary CPython control flow;
- Broker logical calls, receipts and provider starts remain separate evidence;
- strict temporal modes require Host validation; `CURRENT` cannot adopt an early read;
- setup failure, cancellation, late completion and typed uncertain provider outcomes have bounded Run-owned dispositions;
- unsupported source and a disabled pass execute unchanged.

The predecessor `issue_or_reuse`/`collect` bridge is Removed. Its immutable-read evidence and `+151.40%` cold result remain historical facts. PLM's initial Gate 6 result was `+32.01%` cold and `+48.27%` with Engines precompiled. The production refactor reduced those deltas to `+4.57%` and `+6.42%` under the same protocol. All remain fixture-scoped, non-positive results rather than broad speedup claims.

## Two time domains

Let:

```text
t ∈ T   physical wall-clock time
ℓ ∈ L   source-program logical execution point
```

For one Host operation `H`, PLM may place its phases at:

```text
tP ≤ tL ≤ tM
```

where:

- `tP` is physical candidate preparation;
- `tL` is the moment synchronous CPython reaches the original Host call and the Host
  linearizes it;
- `tM` is the moment a concrete value or exception is materialized.

Optimisation may change `tP` and `tM`. It preserves the source-program order and logical
anchor `ℓH`. It does not promise identical wall-clock observations between baseline and
optimised executions.

## Time-indexed Host semantics

Write the Host environment as:

```text
Θt = (Wt, At, Ct, Qt, ...)
```

including external state, authority, execution context, quota, nonce, provider session
and other operation-relevant state. A Host operation is interpreted as:

```text
⟦H⟧(arguments, Θt) ⊆ Outcome
```

A read-only operation need not be temporally stable:

```text
read-only ≠ immutable
```

`read-only` says that the operation does not mutate its abstract resource. It does not
say that two invocations against different external states return the same outcome.

## PLM phases

### Prepare

```text
P_H(request, tP) -> candidate
```

A candidate may contain physical work, a completed result, transport/session state or a
cache/single-flight reference. It is not a logical Host invocation.

Prepare must satisfy two separate conditions:

1. **Guest-semantic silence:** it cannot return a Python value, raise a Python exception,
   publish a logical write or mutate Guest state.
2. **Host-policy non-interference:** any provider-visible request, billing, quota,
   rate-limit use or contention must be explicitly admitted and must not change future
   logical outcomes except as allowed by the operation contract.

Provider-visible work is not erased from evidence merely because it is hidden from the
Python trace.

### Linearize

```text
L_H(candidate, actual request, ΘtL) -> job
```

At the original logical call, the Run-owned Host context:

1. rechecks exact tool and arguments;
2. rechecks Run, Plan and authority identity;
3. validates the candidate's temporal certificate against the current operation context;
4. adopts the candidate when validation is sound;
5. otherwise cancels and settles it before any canonical start;
6. materialises a terminal ambiguity when settlement is uncertain, or starts the canonical
   operation at the logical point when replay is proven safe.

The abstract Host operation begins at linearization, not at physical preparation.
Classical linearizability places an operation's effect between its invocation and
response. PLM uses the same semantic anchoring idea, but treats preparation as an
internal precursor rather than the abstract operation's invocation.

### Materialize

```text
M_H(job, tM) -> ordinary value or exception
```

Materialize blocks if necessary and exposes the adopted or restarted job's result. Python
never receives a Future or candidate handle.

V1 folds linearize and materialize at the original call:

```text
P + (L=M)
```

Materialize sinking is a later research reserve. It is allowed only across statements
proven pure, total, non-reflective and non-throwing with respect to the operation.

## Conditional correctness contract

For an admitted operation:

```text
M_H(L_H(P_H(a, tP), a, ΘtL), tM) ∈ ⟦H⟧(a, ΘtL)
```

This requires:

- safe argument capture;
- Guest-semantic silence and Host-policy non-interference of preparation;
- sound temporal validation;
- exact Run/Plan/authority binding;
- one linearization per logical occurrence;
- original source order for non-commuting logical operations;
- legal exception placement;
- bounded terminal disposition for every candidate and job;
- unchanged synchronous fallback when proof is unavailable.

The intended theorem is conditional trace refinement over a small core calculus, not a
proof of full CPython. Internal events can be hidden only when their non-interference
obligation holds. Provider/economic events remain separate evidence.

## Temporal contract

Each stageable operation declares an operation-specific temporal mode. The initial
vocabulary is deliberately small:

```text
IMMUTABLE
SNAPSHOT
VERSIONED
LEASED
CURRENT
WALLCLOCK_OBSERVING
```

V1 behavior:

| Mode | Candidate result may be prepared? | Linearization requirement |
|---|---|---|
| `IMMUTABLE` | yes | exact resource and authority identity |
| `SNAPSHOT` | yes | exact immutable snapshot identity |
| `VERSIONED` | yes | sound provider/Host version validation |
| `LEASED` | yes | provider-guaranteed lease plus safe clock/epoch handling |
| `CURRENT` | no strict final-value prefetch | prepare transport/session only; execute the read at linearization |
| `WALLCLOCK_OBSERVING` | no | unchanged original call unless a later explicit semantic model exists |

A candidate certificate binds at least:

```text
operation and handler identity
canonical request fingerprint
resource identity
Run and Plan identity
source site and dynamic occurrence
authority epoch
mode-specific snapshot/version/lease evidence
provider session generation when relevant
```

A heuristic TTL is not a strict certificate. It belongs only to an explicitly
stale-tolerant or approximate tool contract, outside strict PLM equivalence.

V1 also fixes failure and authority policy:

```text
prepared success      validate temporally at linearization
ordinary prepared failure
                      restart canonically at linearization by default
uncertain physical outcome
                      terminal error at linearization; never replay
stable certified failure
                      adopt only under an operation-specific sound contract
authority             always recheck at linearization
source-time speculation
                      require explicit bounded admission and silent discard
```

An early timeout, absence or provider error is not automatically the error that the
original call may observe later. `STABLE_FAILURE` is a positive adapter guarantee, not a
default inferred from idempotency.

## Run ownership

One Run-owned execution context must create and own:

```text
RunPLMContext
├─ immutable Run identity
├─ immutable Plan identity
├─ exactly one Broker
├─ candidate/job table
├─ budgets and bounded evidence
└─ finalization/cancellation owner
```

Consequences:

- a table cannot be reused across Runs with the same Plan;
- materialization cannot accept an arbitrary Broker;
- attachment is atomic and one-shot, not an optional late convention;
- cleanup ownership exists before any physical candidate can start;
- every public constructor applies the same research/product mechanism gates;
- duplicate issue/reuse evidence is bounded independently of candidate count.

This ownership repair is required even if later PLM optimisation gates fail.

## Dependence and scheduling

The Host does not maintain a Python graph. The sealed-source compiler may temporarily
reason about:

```text
P_h -> L_h -> M_h
```

with edges from:

- argument definitions to `P_h` or `L_h`;
- branch reachability to `L_h`;
- authority, environment, transaction and write changes to `L_h`;
- earlier exceptions to later logical operations;
- source order between non-commuting temporal reads;
- `M_h` to concrete Python consumers.

After lowering, normal synchronous CPython expresses control and data dependencies. The
Host sees only complete candidate/job requests and terminal lifecycle operations.

V1 does not build a general CFG/PDG/SSA framework. It retains the current direct typed
assignment subset and statement-local/basic-block reasoning.

## Economics

The predecessor cold exact-Guest fixture measured:

```text
baseline median   3.755490702 s
unified median    9.441244409 s
change            +151.40%
```

That result remains permanent negative evidence for the predecessor. The initial PLM Gate 6 path removed the extra analysis Guest and measured separately:

```text
cold end-to-end        4.018 s -> 5.305 s   +32.01%
engines precompiled    2.690 s -> 3.989 s   +48.27%
```

After removing duplicate transform replay, unverifiable AST digests, dual compilation, derived-source transport and a quadratic source scan, the same protocol measured:

```text
cold end-to-end        4.125 s -> 4.314 s    +4.57%
engines precompiled    2.736 s -> 2.912 s    +6.42%
```

The remaining cost is bounded final-Guest compiler work, not a hidden second execution or temporal validation. Correctness and economics remain separate; PLM stays default-off because both controlled profiles remain negative.

## V1 scope

Implemented in V1:

```text
source-time or runtime prepare
+ original-site linearize
+ original-site materialize
+ sound immutable/snapshot/versioned/leased validation
+ CURRENT transport-only preparation
```

Defer:

- materialize sinking;
- cross-branch result speculation;
- prepare/commit writes;
- online JIT issue-window optimisation;
- demand-priority scheduling;
- multi-level transport/auth/request physical phase decomposition;
- general commutativity or effect inference;
- arbitrary Python continuation, Future propagation or Host-side expression evaluation.

## Related foundations

PLM combines established ideas rather than claiming each component as novel:

- Herlihy and Wing, *Linearizability: A Correctness Condition for Concurrent
  Objects* (1990): <https://dl.acm.org/doi/10.1145/78969.78972>
- Liskov and Shrira, *Promises: Linguistic Support for Efficient Asynchronous
  Procedure Calls in Distributed Systems* (1988):
  <https://heather.miller.am/teaching/cs7680/pdfs/liskov1988.pdf>

The Pysolate-specific contribution is the combination of source-time authority-free
analysis, Host-private candidates, original-point temporal validation, one sealed
synchronous CPython execution and no Python-visible promise graph.
