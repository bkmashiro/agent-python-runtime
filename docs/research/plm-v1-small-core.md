# PLM V1 small core and conditional refinement

Status: **Current, bounded formalisation.**

Implementation reference: `c674d40f36716aa57820101d63f18cf44d0cbdeb` plus the Gate 8 property tests. This document is not a proof of full CPython or arbitrary providers.

## Core language

```text
e ::= v | x | op(e)
s ::= skip | x := e | s ; s | if e then s else s | raise E | x := H(a)
```

A baseline state is `(s, sigma, theta)`, where `sigma` is visible Guest state and `theta` is the Host world. A PLM state adds a Run-owned candidate map `C` and job map `J`. V1 admits only compiler-recognised direct assignments whose arguments are captured without moving evaluation across an unsafe statement.

## Labels and observations

Logical-visible labels are `call(l,H,a)`, `return(l,v)` and `raise(l,E)`. Provider/economic labels include physical start, billing, quota, rate-limit, session and uncertain-completion events. Internal labels include candidate allocation, validation, discard and job bookkeeping.

Only internal labels may be hidden, and only when preparation is Guest-silent and the adapter's provider non-interference obligation holds. Provider/economic labels are never erased from evidence.

## Candidate and job transitions

```text
candidate: prepared -> running -> ready | failed
           ready | failed -> adopted | discarded
           prepared | running -> cancelled

job: linearized -> completed | failed -> materialized
     linearized -> cancelled
```

The implementation checks these transitions with `ValidCandidateTransition` and `ValidJobTransition`. One table belongs to one Run and one Broker; candidate or job identity cannot be injected from another owner.

## PLM transitions

Prepare may emit physical and internal labels but no logical-visible label. At the original source call, the Host reconstructs the actual request and validates Run, Plan, source seal, site, occurrence, capability, handler, arguments, authority and provider session.

If the candidate and temporal proof are valid, `L` adopts it. Otherwise `L` cancels and settles the exact candidate before deciding whether a canonical operation may start. A settled typed uncertain provider outcome is materialised once as `provider_outcome_uncertain`, including when temporal or provider proof failed; it is never replayed. Every other rejected candidate starts the canonical operation through the same Broker call. V1 immediately materialises at the same source point, so `L=M`.

## Conditional results

**Prepare stuttering.** If prepare leaves Guest state unchanged and provider-visible preparation cannot alter the allowed logical outcome, removing its internal labels leaves the baseline logical trace unchanged.

**Sound adoption.** Assume the operation-specific validator is sound: a positive result implies the candidate outcome belongs to the baseline Host outcome set at `theta_L`. Then adoption returns an allowed baseline result at the original call.

**Invalid restart.** A binding, temporal, authority or provider-proof failure cancels and settles the candidate before the Broker may execute the canonical handler. If settlement proves an uncertain physical outcome, the Broker emits one terminal uncertainty receipt and does not replay. Otherwise the canonical call has baseline order and one receipt.

**Original-point order and exceptions.** Because V1 emits a concrete value or exception at the original direct-assignment call, and lowering does not cross an unsafe statement, surrounding assignment, branch and exception order matches sequential execution.

**Discard invisibility.** An unclaimed candidate is logically invisible only if its preparation was Guest-silent, discard is terminal and provider non-interference holds. Provider cost and physical events remain observable in the economic projection.

**Forward simulation sketch.** Relate a baseline state to a PLM state when visible Guest state and logical call position agree, while `C` may contain only contract-admitted candidates. Prepare steps stutter. At `x := H(a)`, sound adoption or canonical restart matches one baseline Host step. A settled uncertain physical outcome instead matches the baseline adapter's one terminal ambiguity result and forbids a second physical attempt. `L=M` then reaches related successor states. Sequence and condition rules preserve the relation compositionally for the implemented direct-assignment subset.

## Implementation map

| Assumption | Runtime or compiler check |
|---|---|
| one owner | `NewSplitPhaseTable`, Broker attachment and owner tests |
| exact request | `CandidateBinding`, canonical argument digest and runtime source seal |
| temporal proof | sealed `PLMContract`, `PLMValidator` and `DecidePLMLinearization` |
| authority at L | `AuthorityRecheckAtLinearize` and logical-context comparison |
| provider proof | `ProviderNonInterferenceValidator` and Host result |
| original point | `plm_capability_calls` lowering and exact Guest tests |
| terminal lifecycle | candidate/job transition checks and Run finalisation |
| no unsafe replay | typed uncertain outcome plus ready and invalidated no-replay tests |
| fallback | disabled/unsupported exact Guest tests |

## External and unmechanised obligations

Validator soundness and provider non-interference are adapter obligations; generic PLM code checks identity and receives the result but cannot prove an external world's semantics. The source transform covers a bounded Python subset, not reflection, arbitrary descriptors or full CPython evaluation. The simulation is a proof sketch checked by finite properties and differential tests, not a mechanised theorem. Cross-branch speculation, materialise sinking, writes and global control-flow reasoning are outside V1.

The controlled economics fixture is negative. Correctness and this conditional formalisation do not establish a general latency benefit.
