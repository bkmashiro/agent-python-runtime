# ADR: Pysolate is an authority-lifecycle runtime

Status: **Accepted direction; implementation remains mixed Current/Proposed.**
Date: 2026-08-13

## Context

Sandboxed model-authored code, mediated connectors, ledgers, approval replay,
and reverse-order compensation are established adjacent product mechanisms.
Pysolate cannot justify itself as “Code Mode, but Python” or by accumulating the
same surfaces.

Python and CPython/WASI remain useful implementation choices: Python is a strong
language for Agent-authored control flow, while WASI makes ambient OS authority
small and explicit. Neither is the project contribution by itself.

## Decision

Pysolate will focus on the lifecycle and verification of authority-bearing Agent
programs:

> A Run is a Host-governed state transition whose interpreter state, authority
> context, workspace state, scratch state, and external-effect state have
> separate identities and terminal dispositions.

The Host—not Agent source, generated wrappers, or the execution backend—owns:

- admitted artifact and execution profile;
- frozen capability specs, grants, handler identities, policies, and budgets;
- workspace view and terminal disposition;
- external-effect operation and attempt identities;
- approval provenance;
- ambiguity, reconciliation, compensation, and retry decisions;
- evidence publication and verification.

Ordinary Python remains ordinary. Pure computation and private filesystem work
do not need to become fake tool calls. Authority-bearing external operations use
typed Host capabilities generated from one canonical definition.

## Orthogonal Run lifecycles

Model each Run as:

```text
R = (B, D, I, N, A, T, W, E)
```

- `B`: admitted execution baseline/artifact;
- `D`: private mutable interpreter and WASM state;
- `I`: source, input, schema, and Host configuration;
- `N`: clock, randomness, and captured external observations;
- `A`: frozen authority context, grants, leases, and budgets;
- `T`: per-Run scratch filesystem;
- `W`: workspace view and attempt state;
- `E`: external-effect operation/attempt state.

Terminal state is a vector, not one success boolean:

| Axis | Example terminal disposition |
|---|---|
| Interpreter | retired / poisoned / future verified restore |
| Authority | finalized / revoked / stale-context rejected |
| Scratch | deleted |
| Workspace | unchanged / retained-direct / committed / discarded / frozen / conflict |
| External effect | none / rejected / applied / failed / ambiguous / reconciled / compensated |
| Evidence | complete / bounded / unavailable / invalid |

No axis may be inferred from another. Retiring a Guest does not roll back files,
prove a provider call failed, or revoke already-applied external effects.

## Current truth

Current implementation provides:

- fresh CPython/WASI Guest execution;
- no ambient shell, subprocess, network, credential, or Host filesystem;
- static import admission bound to a verified artifact/profile;
- a sealed per-Run capability plan binding specs, opaque grants, handlers, and a
  total call budget;
- strict schema validation before and after Host handlers;
- Host-authored receipts and bounded observation;
- a Host-owned rooted workspace and separate per-Run scratch;
- explicit Host disposition and Capsule publication;
- scoped playback for captured read capabilities;
- bounded read-only Git inspection.

Current mounted workspace writes are direct. `discard` can prevent Capsule
publication; it does not establish that bytes already written to a retained live
workspace were transactionally rolled back. Current receipts are evidence of
Host mediation, not independent provider truth. Current playback is not general
deterministic re-execution.

## Proposed proof target

The first system-level proof should exercise the full conjunction:

```text
Host freezes exact authority and base workspace revision
→ fresh Guest computes in a private workspace attempt
→ Guest stages an immutable external EffectIntent
→ Host journals operation and attempt before dispatch
→ provider accepts but response is deliberately lost
→ outcome becomes ambiguous; blind retry is denied
→ Host reconciles through a provider readback oracle
→ Host chooses workspace commit/discard/freeze from the full terminal vector
→ independent verifier checks artifact, plan, base, intent, attempt,
  reconciliation, final workspace, and response identities
```

This is not a promise of universal transactions or exactly-once effects. It is a
bounded demonstration that uncertainty is represented rather than hidden.

## Non-negotiable semantic distinctions

- **Receipt is not provider truth.** It proves what the trusted Host recorded.
- **Digest is not semantic correctness.** It binds bytes and identity.
- **Playback is not deterministic re-execution.** Captured calls may be replayed;
  live providers and uncaptured nondeterminism may differ.
- **Compensation is not rollback.** A forward revert may fail or only partially
  restore business state.
- **Workspace discard is not external-effect reversal.** State planes terminate
  independently.
- **Agent commit is not user approval.** Human authorization must originate
  outside Agent/Guest authority and bind an exact immutable intent.
- **Compatibility is not authority.** Another backend may add POSIX or package
  compatibility without silently adding credentials, network targets, mounts,
  or commit rights.

## Consequences

### Extension: content-addressed Agent Functions

The authority-lifecycle direction is a foundation rather than the complete
feature hypothesis. A proposed extension uses the same fresh-Guest and typed
effect boundaries to admit selected computations as local content-addressed
Agent Functions. Live I/O remains explicit; immutable observations and
filesystem roots become inputs; cacheable computation may be reused by later
fresh Guests without restoring Python interpreter state.

This extends rather than replaces the existing conjunction:

- frozen authority defines what a cacheable function cannot observe or mutate;
- typed effects provide hard boundaries at which result caching stops;
- workspace/Capsule identities provide movable immutable inputs and outputs;
- fresh execution removes hidden continuation state;
- optional worker-local memory COW improves density without becoming semantic
  state;
- Host-owned disposition still governs the eventual real-world commit.

The initial design is intentionally local and binary (`cacheable` or
`not_cacheable`); it does not claim automatic arbitrary-Python purity analysis,
distributed cache coherence, or cross-tenant result sharing. See
[content-addressed-agent-functions.md](content-addressed-agent-functions.md).

### We will prioritize

1. a binary `cacheable | not_cacheable` contract and one-Host reuse proof that
   composes fresh Guests, immutable filesystem roots, and typed I/O boundaries;
2. private workspace attempts and explicit terminal disposition for derived
   outputs and eventual commit;
3. one fake-but-protocol-real external adapter with deterministic fault
   injection and readback reconciliation;
4. exact intent/approval/operation/attempt identity;
5. an independent terminal-vector verifier;
6. conformance of the same capability contract across execution backends only
   after the single-backend proof is complete.

### We will not prioritize

- broad connector count;
- a generic MCP marketplace;
- ambient shell or package installation;
- parity with Cloudflare's UI or connector ecosystem;
- adding ledger/approval/revert as undifferentiated checklist features;
- elaborate Lab visualization before the runtime records the claimed mechanism;
- claims that Python, WASI, receipts, or compensation are independently novel.

## Evidence required to keep this direction

Reframe or narrow the project if a fair comparison shows that:

- the conjunction adds no meaningful safety, verification, or operator value;
- attempt overlays impose unacceptable cost for the target workload;
- provider ambiguity cannot be qualified without bespoke unverifiable claims;
- backend-neutral contracts collapse into backend-specific ambient authority;
- the north-star workflow is no easier to reason about than an ordinary
  orchestrator plus sandbox.

The adjacent-product baseline is recorded in
[cloudflare-code-mode-comparison.md](research/cloudflare-code-mode-comparison.md).
