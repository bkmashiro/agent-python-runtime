# Effect-aware execution observable-semantics contract

Status: **Frozen research contract for pre-overlay experiments**

Date: 2026-08-14

This document defines what an effect-aware runtime consumer must preserve before any
semantic overlay or pre-dispatch mechanism is admitted. It is intentionally stronger
than “same returned JSON” and weaker than byte-identical physical scheduling.

## Research questions

### RQ1 — analyzable coverage

For a frozen generated-program corpus and exact target Guest, what fraction of programs and source regions can be represented without unknown control, call, alias, exception, freshness, or capability semantics?

Report acceptance and each rejection reason. Do not count a syntactic pattern as accepted merely because the parser visited it.

### RQ2 — incremental semantic value

How many useful opportunities are exposed by program-level control/data dependencies beyond a baseline that sees only capability calls and Host-authored resource labels?

The baseline must receive the same effect contracts. The comparison isolates value from program structure rather than better annotations.

### RQ3 — shared representation

Can the same verified facts drive at least two independently useful decisions—initially
semantic pre-dispatch and exact execution identity or pre-execution placement—without
consumer-specific semantic heuristics?

A consumer may add policy and cost thresholds, but it may not reinterpret unknown facts as safe.

### RQ4 — observable equivalence

For every admitted transformation, does the optimized execution preserve the observable semantics defined below under the exact declared contracts?

Performance evidence is invalid if the equivalence oracle fails or cannot classify the trace.

### RQ5 — cost of conservatism

What useful opportunities are rejected, and which missing fact causes each rejection? Measure the cost before extending the effect schema or graph.

## Frozen execution context

A baseline/optimized comparison is valid only when both executions bind the same frozen context:

- source and accepted-region identity;
- target Guest artifact and runtime version;
- execution profile and qualified import closure;
- capability Plan, Spec versions, handler identities and schemas;
- grants, project/principal, privacy scope and policy epoch;
- canonical inputs and immutable-root identities;
- workspace baseline or explicit absence of a workspace;
- freshness/snapshot context for every live observation;
- deterministic clock/random settings where applicable;
- output/exception schema and result bounds;
- cancellation/deadline experiment schedule.

If any binding differs, the pair is not an equivalence trial.

## Two observable surfaces

### Program-visible surface

The program-visible outcome contains:

1. canonical result value, or typed exception/trap outcome;
2. values returned by capability and WASI observations;
3. Python-visible mutation and control-flow consequences;
4. cancellation/timeout outcome visible to the program;
5. final private workspace state visible through allowed Guest operations.

Wall-clock latency, physical execution ID, backend name, cache-hit status and worker identity are not program-visible unless the accepted program can observe them through an explicitly declared capability.

### Host audit/effect surface

The Host-visible outcome contains:

1. admitted capability attempts with canonical capability/spec identity and argument digest;
2. captured/live observation identity, freshness/snapshot identity and returned-value digest;
3. workspace read/write resource identity and terminal workspace disposition;
4. external-effect intent, attempt, receipt, ambiguity and reconciliation disposition;
5. terminal result/exception/cancellation/timeout/OOM/trap disposition;
6. authority bindings and policy decisions;
7. whether physical execution started and whether any effect may have started;
8. backend/placement decision and any typed promotion proof.

The audit surface may contain extra body-free timing and physical-resource evidence. Such evidence need not be byte-identical, but it must not contradict the semantic dispositions above.

## Event model

A comparison trace is represented as a bounded set of typed events plus required order edges.

```text
RunStart
CapabilityAttempt
CapabilityObservation
WorkspaceRead
WorkspaceWrite
ExternalEffectIntent
ExternalEffectAttempt
ExternalEffectTerminal
Result
Raise
Cancel
Timeout
Trap
RunTerminal
```

Each event has:

- stable logical event identity derived from source region and dynamic occurrence;
- source span/region identity where available;
- capability or WASI contract identity;
- canonical argument/resource digest;
- result/evidence digest where body-safe;
- terminal status;
- freshness/snapshot identity where relevant;
- authority/policy binding digest;
- predecessor event identities required by program order, control dependence, data dependence, conflict order, exception order, or declared effect order.

A physical timestamp is evidence, not semantic order by itself.

## Equivalence relation

Let `B(P, C)` be baseline execution and `O(P, C)` optimized execution for program `P` and frozen context `C`.

They are equivalent only if all of the following hold:

1. **same terminal class:** both return, both raise the same typed accepted exception class, or both reach the same cancellation/timeout/trap class;
2. **same canonical program-visible value:** returned values, accepted exception payload and visible workspace state match;
3. **same logical effect multiset:** there is a one-to-one mapping between all
   dynamically reached Host effect/observation events, including attempts and
   ambiguous terminals; explicitly qualified speculative physical reads are accounted
   separately with physical dispositions and do not count as logical calls until
   claimed at the original dynamic boundary;
4. **same mandatory partial order:** the mapping preserves every data, control, conflict, exception and contract-observable order edge;
5. **same observation context:** live reads use an equivalent declared snapshot/freshness context; equal arguments alone are insufficient;
6. **same authority:** no event is admitted under a broader principal, grant, capability Plan, privacy scope or policy epoch;
7. **same failure boundary:** optimization does not introduce a logical event on a
   path where baseline terminates, raises, cancels, or skips it; an extra physical read
   is allowed only under explicit speculative authority and must end as a bounded,
   typed cancelled, late, or orphaned disposition if the logical call is never reached;
8. **same terminal dispositions:** workspace, effect and reconciliation dispositions match;
9. **no forbidden replay:** a second physical execution is never substituted after an effect may have started or completion is ambiguous.

Independent events may occur in a different physical order only when the Host-owned
contracts explicitly permit overlap/reordering and no required order edge connects
them. A pre-dispatched read does not become a logical call until unchanged Python
reaches the same call boundary and claims the exact staged-observation identity.
Unqualified or unmatched physical work is a divergence.

## Divergence classes

Every failed or unclassifiable trial emits one primary typed reason:

- `terminal_class_mismatch`
- `result_mismatch`
- `exception_mismatch`
- `missing_effect_event`
- `extra_effect_event`
- `effect_argument_mismatch`
- `required_order_inversion`
- `workspace_state_mismatch`
- `freshness_context_mismatch`
- `authority_binding_mismatch`
- `cancellation_boundary_mismatch`
- `terminal_disposition_mismatch`
- `post_effect_replay`
- `trace_unclassifiable`

`trace_unclassifiable` fails closed. It is not excluded from the denominator.

## Legality versus oracle

The static legality engine and dynamic oracle have different roles:

- legality attempts to prove before execution that a transformation is admissible;
- the oracle tests admitted transformations against baseline executions;
- a passing oracle does not make an unsound legality rule sound;
- an oracle cannot prove absence of divergence outside the tested corpus;
- a failing oracle blocks the rule and triggers minimization/root-cause analysis.

No optimizer may consult baseline results at runtime to retroactively authorize an effect.

## Initial semantic pre-dispatch restriction

Before an effect-contract extension proves more, the first runtime experiment may
pre-dispatch only an exact call that satisfies all of the following:

- the target-Guest overlay identifies an exact source occurrence;
- capability name and canonical arguments are available before Guest execution;
- the Host capability binding is `read_only + idempotent + speculative_safe`;
- spec, handler, Plan, grant/policy, freshness, expiry and privacy identities are
  frozen;
- an independent per-Run speculation budget admits the physical request;
- the result is stored only as a one-shot `StagedObservation`, never durable cache;
- unchanged Python must claim the exact source occurrence and dynamic occurrence;
- duplicate equal calls retain distinct physical records unless a separate
  coalescing contract explicitly permits sharing;
- cancellation, timeout, late completion and non-reach produce typed terminal or
  orphaned dispositions and never publish a logical result.

Writes, unknown calls, dynamic arguments, unresolved freshness and authority
mismatches are rejected. This restriction is part of the research contract, not an
implementation suggestion.

## Initial placement restriction

Semantic placement is pre-execution only. Native promotion after a Pysolate attempt is legal only when Host evidence proves both:

```text
workspace = not_started
external effects = not_started
```

Ordinary failure, unsupported behavior discovered after possible effects, timeout, cancellation, lost reply or ambiguous completion never authorizes replay on another backend.

## Required evidence per experiment

A machine-readable experiment record must contain:

- source/region and frozen-context digests;
- baseline and optimized mechanism identities;
- legality decision and rejection/acceptance reasons;
- body-free baseline and optimized event traces;
- equivalence result or divergence class;
- logical versus physical execution counts, including consumed, cancelled, late and
  orphaned staged observations;
- latency/resource measurements separately from correctness;
- skipped/unsupported disposition rather than fabricated success.

The first graph/census may report only structural candidates. It must set legality and equivalence to `not_evaluated`, never `true`.
